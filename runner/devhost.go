// Package runner runs a Plumtree guest .wasm locally in wazero, exactly the
// way the hosted runner will: the guest drives its own loop and calls two host
// imports — recv (next input event) and present (a rendered frame). The host
// supplies input through a Source and renders frames to a Sink, enforcing a
// linear-memory cap and a per-frame wall-clock deadline. It is the engine
// behind `pt dev`.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// Limits are the per-session resource caps the dev host enforces.
type Limits struct {
	MemoryPages  uint32        // linear-memory cap, in 64 KiB WASM pages
	FrameTimeout time.Duration // wall-clock deadline for one frame's compute

	// SessionTimeout caps the total wall-clock time for the whole session, a
	// proxy for a CPU/time quota. 0 means unlimited. It bounds a guest that
	// yields every frame (so the per-frame watchdog never fires) but runs
	// forever.
	SessionTimeout time.Duration
	// MaxEventsPerSec caps how fast input events are delivered to the guest. 0
	// means unlimited. Excess input is delayed, never dropped, so keystrokes are
	// not lost while a flooding client cannot drive unbounded guest work.
	MaxEventsPerSec int
	// MaxFramesPerSec caps how fast the guest may present frames. 0 means
	// unlimited. Excess frames are dropped (only the latest frame matters for a
	// TUI), bounding host render work from a guest spamming present.
	MaxFramesPerSec int
}

// MaxMemoryPages is WebAssembly's 32-bit linear-memory ceiling (4 GiB).
const MaxMemoryPages uint32 = 65_536

func validateLimits(lim Limits) error {
	if lim.MemoryPages == 0 || lim.MemoryPages > MaxMemoryPages {
		return fmt.Errorf("runner: memory pages must be between 1 and %d", MaxMemoryPages)
	}
	if lim.FrameTimeout < 0 || lim.SessionTimeout < 0 || lim.MaxEventsPerSec < 0 || lim.MaxFramesPerSec < 0 {
		return errors.New("runner: timeouts and rate limits must not be negative")
	}
	return nil
}

// DefaultLimits are conservative caps suitable for local development.
var DefaultLimits = Limits{
	MemoryPages:     512,
	FrameTimeout:    2 * time.Second,
	SessionTimeout:  30 * time.Minute,
	MaxEventsPerSec: 200,
	MaxFramesPerSec: 120,
}

// ErrSessionDeadline reports that the whole session exceeded SessionTimeout and
// was terminated.
var ErrSessionDeadline = errors.New("session exceeded total time budget; terminated")

// Source supplies input events to the guest. Next blocks until an event is
// ready and returns ok=false to tell the guest to stop (disconnect, scripted
// end, or cancellation).
type Source interface {
	Next(ctx context.Context) (abi.Event, bool)
}

// Sink receives each structured frame the guest renders.
type Sink interface {
	Present(abi.Frame)
}

// ErrFrameDeadline reports that a single frame's compute exceeded FrameTimeout
// and the guest was forcibly terminated.
var ErrFrameDeadline = errors.New("guest exceeded per-frame deadline; session terminated")

// Run instantiates the guest and drives it to completion. It returns nil on a
// clean guest exit, ErrFrameDeadline if a frame overran, or ctx.Err() if the
// caller cancelled. Guest stdout/stderr go to logs, never to the terminal.
//
// Run compiles the WASM from scratch each call. Servers that run the same
// module across many sessions should use a Runner, which reuses compiled code
// via a shared compilation cache.
func Run(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, src Source, sink Sink, logs io.Writer) error {
	return runGuest(ctx, nil, wasm, lim, caps, src, sink, logs)
}

