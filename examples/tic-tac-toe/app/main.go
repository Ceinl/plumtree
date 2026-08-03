// Tic-tac-toe is a clean SDK example with durable score state.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type game struct {
	user  string
	score int
}

type userLoaded struct{ result identity.Result }
type scoreLoaded struct{ result kv.GetResult }

func (g *game) Init() app.Command {
	return app.Batch(
		identity.Whoami().Map(func(result identity.Result) app.Event { return userLoaded{result: result} }),
		kv.Get("ttt/score").Map(func(result kv.GetResult) app.Event { return scoreLoaded{result: result} }),
	)
}

func (g *game) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case userLoaded:
		if value.result.Err == nil {
			g.user = value.result.User
		}
	case scoreLoaded:
		if value.result.Err == nil && value.result.Found {
			_, _ = fmt.Sscanf(string(value.result.Value), "%d", &g.score)
		}
	case app.KeyEvent:
		switch value.Key {
		case 'w':
			g.score++
			return kv.Set("ttt/score", []byte(fmt.Sprint(g.score))).Ignore()
		case 'q', app.KeyCtrlC:
			return app.Quit()
		}
	}
	return app.Noop()
}

func (g *game) View() ui.Node {
	return ui.Column(
		ui.Text("Tic-tac-toe").Role(ui.Accent).Bold(),
		ui.Textf("player: %s", g.user),
		ui.Textf("score: %d", g.score),
		ui.Text("press w to win · q quits").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&game{}) }
