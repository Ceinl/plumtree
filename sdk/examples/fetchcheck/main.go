// Command fetchcheck demonstrates clean secret and gated HTTP operations.
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/fetch"
	"github.com/Ceinl/plumtree/sdk/secrets"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type model struct {
	url  string
	line string
}

type configured struct{ url string }
type fetched struct{ result fetch.Result }

func (m *model) Init() app.Command {
	return secrets.Get("FETCH_URL").Map(func(result secrets.Result) app.Event {
		return configured{url: result.Value}
	})
}

func (m *model) Update(event app.Event) app.Command {
	switch value := event.(type) {
	case configured:
		m.url = value.url
		if m.url == "" {
			m.line = "FETCH_URL is not configured"
		}
	case fetched:
		if value.result.Err != nil {
			m.line = value.result.Err.Error()
		} else {
			m.line = fmt.Sprintf("status %d: %s", value.result.Status, string(value.result.Body))
		}
	case app.KeyEvent:
		switch value.Key {
		case 'g':
			if m.url == "" {
				return app.Noop()
			}
			return fetch.Get(m.url).Map(func(result fetch.Result) app.Event { return fetched{result: result} })
		case 'q', app.KeyCtrlC:
			return app.Quit()
		}
	}
	return app.Noop()
}

func (m *model) View() ui.Node {
	return ui.Column(
		ui.Text("Clean fetch check").Role(ui.Accent).Bold(),
		ui.Text(m.line),
		ui.Text("press g to fetch · q quits").Role(ui.Muted),
	).Fill().Gap(1).Align(ui.Center).Justify(ui.Center)
}

func main() { app.Run(&model{line: "press g to fetch"}) }
