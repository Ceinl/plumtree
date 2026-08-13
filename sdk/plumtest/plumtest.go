// Package plumtest provides deterministic, in-process tests for interactive
// Plumtree models. It does not mutate process argv/stdio, sleep, spawn a
// subprocess, or contact an external service.
package plumtest

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type settings struct {
	width, height int
	kv            map[string][]byte
	cliArgs       []string
	cliShell      string
	cliStdin      []byte
	cliContext    context.Context
}

// Option configures Start.
type Option func(*settings)

// Viewport sets deterministic render dimensions.
func Viewport(width, height int) Option {
	return func(options *settings) { options.width, options.height = width, height }
}

// KV seeds an isolated fake key/value fixture. Interactive tests inspect it
// through GetKV/SetKV; InvokeCLI also injects it into typed kv operations.
func KV(key string, value []byte) Option {
	return func(options *settings) {
		if options.kv == nil {
			options.kv = map[string][]byte{}
		}
		options.kv[key] = append([]byte(nil), value...)
	}
}

// Args supplies exact argv to InvokeCLI without touching process globals.
func Args(args ...string) Option {
	return func(options *settings) { options.cliArgs = append([]string(nil), args...) }
}

// Shell supplies a bounded shell-style command string to InvokeCLI.
func Shell(command string) Option { return func(options *settings) { options.cliShell = command } }

// Stdin supplies binary-safe invocation input to InvokeCLI.
func Stdin(data []byte) Option {
	return func(options *settings) { options.cliStdin = append([]byte(nil), data...) }
}

// CLIContext supplies cancellation and a deadline to InvokeCLI.
func CLIContext(ctx context.Context) Option {
	return func(options *settings) { options.cliContext = ctx }
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

// CLIRuntime wraps one deterministic finite command invocation. It uses the
// same Option type as Start so Args, Shell, Stdin, KV, and a context can be
// composed without global argv/stdio mutation.
type CLIRuntime struct {
	t         testing.TB
	execution cli.Execution
	stdout    bytes.Buffer
	stderr    bytes.Buffer
	kv        *kv.Memory
}

// InvokeCLI validates and runs a command tree in-process.
func InvokeCLI(t testing.TB, command cli.Command, options ...Option) *CLIRuntime {
	t.Helper()
	settings := settings{cliContext: context.Background(), kv: map[string][]byte{}}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	args := settings.cliArgs
	if settings.cliShell != "" {
		lexed, err := cli.Lex(settings.cliShell)
		if err != nil {
			t.Fatalf("plumtest CLI lexer: %v", err)
		}
		args = lexed
	}
	runtime := &CLIRuntime{t: t, kv: kv.NewMemory(settings.kv)}
	cliContext := kv.WithAdapter(settings.cliContext, runtime.kv)
	runtime.execution = cli.Execute(cliContext, command, args, cli.Streams{
		Stdin: bytes.NewReader(settings.cliStdin), Stdout: &runtime.stdout, Stderr: &runtime.stderr,
	})
	return runtime
}

// ExpectExit asserts the stable command exit status.
func (runtime *CLIRuntime) ExpectExit(code int) {
	runtime.t.Helper()
	if runtime.execution.ExitCode != code {
		runtime.t.Fatalf("CLI exit = %d, want %d (stdout=%q stderr=%q)", runtime.execution.ExitCode, code, runtime.stdout.String(), runtime.stderr.String())
	}
}

// ExpectText asserts the complete human stdout result.
func (runtime *CLIRuntime) ExpectText(text string) {
	runtime.t.Helper()
	if runtime.stdout.String() != text {
		runtime.t.Fatalf("CLI stdout = %q, want %q", runtime.stdout.String(), text)
	}
}

// ExpectNoText asserts that human stdout does not contain text.
func (runtime *CLIRuntime) ExpectNoText(text string) {
	runtime.t.Helper()
	if strings.Contains(runtime.stdout.String(), text) {
		runtime.t.Fatalf("CLI stdout unexpectedly contains %q: %q", text, runtime.stdout.String())
	}
}

// ExpectStderr asserts the complete bounded diagnostic stream.
func (runtime *CLIRuntime) ExpectStderr(text string) {
	runtime.t.Helper()
	if runtime.stderr.String() != text {
		runtime.t.Fatalf("CLI stderr = %q, want %q", runtime.stderr.String(), text)
	}
}

// ExpectJSON compares the JSON result body against one typed value.
func (runtime *CLIRuntime) ExpectJSON(value any) {
	runtime.t.Helper()
	var got any
	if err := json.Unmarshal(runtime.stdout.Bytes(), &got); err != nil {
		runtime.t.Fatalf("CLI stdout is not JSON: %v (%q)", err, runtime.stdout.String())
	}
	wantBytes, err := json.Marshal(map[string]any{"ok": true, "result": value})
	if err != nil {
		runtime.t.Fatalf("marshal expected JSON: %v", err)
	}
	var want any
	if err := json.Unmarshal(wantBytes, &want); err != nil {
		runtime.t.Fatalf("marshal expected JSON body: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		runtime.t.Fatalf("CLI JSON = %s, want %s", runtime.stdout.Bytes(), wantBytes)
	}
}

// ExpectUsage asserts generated help or usage text was emitted.
func (runtime *CLIRuntime) ExpectUsage() {
	runtime.t.Helper()
	if !strings.Contains(runtime.stdout.String()+runtime.stderr.String(), "Usage:") {
		runtime.t.Fatalf("CLI output has no usage text: stdout=%q stderr=%q", runtime.stdout.String(), runtime.stderr.String())
	}
}

// Stdout and Stderr expose copies for focused assertions.
func (runtime *CLIRuntime) Stdout() string { return runtime.stdout.String() }
func (runtime *CLIRuntime) Stderr() string { return runtime.stderr.String() }

// GetKV reads the isolated CLI KV fixture.
func (runtime *CLIRuntime) GetKV(key string) []byte { return runtime.kv.Value(key) }

// ExpectKV asserts an isolated CLI KV fixture value.
func (runtime *CLIRuntime) ExpectKV(key string, value []byte) {
	runtime.t.Helper()
	if got := runtime.GetKV(key); !bytes.Equal(got, value) {
		runtime.t.Fatalf("CLI KV[%q] = %q, want %q", key, got, value)
	}
}
