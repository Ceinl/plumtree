// Agentboard is the canonical clean SDK capability example.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type board struct {
	user  string
	count int
}

type identified struct{ result identity.Result }
type loaded struct{ result kv.ListResult }

func (b *board) Init() app.Command {
	return app.Batch(
		identity.Whoami().Map(func(result identity.Result) app.Event { return identified{result: result} }),
		kv.List("boards/", 32).Map(func(result kv.ListResult) app.Event { return loaded{result: result} }),
	)
}

func (b *board) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case identified:
		if value.result.Err == nil {
			b.user = value.result.User
		}
	case loaded:
		if value.result.Err == nil {
			b.count = len(value.result.Keys)
		}
	case app.KeyEvent:
		if value.Key == 'q' || value.Key == app.KeyCtrlC {
			return app.Quit()
		}
	}
	return app.Noop()
}

func (b *board) View() ui.Node {
	return ui.Column(
		ui.Text("Agentboard").Role(ui.Accent).Bold(),
		ui.Textf("user: %s", b.user),
		ui.Textf("boards: %d", b.count),
		ui.Text("q quits").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func commands() cli.Command {
	return cli.Root("agentboard commands").WithCommand(
		cli.New("get_identity", "show the connected identity").WithHandler(func(ctx cli.Context, _ []string) (cli.Output, error) {
			result := identity.Whoami().Run(ctx)
			if result.Err != nil {
				return cli.Empty(), result.Err
			}
			return cli.Value(fmt.Sprintf("%s authenticated=%t", result.User, result.Authenticated)), nil
		}),
	)
}

func main() { app.Run(&board{}, app.WithCommands(commands())) }
