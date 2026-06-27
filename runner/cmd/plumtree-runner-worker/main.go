// Command plumtree-runner-worker is the sandboxed runner subprocess. The
// control plane (or any ProcessRunner) spawns one per session: it reads the
// session parameters and forwards every guest host call to the parent over
// stdin/stdout, so the untrusted WASM guest and the wazero runtime live in a
// disposable process isolated from the control plane.
//
// It is not run by hand; ProcessRunner manages its lifecycle.
package main

import (
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/runner"
)

func main() {
	if err := runner.RunWorker(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "runner-worker:", err)
		os.Exit(1)
	}
}
