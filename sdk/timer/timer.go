// Package timer provides typed finite delays and declarative recurring timer
// subscriptions for the clean app lifecycle. Timers have no external
// authority, live only until completion or model cancellation, and use the
// app runtime's isolated clock/subscription lifetime on both adapters.
package timer

import (
	"context"
	"errors"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/internal/operation"
)

var ErrInvalid = errors.New("timer: invalid duration")

type Result struct{ Err error }

type Operation struct{ inner operation.Operation[Result] }

func (op Operation) Run(ctx context.Context) Result                { return op.inner.Run(ctx) }
func (op Operation) Map(mapper func(Result) app.Event) app.Command { return op.inner.Map(mapper) }
func (op Operation) Ignore() app.Command                           { return op.inner.Ignore() }

// After creates a finite, context-aware delay operation.
func After(delay time.Duration) Operation {
	return Operation{inner: operation.New(func(ctx context.Context) Result {
		if delay < 0 {
			return Result{Err: ErrInvalid}
		}
		if err := ctx.Err(); err != nil {
			return Result{Err: err}
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return Result{}
		case <-ctx.Done():
			return Result{Err: ctx.Err()}
		}
	})}
}

// Every declares a stable recurring event source. The app runtime owns
// cancellation and reconciliation; the returned subscription has no side
// effects until installed by a model.
func Every(key app.SubscriptionKey, interval time.Duration, event app.Event) app.Subscription {
	return app.Every(key, interval, event)
}
