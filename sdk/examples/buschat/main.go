// Command buschat is the canonical pub/sub example: it subscribes to a room
// topic and renders every message delivered over the bus, including its own. It
// is the live-messaging counterpart to kvcounter (durable storage) and runs
// unchanged natively (`go run .`) or hosted as WASM. Two hosted sessions of the
// same app share one bus, so a message one publishes appears in the other.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

const topic = "room"

type chat struct {
	user  string
	lines []string
}

func (c *chat) Update(ev sdk.Event) {
	switch e := ev.(type) {
	case sdk.MessageMsg:
		c.lines = append(c.lines, string(e.Data))
	case sdk.KeyMsg:
		switch e.Key {
		case 'p':
			// Publish a message; it is echoed back to this session and to every
			// other subscribed session.
			_ = sdk.Publish(topic, []byte("ping"))
		case 'q', sdk.KeyCtrlC:
			sdk.Quit()
		}
	}
}

func (c *chat) View() tui.Component {
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.SetSize(tui.Grow, tui.Grow)

	root.AppendChild(components.NewText("user: " + c.user))
	if v, ok, _ := sdk.Env("ROOM_NAME"); ok {
		root.AppendChild(components.NewText("room: " + v))
	}
	root.AppendChild(components.NewText(fmt.Sprintf("messages: %d", len(c.lines))))
	for _, l := range c.lines {
		root.AppendChild(components.NewText(l))
	}
	return root
}

func main() {
	c := &chat{user: "?"}
	if id, err := sdk.Whoami(); err == nil {
		c.user = id.User
	}
	_ = sdk.Subscribe(topic)
	sdk.RunTUI(c, sdk.Meta{Name: "buschat", Type: "tui"})
}
