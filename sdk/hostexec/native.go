//go:build !wasip1

package hostexec

import (
	"context"
	"errors"
	"os/exec"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func execCommand(name string, args []string) (Result, error) {
	if !valid(name, args) {
		return Result{}, ErrTooLarge
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	stdout := &outputBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	stderr := &outputBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	if stdout.overflow || stderr.overflow {
		return Result{}, ErrTooLarge
	}
	result := Result{Stdout: stdout.data, Stderr: stderr.data}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return Result{}, ErrFailed
}

type outputBuffer struct {
	max      int
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (buffer *outputBuffer) Write(value []byte) (int, error) {
	count := min(max(buffer.max-len(buffer.data), 0), len(value))
	if count > 0 {
		buffer.data = append(buffer.data, value[:count]...)
	}
	if count < len(value) {
		buffer.overflow = true
		buffer.cancel()
	}
	return len(value), nil
}
