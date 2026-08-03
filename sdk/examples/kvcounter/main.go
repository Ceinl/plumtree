// Command kvcounter is a counter whose value persists through the KV
// capability: it loads its count on start and saves on every change, so the
// number survives reconnects and is shared across every session of the app. It
// is the canonical example of stateful Plumtree apps and runs unchanged
// natively (`go run .`) or hosted as WASM.
package main

import (
	"strconv"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/ui"
)

const countKey = "count"

type counter struct{ n int }
type loaded struct {
	value []byte
	err   error
}

func (c *counter) Init() app.Command {
	return kv.Get(countKey).Map(func(result kv.GetResult) app.Event {
		return loaded{value: result.Value, err: result.Err}
	})
}

func (c *counter) save() app.Command {
	return kv.Set(countKey, []byte(strconv.Itoa(c.n))).Ignore()
}

func (c *counter) Update(event app.Event) app.Command {
	switch event := event.(type) {
	case loaded:
		if event.err == nil {
			if value, err := strconv.Atoi(string(event.value)); err == nil {
				c.n = value
			}
		}
	case app.KeyEvent:
		switch event.Key {
		case app.KeyUp, '+', 'k':
			c.n++
			return c.save()
		case app.KeyDown, '-', 'j':
			c.n--
			return c.save()
		case 'q', app.KeyCtrlC:
			return app.Quit()
		}
	}
	return app.Noop()
}

func (c *counter) View() ui.Node {
	return ui.Column(
		ui.Textf("Count: %d", c.n),
		ui.Text("(↑/↓ change · q quits · value persists)").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&counter{}) }
