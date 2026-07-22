//go:build unix

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// configureCommandGroup makes context cancellation terminate both the command
// and any descendants it started, rather than leaving children behind after a
// session ends.
func configureCommandGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
}
