// Package plumtest provides deterministic, in-process tests for interactive
// Plumtree models. It does not mutate process argv/stdio, sleep, spawn a
// subprocess, or contact an external service.
package plumtest

import (
	"bytes"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type settings struct {
	width, height int
	kv            map[string][]byte
}

// Option configures Start.
type Option func(*settings)

// Viewport sets deterministic render dimensions.
func Viewport(width, height int) Option {
	return func(options *settings) { options.width, options.height = width, height }
}

// KV seeds an isolated fake key/value fixture. The fixture is available to
// tests through GetKV/SetKV until the typed capability packages are added.
func KV(key string, value []byte) Option {
	return func(options *settings) {
		if options.kv == nil {
			options.kv = map[string][]byte{}
		}
		options.kv[key] = append([]byte(nil), value...)
	}
}

// Runtime wraps app.Runtime with test assertions and isolated fixtures.
type Runtime struct {
	t   testing.TB
	app *app.Runtime
	kv  map[string][]byte
}

// Start initializes model and returns a deterministic test runtime.
func Start(t testing.TB, model app.Model, options ...Option) *Runtime {
	t.Helper()
	settings := settings{width: 80, height: 24, kv: map[string][]byte{}}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	runtime := &Runtime{t: t, app: app.NewRuntime(model, app.Viewport(settings.width, settings.height)), kv: settings.kv}
	if err := runtime.app.Init(nil); err != nil {
		t.Fatalf("plumtest start: %v", err)
	}
	return runtime
}

// Send delivers an app-defined event.
func (runtime *Runtime) Send(event app.Event) {
	runtime.t.Helper()
	runtime.require(runtime.app.Dispatch(event))
}

// Key delivers an unhandled keyboard event.
func (runtime *Runtime) Key(key app.Key) { runtime.t.Helper(); runtime.Send(app.KeyEvent{Key: key}) }

// KeyWithModifiers delivers keyboard input with modifiers.
func (runtime *Runtime) KeyWithModifiers(key app.Key, shift, ctrl, alt bool) {
	runtime.t.Helper()
	runtime.Send(app.KeyEvent{Key: key, Shift: shift, Ctrl: ctrl, Alt: alt})
}

// Mouse delivers pointer input at zero-based cell coordinates.
func (runtime *Runtime) Mouse(x, y int, button app.MouseButton, action app.MouseAction) {
	runtime.t.Helper()
	runtime.Send(app.MouseEvent{X: x, Y: y, Button: button, Action: action})
}

// Paste delivers bounded pasted text.
func (runtime *Runtime) Paste(text string) {
	runtime.t.Helper()
	runtime.Send(app.PasteEvent{Text: text})
}

// Resize delivers a resize event and updates the deterministic viewport.
func (runtime *Runtime) Resize(width, height int) {
	runtime.t.Helper()
	runtime.Send(app.ResizeEvent{Width: width, Height: height})
}

// Advance moves virtual time without sleeping.
func (runtime *Runtime) Advance(duration time.Duration) {
	runtime.t.Helper()
	runtime.require(runtime.app.Advance(duration))
}

// View returns the latest structured frame.
func (runtime *Runtime) View() ui.Frame { runtime.t.Helper(); return runtime.app.Frame() }

// ExpectText asserts that visible text contains value.
func (runtime *Runtime) ExpectText(value string) {
	runtime.t.Helper()
	if !runtime.View().ContainsText(value) {
		runtime.t.Fatalf("expected visible text %q, got:\n%s", value, runtime.View().Text())
	}
}

// ExpectNoText asserts that visible text does not contain value.
func (runtime *Runtime) ExpectNoText(value string) {
	runtime.t.Helper()
	if runtime.View().ContainsText(value) {
		runtime.t.Fatalf("did not expect visible text %q, got:\n%s", value, runtime.View().Text())
	}
}

// ClickButton activates a button by semantic label through the same event
// queue used by keyboard and mouse interactions.
func (runtime *Runtime) ClickButton(label string) {
	runtime.t.Helper()
	event, ok := ui.ButtonEvent(runtime.View().Root(), label)
	if !ok {
		runtime.t.Fatalf("button %q not found", label)
	}
	runtime.Send(event)
}

// ExpectFocus asserts the currently focused stable key.
func (runtime *Runtime) ExpectFocus(key string) {
	runtime.t.Helper()
	if got := runtime.app.FocusKey(); got != key {
		runtime.t.Fatalf("focus = %q, want %q", got, key)
	}
}

// ExpectQuit asserts that the model returned a quit command.
func (runtime *Runtime) ExpectQuit() {
	runtime.t.Helper()
	if !runtime.app.QuitRequested() {
		runtime.t.Fatal("expected runtime to quit")
	}
}

// ExpectGoodbye asserts the bounded post-session message.
func (runtime *Runtime) ExpectGoodbye(text string) {
	runtime.t.Helper()
	if got := runtime.app.Goodbye().Text; got != text {
		runtime.t.Fatalf("goodbye = %q, want %q", got, text)
	}
}

// ExpectError asserts a runtime error by message.
func (runtime *Runtime) ExpectError(text string) {
	runtime.t.Helper()
	if err := runtime.app.Err(); err == nil || err.Error() != text {
		runtime.t.Fatalf("runtime error = %v, want %q", err, text)
	}
}

// GetKV reads an isolated fixture copy.
func (runtime *Runtime) GetKV(key string) []byte {
	runtime.t.Helper()
	return append([]byte(nil), runtime.kv[key]...)
}

// SetKV writes an isolated fixture copy.
func (runtime *Runtime) SetKV(key string, value []byte) {
	runtime.t.Helper()
	runtime.kv[key] = append([]byte(nil), value...)
}

// ExpectKV asserts an isolated fixture value.
func (runtime *Runtime) ExpectKV(key string, value []byte) {
	runtime.t.Helper()
	if got := runtime.GetKV(key); !bytes.Equal(got, value) {
		runtime.t.Fatalf("KV[%q] = %q, want %q", key, got, value)
	}
}

func (runtime *Runtime) require(err error) {
	if err != nil {
		runtime.t.Fatalf("plumtest runtime: %v", err)
	}
}
