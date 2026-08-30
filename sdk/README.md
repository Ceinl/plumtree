# Plumtree SDK

Author-facing Go SDK for Plumtree apps. The same source runs natively
(`go run .`) and compiled to WASM for hosted execution; the low-level ABI is
hidden behind `RunTUI`/`CLI`.

The clean interactive surface is the supported author surface:
`sdk/app` owns the serialized model lifecycle and finite commands, `sdk/ui`
owns declarative nodes and structured drawing, and `sdk/plumtest` drives models
without sleeps, subprocesses, global argv/stdio, or external services.

The finite surface is separate: `sdk/cli` owns one bounded immutable command
tree, synchronous handlers, typed parsing, result presentation, and stable
exit/errors. It does not start the interactive model lifecycle.

Each typed capability
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

## Packages

| Import | Responsibility |
| --- | --- |
| `github.com/Ceinl/plumtree/sdk/app` | Clean interactive model lifecycle, input events, finite commands, quit/goodbye, and declarative subscriptions. |
| `github.com/Ceinl/plumtree/sdk/cli` | Bounded immutable command trees, typed flags/arguments, help/schema, synchronous handlers, human/JSON output, and shell-style lexing without shell execution. |
| `github.com/Ceinl/plumtree/sdk/ui` | Chained declarative nodes, semantic themes, focus/input routing, structured frames, and clipped canvas drawing. |
| `github.com/Ceinl/plumtree/sdk/plumtest` | Deterministic in-process model/runtime and `InvokeCLI` harness with virtual time, argv, streams, output, and fixture assertions. |
| `github.com/Ceinl/plumtree/sdk/kv` | Typed durable `Get`, `Set`, `Delete`, `List`, and `CompareAndSwap` operations. |
| `github.com/Ceinl/plumtree/sdk/bus` | Typed best-effort `Publish` and declarative `Messages` subscriptions. |
| `github.com/Ceinl/plumtree/sdk/identity` | Typed connected-session `Whoami` operation. |
| `github.com/Ceinl/plumtree/sdk/secrets` | Typed owner-enabled secret `Get` operation. |
| `github.com/Ceinl/plumtree/sdk/fetch` | Typed bounded gated HTTP `Request` and `Get` operations. |
| `github.com/Ceinl/plumtree/sdk/hostexec` | Typed bounded opt-in `Run` operation for trusted host commands. |
| `github.com/Ceinl/plumtree/sdk/timer` | Typed finite `After` and declarative recurring `Every` timers. |
| `github.com/Ceinl/plumtree/sdk/abi` | The versioned WASM wire format (events in, structured frames out). Canonical home of the ABI. |

The SDK module is self-contained. Its TUI implementation is private under
`internal/tui`; app code should use only the public packages listed above.

## Capability contract inventory

| Package | Authority and lifetime | Native / hosted isolation | Stable result errors |
| --- | --- | --- | --- |
| `kv` | Private app namespace; copied value/result per operation | In-process store / selected isolated host capability | `ErrInvalid`, `ErrTooLarge`, `ErrQuota`, `ErrConflict`, `ErrUnavailable` |
| `bus` | App-scoped topic; notification lives until delivery | Process-local fan-out / clean hosted event bridge | `ErrInvalid`, `ErrTooLarge`, `ErrUnavailable` |
| `identity` | Connected session; immutable lookup result | Local development identity / verified isolated session | `ErrUnavailable` |
| `secrets` | Owner-enabled app secret store; value lives in result | Process environment / isolated server secret capability | `ErrInvalid`, `ErrTooLarge`, `ErrUnavailable` |
| `fetch` | Owner-enabled app egress allowlist; response lives in result | Local network / isolated gated host fetch | `ErrInvalid`, `ErrTooLarge`, `ErrDenied`, `ErrFailed`, `ErrUnavailable` |
| `hostexec` | Explicit operator authority; output lives in result | Local process / isolated opt-in host command | `ErrInvalid`, `ErrTooLarge`, `ErrFailed`, `ErrUnavailable` |
| `timer` | No external authority; `After` completes once and `Every` lives until model cancellation | Native clock / app-managed isolated runtime clock | `ErrInvalid` plus context cancellation |

No package exposes a generic capability registry, string dispatch, or generic
RPC payload. Applications use the typed operation packages directly.

## Finite CLI

Declare commands as values and attach handlers without mutating process
arguments or streams in application code:

