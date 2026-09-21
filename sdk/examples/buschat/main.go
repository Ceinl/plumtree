// Command buschat demonstrates clean typed pub/sub and declarative messages.
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/bus"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/secrets"
	"github.com/Ceinl/plumtree/sdk/ui"
)

const topic = "room"

type chat struct {
	messages int
	user     string
	room     string
	last     string
}
type received struct{ message bus.Message }
type identified struct{ result identity.Result }
type configured struct{ result secrets.Result }

func (c *chat) Init() app.Command {
	return app.Batch(
		identity.Whoami().Map(func(result identity.Result) app.Event { return identified{result: result} }),
		secrets.Get("ROOM_NAME").Map(func(result secrets.Result) app.Event { return configured{result: result} }),
	)
}

func (c *chat) Subscriptions() app.Subscription {
	return bus.Messages("room", topic, func(message bus.Message) app.Event {
		return received{message: message}
	})
}

func (c *chat) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case received:
		if value.message.Err == nil {
			c.messages++
			c.last = string(value.message.Data)
		}
	case identified:
		if value.result.Err == nil {
			c.user = value.result.User
		}
	case configured:
		if value.result.Err == nil {
			c.room = value.result.Value
		}
	case app.KeyEvent:
		switch value.Key {
		case 'p':
			return bus.Publish(topic, []byte("ping")).Ignore()
		case 'q', app.KeyCtrlC:
			return app.Quit()
		}
	}
	return app.Noop()
}

func (c *chat) View() ui.Node {
	return ui.Column(
		ui.Text("Clean bus chat").Role(ui.Accent).Bold(),
		ui.Textf("messages: %d", c.messages),
		ui.Textf("last: %s", c.last),
		ui.Textf("user: %s", c.user),
		ui.Textf("room: %s", c.room),
		ui.Text("press p to publish · q quits").Role(ui.Muted),
	).Fill().Gap(0).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&chat{}) }
