package app

import (
	"context"
	"sync"
)

// Command is one finite effect. Commands are inert until returned from Init
// or Update. The operation emits at most one app event; a nil event is ignored.
type Command struct {
	run      func(context.Context) (Event, error)
	children []Command
	quit     bool
	goodbye  string
}

// Task wraps a finite operation that returns an app event on completion.
// Capability packages can build typed operations on top of this primitive
// without exposing runtime correlation IDs to app authors.
func Task(run func(context.Context) (Event, error)) Command {
	if run == nil {
		return Noop()
	}
	return Command{run: run}
}

// Batch starts independent finite commands together. Completion events are
// queued in completion order; dependent work should be returned by the event's
// Update method instead.
func Batch(commands ...Command) Command {
	filtered := make([]Command, 0, len(commands))
	for _, command := range commands {
		if !command.empty() {
			filtered = append(filtered, command)
		}
	}
	if len(filtered) == 0 {
		return Noop()
	}
	return Command{children: filtered}
}

// CommandFunc is a convenient named operation for tests and native adapters.
type CommandFunc func(context.Context) (Event, error)

func (function CommandFunc) Command() Command { return Task(function) }

func (command Command) empty() bool {
	return command.run == nil && len(command.children) == 0 && !command.quit
}

func (r *Runtime) runCommandsLocked(ctx context.Context) {
	for len(r.commands) > 0 && !r.stopped {
		current := r.commands[0]
		r.commands = r.commands[1:]
		r.runCommandLocked(ctx, current)
	}
}

func (r *Runtime) runCommandLocked(ctx context.Context, current Command) {
	if current.quit {
		r.quit = true
		r.goodbye = Goodbye{Text: current.goodbye}
		r.stopLocked()
		return
	}
	if len(current.children) > 0 {
		type result struct {
			event Event
			err   error
		}
		results := make(chan result, len(current.children))
		var group sync.WaitGroup
		for _, child := range current.children {
			child := child
			group.Add(1)
			go func() {
				defer group.Done()
				event, err := child.runResult(ctx)
				results <- result{event: event, err: err}
			}()
		}
		group.Wait()
		close(results)
		for result := range results {
			if result.err != nil {
				r.failLocked(result.err)
				return
			}
			if result.event != nil {
				r.queue = append(r.queue, result.event)
			}
		}
		return
	}
	event, err := current.runResult(ctx)
	if err != nil {
		r.failLocked(err)
		return
	}
	if event != nil {
		r.queue = append(r.queue, event)
	}
}

func (command Command) runResult(ctx context.Context) (Event, error) {
	if command.run == nil {
		return nil, nil
	}
	return command.run(ctx)
}
