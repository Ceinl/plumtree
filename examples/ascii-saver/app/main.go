// Command ascii-saver is a clean animated text example.
package main

import (
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/timer"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type saver struct{ frame int }
type shimmer struct{}

func (s *saver) Subscriptions() app.Subscription {
	return timer.Every("shimmer", 90*time.Millisecond, shimmer{})
}

func (s *saver) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case shimmer:
		s.frame++
	case app.KeyEvent:
		if value.Key == 'q' || value.Key == app.KeyEscape || value.Key == app.KeyCtrlC {
			return app.Quit()
		}
	}
	return app.Noop()
}

func (s *saver) View() ui.Node {
	return ui.Column(
		ui.Text("PLUMTREE NIGHT GARDEN").Role(ui.Accent).Bold(),
		ui.Textf("shimmer frame: %d", s.frame),
		ui.Text("ssh garden // q to disconnect").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&saver{}) }
