# Plumtree SDK

Author-facing Go SDK for Plumtree apps. The same source runs natively
(`go run .`) and compiled to WASM for hosted execution; the low-level ABI is
hidden behind `RunTUI`/`CLI`.

The clean interactive surface is additive during the consolidation sequence:
`sdk/app` owns the serialized model lifecycle and finite commands, `sdk/ui`
owns declarative nodes and structured drawing, and `sdk/plumtest` drives models
without sleeps, subprocesses, global argv/stdio, or external services. Existing
root SDK APIs remain selected until the later consumer cutover.

The typed capability surface is also additive at this step. Each capability
owns its operation builder, result, bounds, stable errors, and native/hosted
adapter boundary. `Run(ctx)` executes once for finite code; `Map(...)` converts
the same inert operation into one `app.Command` for interactive models.

```go
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/ui"
)

type increment struct{}
type model struct{ count int }

func (m *model) Update(event app.Event) app.Command {
	if _, ok := event.(increment); ok {
		m.count++
	}
	return app.Noop()
}

func (m *model) View() ui.Node {
	return ui.Column(
		ui.Textf("Count: %d", m.count),
		ui.Button("+", increment{}).Key("increment"),
	).Fill()
}

func main() { app.Run(&model{}) }
```

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
| `github.com/Ceinl/plumtree/sdk/app` | Clean interactive model lifecycle, input events, finite commands, quit/goodbye, and declarative subscriptions. |
| `github.com/Ceinl/plumtree/sdk/ui` | Chained declarative nodes, semantic themes, focus/input routing, structured frames, and clipped canvas drawing. |
| `github.com/Ceinl/plumtree/sdk/plumtest` | Deterministic in-process model/runtime harness with virtual time, viewport, input, view, and fixture assertions. |
| `github.com/Ceinl/plumtree/sdk/kv` | Typed durable `Get`, `Set`, `Delete`, `List`, and `CompareAndSwap` operations. |
| `github.com/Ceinl/plumtree/sdk/bus` | Typed best-effort `Publish` and declarative `Messages` subscriptions. |
| `github.com/Ceinl/plumtree/sdk/identity` | Typed connected-session `Whoami` operation. |
| `github.com/Ceinl/plumtree/sdk/secrets` | Typed owner-enabled secret `Get` operation. |
| `github.com/Ceinl/plumtree/sdk/fetch` | Typed bounded gated HTTP `Request` and `Get` operations. |
| `github.com/Ceinl/plumtree/sdk/hostexec` | Typed bounded opt-in `Run` operation for trusted host commands. |
| `github.com/Ceinl/plumtree/sdk/timer` | Typed finite `After` and declarative recurring `Every` timers. |
| `github.com/Ceinl/plumtree/sdk` (compatibility surface) | Existing root capability functions remain selected until the later consumer cutover; Env (claimed-only secrets) and Fetch (claimed-only egress) retain their existing behavior. |
| `github.com/Ceinl/plumtree/sdk/tui` | Layout primitives (`Component`, `Unit`, `Direction`, `Style`, …) re-exported from the runtime. |
| `github.com/Ceinl/plumtree/sdk/tui/components` | Default widgets: `Div`, `Text`, `Button`. |
| `github.com/Ceinl/plumtree/sdk/abi` | The versioned WASM wire format (events in, structured frames out). Canonical home of the ABI. |

The SDK module is self-contained. Its TUI implementation is private under
`internal/tui`; app code should use only the public packages listed above.

## Capability contract inventory

| Package | Authority and lifetime | Native / hosted isolation | Stable result errors |
| --- | --- | --- | --- |
| `kv` | Private app namespace; copied value/result per operation | In-process store / selected isolated host capability | `ErrInvalid`, `ErrTooLarge`, `ErrQuota`, `ErrConflict`, `ErrUnavailable` |
| `bus` | App-scoped topic; notification lives until delivery | Process-local fan-out / clean hosted event selection pending | `ErrInvalid`, `ErrTooLarge`, `ErrUnavailable` |
| `identity` | Connected session; immutable lookup result | Local development identity / verified isolated session | `ErrUnavailable` |
| `secrets` | Owner-enabled app secret store; value lives in result | Process environment / isolated server secret capability | `ErrInvalid`, `ErrTooLarge`, `ErrUnavailable` |
| `fetch` | Owner-enabled app egress allowlist; response lives in result | Local network / isolated gated host fetch | `ErrInvalid`, `ErrTooLarge`, `ErrDenied`, `ErrFailed`, `ErrUnavailable` |
| `hostexec` | Explicit operator authority; output lives in result | Local process / isolated opt-in host command | `ErrInvalid`, `ErrTooLarge`, `ErrFailed`, `ErrUnavailable` |
| `timer` | No external authority; `After` completes once and `Every` lives until model cancellation | Native clock / app-managed isolated runtime clock | `ErrInvalid` plus context cancellation |

