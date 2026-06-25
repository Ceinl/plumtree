package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/Ceinl/plumtree/spike/abi"
)

// Limits collects the per-session resource caps the runner enforces. These are
// the spike's stand-ins for the full set in PLATFORM_SPEC.md.
type Limits struct {
	MemoryPages  uint32        // linear-memory cap, in 64 KiB WASM pages
	FrameTimeout time.Duration // wall-clock deadline for a single frame() call
}

// ErrFrameDeadline reports that the guest blew its per-frame wall-clock budget
// and the session was forcibly torn down. The Session is unusable afterward.
var ErrFrameDeadline = errors.New("guest exceeded per-frame deadline; session terminated")

// Session is one instantiated guest module plus its host bindings. It mediates
// every guest interaction: the guest gets no ambient filesystem, env, args, or
// network — only the alloc/free/frame exports reachable here.
type Session struct {
	runtime wazero.Runtime
	mod     api.Module
	alloc   api.Function
	free    api.Function
	frameFn api.Function
	limits  Limits
	dead    bool
}

// NewSession compiles and instantiates the guest with the given limits. Guest
// stdout/stderr are routed to logs (treated as untrusted app logs), never to
// the user's terminal. The WASI environment is empty: no FS, env, args, or net.
func NewSession(ctx context.Context, wasm []byte, lim Limits, logs io.Writer) (*Session, error) {
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true). // lets a frame deadline abort a busy guest
		WithMemoryLimitPages(lim.MemoryPages)

	r := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("instantiate WASI: %w", err)
	}

	modCfg := wazero.NewModuleConfig().
		WithName("counter").
		WithStdout(logs).
		WithStderr(logs)
	// Note: no WithFSConfig / WithEnv / WithArgs / network — default-deny.

	mod, err := r.InstantiateWithConfig(ctx, wasm, modCfg)
	if err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("instantiate module: %w", err)
	}

	// Reactor module: run the Go runtime initializer once before any export.
	if init := mod.ExportedFunction("_initialize"); init != nil {
		if _, err := init.Call(ctx); err != nil {
			r.Close(ctx)
			return nil, fmt.Errorf("_initialize: %w", err)
		}
	}

	s := &Session{
		runtime: r,
		mod:     mod,
		alloc:   mod.ExportedFunction("alloc"),
		free:    mod.ExportedFunction("free"),
		frameFn: mod.ExportedFunction("frame"),
		limits:  lim,
	}
	if s.alloc == nil || s.free == nil || s.frameFn == nil {
		r.Close(ctx)
		return nil, errors.New("guest is missing required exports (alloc/free/frame)")
	}
	return s, nil
}

// Frame sends one input event to the guest and returns the structured frame it
// produces. A zero-length event means "repaint only" (resize / first frame).
//
// The guest's frame() call runs under a wall-clock deadline; if it overruns,
// the module is closed and ErrFrameDeadline is returned. alloc/free run under
// the parent context so they aren't charged against the compute budget.
func (s *Session) Frame(ctx context.Context, w, h int, event []byte) (abi.Frame, error) {
	if s.dead {
		return abi.Frame{}, ErrFrameDeadline
	}

	evPtr, freeEvent, err := s.writeEvent(ctx, event)
	if err != nil {
		return abi.Frame{}, err
	}
	defer freeEvent()

	callCtx, cancel := context.WithTimeout(ctx, s.limits.FrameTimeout)
	defer cancel()

	res, err := s.frameFn.Call(callCtx, uint64(w), uint64(h), uint64(evPtr), uint64(len(event)))
	if err != nil {
		// A canceled/expired context closes the module; the session is now dead.
		if callCtx.Err() != nil {
			s.dead = true
			if ctx.Err() != nil {
				return abi.Frame{}, ctx.Err() // parent canceled (e.g. disconnect)
			}
			return abi.Frame{}, ErrFrameDeadline
		}
		return abi.Frame{}, fmt.Errorf("guest frame() trapped: %w", err)
	}

	packed := res[0]
	outPtr := uint32(packed >> 32)
	outLen := uint32(packed & 0xffffffff)
	raw, ok := s.mod.Memory().Read(outPtr, outLen)
	if !ok {
		return abi.Frame{}, fmt.Errorf("frame buffer [%d:%d] out of range", outPtr, outPtr+outLen)
	}
	// Copy out before freeing or making further guest calls — Read may alias.
	buf := make([]byte, len(raw))
	copy(buf, raw)
	s.free.Call(ctx, uint64(outPtr))

	return abi.DecodeFrame(buf)
}

// writeEvent allocates a guest buffer and copies the event bytes into it,
// returning the offset and a cleanup func. A zero-length event allocates
// nothing.
func (s *Session) writeEvent(ctx context.Context, event []byte) (uint32, func(), error) {
	if len(event) == 0 {
		return 0, func() {}, nil
	}
	res, err := s.alloc.Call(ctx, uint64(len(event)))
	if err != nil {
		return 0, func() {}, fmt.Errorf("guest alloc: %w", err)
	}
	ptr := uint32(res[0])
	if ptr == 0 {
		return 0, func() {}, errors.New("guest alloc returned null")
	}
	if !s.mod.Memory().Write(ptr, event) {
		return 0, func() {}, fmt.Errorf("event buffer [%d:%d] out of range", ptr, ptr+uint32(len(event)))
	}
	return ptr, func() {
		if !s.dead {
			s.free.Call(ctx, uint64(ptr))
		}
	}, nil
}

// Close tears down the runtime and the guest instance.
func (s *Session) Close(ctx context.Context) error { return s.runtime.Close(ctx) }
