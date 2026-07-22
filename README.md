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
pt new myapp --tui
pt dev
pt deploy
```

---

## How it works

A Plumtree app is a small Go program written against the SDK. You never touch
raw `os`, `net`, or ANSI — the app reaches the outside world *only* through a
capability object (`ctx`) the platform hands it.

1. **Author** — `pt new` scaffolds the standard app shape; `pt dev` compiles to
   WASM and runs it locally in [wazero](https://wazero.io) over a real PTY.
2. **Deploy** — `pt deploy` uploads source; the platform builds it to WASM in a
   sandboxed build worker and stores the artifact.
3. **Run** — a user connects with plain `ssh`. The platform instantiates the
   WASM module in an isolated runner, bridges keystrokes in and rendered frames
   out, and streams it over the SSH PTY.

The connecting user runs **nothing locally** — only `ssh` and a terminal. The
app's code never reaches their machine, so a malicious app can't touch their
files, env, or disk. The execution risk lives entirely with the platform, which
is why every app is sandboxed by default.

```
author ── pt deploy ──▶ control-plane ──▶ build-worker (Go ─▶ WASM)
                            │
user ── ssh ──▶ ssh-gateway ──▶ runner (wazero sandbox) ──▶ your app
                                   │
                       ctx: kv · pubsub · auth · env · fetch
```

## Writing an app

A TUI is *state → build a component tree → the runtime lays it out and
diff-renders it to a cell grid*. The app returns structured cells; the host
turns them into terminal output (so apps can never emit raw escape codes).

```go
package main

import (
    "fmt"

    "github.com/Ceinl/plumtree/sdk"
    "github.com/Ceinl/plumtree/sdk/tui"
    "github.com/Ceinl/plumtree/sdk/tui/components"
)

type state struct{ n int }

func (s *state) Update(ev sdk.Event) {
    if k, ok := ev.(sdk.KeyMsg); ok {
        switch k.Key {
        case sdk.KeyUp:
            s.n++
        case sdk.KeyDown:
            s.n--
        case 'q':
            sdk.Quit()
        }
    }
}

func (s *state) View() tui.Component {
    root := components.NewDiv()
    root.SetDirection(tui.Column)
    root.SetJustify(tui.JCenter)
    root.SetAlign(tui.ACenter)

    count := components.NewText()
    count.SetContent(fmt.Sprintf("Count: %d", s.n))
    hint := components.NewText()
    hint.SetContent("(↑/↓ to change, q to quit)")

    root.Add(count, hint)
    return root
}

func main() { sdk.RunTUI(&state{}, sdk.Meta{Name: "counter", Type: "tui"}) }
```

…or a non-interactive CLI:

```go
func main() {
    sdk.CLI(sdk.Meta{Name: "hello", Type: "cli"},
        func(ctx sdk.Ctx, args []string) error {
            name := "world"
            if len(args) > 0 {
                name = args[0]
            }
            ctx.Out().Printf("Hello %s\n", name)
            return nil
        })
}
```

### App shape

```
app/main.go                  # entrypoint: the CLI/TUI definition
go.mod
plumtree.json                # committed: { "deployId": "..." } once claimed
.env.plumtree.server.local   # optional, gitignored; secrets live server-side
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
| `sdk.Exec`  | run a program as the server OS user   | **claimed** apps when operator-enabled |

### Trusted self-hosted apps

Private/self-hosted servers can opt into host command execution for claimed
apps. Set `allowHostCommands` to `true` in the server JSON config, pass
`-allow-host-commands`, or set `PLUMTREE_ALLOW_HOST_COMMANDS=true`. Apps can
then invoke installed tools directly:

```go
result, err := sdk.Exec("codex", "exec", "summarize the current project")
```

The option is off by default and never applies to unclaimed preview apps. It is
an intentional trust-boundary change: commands inherit the server process's
user, working directory, and environment. Enable it only when every claimed app
and author on that server is trusted. Command output is capped and execution is
cancelled when the app session ends.

## The `pt` CLI

`pt` is the author tool — scaffold, dev-run, deploy, inspect. It is **not**
needed to *run* apps (that's `ssh`).

```
pt new <name> --tui|--cli   # scaffold the standard Go app shape
pt dev                      # compile to WASM + run locally in wazero
pt dev --ssh                # serve the local app over a local SSH channel
pt deploy                   # build server-side + deploy

pt claim                    # browser-claim the deploy to your owner (this is author auth)
pt whoami                   # show your claimed namespace
pt secret set KEY           # server-side secret (claimed apps)
pt egress add HOST          # egress allowlist entry (claimed apps)

pt logs <app>               # session logs
pt inspect <deploy|handle>  # deploy details
```

**Author auth is the deploy claim**, not a separate login: `pt claim` opens a
Shoo browser flow that binds the deploy to your owner. Possession of the claim
token (in `.plumtree/deploy.json`) authorizes later updates, secrets, and egress.

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

A multi-module Go workspace (`go.work`), split by product boundary:

| Path             | Module                                    | Purpose                                                        |
|------------------|-------------------------------------------|---------------------------------------------------------------|
| `tui-runtime/`   | `github.com/Ceinl/plumtree/tui-runtime`   | Standalone TUI runtime: layout, components, screen/diff.       |
| `sdk/`           | `github.com/Ceinl/plumtree/sdk`           | Author-facing Go SDK and the versioned WASM ABI wrapper.       |
| `pt/`            | `github.com/Ceinl/plumtree/pt`            | Author CLI: scaffold, dev, deploy, claim, logs, secrets.      |
| `control-plane/` | `github.com/Ceinl/plumtree/control-plane` | Platform API: app/deploy metadata, auth, tokens, quotas.      |
| `build-worker/`  | `github.com/Ceinl/plumtree/build-worker`  | Sandboxed source-to-WASM build service.                        |
| `runner/`        | `github.com/Ceinl/plumtree/runner`        | Isolated WASM session runner + host capability implementation. |
| `ssh-gateway/`   | `github.com/Ceinl/plumtree/ssh-gateway`   | SSH front end mapping connections to deployed app sessions.    |

## Status

The end-to-end author loop works against a local control plane:

```
server: go run ./control-plane/cmd/control-plane
author: pt new → pt dev → pt deploy → pt claim → ssh -p 2222 <app>@127.0.0.1
```

Local server startup is zero-config: HTTP and SSH bind to loopback, and a
private persistent deploy token is shared automatically with a same-user `pt`
client. Use `--tailscale` to detect and bind the machine's Tailscale IPv4
address; startup then prints the one-time client configuration needed on other
machines.

A deployed app is built server-side from uploaded source, stored as a WASM
artifact, and streamed over SSH; every session runs in its own wazero sandbox
with no ambient authority. The full host-capability surface — KV, pub/sub, auth,
env/secrets, and gated fetch — is wired end to end on a shared host-import +
`ptr/len` ABI, with native + `wasip1` builds from one source and e2e tests that
build the real WASM guest.

Production hardening is in place: hostile WASM runs behind an authenticated
Unix socket in a separate networkless runner container, with a disposable
worker process per session. Durable artifact storage, an isolated build worker,
deploy-rate limiting, anonymous preview mode, and a separate SSH gateway are
also included.

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
