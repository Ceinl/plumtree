package runner

import (
	"context"
	"io"

	"github.com/tetratelabs/wazero"
)

// Runner runs guests while reusing compiled code across sessions. It holds a
// shared wazero compilation cache: the first session that runs a given .wasm
// pays the compile cost, and later sessions of the same module reuse the
// generated code. Each session still gets its own runtime and guest instance,
// so sessions stay isolated — the cache only shares immutable compiled code,
// never guest memory or host state.
//
// A Runner is safe for concurrent use. Long-lived services (the SSH gateway,
// the dev SSH server) should hold one Runner for their lifetime; single-shot
// callers like `pt dev` can use the package-level Run/RunCLI instead.
type Runner struct {
	cache wazero.CompilationCache
}

// New returns a Runner backed by an in-memory compilation cache. Call Close to
// release it.
func New() *Runner {
	return &Runner{cache: wazero.NewCompilationCache()}
}

// Close releases the compilation cache. The Runner must not be used afterward.
func (rn *Runner) Close(ctx context.Context) error {
	if rn.cache == nil {
		return nil
	}
	return rn.cache.Close(ctx)
}

// Run is like the package-level Run but reuses compiled code via the Runner's
// cache. See Run for semantics.
func (rn *Runner) Run(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, src Source, sink Sink, logs io.Writer) error {
	return runGuest(ctx, rn.cache, wasm, lim, caps, src, sink, logs)
}

// RunCLI is like the package-level RunCLI but reuses compiled code via the
// Runner's cache. See RunCLI for semantics.
func (rn *Runner) RunCLI(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, args []string, out io.Writer) error {
	return runCLI(ctx, rn.cache, wasm, lim, caps, args, out)
}
