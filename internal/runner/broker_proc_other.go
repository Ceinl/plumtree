//go:build !unix

package runner

import (
	"errors"
	"os/exec"
)

func configureBrokerWorker(_ *exec.Cmd, uid, _ uint32) error {
	if uid != 0 {
		return errors.New("per-session worker identities are unsupported on this platform")
	}
	return nil
}
