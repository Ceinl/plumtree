// Package operation contains the small execution primitive shared by typed
// capability packages. It has no registry, string dispatch, or wire payload;
// each capability owns its operation, result, validation, and adapter.
package operation

import (
	"context"

	"github.com/Ceinl/plumtree/sdk/app"
)

// Operation is an inert typed finite operation. The capability package that
// returns it owns the concrete result and error contract.
type Operation[T any] struct {
	run func(context.Context) T
}

// New constructs an inert typed operation.
func New[T any](run func(context.Context) T) Operation[T] { return Operation[T]{run: run} }

// Run executes the operation with cancellation supplied by the caller.
func (operation Operation[T]) Run(ctx context.Context) T {
	if ctx == nil {
		ctx = context.Background()
	}
	return operation.run(ctx)
}

// Map converts the typed completion into one app event. The operation itself
// remains inert until the command is returned from Init or Update.
func (operation Operation[T]) Map(mapper func(T) app.Event) app.Command {
	if mapper == nil {
		return app.Noop()
	}
	return app.Task(func(ctx context.Context) (app.Event, error) {
		return mapper(operation.Run(ctx)), nil
	})
}

// Ignore explicitly discards the typed result.
func (operation Operation[T]) Ignore() app.Command {
	return app.Task(func(ctx context.Context) (app.Event, error) {
		operation.Run(ctx)
		return nil, nil
	})
}
