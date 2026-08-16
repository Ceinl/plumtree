// Command counter is the canonical clean Plumtree app example.
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type counter struct{ n int }

func (c *counter) Update(event app.Event) app.Command {
	key, ok := event.(app.KeyEvent)
	if !ok {
		return app.Noop()
	}
	switch key.Key {
	case app.KeyUp, '+', 'k':
		c.n++
	case app.KeyDown, '-', 'j':
		c.n--
	case 'q', app.KeyCtrlC:
		return app.Quit()
	}
	return app.Noop()
}

func (c *counter) View() ui.Node {
	return ui.Column(
		ui.Text("Plumtree counter").Role(ui.Accent).Bold(),
		ui.Textf("Count: %d", c.n),
		ui.Text("(↑/↓ change · q quits)").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&counter{}) }
