package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/ui"
)

func main() {
	command := cli.Root("clean cli").WithCommand(
		cli.New("greet", "write a greeting").
			WithArgs(cli.ExactArgs(1)).
			WithArgument(cli.StringArg("name", "name to greet")).
			WithHandler(func(_ cli.Context, _ []string) (cli.Output, error) {
				return cli.Value("clean-cli-ok"), nil
			}),
	).WithCommand(
		cli.New("fail", "write an error").
			WithHandler(func(context cli.Context, _ []string) (cli.Output, error) {
				context.Stderr.Println("clean-cli-stderr")
				return cli.Empty(), cli.Error{Code: "failed", Message: "clean-cli-failed", ExitCode: 7}
			}),
	)
	app.Run(&model{}, app.WithCommands(command))
}

type model struct{}

func (*model) Init() app.Command {
	return app.Quit(app.WithGoodbye("clean CLI complete"))
}

func (*model) Update(app.Event) app.Command { return app.Noop() }
func (*model) View() ui.Node                { return ui.Text("clean-cli") }
