// Command mousebutton is a hosted-loop fixture for clean semantic buttons.
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type clicked struct{}

type model struct {
	clicked int
}

func (m *model) Update(event app.Event) app.Command {
	switch event := event.(type) {
	case clicked:
		m.clicked++
	case app.KeyEvent:
		if event.Key == 'q' || event.Key == app.KeyCtrlC {
			return app.Quit()
		}
	}
	return app.Noop()
}

func (m *model) View() ui.Node {
	return ui.Column(
		ui.Textf("clicked=%d", m.clicked),
		ui.Button("click", clicked{}).Key("click"),
	).Fill().Gap(1)
}

func main() { app.Run(&model{}) }
