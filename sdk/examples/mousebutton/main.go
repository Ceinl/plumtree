// Command mousebutton is a hosted-loop fixture proving SDK buttons receive
// automatic mouse clicks while Model.Update still observes MouseMsg.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type model struct {
	clicked int
	events  int
}

func (m *model) Update(ev sdk.Event) {
	switch e := ev.(type) {
	case sdk.MouseMsg:
		m.events++
	case sdk.KeyMsg:
		if e.Key == 'q' || e.Key == sdk.KeyCtrlC {
			sdk.Quit()
		}
	}
}

func (m *model) View() tui.Component {
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.SetSize(tui.Grow, tui.Grow)
	root.AppendChild(components.NewText(fmt.Sprintf("clicked=%d events=%d", m.clicked, m.events)))
	button := components.NewButton("click")
	button.OnClick = func() { m.clicked++ }
	root.AppendChild(button)
	return root
}

func main() { sdk.RunTUI(&model{}, sdk.Meta{Name: "mousebutton", Type: "tui"}) }
