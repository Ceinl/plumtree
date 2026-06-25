package runner

import (
	"context"
	"errors"
	"fmt"
	"io"

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

// controlFilter drops C0 control bytes (except tab and newline) and DEL before
// forwarding, neutralizing ANSI escape sequences (which begin with ESC, 0x1b).
type controlFilter struct{ w io.Writer }

func (f *controlFilter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		if (b < 0x20 && b != '\t' && b != '\n') || b == 0x7f {
			continue
		}
		out = append(out, b)
	}
	if _, err := f.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
