// Package app defines the clean interactive Plumtree application lifecycle.
//
// An application owns mutable state in a Model. Update is serialized and is
// the only lifecycle callback that may change that state; View is a pure,
// ephemeral description of the current UI.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/sdk/ui"
)

// Event is an input or application-defined value delivered to Model.Update.
// Domain events are intentionally ordinary Go values; an app chooses the
// concrete types it wants to handle.
type Event any

// Model is the complete interactive application contract.
type Model interface {
	Update(Event) Command
	View() ui.Node
}

// Initializer may return finite startup work before the first rendered frame.
type Initializer interface {
	Init() Command
}

// Subscriber declares ongoing event sources from current model state.
type Subscriber interface {
	Subscriptions() Subscription
}

// Goodbye is the bounded post-session message requested by Quit.
type Goodbye struct {
	Text string
}

// QuitOption configures a quit command.
type QuitOption func(*quitOptions)

type quitOptions struct{ goodbye string }

// WithGoodbye attaches a bounded message to a quit command. The runtime
// delivers it only after the session has stopped accepting input.
func WithGoodbye(text string) QuitOption {
	return func(options *quitOptions) { options.goodbye = text }
}

// Quit returns a command that ends the session after the current update.
func Quit(options ...QuitOption) Command {
	var settings quitOptions
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	return Command{quit: true, goodbye: settings.goodbye}
}

// Noop returns a command with no effect.
func Noop() Command { return Command{} }

// Run starts an application using the native runtime. The runtime is kept
// small and deterministic; terminal adapters can feed the same Input values
// through NewRuntime in tests or a host integration.
func Run(model Model, options ...Option) {
	if err := RunErr(model, options...); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
}

// RunErr starts an application and returns initialization or platform errors.
func RunErr(model Model, options ...Option) error {
	runtime := NewRuntime(model, options...)
	runtime.enableRealTime()
	if err := runtime.Init(context.Background()); err != nil {
		return err
	}
	return runPlatform(runtime)
}

// Option configures a Runtime.
type Option func(*runtimeOptions)

type runtimeOptions struct {
	width  int
	height int
}

// Viewport supplies the initial render dimensions. Zero uses 80 by 24.
func Viewport(width, height int) Option {
	return func(options *runtimeOptions) {
		options.width, options.height = width, height
	}
}

var (
	// ErrInvalidModel reports a nil model or a nil view.
	ErrInvalidModel = errors.New("app: invalid model")
	// ErrRuntimeStopped reports input sent after a session has ended.
	ErrRuntimeStopped = errors.New("app: runtime stopped")
	// ErrPlatformUnsupported reports that app.Run has no platform event loop.
	ErrPlatformUnsupported = errors.New("app: platform run loop unsupported")
)

// Runtime is a deterministic serialized app runner. It is exported so the
// plumtest package and host integrations can drive the same lifecycle without
// reaching into UI or renderer internals.
type Runtime struct {
	mu sync.Mutex

	model  Model
	ctx    context.Context
	cancel context.CancelFunc

	width  int
	height int
	frame  ui.Frame
	focus  *ui.Focus

	initialized bool
	stopped     bool
	quit        bool
	goodbye     Goodbye
	lastErr     error

	queue     []Event
	commands  []Command
	subs      map[SubscriptionKey]SubscriptionSpec
	subCancel map[SubscriptionKey]context.CancelFunc
	done      chan struct{}
	doneOnce  sync.Once

	virtualTime timeState
	realTime    bool
}

// NewRuntime constructs a runtime without running model code.
func NewRuntime(model Model, options ...Option) *Runtime {
	settings := runtimeOptions{width: 80, height: 24}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	if settings.width <= 0 {
		settings.width = 80
	}
	if settings.height <= 0 {
		settings.height = 24
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		model: model, ctx: ctx, cancel: cancel,
		width: settings.width, height: settings.height,
		focus: ui.NewFocus(), subs: make(map[SubscriptionKey]SubscriptionSpec), subCancel: make(map[SubscriptionKey]context.CancelFunc),
		done: make(chan struct{}),
	}
}

// Init runs optional initialization, reconciles subscriptions, and renders.
func (r *Runtime) Init(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return r.initLocked(ctx)
}

