// Command goodbye-tui is a Plumtree TUI app.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type model struct{ n int }

// Update handles input by mutating state.
func (m *model) Update(ev sdk.Event) {
	if k, ok := ev.(sdk.KeyMsg); ok {
		switch k.Key {
		case sdk.KeyUp, '+':
			m.n++
		case sdk.KeyDown, '-':
			m.n--
		case 'q', sdk.KeyCtrlC:
			sdk.SetGoodbye("Thanks for playing! Final count: " + fmt.Sprint(m.n))
			sdk.Quit()
		}
	}
}

// View builds the component tree from state each frame.
func (m *model) View() tui.Component {
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.JustifyContent(tui.JCenter)
	root.AlignItems(tui.ACenter)
	root.SetSize(tui.Grow, tui.Grow)

	count := components.NewText(fmt.Sprintf("Count: %d", m.n))
	count.SetAlign(components.AlignCenter)

	hint := components.NewText("(↑/↓ to change, q to quit)")
	hint.SetAlign(components.AlignCenter)

	root.AppendChild(count)
	root.AppendChild(hint)
	return root
}

func main() {
	sdk.RunTUI(&model{}, sdk.Meta{Name: "goodbye-tui", Type: "tui"})
}
