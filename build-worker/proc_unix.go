//go:build unix

package buildworker

import (
	"os/exec"
	"syscall"
	"time"
)

// configureSandboxProc puts the build in its own process group and makes
// cancellation kill the whole group, so a timeout reliably stops the go driver
// and any compile/link children it spawned rather than orphaning them.
func configureSandboxProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID signals the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
