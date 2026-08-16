// Command timer demonstrates finite and recurring clean SDK timers.
package main

import (
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/timer"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type clock struct {
	ticks int
	fired bool
}

type tick struct{}
type fired struct{}

func (c *clock) Init() app.Command {
	return timer.After(time.Second).Map(func(timer.Result) app.Event { return fired{} })
}

func (c *clock) Subscriptions() app.Subscription {
	return timer.Every("clock", 250*time.Millisecond, tick{})
}

func (c *clock) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case tick:
		c.ticks++
	case fired:
		c.fired = true
	case app.KeyEvent:
		if value.Key == 'q' || value.Key == app.KeyCtrlC {
			return app.Quit()
		}
	}
	return app.Noop()
}

func (c *clock) View() ui.Node {
	return ui.Column(
		ui.Textf("ticks: %d", c.ticks),
		ui.Textf("one-shot fired: %t", c.fired),
		ui.Text("(q quits)"),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&clock{}) }
