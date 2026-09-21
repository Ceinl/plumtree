//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

func configureBrokerWorker(cmd *exec.Cmd, uid, gid uint32) error {
	if uid == 0 {
		return nil
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uid, Gid: gid, NoSetGroups: true},
		Setpgid:    true,
	}
	return nil
}
