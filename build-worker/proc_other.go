//go:build !unix

package buildworker

import (
	"os/exec"
	"time"
)

// configureSandboxProc applies the portable fallback: rely on CommandContext's
// default kill. Stronger process-group isolation is platform specific and added
// per-OS as needed.
func configureSandboxProc(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
