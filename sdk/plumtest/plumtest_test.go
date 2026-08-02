package plumtest

import (
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/cli"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type increment struct{}
type tick struct{}

type counter struct{ count, ticks int }

func (counter *counter) Update(event app.Event) app.Command {
	switch event.(type) {
	case increment:
		counter.count++
	case tick:
		counter.ticks++
	case app.KeyEvent:
		if event.(app.KeyEvent).Key == 'q' {
			return app.Quit(app.WithGoodbye("finished"))
		}
	}
	return app.Noop()
}

func (counter *counter) Subscriptions() app.Subscription {
	return app.Every("clock", time.Second, tick{})
}

func (counter *counter) View() ui.Node {
	return ui.Column(
		ui.Textf("count=%d ticks=%d", counter.count, counter.ticks),
		ui.Button("+", increment{}).Key("increment"),
	).Fill()
}

func TestStartDrivesModelWithoutSleepsOrGlobalState(t *testing.T) {
	model := &counter{}
	runtime := Start(t, model, Viewport(32, 3), KV("count", []byte("4")))
	runtime.ExpectText("count=0 ticks=0")
	runtime.ClickButton("+")
	runtime.ExpectText("count=1 ticks=0")
	runtime.Advance(2200 * time.Millisecond)
	runtime.ExpectText("count=1 ticks=2")
	runtime.ExpectKV("count", []byte("4"))
	runtime.Key('q')
	runtime.ExpectQuit()
	runtime.ExpectGoodbye("finished")
}

func TestInvokeCLIDrivesOneBoundedInvocation(t *testing.T) {
	command := cliCommand()
	run := InvokeCLI(t, command, Args("show", "--json"))
	run.ExpectExit(0)
	run.ExpectJSON(map[string]any{"message": "hello"})
	run.ExpectStderr("")

	run = InvokeCLI(t, command, Shell(`show --message "quoted hello"`))
	run.ExpectExit(0)
	run.ExpectText("quoted hello\n")
}

func TestInvokeCLIInjectsTypedKVFixture(t *testing.T) {
	command := cli.Root("storage", cli.New("show", "show value").WithHandler(func(ctx cli.Context, _ []string) (cli.Output, error) {
		result := kv.Get("value").Run(ctx)
		if result.Err != nil {
			return cli.Empty(), result.Err
		}
		return cli.Value(string(result.Value)), nil
	}))
	run := InvokeCLI(t, command, Args("show"), KV("value", []byte("fixture")))
	run.ExpectExit(0)
	run.ExpectText("fixture\n")
	run.ExpectKV("value", []byte("fixture"))
}

func cliCommand() cli.Command {
	message := cli.StringFlag("message", "message").WithDefault("hello")
	return cli.Root("messages", cli.New("show", "show a message").WithFlag(message).WithHandler(func(ctx cli.Context, _ []string) (cli.Output, error) {
		value, err := ctx.String("message")
		if err != nil {
			return cli.Empty(), err
		}
		return cli.Present(struct {
			Message string `json:"message"`
		}{Message: value}, func(writer cli.Writer, value struct {
			Message string `json:"message"`
		}) {
			writer.Println(value.Message)
		}), nil
	}))
}
