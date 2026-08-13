// Package identity provides the typed identity operation for the connected
// session. Authority is the authenticated session; the result lives for one
// operation and is an immutable copy. The native adapter supplies a stable
// local identity, while the isolated hosted adapter returns the verified
// session identity or ErrUnavailable.
package identity

import (
	"context"
	"errors"
	"fmt"

	legacy "github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var ErrUnavailable = errors.New("identity: capability unavailable")

// Kind identifies how the session was authenticated.
type Kind = legacy.IdentityKind

const (
	KindSSHKey    = legacy.IdentitySSHKey
	KindAnonymous = legacy.IdentityAnonymous
)

// Identity is an immutable copy of the connected session identity.
type Identity = legacy.Identity

type Result struct {
	Identity
	Err error
}

// Operation is an inert identity lookup.
type Operation struct{ inner operation.Operation[Result] }

func (op Operation) Run(ctx context.Context) Result                { return op.inner.Run(ctx) }
func (op Operation) Map(mapper func(Result) app.Event) app.Command { return op.inner.Map(mapper) }
func (op Operation) Ignore() app.Command                           { return op.inner.Ignore() }

// Whoami creates a lookup operation.
func Whoami() Operation {
	return Operation{inner: operation.New(func(ctx context.Context) Result {
		if err := ctx.Err(); err != nil {
			return Result{Err: err}
		}
		value, err := legacy.Whoami()
		return Result{Identity: value, Err: normalize(err)}
	})}
}

func normalize(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, legacy.ErrAuthUnavailable) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}
