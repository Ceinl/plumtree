# Plumtree

**A cloud for terminal apps.** Write a CLI or TUI in Go against the Plumtree
SDK, deploy it with one command, and anyone runs it with just `ssh` — no install
of any kind. Plumtree compiles each app to WebAssembly, runs it sandboxed on its
own servers, and streams the rendered terminal to the user.

> Think *Lakebed, but for the terminal* — instead of full-stack web capsules,
> Plumtree hosts SDK-written terminal apps, rendered server-side and streamed
> over SSH.

```
# run any deployed app on your paired server — nothing to install but ssh
ssh -p 2222 <owner>/<app>@localhost      # hosted: ssh <owner>/<app>@<your-server>

# ship your own, end to end on one machine
plumtree bootstrap -handle alice -device laptop   # prints a one-use bootstrap id
plumtree serve                           # control + gateway roles, SSH on :2222
pt pair --bootstrap <id> --yes localhost
pt new myapp --tui --access public
pt dev                                   # local run; `pt dev --ssh` serves it over SSH
pt deploy
ssh -p 2222 alice/myapp@localhost        # run the deployed app
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
3. **Run** — `ssh <author>/<app>@<host>` starts the active deployment. A shell
   runs the TUI path; SSH exec passes bounded arguments to the finite CLI path.

The connecting user runs **nothing locally** — only `ssh` and a terminal. The
app's code never reaches their machine, so a malicious app can't touch their
files, env, or disk. The execution risk lives entirely with the platform, which
is why every app is sandboxed by default.

```
author ── pt build/deploy ──▶ plumtree (SQLite repository)
                                  │
                       SSH control subsystem ──▶ /api/v1
                                  │
                     public/restricted leaf session
                                   │
              in-process session, or the runner-worker
                 boundary when runtime.runnerEndpoint is set
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
| `ctx.Env`   | server-side secrets                    | paired apps         |
| `ctx.Fetch` | gated, default-deny egress allowlist   | paired apps         |

"Paired" means the app was deployed by a paired owner: secrets and egress are
owner-relative capabilities and stay absent for ownerless deployments. A failed
capability lookup fails closed — the session runs without it.

### Capability examples

The deployable apps under `examples/` show how the capabilities compose into
something larger than a single-feature fixture:

| Example | SDK capabilities | Try it |
|---------|------------------|--------|
| [`chat`](examples/chat) | SSH identity + durable KV profiles/history + live pub/sub | `ssh -p 2222 <owner>/chat@localhost` |
| [`ascii-saver`](examples/ascii-saver) | timers + resize-safe custom cell rendering | `ssh -p 2222 <owner>/ascii-saver@localhost` |
| [`tic-tac-toe`](examples/tic-tac-toe) | mouse input + leased player seats + KV/CAS + live pub/sub | `ssh -p 2222 <owner>/tic-tac-toe@localhost` |
| [`agentboard`](examples/agentboard) | identity-aware KV domain model + pub/sub + clean CLI | `ssh -p 2222 <owner>/agentboard@localhost` |

Deploy each example from its directory with `pt deploy` after pairing, then
connect with your own `<owner>/<app>` handle — on a hosted server, replace
`-p 2222 …@localhost` with `<owner>/<app>@<your-server>`.

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
pt server list              # paired servers and current selection
pt server use <name>        # select a paired server
pt device invite <name>     # create a one-use second-device invitation
pt device list              # list active and revoked author devices
pt audit                    # audit records
pt access                   # typed access-key workflow

pt logs <app>               # session logs
pt help <command>           # usage and grammar for every command
```

Use `--` before app arguments that start with `-`. Headless development accepts
`--script`, `-w`, `-h`, `--mem-pages`, `--frame-timeout`, and `--max-fps`.
Development SSH listens on `127.0.0.1:2222` by default; use `--addr` to select a
different loopback address and `--allow-nonloopback-ssh` to lift that guard.
`pt dev --ssh` also installs a `plumtree.dev` alias into `~/.ssh/config`, so a
plain `ssh plumtree.dev` reaches the running app — rename it with `--host
ALIAS`, or pass `--no-ssh-config` to skip the write and print the raw ssh
command instead.

Deploy and destructive `secret rm`, `egress rm`, and `access rm` operations ask
for confirmation in a terminal. Use `--yes` for a non-interactive command.

Author and device identity use dedicated per-server Ed25519 keys over the
`plumtree-pair-v1` SSH subsystem. A local operator creates a one-use bootstrap
authority before the first author pairs. Later devices use an invitation from
an active device, and offline recovery rotates the recovery phrase and revokes
lost devices. No browser claim, dashboard, or bearer token is part of the
clean API contract.

## Security model

RCE is the product, not a bug — every app is hostile by default, so the goal is
**containment, not prevention**.

- **WASM/wazero is the primary boundary.** Each app runs as a WASI *reactor*
  with no ambient filesystem, env, args, or network — it can only call the host
  functions we import. By default a session executes in the serving process;
  production configuration requires the isolated runner-worker boundary
  (`runtime.runnerEndpoint` over `unix://` or `tls://`, with the disposable
  worker owned by a separate runner role), so untrusted WASM leaves the
  server process whenever isolation is configured.
- **App-scoped capability.** Secrets and egress are loaded only for the app
  selected for that session. Egress stays default-deny.
- **No raw-ANSI escape path.** The guest returns structured cells (`rune + RGB +
  decor`); the host renders them and sanitizes every rune, so apps can't attack
  the viewer's terminal.
- **Build is local.** `pt build` compiles the author's project before the typed
  artifact is uploaded. The server never runs uploaded source or build tools.
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

The end-to-end author loop works against a fresh local control plane:

```
operator: plumtree bootstrap -handle alice -device laptop
server:   go run ./cmd/plumtree
author:   pt pair → pt status → pt new → pt dev → pt build → pt deploy
```

Local server startup persists the SQLite repository and SSH host key (point
`storage.sshIdentity` at your own key with `-host-key` or `PLUMTREE_HOST_KEY` to
pin it explicitly). The clean transport is SSH-only; there is no public HTTP
listener or shared deploy token. Run `pt help <command>` for the exact grammar
of every author command. The first `plumtree serve` creates the strict configuration at the platform
config path. Operators can use `plumtree config show`,
`plumtree config set <field> <value>`, and `plumtree config unset <field>`;
changes take effect after restart. `-config` or `PLUMTREE_CONFIG` selects an
explicit file.

A locally built app is stored as a typed WASM artifact through `/api/v1`.
Public leaf sessions admit anonymous and proved-key visitors. Restricted leaf
sessions admit only the owner devices and app access keys. Native development
can run in process; production configuration requires the authenticated Unix
runner boundary.

Compose uses a combined control/gateway service and a networkless runner
service. They share only an authenticated Unix socket. Both use read-only root
filesystems and bounded resources, and only SSH is published.

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
- **Deploy** — publish an app via `pt` (the privileged author action).
- **Run** — connect to an app with plain `ssh`; the platform executes it.

## License

Plumtree is licensed under the [MIT License](LICENSE).
