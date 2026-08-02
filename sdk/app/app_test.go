package app

import (
	"context"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type readyEvent struct{}
type tickEvent struct{}
type incrementEvent struct{}

type testModel struct {
	ready, ticks, increments int
	quit                     bool
}

func (model *testModel) Init() Command {
	return Task(func(_ context.Context) (Event, error) { return readyEvent{}, nil })
}

func (model *testModel) Update(event Event) Command {
	switch event.(type) {
	case readyEvent:
		model.ready++
	case tickEvent:
		model.ticks++
	case incrementEvent:
		model.increments++
	case KeyEvent:
		if event.(KeyEvent).Key == 'q' {
			model.quit = true
			return Quit(WithGoodbye("done"))
		}
	}
	return Noop()
}

func (model *testModel) Subscriptions() Subscription {
	return Every("clock", time.Second, tickEvent{})
}

func (model *testModel) View() ui.Node {
	return ui.Column(
		ui.Textf("ready=%d ticks=%d increments=%d", model.ready, model.ticks, model.increments),
		ui.Button("+", incrementEvent{}).Key("increment"),
	).Fill()
}

func TestRuntimeSerializesInitInputAndVirtualSubscriptions(t *testing.T) {
	model := &testModel{}
	runtime := NewRuntime(model, Viewport(40, 4))
	if err := runtime.Init(nil); err != nil {
		t.Fatal(err)
	}
	if model.ready != 1 {
		t.Fatalf("ready = %d, want 1", model.ready)
	}
	if err := runtime.Advance(2500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if model.ticks != 2 {
		t.Fatalf("ticks = %d, want 2", model.ticks)
	}
	if err := runtime.Dispatch(KeyEvent{Key: KeyEnter}); err != nil {
		t.Fatal(err)
	}
	if model.increments != 1 {
		t.Fatalf("increments = %d, want 1", model.increments)
	}
	if got := runtime.FocusKey(); got != "increment" {
		t.Fatalf("focus = %q", got)
	}
}

func TestWithCommandsAttachesWithoutInitializingRuntime(t *testing.T) {
	command := cli.Root("attached")
	runtime := NewRuntime(&testModel{}, WithCommands(command))
	got, attached := runtime.Commands()
	if !attached || got.Summary != "attached" {
		t.Fatalf("attached command = %#v, attached=%t", got, attached)
	}
	if runtime.Err() != nil {
		t.Fatal(runtime.Err())
	}
}

func TestRuntimeQuitCarriesGoodbyeAndStopsInput(t *testing.T) {
	model := &testModel{}
	runtime := NewRuntime(model)
	if err := runtime.Init(nil); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(KeyEvent{Key: 'q'}); err != nil {
		t.Fatal(err)
	}
	if !runtime.QuitRequested() || runtime.Goodbye().Text != "done" {
		t.Fatalf("quit=%t goodbye=%q", runtime.QuitRequested(), runtime.Goodbye().Text)
	}
	if err := runtime.Dispatch(KeyEvent{Key: 'q'}); err != ErrRuntimeStopped {
		t.Fatalf("dispatch after quit = %v", err)
	}
}

func TestRuntimeRejectsDuplicateSubscriptionKeys(t *testing.T) {
	model := &duplicateSubscriptionModel{}
	runtime := NewRuntime(model)
	if err := runtime.Init(nil); err == nil {
		t.Fatal("expected duplicate-key error")
	}
	if runtime.Err() == nil {
		t.Fatal("expected stored runtime error")
	}
}

func TestRuntimeCancelsReplacedSubscriptionsByStableKey(t *testing.T) {
	model := &cancelSubscriptionModel{enabled: true, canceled: make(chan struct{})}
	runtime := NewRuntime(model)
	if err := runtime.Init(nil); err != nil {
		t.Fatal(err)
	}
	model.enabled = false
	if err := runtime.Dispatch(struct{}{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.canceled:
	default:
		t.Fatal("subscription source was not canceled during reconciliation")
	}
}

type duplicateSubscriptionModel struct{}

func (*duplicateSubscriptionModel) Update(Event) Command { return Noop() }
func (*duplicateSubscriptionModel) View() ui.Node        { return ui.Text("duplicate") }
func (*duplicateSubscriptionModel) Subscriptions() Subscription {
	return Merge(Every("same", time.Second, tickEvent{}), Every("same", time.Second, tickEvent{}))
}

type cancelSubscriptionModel struct {
	enabled  bool
	canceled chan struct{}
}

func (model *cancelSubscriptionModel) Update(Event) Command { model.enabled = false; return Noop() }
func (*cancelSubscriptionModel) View() ui.Node              { return ui.Text("cancel") }
func (model *cancelSubscriptionModel) Subscriptions() Subscription {
	if !model.enabled {
		return nil
	}
	return Source("source", "source-v1", func(ctx context.Context, _ func(Event)) { <-ctx.Done(); close(model.canceled) })
}
