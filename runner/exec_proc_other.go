//go:build !unix

package runner

import "os/exec"

func configureCommandGroup(cmd *exec.Cmd) {}
