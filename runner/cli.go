package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// RunCLI runs a non-interactive guest command, forwarding its text output to
// out with control characters stripped (the host-side filtering that stops a
// CLI guest from emitting raw terminal escapes). args are passed as the guest's
// program arguments.
//
// Like Run, RunCLI compiles the WASM from scratch each call; use a Runner to
// reuse compiled code across sessions of the same module.
func RunCLI(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, args []string, out io.Writer) error {
	return runCLI(ctx, nil, wasm, lim, caps, args, out)
}

// runCLI is the shared engine behind RunCLI and (*Runner).RunCLI. A non-nil
// cache reuses generated code across calls; the runtime is still per-call.
func runCLI(ctx context.Context, cache wazero.CompilationCache, wasm []byte, lim Limits, caps Capabilities, args []string, out io.Writer) error {
	if err := validateLimits(lim); err != nil {
		return err
	}
	callerCtx := ctx
	if lim.SessionTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.SessionTimeout)
		defer cancel()
	}
	cfg := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(lim.MemoryPages)
	if cache != nil {
		cfg = cfg.WithCompilationCache(cache)
	}
	r := wazero.NewRuntimeWithConfig(ctx, cfg)
	defer r.Close(context.Background())

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return fmt.Errorf("instantiate WASI: %w", err)
	}

	// Satisfy the SDK's TUI host imports if the linker kept them; a CLI guest
	// never calls recv/present. The KV imports get the real capability, so a CLI
	// app may use storage.
	hostMod := r.NewHostModuleBuilder("plumtree").
		NewFunctionBuilder().WithFunc(func(context.Context, api.Module, int32, int32) int32 { return -1 }).Export("recv").
		NewFunctionBuilder().WithFunc(func(context.Context, api.Module, int32, int32) {}).Export("present")
	hostMod = registerKV(hostMod, caps.KV)
	// A CLI guest has no session loop to receive messages, but may publish; give
	// it the bus with a nil subscription (bus_sub becomes a no-op error).
	var busSub Subscriber
	if caps.Bus != nil {
		busSub = caps.Bus.Open()
		defer busSub.Close()
	}
	hostMod = registerBus(hostMod, caps.Bus, busSub)
	hostMod = registerAuth(hostMod, caps.Auth)
	hostMod = registerEnv(hostMod, caps.Env)
	hostMod = registerFetch(hostMod, caps.Fetch)
	if _, err := hostMod.Instantiate(ctx); err != nil {
		return fmt.Errorf("install host module: %w", err)
	}

	filtered := &controlFilter{w: out}
	modCfg := wazero.NewModuleConfig().
		WithName("app").
		WithArgs(append([]string{"app"}, args...)...).
		WithStdout(filtered).
		WithStderr(filtered)

	_, err := r.InstantiateWithConfig(ctx, wasm, modCfg)
	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 0 {
				return nil
			}
			return fmt.Errorf("app exited with code %d", exit.ExitCode())
		}
		if callerCtx.Err() != nil {
			return callerCtx.Err()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrSessionDeadline
		}
		return fmt.Errorf("guest: %w", err)
	}
	return nil
}

// controlFilter forwards a CLI guest's text output with any character that could
// drive the viewer's terminal removed. It decodes the stream as UTF-8 and drops,
// at the rune level, C0 controls (except tab and newline), DEL, and C1 controls
// (0x80–0x9f) — the same policy as sanitizeRune. C1 matters because 8-bit
// terminals honor bytes like 0x9b/0x9d/0x90 as CSI/OSC/DCS introducers, so a lone
// such byte is as dangerous as ESC; a byte-wise filter that only dropped C0 would
// pass it. Working at the rune level (rather than dropping the raw 0x80–0x9f byte
// range) is what preserves legitimate multibyte UTF-8, whose continuation bytes
// share that range. Bytes that are not valid UTF-8 are dropped, and a rune split
// across two Write calls is carried to the next so it is decoded whole.
type controlFilter struct {
	w   io.Writer
	buf []byte // carried incomplete trailing UTF-8 sequence (< utf8.UTFMax bytes)
}

func (f *controlFilter) Write(p []byte) (int, error) {
	data := p
	if len(f.buf) > 0 {
		data = append(f.buf, p...)
		f.buf = nil
	}
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			// A single unusable byte: either genuinely invalid UTF-8, or an
			// incomplete-but-valid trailing sequence. FullRune is false only for
			// the latter, so carry those bytes for the next Write and drop the rest.
			if !utf8.FullRune(data) {
				f.buf = append(f.buf, data...)
				break
			}
			data = data[1:]
			continue
		}
		if !dropRune(r) {
			out = append(out, data[:size]...)
		}
		data = data[size:]
	}
	if len(out) > 0 {
		if _, err := f.w.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// dropRune reports whether r is a terminal-driving control character that must
// not reach the viewer: C0 controls other than tab/newline, DEL, or a C1 control.
func dropRune(r rune) bool {
	switch {
	case r < 0x20:
		return r != '\t' && r != '\n'
	case r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	default:
		return false
	}
}
