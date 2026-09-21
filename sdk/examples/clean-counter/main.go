// clean-counter demonstrates the additive interactive SDK surface. The
// existing sdk examples intentionally remain on the selected legacy surface
// until the ordered consumer cutover PR.
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type increment struct{}

type counter struct{ count int }

func (model *counter) Update(event app.Event) app.Command {
	switch event := event.(type) {
	case increment:
		model.count++
	case app.KeyEvent:
		if event.Key == 'q' {
			return app.Quit(app.WithGoodbye("Thanks for using clean-counter."))
		}
	}
	return app.Noop()
}

func (model *counter) View() ui.Node {
	return ui.Column(
		ui.Text("Clean counter").Role(ui.Accent).Bold(),
		ui.Textf("Count: %d", model.count),
		ui.Row(
			ui.Button("+", increment{}).Key("increment"),
			ui.Text("press q to quit").Role(ui.Muted),
		).Gap(1),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&counter{}) }