```go
func commands() cli.Command {
    by := cli.IntFlag("by", "amount to add").WithShort('b').WithDefault(1)
    return cli.Root("counter", cli.New("add", "add to the count").
        WithFlag(by).
        WithHandler(func(ctx cli.Context, _ []string) (cli.Output, error) {
            amount, err := ctx.Int("by")
            if err != nil { return cli.Empty(), err }
            result := struct { Count int `json:"count"` }{Count: amount}
            return cli.Present(result, func(out cli.Writer, value struct { Count int `json:"count"` }) {
                out.Printf("Count: %d\n", value.Count)
            }), nil
        }))
}

func main() { cli.Run(commands()) }
```

`--json` serializes the same typed result as the human renderer. `--help`,
typed flags, `--`, bounded positional arguments, and nested subcommands are
validated before a handler runs. `cli.Lex` accepts quoted and escaped SSH exec
words but never expands variables or invokes a shell. Use
`plumtest.InvokeCLI(t, commands(), plumtest.Args("add", "--by", "2"))` for
deterministic argv, stdin, stderr, exit, human, JSON, and lexer tests.

An interactive leaf may attach the same tree with `app.WithCommands`. A native
exec invocation dispatches the tree before model initialization; an ordinary
interactive invocation keeps the serialized `Update`/`View` lifecycle.

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

- **Native** (`!wasip1`): `app.Run` drives the runtime's terminal loop directly.
- **Hosted** (`GOOS=wasip1 GOARCH=wasm`, command module): `app.Run` runs a
  guest-driven loop calling two host imports — `recv` (next input event) and
  `present` (a rendered frame). Because the guest is a WASI *command*, `main`
  runs, so the author's `func main(){ app.Run(...) }` works unchanged.

The guest returns structured cells (rune + RGB + decoration), never raw ANSI;
the host owns all terminal output. Build and run apps with `pt dev`.

## Asynchronous commands and timers

Commands let an app start asynchronous work while keeping `Update -> View` as
its only state and rendering model. Timer completions are serialized with
keyboard, resize, mouse, and pub/sub events through `Update`:

```go
type model struct {
    timer timer.TimerID
    ticks int
}

func (m *model) Update(event app.Event) app.Command {
    if m.timer == 0 {
        m.timer = timer.Every(time.Second)
    }
    if tick, ok := event.(timer.Event); ok && tick.ID == m.timer {
        m.ticks++
        if m.ticks == 10 {
            return app.Quit()
        }
    }
    return app.Noop()
}
```

Use `timer.After` for a one-shot command and `timer.Every` for a recurring command.
Each session may have at most 64 active commands; durations are bounded, and
all remaining commands are canceled when the session ends. See the complete
[`examples/timer`](examples/timer) app.

## Trusted host commands

`hostexec.Run(name, args...)` executes a local program and returns its exit code,
stdout, and stderr. Native development uses the current process context.

Hosted policy is deny-by-default: the server operator must configure an
explicit executable allowlist (`runtime.hostCommandAllowlist` CSV, or
PLUMTREE_HOST_COMMAND_ALLOWLIST), and only allowlisted programs run. Bare-name
entries authorize bare-name requests resolved against a fixed sandbox PATH;
absolute-path entries authorize exactly that cleaned path. Shell interpreters
(sh, bash, zsh, dash, ksh, csh, fish, cmd, powershell, pwsh) are always
refused — by name and by symlink-resolved path — because a shell turns bounded
arguments back into arbitrary execution. The allowlist is not execution
confinement: an allowed program receives the serving process's OS authority,
and caller-controlled arguments can let it access files or start child
processes. Each command runs in a fresh temporary working directory with a
minimal environment (PATH, HOME) under a per-exec timeout. Server secrets are
not added to that child environment. Do not invoke commands through shells in
apps that target hosted execution: dispatch your bounded argument vector
directly instead.

This capability is intended for trusted apps on private/self-hosted servers,
including apps that invoke locally installed AI-agent CLIs during development.
It grants the app the serving process's OS authority within the allowlist.

Does not own: platform capability implementations, SSH serving, deploy storage.

## KV collection and concurrency semantics

`kv.List(prefix, limit)` returns lexicographically ordered keys and requires a
limit from 1 through 256. An empty prefix lists the app's private namespace.
`kv.CompareAndSwap` compares the SHA-256 hash of the current value atomically;
use `kv.Hash(value)` for an existing value or the zero `[32]byte{}` hash to
create only when absent. A stale expectation returns `ErrKVConflict` and leaves
state unchanged. Existing key/value and aggregate store quotas still apply.

## Identity and mouse input

`identity.Whoami` distinguishes `Kind` (`ssh-key` or `anonymous`) and reports
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
