// Package hostexec provides typed opt-in host command operations. Authority is
// the explicitly enabled operator capability; output lives in the finite
// result and is copied. A hosted isolated runtime may deny the capability,
// while native runs execute through the local adapter with the same request
// and output bounds.
package hostexec

import (
	"context"
	"errors"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var (
	ErrUnavailable = errors.New("hostexec: capability unavailable")
	ErrTooLarge    = errors.New("hostexec: request or output too large")
	ErrFailed      = errors.New("hostexec: command failed")
	ErrInvalid     = errors.New("hostexec: invalid command")
)

type Result struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Err      error
}

type Operation struct{ inner operation.Operation[Result] }

func (op Operation) Run(ctx context.Context) Result                { return op.inner.Run(ctx) }
func (op Operation) Map(mapper func(Result) app.Event) app.Command { return op.inner.Map(mapper) }
func (op Operation) Ignore() app.Command                           { return op.inner.Ignore() }

// Run creates a bounded direct host-command operation. Arguments are copied.
func Run(name string, args ...string) Operation {
	argsCopy := append([]string(nil), args...)
	return Operation{inner: operation.New(func(ctx context.Context) Result {
		if !valid(name, argsCopy) {
			return Result{Err: ErrInvalid}
		}
		if err := ctx.Err(); err != nil {
			return Result{Err: err}
		}
		value, err := execCommand(name, argsCopy)
		return Result{ExitCode: value.ExitCode, Stdout: append([]byte(nil), value.Stdout...), Stderr: append([]byte(nil), value.Stderr...), Err: normalize(err)}
	})}
}

func valid(name string, args []string) bool {
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

func normalize(err error) error {
	if err == nil {
		return nil
	}
	return err
}
