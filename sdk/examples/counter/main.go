// Command counter is the canonical Plumtree TUI example: a number you move with
// the arrow keys. It builds against the SDK only and runs unchanged natively
// (`go run .`) or compiled to WASM and hosted by the platform.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type counter struct{ n int }

// Update handles input by mutating state.
func (c *counter) Update(ev sdk.Event) {
	k, ok := ev.(sdk.KeyMsg)
	if !ok {
		return
	}
	switch k.Key {
	case sdk.KeyUp, '+', 'k':
		c.n++
	case sdk.KeyDown, '-', 'j':
		c.n--
	case 'q', sdk.KeyCtrlC:
		sdk.Quit()
	}
}

// View builds the component tree for the current state each frame.
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

	var titleStyle tui.Style
	titleStyle.SetBackground(25, 23, 29)
	titleStyle.SetForeground(120, 200, 255)
	titleStyle.AddTextDecoration(tui.Bold)
	title := components.NewText("Plumtree counter")
	title.SetStyle(titleStyle)
	title.SetAlign(components.AlignCenter)

	count := components.NewText(fmt.Sprintf("Count: %d", c.n))
	count.SetAlign(components.AlignCenter)

	hint := components.NewText("(↑/↓ change · q quits)")
	hint.SetAlign(components.AlignCenter)

	root.AppendChild(title)
	root.AppendChild(count)
	root.AppendChild(hint)
	return root
}

func main() {
	sdk.RunTUI(&counter{}, sdk.Meta{Name: "counter", Type: "tui"})
}
