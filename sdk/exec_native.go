//go:build !wasip1

package sdk

import (
	"context"
	"errors"
	"os/exec"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func execCommand(name string, args []string) (ExecResult, error) {
	if !validExecRequest(name, args) {
		return ExecResult{}, ErrExecTooLarge
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := &execOutputBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	stderr := &execOutputBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return ExecResult{}, ErrExecTooLarge
	}
	result := ExecResult{ExitCode: 0, Stdout: stdout.b, Stderr: stderr.b}
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

type execOutputBuffer struct {
	max      int
	b        []byte
	overflow bool
	cancel   context.CancelFunc
}

func (b *execOutputBuffer) Write(p []byte) (int, error) {
	n := min(max(b.max-len(b.b), 0), len(p))
	if n > 0 {
		b.b = append(b.b, p[:n]...)
	}
	if n < len(p) {
		b.overflow = true
		b.cancel()
	}
	return len(p), nil
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
