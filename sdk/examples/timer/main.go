// Command timer demonstrates one-shot and recurring asynchronous commands.
// The same source runs natively and hosted; completions always arrive through
// Update, so model state stays single-threaded.
package main

import (
	"fmt"
	"time"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type clock struct {
	started bool
	every   sdk.CommandID
	once    sdk.CommandID
	ticks   int
	fired   bool
}

func (c *clock) Update(event sdk.Event) {
	if !c.started {
		c.started = true
		c.every, _ = sdk.Schedule(sdk.Every(250 * time.Millisecond))
		c.once, _ = sdk.Schedule(sdk.After(time.Second))
	}

	switch event := event.(type) {
	case sdk.TimerMsg:
		switch event.ID {
		case c.every:
			c.ticks++
		case c.once:
			c.fired = true
		}
	case sdk.KeyMsg:
		if event.Key == 'q' || event.Key == sdk.KeyCtrlC {
			sdk.Quit()
		}
	}
}

func (c *clock) View() tui.Component {
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.JustifyContent(tui.JCenter)
	root.AlignItems(tui.ACenter)
	root.SetSize(tui.Grow, tui.Grow)
	root.AppendChild(components.NewText(fmt.Sprintf("ticks: %d", c.ticks)))
	root.AppendChild(components.NewText(fmt.Sprintf("one-shot fired: %t", c.fired)))
	root.AppendChild(components.NewText("(q quits)"))
	return root
}

func main() {
	sdk.RunTUI(&clock{}, sdk.Meta{Name: "timer", Type: "tui"})
}