No package exposes a generic capability registry, string dispatch, or generic
RPC payload. The compatibility root remains in place solely for the ordered
consumer cutover that follows this issue.

## Clean interactive example

The additive [`examples/clean-counter`](examples/clean-counter) app shows the
new lifecycle without changing the currently selected examples:

```sh
go test ./examples/clean-counter
go run ./examples/clean-counter
```

Only `Update` changes model state. `View` returns a fresh node tree, buttons
emit app-defined values, and `plumtest.Start` drives the same model with a
virtual viewport and clock. Use stable `.Key(...)` values for dynamic controls
so focus survives insertion, deletion, and reordering.

## How it runs

- **Native** (`!wasip1`): `RunTUI` drives the runtime's terminal loop directly.
- **Hosted** (`GOOS=wasip1 GOARCH=wasm`, command module): `RunTUI` runs a
  guest-driven loop calling two host imports — `recv` (next input event) and
  `present` (a rendered frame). Because the guest is a WASI *command*, `main`
  runs, so the author's `func main(){ sdk.RunTUI(...) }` works unchanged.

The guest returns structured cells (rune + RGB + decoration), never raw ANSI;
the host owns all terminal output. Build and run apps with `pt dev`.

## Asynchronous commands and timers

Commands let an app start asynchronous work while keeping `Update -> View` as
its only state and rendering model. Timer completions are serialized with
keyboard, resize, mouse, and pub/sub events through `Model.Update`:

```go
type model struct {
    timer sdk.CommandID
    ticks int
}

func (m *model) Update(event sdk.Event) {
    if m.timer == 0 {
        m.timer, _ = sdk.Schedule(sdk.Every(time.Second))
    }
    if timer, ok := event.(sdk.TimerMsg); ok && timer.ID == m.timer {
        m.ticks++
        if m.ticks == 10 {
            sdk.Cancel(m.timer)
        }
    }
}
```

Use `sdk.After` for a one-shot command and `sdk.Every` for a recurring command.
Each session may have at most 64 active commands; durations are bounded, and
all remaining commands are canceled when the session ends. See the complete
[`examples/timer`](examples/timer) app.

## Trusted host commands

`sdk.Exec(name, args...)` executes a local program and returns its exit code,
stdout, and stderr. Native development always uses the current process context;
hosted apps receive this capability only when the server operator explicitly
enables `allowHostCommands`, and only after the app is claimed. For shell
syntax, invoke a shell explicitly: `sdk.Exec("sh", "-lc", script)`.

This capability is intended for trusted apps on private/self-hosted servers,
including apps that invoke locally installed AI-agent CLIs. It grants the app
the server process's OS authority; it is not part of the default sandbox.

Does not own: platform capability implementations, SSH serving, deploy storage.

## JSON actions over SSH

TUI apps can opt into programmatic actions without changing ordinary
interactive behavior:

```go
sdk.RunTUIWithActions(model, meta, sdk.Actions{
    "lookup": func(ctx sdk.Ctx, raw json.RawMessage) (any, error) {
        return map[string]any{"found": true}, nil
    },
})
```

Invoke with standard SSH exec:

```sh
ssh owner/app@plumtree.dev 'action lookup {"id":"123"}'
```

The response is exactly one JSON object: `{"ok":true,"result":...}` or
`{"ok":false,"error":{"code":"...","message":"..."}}`. Return
`*sdk.ActionError` for stable application codes. Action name, command, and JSON
sizes are bounded; the gateway never invokes a shell. `CLIWithActions` provides
the same dispatch while preserving ordinary CLI arguments.

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
