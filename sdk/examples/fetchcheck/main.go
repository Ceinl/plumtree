// Command fetchcheck is the gated-egress example: it issues an HTTP GET to a URL
// passed as a secret (FETCH_URL) and renders the status, or the egress-denied
// error. It demonstrates that Fetch reaches the network only when the host is
// allowlisted. Runs natively (`go run .`, real network) or hosted (gated).
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type model struct{ line string }

func (m *model) fetch() {
	url, ok, _ := sdk.Env("FETCH_URL")
	if !ok {
		m.line = "no FETCH_URL set"
		return
	}
	resp, err := sdk.Get(url)
	switch {
	case err == sdk.ErrEgressDenied:
		m.line = "denied"
	case err != nil:
		m.line = "error: " + err.Error()
	default:
		m.line = fmt.Sprintf("status %d: %s", resp.Status, string(resp.Body))
	}
}

func (m *model) Update(ev sdk.Event) {
	if k, ok := ev.(sdk.KeyMsg); ok {
		switch k.Key {
		case 'g':
			m.fetch()
		case 'q', sdk.KeyCtrlC:
			sdk.Quit()
		}
	}
}

func (m *model) View() tui.Component {
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.SetSize(tui.Grow, tui.Grow)
	root.AppendChild(components.NewText("result: " + m.line))
	return root
}

func main() { sdk.RunTUI(&model{line: "(press g)"}, sdk.Meta{Name: "fetchcheck", Type: "tui"}) }
