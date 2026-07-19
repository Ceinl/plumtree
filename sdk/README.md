# Plumtree SDK

Author-facing Go SDK for Plumtree apps. The same source runs natively
(`go run .`) and compiled to WASM for hosted execution; the low-level ABI is
hidden behind `RunTUI`/`CLI`.

```go
package main

import (
	"fmt"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

type model struct{ n int }

func (m *model) Update(ev sdk.Event) {
	if k, ok := ev.(sdk.KeyMsg); ok {
		switch k.Key {
		case sdk.KeyUp:
			m.n++
		case sdk.KeyDown:
			m.n--
		case 'q', sdk.KeyCtrlC:
			sdk.Quit()
		}
	}
}

func (m *model) View() tui.Component {
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.AlignItems(tui.ACenter)
	root.SetSize(tui.Grow, tui.Grow)
	root.AppendChild(components.NewText(fmt.Sprintf("Count: %d", m.n)))
	return root
}

func main() { sdk.RunTUI(&model{}, sdk.Meta{Name: "counter", Type: "tui"}) }
```

## Packages

| Import | Responsibility |
| --- | --- |
| `github.com/Ceinl/plumtree/sdk` | `RunTUI`, `CLI`, `Model`, `Event`/`KeyMsg`/`MouseMsg`/`ResizeMsg`/`MessageMsg`, `Meta`, `Quit`, `Ctx`/`Out`. |
| `github.com/Ceinl/plumtree/sdk` (capabilities) | `KVGet`/`KVSet`/`KVDelete`/`KVList`/`KVCompareAndSwap` (durable state); `Subscribe`/`Publish` + `MessageMsg` (live pub/sub); `Whoami` (SSH-key identity); `Env` (claimed-only secrets); `Fetch`/`Get` (claimed-only gated egress). The same calls work natively and hosted. |
| `github.com/Ceinl/plumtree/sdk/tui` | Layout primitives (`Component`, `Unit`, `Direction`, `Style`, …) re-exported from the runtime. |
| `github.com/Ceinl/plumtree/sdk/tui/components` | Default widgets: `Div`, `Text`, `Button`. |
| `github.com/Ceinl/plumtree/sdk/abi` | The versioned WASM wire format (events in, structured frames out). Canonical home of the ABI. |

## How it runs

- **Native** (`!wasip1`): `RunTUI` drives the runtime's terminal loop directly.
- **Hosted** (`GOOS=wasip1 GOARCH=wasm`, command module): `RunTUI` runs a
  guest-driven loop calling two host imports — `recv` (next input event) and
  `present` (a rendered frame). Because the guest is a WASI *command*, `main`
  runs, so the author's `func main(){ sdk.RunTUI(...) }` works unchanged.

The guest returns structured cells (rune + RGB + decoration), never raw ANSI;
the host owns all terminal output. Build and run apps with `pt dev`.

Does not own: platform capability implementations, SSH serving, deploy storage.

## KV collection and concurrency semantics

`KVList(prefix, limit)` returns lexicographically ordered keys and requires a
limit from 1 through 256. An empty prefix lists the app's private namespace.
`KVCompareAndSwap` compares the SHA-256 hash of the current value atomically;
use `KVHash(value)` for an existing value or the zero `[32]byte{}` hash to
create only when absent. A stale expectation returns `ErrKVConflict` and leaves
state unchanged. Existing key/value and aggregate store quotas still apply.

## Identity and mouse input

`Whoami` now distinguishes `Kind` (`ssh-key` or `anonymous`) and reports
`OwnsApp` only when the verified SSH-key owner owns the running app. Registered
non-owners remain `Authenticated` but do not own the app; proved unregistered
keys are stable `ssh-key` identities with `Authenticated == false`. Native
development defaults to a local owner identity and can be overridden with
`PLUMTREE_IDENTITY_USER`, `PLUMTREE_IDENTITY_KIND`,
`PLUMTREE_IDENTITY_AUTHENTICATED`, and `PLUMTREE_IDENTITY_OWNS_APP`.

`MouseMsg` carries zero-based coordinates, button, and action. The TUI loop
automatically routes left-button down/up through the previously laid-out
component tree, so nested `Button` values fire `OnClick`; the same event is
still delivered to `Model.Update` for custom handling.
