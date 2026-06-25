// Command kvcounter is a counter whose value persists through the KV
// capability: it loads its count on start and saves on every change, so the
// number survives reconnects and is shared across every session of the app. It
// is the canonical example of stateful Plumtree apps and runs unchanged
// natively (`go run .`) or hosted as WASM.
package main

import (
	"fmt"
	"strconv"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

const countKey = "count"

type counter struct{ n int }

// load reads the persisted count, defaulting to 0 when unset or unreadable.
func (c *counter) load() {
	if v, ok, err := sdk.KVGet(countKey); ok && err == nil {
		if n, err := strconv.Atoi(string(v)); err == nil {
			c.n = n
		}
	}
}

// save persists the current count, ignoring storage errors so the UI stays
// responsive even if the capability is unavailable.
func (c *counter) save() {
	_ = sdk.KVSet(countKey, []byte(strconv.Itoa(c.n)))
}

func (c *counter) Update(ev sdk.Event) {
	k, ok := ev.(sdk.KeyMsg)
	if !ok {
		return
	}
	switch k.Key {
	case sdk.KeyUp, '+', 'k':
		c.n++
		c.save()
	case sdk.KeyDown, '-', 'j':
		c.n--
		c.save()
	case 'q', sdk.KeyCtrlC:
		sdk.Quit()
	}
}

func (c *counter) View() tui.Component {
	var bg tui.Style
	bg.SetBackground(25, 23, 29)
	bg.SetForeground(200, 200, 200)

	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.JustifyContent(tui.JCenter)
	root.AlignItems(tui.ACenter)
	root.SetSize(tui.Grow, tui.Grow)
	root.SetStyle(bg)

	count := components.NewText(fmt.Sprintf("Count: %d", c.n))
	count.SetAlign(components.AlignCenter)

	hint := components.NewText("(↑/↓ change · q quits · value persists)")
	hint.SetAlign(components.AlignCenter)

	root.AppendChild(count)
	root.AppendChild(hint)
	return root
}

func main() {
	c := &counter{}
	c.load()
	sdk.RunTUI(c, sdk.Meta{Name: "kvcounter", Type: "tui"})
}
