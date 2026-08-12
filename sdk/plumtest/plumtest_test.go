package plumtest

import (
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type increment struct{}
type tick struct{}

type counter struct{ count, ticks int }

func (counter *counter) Update(event app.Event) app.Command {
	switch event := event.(type) {
	case increment:
		counter.count++
	case tick:
		counter.ticks++
	case app.KeyEvent:
		if event.Key == 'q' {
			return app.Quit(app.WithGoodbye("finished"))
		}
	}
	return app.Noop()
}

func (counter *counter) Subscriptions() app.Subscription {
	return app.Every("clock", time.Second, tick{})
}

func (counter *counter) View() ui.Node {
	return ui.Column(
		ui.Textf("count=%d ticks=%d", counter.count, counter.ticks),
		ui.Button("+", increment{}).Key("increment"),
	).Fill()
}

func TestStartDrivesModelWithoutSleepsOrGlobalState(t *testing.T) {
	model := &counter{}
	runtime := Start(t, model, Viewport(32, 3), KV("count", []byte("4")))
	runtime.ExpectText("count=0 ticks=0")
	runtime.ClickButton("+")
	runtime.ExpectText("count=1 ticks=0")
	runtime.Advance(2200 * time.Millisecond)
	runtime.ExpectText("count=1 ticks=2")
	runtime.ExpectKV("count", []byte("4"))
	runtime.Key('q')
	runtime.ExpectQuit()
	runtime.ExpectGoodbye("finished")
}