// Dispatch delivers one event after initialization. Input consumed by the UI
// becomes a semantic application event; handled input is never also delivered
// as a raw event.
func (r *Runtime) Dispatch(event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.initialized {
		if err := r.initLocked(context.Background()); err != nil {
			return err
		}
	}
	if r.stopped {
		return ErrRuntimeStopped
	}
	if resize, ok := event.(ResizeEvent); ok {
		if resize.Width > 0 {
			r.width = resize.Width
		}
		if resize.Height > 0 {
			r.height = resize.Height
		}
	}
	if semantic, handled := ui.HandleFrame(r.frame, toUIInput(event), r.focus); handled {
		if semantic != nil {
			r.queue = append(r.queue, semantic)
		}
	} else {
		r.queue = append(r.queue, event)
	}
	r.processQueueLocked()
	return r.lastErr
}

func (r *Runtime) initLocked(ctx context.Context) error {
	if r.initialized {
		return r.lastErr
	}
	if r.model == nil {
		return r.failLocked(ErrInvalidModel)
	}
	r.initialized = true
	if initializer, ok := r.model.(Initializer); ok {
		r.commands = append(r.commands, initializer.Init())
	}
	r.runCommandsLocked(ctx)
	r.processQueueLocked()
	r.reconcileLocked()
	r.renderLocked()
	return r.lastErr
}

func (r *Runtime) processQueueLocked() {
	for len(r.queue) > 0 && !r.stopped {
		event := r.queue[0]
		r.queue = r.queue[1:]
		command := r.model.Update(event)
		r.commands = append(r.commands, command)
		r.runCommandsLocked(r.ctx)
		r.reconcileLocked()
		r.renderLocked()
	}
}

// Frame returns the latest structured frame copy.
func (r *Runtime) Frame() ui.Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Clone()
}

// Model returns the application model for native test integrations. Callers
// must only inspect it outside Update; mutating it breaks the lifecycle rules.
func (r *Runtime) Model() Model { return r.model }

// Context returns the runtime lifetime context.
func (r *Runtime) Context() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ctx
}

// Stopped reports whether the runtime has stopped.
func (r *Runtime) Stopped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

// Advance moves the deterministic clock and queues due timer events. It never
// sleeps, making virtual-time tests independent of wall-clock scheduling.
func (r *Runtime) Advance(duration time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return ErrRuntimeStopped
	}
	r.virtualTime.advance(duration, r.subs, &r.queue)
	r.processQueueLocked()
	return r.lastErr
}

// QuitRequested reports whether a quit command has ended input processing.
func (r *Runtime) QuitRequested() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.quit
}

// Goodbye returns the post-session message, if any.
func (r *Runtime) Goodbye() Goodbye {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.goodbye
}

// FocusKey returns the stable UI key currently focused.
func (r *Runtime) FocusKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.focus.Key()
}

// Err returns the first runtime or command error.
func (r *Runtime) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// Done closes when the runtime stops.
func (r *Runtime) Done() <-chan struct{} { return r.done }

// Stop cancels commands and subscriptions without emitting a goodbye.
func (r *Runtime) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

func (r *Runtime) enableRealTime() {
	r.mu.Lock()
	r.realTime = true
	r.mu.Unlock()
}

func (r *Runtime) stopLocked() {
	if r.stopped {
		return
	}
	r.stopped = true
	r.cancel()
	r.doneOnce.Do(func() { close(r.done) })
}

func (r *Runtime) failLocked(err error) error {
	if r.lastErr == nil {
		r.lastErr = err
	}
	r.stopLocked()
	return r.lastErr
}

func (r *Runtime) renderLocked() {
	if r.model == nil {
		return
	}
	node := r.model.View()
	if node == nil {
		_ = r.failLocked(ErrInvalidModel)
		return
	}
	r.frame = ui.Render(node, r.width, r.height)
	ui.ReconcileFocus(r.frame.Root(), r.focus)
}

func toUIInput(event Event) ui.Input {
	switch value := event.(type) {
	case KeyEvent:
		var key ui.Key
		switch value.Key {
		case KeyEnter:
			key = ui.KeyEnter
		case KeyEscape:
			key = ui.KeyEscape
		case KeyTab:
			key = ui.KeyTab
		case KeySpace:
			key = ui.KeySpace
		default:
			if value.Key < 0 {
				return ui.Input{}
			}
			key = ui.Key(value.Key)
		}
		return ui.KeyInput{Kind: ui.KeyInputKind, Key: key, Shift: value.Shift, Ctrl: value.Ctrl, Alt: value.Alt}
	case MouseEvent:
		return ui.MouseInput{Kind: ui.MouseInputKind, X: value.X, Y: value.Y, Button: uint8(value.Button), Action: uint8(value.Action)}
	case PasteEvent:
		return ui.PasteInput{Kind: ui.PasteInputKind, Text: value.Text}
	default:
		return ui.Input{}
	}
}
