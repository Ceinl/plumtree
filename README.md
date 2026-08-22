# Plumtree

**A cloud for terminal apps.** Write a CLI or TUI in Go against the Plumtree
SDK, deploy it with one command, and anyone runs it with just `ssh` — no install
of any kind. Plumtree compiles each app to WebAssembly, runs it sandboxed on its
own servers, and streams the rendered terminal to the user.

> Think *Lakebed, but for the terminal* — instead of full-stack web capsules,
> Plumtree hosts SDK-written terminal apps, rendered server-side and streamed
> over SSH.

```
# run any deployed app — nothing to install but ssh
ssh <owner>/<app>@plumtree.app

# ship your own
pt new myapp --tui --access public
pt dev
pt deploy
```

---

## How it works

A Plumtree app is a small Go program written against the clean SDK. You never
touch raw terminal rendering or hosted capability plumbing; the app reaches
the outside world through typed operation packages.

1. **Author** — `pt new` scaffolds the standard app shape; `pt dev` compiles to
   WASM and runs it locally in [wazero](https://wazero.io) over a real PTY.
2. **Deploy** — `pt build` compiles the app locally to WASM and the clean API
   stores the typed artifact and metadata.
3. **Run** — the root server exposes the authenticated SSH control subsystem.
   Hosted leaf execution remains a release qualification gate and is not
   claimed here until a live fixture passes.

The connecting user runs **nothing locally** — only `ssh` and a terminal. The
app's code never reaches their machine, so a malicious app can't touch their
files, env, or disk. The execution risk lives entirely with the platform, which
is why every app is sandboxed by default.

```
author ── pt build/deploy ──▶ plumtree (SQLite repository)
                                  │
                       SSH control subsystem ──▶ /api/v1
                                  │
                       ctx: kv · pubsub · auth · env · fetch
```

## Writing an app

A TUI is *state → build a UI node tree → the runtime lays it out and
diff-renders it to a cell grid*. The app returns structured cells; the host
turns them into terminal output (so apps can never emit raw escape codes).

```go
package main

import (
    "github.com/Ceinl/plumtree/sdk/app"
    "github.com/Ceinl/plumtree/sdk/ui"
)

type state struct{ n int }

func (s *state) Update(ev app.Event) app.Command {
    if k, ok := ev.(app.KeyEvent); ok {
        switch k.Key { case app.KeyUp: s.n++; case app.KeyDown: s.n--; case 'q': return app.Quit() }
    }
    return app.Noop()
}
func (s *state) View() ui.Node { return ui.Column(ui.Textf("Count: %d", s.n)) }
func main() { app.Run(&state{}) }
```

…or a non-interactive CLI:

```go
func main() { cli.Run(cli.Root("hello").WithCommand(cli.New("hello", "greet"))) }
```

### App shape

```
app/main.go                  # entrypoint: the CLI/TUI definition
go.mod
plumtree.json                 # committed: { "name", "type", "access" }
.plumtree/                    # local development state; gitignored
```

### Capabilities (`ctx`)

The app touches the world only through host functions imported into the WASM
guest. More trust unlocks more capability:

| Capability  | What it gives                          | Availability        |
|-------------|----------------------------------------|---------------------|
| `ctx.KV`    | durable per-app key/value state        | all apps            |
| pub/sub     | live cross-session messaging (no poll) | all apps            |
| `ctx.Auth`  | proved SSH-key or explicit anonymous identity | all apps       |
| `ctx.Env`   | server-side secrets                    | **claimed** apps    |
| `ctx.Fetch` | gated, default-deny egress allowlist   | **claimed** apps    |

### Capability examples

The deployable apps under `examples/` show how the capabilities compose into
something larger than a single-feature fixture:

| Example | SDK capabilities | Try it |
|---------|------------------|--------|
| [`chat`](examples/chat) | SSH identity + durable KV profiles/history + live pub/sub | `ssh <owner>/chat@plumtree.app` |
| [`ascii-saver`](examples/ascii-saver) | timers + resize-safe custom cell rendering | `ssh <owner>/ascii-saver@plumtree.app` |
| [`tic-tac-toe`](examples/tic-tac-toe) | mouse input + leased player seats + KV/CAS + live pub/sub | `ssh <owner>/tic-tac-toe@plumtree.app` |
| [`agentboard`](examples/agentboard) | identity-aware KV domain model + pub/sub + clean CLI | `ssh <owner>/agentboard@plumtree.app` |

The chat remembers display names only for stable SSH-key identities; anonymous
session IDs are intentionally ephemeral. Tic-tac-toe gives its first two live
identities the X and O seats; everyone else watches until a seat is released.

## The `pt` CLI

`pt` is the author tool — scaffold, dev-run, build, deploy, and administration. It is **not**
needed to *run* apps (that's `ssh`).

```
pt new <name> --tui|--cli --access public|restricted  # scaffold the app shape
pt new --cli --access restricted <name>                # flags can also come first
pt dev [args...]            # compile and run; TUI apps use the current terminal
pt dev --headless           # run a deterministic scripted TUI session
pt dev --ssh                # serve the app through loopback SSH
pt build                    # compile to a typed WASM artifact
pt deploy                   # build locally and deploy the artifact

pt status                   # server and app state
pt audit                    # audit records
pt access                   # typed access-key workflow

pt logs <app>               # session logs
```

Use `--` before app arguments that start with `-`. Headless development accepts
`--script`, `-w`, `-h`, `--mem-pages`, `--frame-timeout`, and `--max-fps`.
Development SSH listens on `127.0.0.1:2222` by default. Use `--addr` to select a
different loopback address.

Deploy and destructive `secret rm`, `egress rm`, and `access rm` operations ask
for confirmation in a terminal. Use `--yes` for a non-interactive command.

Author and device identity are represented by the paired SSH workflow and
persistent root SQLite repository. First-run pairing remains a release gate;
no browser claim, dashboard, or bearer token is part of the clean API contract.

## Security model

RCE is the product, not a bug — every app is hostile by default, so the goal is
**containment, not prevention**.

- **WASM/wazero is the primary boundary.** Each app runs as a WASI *reactor*
  with no ambient filesystem, env, args, or network — it can only call the host
  functions we import. Production runners are separate worker processes from the
  control plane.
- **Progressive trust = capability.** Unclaimed apps run in the tightest
  sandbox (KV only, no secrets, no egress). Claiming unlocks `ctx.Env` and gated
  `ctx.Fetch`.
- **No raw-ANSI escape path.** The guest returns structured cells (`rune + RGB +
  decor`); the host renders them and sanitizes every rune, so apps can't attack
  the viewer's terminal.
- **Build is sandboxed too.** Compiling untrusted Go runs code before run-time,
  so builds happen in isolated workers — no secrets, no default network, bounded
  CPU/memory/disk, isolated module cache, checksum + module policy enforcement.
- **Hard limits everywhere** — per-frame wall-clock deadlines, memory page caps,
  output/input rate, storage quotas, per-author concurrency caps, deploy rate
  limits, and kill switches. **Deploy is gated harder than run.**

SSH secures the *channel only* — it does not protect data at rest, from other
tenants, or from the operator.

## Repository layout

A multi-module Go workspace (`go.work`) with the root product module:

| Path             | Module                                    | Purpose                                                        |
|------------------|-------------------------------------------|---------------------------------------------------------------|
| `./`             | `github.com/Ceinl/plumtree`               | Root product module and staged internal ownership boundaries.  |
| `sdk/`           | `github.com/Ceinl/plumtree/sdk`           | Author-facing Go SDK and the versioned WASM ABI wrapper.       |
| `sdk/app/`       | `github.com/Ceinl/plumtree/sdk/app`       | Additive clean interactive lifecycle, commands, and subscriptions. |
| `sdk/ui/`        | `github.com/Ceinl/plumtree/sdk/ui`        | Additive declarative UI nodes, themes, focus, and canvas.      |
| `sdk/plumtest/`  | `github.com/Ceinl/plumtree/sdk/plumtest`  | Deterministic in-process interactive model test harness.      |
| `cmd/pt/`         | `github.com/Ceinl/plumtree/cmd/pt`        | Root author CLI entrypoint.                            |
| `cmd/plumtree/`   | `github.com/Ceinl/plumtree/cmd/plumtree`  | Root server-role entrypoint.                          |
| `internal/cli/`   | `github.com/Ceinl/plumtree/internal/cli`  | Root-owned author CLI, scaffold, local dev, deploy, and management. |
| `internal/build/` | `github.com/Ceinl/plumtree/internal/build`| Root-owned local WASM build and source packaging.              |
| `cmd/runner-worker/` | `github.com/Ceinl/plumtree/cmd/runner-worker` | Root-owned isolated WASM worker boundary. |
| `internal/runner/` | `github.com/Ceinl/plumtree/internal/runner` | Isolated WASM session runner, broker, worker, and host capabilities. |
| `internal/gateway/` | `github.com/Ceinl/plumtree/internal/gateway` | Retained hosted-runner qualification harness. |
| `internal/httpapi/v1/` | `github.com/Ceinl/plumtree/internal/httpapi/v1` | Clean authenticated control and artifact API. |
| `internal/sqlite/` | `github.com/Ceinl/plumtree/internal/sqlite` | Root-owned strict SQLite/SQLCipher repository. |
| `internal/protocol/` | `github.com/Ceinl/plumtree/internal/protocol` | Bounded runner, gateway, and exec contracts. |
| `internal/server/cleanrole/` | `github.com/Ceinl/plumtree/internal/server/cleanrole` | Root native SSH/SQLite assembly. |

## Status

The end-to-end author loop works against a local control plane:

```
server: go run ./cmd/plumtree
author: pt new → pt dev → pt build → pt deploy
```

Local server startup persists the SQLite repository and SSH host key. The clean
transport is SSH-only; there is no public HTTP listener or shared deploy token.
The first `plumtree serve` creates the strict configuration at the platform
config path. Operators can use `plumtree config show`,
`plumtree config set <field> <value>`, and `plumtree config unset <field>`;
changes take effect after restart. `-config` or `PLUMTREE_CONFIG` selects an
explicit file.

A locally built app is stored as a typed WASM artifact through `/api/v1`; the
SDK and ABI suites cover the in-process and isolated runner paths. Native
SQLCipher release linkage and live hosted-leaf execution remain qualification
gates for a product tag.

Compose uses one root-owned service with a read-only root filesystem, bounded
resources, a persistent state volume, and only the SSH control transport.

**Next up:** moderation & per-author quotas at scale, richer scoped storage
(`ctx.DB`), and content-addressed artifact caching on the gateway.

**Deferred:** fully anonymous public deploy, native binaries / microVMs, non-Go
languages, teams/orgs, billing, custom handles, and compatibility with arbitrary
existing terminal apps.

## Glossary

- **Plumtree / pt** — the platform and its author CLI.
- **App** — an SDK-written Go terminal program, namespaced `<owner>/<app>`,
  compiled to WASM.
- **ctx** — the capability object (host functions) handed to an app: kv, pubsub,
  auth, env, fetch, io.
- **Sandbox** — the wazero WASM instance an app runs in, server-side.
- **Claim** — authenticating ownership to unlock higher-trust capabilities.
- **Deploy** — publish an app via `pt` (the privileged author action).
- **Run** — connect to an app with plain `ssh`; the platform executes it.

## License

Plumtree is licensed under the [MIT License](LICENSE).