// runGuest is the shared engine behind Run and (*Runner).Run. A non-nil cache
// is installed on the per-session runtime so repeated compilations of the same
// WASM reuse generated code; the runtime (and thus the guest instance) is still
// created fresh per call, preserving isolation between sessions.
func runGuest(ctx context.Context, cache wazero.CompilationCache, wasm []byte, lim Limits, caps Capabilities, src Source, sink Sink, logs io.Writer) error {
	if err := validateLimits(lim); err != nil {
		return err
	}
	sessCtx := ctx
	if lim.SessionTimeout > 0 {
		var sessCancel context.CancelFunc
		sessCtx, sessCancel = context.WithTimeout(ctx, lim.SessionTimeout)
		defer sessCancel()
	}
	runCtx, cancel := context.WithCancel(sessCtx)
	defer cancel()

	inBucket := newTokenBucket(lim.MaxEventsPerSec)
	outBucket := newTokenBucket(lim.MaxFramesPerSec)

	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(lim.MemoryPages)
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}
	r := wazero.NewRuntimeWithConfig(runCtx, cfg)
	defer r.Close(context.Background())

	if _, err := wasi_snapshot_preview1.Instantiate(runCtx, r); err != nil {
		return fmt.Errorf("instantiate WASI: %w", err)
	}

	wd := &watchdog{timeout: lim.FrameTimeout, cancel: cancel}

	// Open a per-session bus subscription and let the Source select on incoming
	// messages, so a session blocked in recv wakes the moment another session
	// publishes. The subscription is nil when the app has no Bus capability.
	var sub Subscriber
	if caps.Bus != nil {
		sub = caps.Bus.Open()
		defer sub.Close()
		if bb, ok := src.(BusBinder); ok {
			bb.BindBus(sub.Events())
		}
	}

	hostMod := r.NewHostModuleBuilder("plumtree").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, ptr, capBytes int32) int32 {
			// Idle while waiting for input — not charged against a frame.
			wd.disarm()
			ev, ok := src.Next(ctx)
			if !ok {
				return -1
			}
			// Throttle input delivery without dropping events: a flooding client
			// is slowed, never loses keystrokes. Cancellation aborts the wait.
			if !inBucket.wait(ctx.Done()) {
				return -1
			}
			b := abi.EncodeEvent(ev)
			if int32(len(b)) > capBytes || !m.Memory().Write(uint32(ptr), b) {
				return -1
			}
			wd.arm() // the guest now has a frame's worth of work to do
			return int32(len(b))
		}).
		Export("recv").
		NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, ptr, length int32) {
			wd.disarm()
			// Drop frames presented faster than the output budget: only the
			// latest frame matters for a TUI, so this bounds host render work
			// without stalling the guest.
			if !outBucket.allow() {
				return
			}
			raw, ok := m.Memory().Read(uint32(ptr), uint32(length))
			if !ok {
				return
			}
			buf := make([]byte, len(raw))
			copy(buf, raw)
			if f, err := abi.DecodeFrame(buf); err == nil {
				sink.Present(f)
			}
		}).
		Export("present")

	hostMod = registerKV(hostMod, caps.KV)
	hostMod = registerBus(hostMod, caps.Bus, sub)
	hostMod = registerAuth(hostMod, caps.Auth)
	hostMod = registerEnv(hostMod, caps.Env)
	hostMod = registerFetch(hostMod, caps.Fetch)
	hostMod = registerGoodbye(hostMod, caps.Goodbye)
	if _, err := hostMod.Instantiate(runCtx); err != nil {
		return fmt.Errorf("install host module: %w", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithName("app").
		WithStdout(logs).
		WithStderr(logs)
	// Command module: Instantiate runs _start (main) to completion.
	_, err := r.InstantiateWithConfig(runCtx, wasm, modCfg)
	wd.disarm()

	if err != nil {
		if wd.fired.Load() {
			return ErrFrameDeadline
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The caller's ctx is still live, so a cancelled session ctx means the
		// total time budget elapsed.
		if errors.Is(sessCtx.Err(), context.DeadlineExceeded) {
			return ErrSessionDeadline
		}
		// A normal main() return calls proc_exit(0); wazero surfaces that as an
		// ExitError with code 0, which is a clean finish, not a failure.
		var exit *sys.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 0 {
			return nil
		}
		return fmt.Errorf("guest: %w", err)
	}
	return nil
}

// watchdog cancels the run context if a frame's compute exceeds timeout. It is
// armed when the host hands the guest an event and disarmed when the guest
// presents the resulting frame (or blocks waiting for the next event).
type watchdog struct {
	timeout time.Duration
	cancel  context.CancelFunc

	mu    sync.Mutex
	timer *time.Timer
	fired atomic.Bool
}

func (w *watchdog) arm() {
	if w.timeout <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(w.timeout, func() {
		w.fired.Store(true)
		w.cancel()
	})
}

func (w *watchdog) disarm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}
