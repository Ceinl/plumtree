package sdk

import (
	"errors"
	"time"
)

// Command describes asynchronous work whose completion is delivered through
// Model.Update. Commands are inert until passed to Schedule. The abstraction is
// intentionally small today—timers are its first effect—and leaves room for
// future fetch, KV, and other non-blocking capabilities without changing the
// Update -> View model.
type Command struct {
	delay     time.Duration
	recurring bool
}

// CommandID identifies a scheduled command for TimerMsg and Cancel.
type CommandID uint32

var (
	// ErrInvalidCommand means a duration is outside the supported range.
	ErrInvalidCommand = errors.New("sdk: invalid command")
	// ErrCommandLimit means the session already has the maximum active commands.
	ErrCommandLimit = errors.New("sdk: command limit reached")
	// ErrCommandUnavailable means the host could not schedule the command.
	ErrCommandUnavailable = errors.New("sdk: command capability unavailable")
)

// After returns a command that fires once after delay.
func After(delay time.Duration) Command {
	return Command{delay: delay}
}

// Every returns a command that fires repeatedly at interval until canceled or
// the session ends.
func Every(interval time.Duration) Command {
	return Command{delay: interval, recurring: true}
}

// Schedule starts cmd and returns the ID carried by its TimerMsg completions.
// A session may have at most 64 active commands. One-shot delays must be at
// least 1 ms; recurring intervals at least 10 ms; both are capped at 24 hours.
func Schedule(cmd Command) (CommandID, error) {
	return scheduleCommand(cmd)
}

// Cancel stops future completions from a command. A completion already selected
// by the event loop may still arrive. It reports whether the command was active.
func Cancel(id CommandID) bool {
	if id == 0 {
		return false
	}
	return cancelCommand(id)
}
