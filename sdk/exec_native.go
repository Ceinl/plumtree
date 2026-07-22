//go:build !wasip1

package sdk

import (
	"bytes"
	"errors"
	"os/exec"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func execCommand(name string, args []string) (ExecResult, error) {
	if !validExecRequest(name, args) {
		return ExecResult{}, ErrExecTooLarge
	}
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if stdout.Len() > abi.ExecMaxOutput || stderr.Len() > abi.ExecMaxOutput {
		return ExecResult{}, ErrExecTooLarge
	}
	result := ExecResult{ExitCode: 0, Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return ExecResult{}, ErrExecFailed
}

func validExecRequest(name string, args []string) bool {
	if name == "" || len(name) > abi.ExecMaxName || len(args) > abi.ExecMaxArgs {
		return false
	}
	for _, arg := range args {
		if len(arg) > abi.ExecMaxArg {
			return false
		}
	}
	return true
}
