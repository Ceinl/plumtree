package config

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Run starts the composed components, waits for cancellation, and drains
// started components in reverse order within timeout. A canceled parent does
// not cancel the bounded drain context.
func (l *Lifecycle) Run(ctx context.Context, timeout time.Duration) error {
	if err := l.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	stopErr := StopWithin(l, context.Background(), timeout)
	if stopErr != nil {
		return errors.Join(ctx.Err(), stopErr)
	}
	return ctx.Err()
}

// RunWithSignals adapts conventional interrupt/termination signals to the
// same testable lifecycle path without touching global flags or environment.
func (l *Lifecycle) RunWithSignals(parent context.Context, timeout time.Duration) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return l.Run(ctx, timeout)
}
