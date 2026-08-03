// Chat is a small clean SDK example combining identity, KV, and pub/sub.
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/bus"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/ui"
)

const roomTopic = "chat-room"

type chat struct {
	user     string
	messages int
}

type userLoaded struct{ result identity.Result }
type messageReceived struct{ message bus.Message }

func (c *chat) Init() app.Command {
	return identity.Whoami().Map(func(result identity.Result) app.Event { return userLoaded{result: result} })
}

func (c *chat) Subscriptions() app.Subscription {
	return bus.Messages("room", roomTopic, func(message bus.Message) app.Event {
		return messageReceived{message: message}
	})
}

func (c *chat) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case userLoaded:
		if value.result.Err == nil {
			c.user = value.result.User
		}
	case messageReceived:
		if value.message.Err == nil {
			c.messages++
		}
	case app.KeyEvent:
		switch value.Key {
		case 'p':
			return app.Batch(
				kv.Set("chat/last", []byte("ping")).Ignore(),
				bus.Publish(roomTopic, []byte("ping")).Ignore(),
			)
		case 'q', app.KeyCtrlC:
			return app.Quit()
		}
	}
	return app.Noop()
}

func (c *chat) View() ui.Node {
	return ui.Column(
		ui.Text("Clean chat").Role(ui.Accent).Bold(),
		ui.Textf("user: %s", c.user),
		ui.Textf("messages: %d", c.messages),
		ui.Text("press p to publish · q quits").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&chat{}) }
