// Package secrets provides typed read-only server-secret operations. Authority
// is the owner-enabled app's server-side secret store; values live only for the
// operation result and are never build input. Native uses process environment
// values for author development, while hosted isolation supplies only the
// selected secret capability and reports ErrUnavailable otherwise.
package secrets

import (
	"context"
	"errors"
	"fmt"

	legacy "github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var (
	ErrUnavailable = errors.New("secrets: capability unavailable")
	ErrInvalid     = errors.New("secrets: invalid key")
	ErrTooLarge    = errors.New("secrets: key too large")
)

type Result struct {
	Value string
	Found bool
	Err   error
}

type Operation struct{ inner operation.Operation[Result] }

func (op Operation) Run(ctx context.Context) Result                { return op.inner.Run(ctx) }
func (op Operation) Map(mapper func(Result) app.Event) app.Command { return op.inner.Map(mapper) }
func (op Operation) Ignore() app.Command                           { return op.inner.Ignore() }

// Get creates a typed read operation for a secret key.
func Get(key string) Operation {
	return Operation{inner: operation.New(func(ctx context.Context) Result {
		if len(key) == 0 {
			return Result{Err: ErrInvalid}
		}
		if len(key) > abi.EnvMaxKey {
			return Result{Err: ErrTooLarge}
		}
		if err := ctx.Err(); err != nil {
			return Result{Err: err}
		}
		value, found, err := legacy.Env(key)
		return Result{Value: value, Found: found, Err: normalize(err)}
	})}
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, legacy.ErrEnvUnavailable):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	case errors.Is(err, legacy.ErrEnvTooLarge):
		return fmt.Errorf("%w: %v", ErrTooLarge, err)
	default:
		return err
	}
}
