package app

import (
	"context"
	"errors"
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

func TestCommandMayCallRuntimeWithoutDeadlocking(t *testing.T) {
	model := &callbackModel{}
	runtime := NewRuntime(model)
	model.runtime = runtime
	done := make(chan error, 1)
	go func() { done <- runtime.Init(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("command deadlocked while calling Runtime.Frame")
	}
}

type callbackModel struct{ runtime *Runtime }

func (model *callbackModel) Init() Command {
	return Task(func(context.Context) (Event, error) {
		_ = model.runtime.Frame()
		return nil, nil
	})
}
func (*callbackModel) Update(Event) Command { return Noop() }
func (*callbackModel) View() ui.Node        { return ui.Text("callback") }

func TestBatchQueuesResultsInDeclarationOrder(t *testing.T) {
	model := &batchModel{releaseFirst: make(chan struct{})}
	runtime := NewRuntime(model)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Init(ctx) }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := model.events; len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("events = %#v", got)
	}
}

type batchModel struct {
	events       []string
	releaseFirst chan struct{}
}

func (model *batchModel) Init() Command {
	return Batch(
		Task(func(ctx context.Context) (Event, error) {
			select {
			case <-model.releaseFirst:
				return "first", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}),
		Task(func(context.Context) (Event, error) {
			close(model.releaseFirst)
			return "second", nil
		}),
	)
}
func (model *batchModel) Update(event Event) Command {
	model.events = append(model.events, event.(string))
	return Noop()
}
func (*batchModel) View() ui.Node { return ui.Text("batch") }

func TestUnsupportedNamedKeyRemainsRaw(t *testing.T) {
	model := &rawKeyModel{}
	runtime := NewRuntime(model)
	if err := runtime.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Dispatch(KeyEvent{Key: KeyLeft}); err != nil {
		t.Fatal(err)
	}
	if model.key != KeyLeft {
		t.Fatalf("key = %v", model.key)
	}
}

type rawKeyModel struct{ key Key }

func (model *rawKeyModel) Update(event Event) Command {
	if key, ok := event.(KeyEvent); ok {
		model.key = key.Key
	}
	return Noop()
}
func (*rawKeyModel) View() ui.Node { return ui.Button("button", "semantic").Key("button") }

func TestRunErrReportsInvalidModel(t *testing.T) {
	if err := RunErr(nil); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("RunErr = %v", err)
	}
}

func TestRealTimeSubscriptionsTickAndStop(t *testing.T) {
	model := &realTimeModel{tick: make(chan struct{}, 1)}
	runtime := NewRuntime(model)
	runtime.enableRealTime()
	if err := runtime.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.tick:
	case <-time.After(time.Second):
		t.Fatal("real-time subscription did not tick")
	}
	runtime.Stop()
}

type realTimeModel struct{ tick chan struct{} }

func (model *realTimeModel) Update(event Event) Command {
	if _, ok := event.(tickEvent); ok {
		select {
		case model.tick <- struct{}{}:
		default:
		}
	}
	return Noop()
}
func (*realTimeModel) View() ui.Node { return ui.Text("timer") }
func (*realTimeModel) Subscriptions() Subscription {
	return Every("real-clock", time.Millisecond, tickEvent{})
}

func TestCanceledSourceCannotEmit(t *testing.T) {
	model := &lateSourceModel{enabled: true, ready: make(chan struct{}), emit: make(chan func(Event), 1)}
	runtime := NewRuntime(model)
	if err := runtime.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	emit := <-model.emit
	model.enabled = false
	if err := runtime.Dispatch(struct{}{}); err != nil {
		t.Fatal(err)
	}
	emit("late")
	if model.late != 0 {
		t.Fatalf("late events = %d", model.late)
	}
	runtime.Stop()
}

type lateSourceModel struct {
	enabled bool
	ready   chan struct{}
	emit    chan func(Event)
	late    int
}

func (model *lateSourceModel) Update(event Event) Command {
	if event == "late" {
		model.late++
	}
	return Noop()
}
func (*lateSourceModel) View() ui.Node { return ui.Text("source") }
func (model *lateSourceModel) Subscriptions() Subscription {
	if !model.enabled {
		return nil
	}
	return Source("late", "late-v1", func(ctx context.Context, emit func(Event)) {
		model.emit <- emit
		close(model.ready)
		<-ctx.Done()
	})
}

func (model *testModel) Init() Command {
	return Task(func(_ context.Context) (Event, error) { return readyEvent{}, nil })
}

func (model *testModel) Update(event Event) Command {
	switch event := event.(type) {
	case readyEvent:
		model.ready++
	case tickEvent:
		model.ticks++
	case incrementEvent:
		model.increments++
	case KeyEvent:
		if event.Key == 'q' {
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
	if err := runtime.Init(context.Background()); err != nil {
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
	command := cli.Root("attached", cli.New("run", "run command").
		WithFlag(cli.StringFlag("name", "name")).
		WithArgument(cli.StringArg("target", "target")))
	option := WithCommands(command)
	command.Subcommands[0].Flags[0].Name = "changed"
	command.Subcommands[0].Arguments[0].Name = "changed"
	runtime := NewRuntime(&testModel{}, option)
	got, attached := runtime.Commands()
	if !attached || got.Summary != "attached" {
		t.Fatalf("attached command = %#v, attached=%t", got, attached)
	}
	if got.Subcommands[0].Flags[0].Name != "name" || got.Subcommands[0].Arguments[0].Name != "target" {
		t.Fatalf("attached command shares caller slices: %#v", got.Subcommands[0])
	}
	got.Subcommands[0].Flags[0].Name = "returned"
	again, _ := runtime.Commands()
	if again.Subcommands[0].Flags[0].Name != "name" {
		t.Fatalf("returned command shares runtime slices: %#v", again.Subcommands[0])
	}
	if runtime.Err() != nil {
		t.Fatal(runtime.Err())
	}
}

func TestRuntimeQuitCarriesGoodbyeAndStopsInput(t *testing.T) {
	model := &testModel{}
	runtime := NewRuntime(model)
	if err := runtime.Init(context.Background()); err != nil {
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
	if err := runtime.Init(context.Background()); err == nil {
		t.Fatal("expected duplicate-key error")
	}
	if runtime.Err() == nil {
		t.Fatal("expected stored runtime error")
	}
}

func TestRuntimeCancelsReplacedSubscriptionsByStableKey(t *testing.T) {
	model := &cancelSubscriptionModel{enabled: true, canceled: make(chan struct{})}
	runtime := NewRuntime(model)
	if err := runtime.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	model.enabled = false
	if err := runtime.Dispatch(struct{}{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.canceled:
	case <-time.After(time.Second):
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
