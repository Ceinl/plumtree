# Plumtree — cloud for SDK-written CLI/TUI apps

A centralized cloud for terminal apps, built in **Go**. Write a CLI/TUI against
the **Plumtree SDK**, deploy it with one command, and anyone runs it with just
`ssh` — no install of any kind. Plumtree's servers run every app sandboxed in
WebAssembly and stream it to the user's terminal.

**Lakebed, but for the terminal** (and in Go): instead of full-stack web
*capsules*, Plumtree hosts SDK-written *terminal apps*, rendered server-side and
streamed over SSH. V1 is a new app platform, not a generic host for arbitrary
existing terminal binaries.

Plumtree is two programs:

- **Server (platform)** — receives deployed apps, compiles them to WASM, and
  runs each in a wazero sandbox, served to users over its own SSH server.
- **CLI** (`pt`) — the author tool: scaffolds, dev-runs, and deploys apps. Not
  needed to *run* apps — running is plain `ssh`.

Prior art: Terminal.shop, Ssh.chat. Model inspiration: **Lakebed**
(lakebed.dev) — agent-native CLI + runtime for deployable apps. Plumtree builds
its **own** TUI runtime — no Charm/Bubble Tea dependency.


## What we adopt from Lakebed

- **Low-friction deploy.** `pt deploy` should feel immediate, but public deploy
  requires lightweight identity and rate limits from day one. Fully anonymous
  deploy is a later mode, gated by quotas, moderation, and kill switches.
- **Progressive trust = progressive capability.** Unclaimed preview apps, once
  enabled, run in the tightest sandbox (no egress, no secrets, hard limits).
  *Claiming* an app (auth) unlocks egress and server env.
- **A standardized app shape the platform recognizes** — `pt new` scaffolds it.
- **The SDK is the sandbox surface.** Apps reach the outside world *only* through
  a capability object handed in by the host.
- **Committed deploy pointer** (`plumtree.json` with a `deployId`) for CI/CD.
- **CLI as the whole control plane** — deploy, logs, inspect, auth.


## The model: centralized, Go SDK, WASM-sandboxed

- **Centralized, multi-tenant.** One platform runs apps from many untrusted
  authors, side by side.
- **Go SDK + own runtime.** A Plumtree app is **Go** written against the
  Plumtree SDK and its own from-scratch TUI runtime (no Charm). The runtime is
  the one already built in **Plums** (`github.com/Ceinl/plums`,
  `internal/ui/tui`), extracted into a separate library and then wrapped by the
  SDK. Its shape:
  - **Flexbox layout** — a `Div` tree with `Direction` (Row/Column),
    `JustifyContent`, `AlignItems`, `Unit` sizing (`Px`/`Percent`/`Grow`), and
    `Padding`.
  - **Double-buffered diffing `Screen`** — a cell grid that calculates changed
    segments. In hosted mode, the guest returns structured cells/ops and the
    host performs terminal rendering; apps never emit raw ANSI.
  - **Loop:** `state → build component tree → Layout(0,0,w,h) → Render(screen)
    → structured frame`. Swappable `theme.Palette`; a widget library (text,
    editor, chatlog, popup, palette, statusbar…).
- **WASM sandbox (decided).** Each app is compiled to **WebAssembly**
  (`GOOS=wasip1 GOARCH=wasm`, or TinyGo) and run **server-side** in **wazero**,
  a pure-Go WASM runtime. WASI is configured with no ambient filesystem, env, or
  network access; the guest can only use the host imports we expose. This is a
  strong primary sandbox, not the only isolation boundary.
  - WASM is **not** a browser thing here. The app runs server-side; WASM is
    purely the isolation boundary. The user still sees a normal terminal.
- **The terminal is a dumb client.** The app runs in its WASM sandbox on the
  platform; keystrokes flow in, rendered frames flow out over SSH/PTY. The
  user's machine runs none of the app's code.


## App shape (the Plumtree app)

```
app/main.go              # entrypoint: the CLI/TUI app definition
go.mod
plumtree.json            # committed: { "deployId": "..." } once claimed
.env.plumtree.server.local # optional gitignored import file; secrets live server-side
```

A TUI, using the Plums runtime (state → build tree → layout → diff-render):

```go
package main

import (
    "fmt"

    "github.com/Ceinl/plumtree/sdk"
    "github.com/Ceinl/plumtree/sdk/tui"            // layout + screen (the Plums runtime)
    "github.com/Ceinl/plumtree/sdk/tui/components" // Div, Text, ...
)

type state struct{ n int }

// Handle input by mutating state.
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

// Build the component tree from state each frame; the runtime lays it out and
// diff-renders it to the cell screen.
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

The app reaches the world *only* through capabilities the host imports into the
WASM guest: `ctx.DB` / `ctx.KV` (scoped storage), `ctx.Auth` (who's connected),
`ctx.Env` (claimed-only secrets), `ctx.Fetch` (gated by trust tier), `ctx.Log`,
and structured I/O. Raw `os`/`net` are not part of the supported SDK surface.

The Go API hides a narrow WASM ABI. Host calls use integer handles plus
`ptr/len` buffers in guest linear memory; larger values such as render frames,
logs, storage requests, and fetch responses are serialized SDK messages. This
ABI is versioned separately from the friendly Go package API.

TUI mode and CLI mode have different output rules:

- TUI apps return a structured component/cell frame; the host renders it.
- CLI apps may write text through `ctx.Out`, but the host filters control
  characters and terminal escape sequences before forwarding output.


## Trust boundary — who runs what

- **Connecting users run nothing locally.** The app executes on Plumtree's
  servers; the user's machine runs only `ssh` + a PTY (a terminal proxy). A
  malicious app **cannot read a user's files, env, or disk** — the code isn't on
  their machine.
- **What a user exposes:** the SSH key offered by their client and whatever
  they type — *not* their local files. Public keys are not secret, but they can
  be stable identifiers, so this is still a privacy surface.
- **The execution risk lives with the platform (us).** We run untrusted code, so
  the sandbox is our problem.


## Security — threat model

RCE is the product, not a bug. Every app is hostile by default; containment, not
prevention, is the goal.

### The WASM sandbox + SDK is the primary boundary

The app is WASM in wazero with a minimal WASI config and **no ambient platform
authority**; it can only call host functions we import (the `ctx` capabilities).
This is the main wall, but not the whole safety story: host function bugs,
runtime bugs, and resource exhaustion still matter. Production runners should be
separate worker processes/containers from the control plane, even when the guest
itself is WASM.

### Progressive trust = capability

| Tier | Unlocks | Network | Secrets |
|------|---------|---------|---------|
| Unclaimed preview | later, quota-gated deploy | none (default-deny) | none |
| Claimed (authed author) | `ctx.Env`, gated `ctx.Fetch` | allowlist egress | yes |

For MVP, anonymous **run** is supported for public apps. Anonymous **deploy** is
deferred until abuse controls exist. Claimed apps get secrets only through the
server-side secret store, and any egress-capable app can exfiltrate its own
secrets by design, so egress policy must be explicit and narrow.

### What SSH does and doesn't protect

SSH secures the **channel only** (encryption in transit, server/client auth). It
does *not* protect data at the server end, data from us (the operator), other
tenants if isolation is weak, the client's terminal, or data at rest.

### The four attack vectors

1. **Malicious author, run-time** — defense: WASM capability sandbox + tiers.
2. **Malicious author, build-time** — compiling untrusted Go (and its deps) runs
   code before run. **Build is sandboxed too**: isolated build workers, no
   secrets, no default network, bounded source/archive size, bounded CPU/memory,
   isolated module cache, checksum enforcement, and an explicit policy for
   third-party modules.
3. **Plumtree's own protocol/CLI** — the avoidable RCE: never shell-interpolate
   app names/handles; strict `[a-z0-9-]` names, no path separators; safe config
   parsing, no eval.
4. **Connection path** — terminal escape-sequence attacks on the viewer.
   **Mitigated by design:** the guest returns a `View` (structured output); the
   *host* renders it to the terminal, and all CLI text output is filtered, so
   apps can't emit raw escape codes.

### Isolation decision

WASM/wazero for v1 — cheap, capability-sandboxed, server-side. **gVisor /
Firecracker microVMs** are needed only if Plumtree later runs *native* binaries
(deferred; would also drop the WASM compile constraints).

### Non-negotiables

- Default-deny egress; allowlist only after claim.
- Ephemeral instances; no cross-tenant state except via scoped `ctx.DB`/`KV`.
- Resource limits: wall-clock deadlines, cancellation, memory page caps, process
  RSS caps, CPU quotas, output rate, input rate, storage quotas, and per-author
  concurrency caps.
- **Deploy is the crown jewel** — gate it harder than run.
- Abuse & cost controls: quotas, rate limits, kill switches.


## Running apps — just `ssh`

Running an app needs **no Plumtree install** — only `ssh`, which everyone
already has. The host is the platform; the **username carries the app**, and the
platform identifies you by your SSH public key when auth is needed (or accepts
anonymous sessions for public apps). This is the terminal.shop model,
multi-tenant.

```
ssh <owner>/<app>@plumtree.app   # run an app from the platform
ssh <app>@plumtree.app           # shorthand for a global / your own app
```

The connecting side is pure `ssh` + a PTY — a dumb terminal. No `pt` needed to
*use* an app; `pt` is only for *building and shipping* them.


## App — CLI (`pt`) — author/deploy only

The `pt` CLI is the **authoring tool**. It scaffolds, runs locally, deploys, and
inspects apps. It is **not** used to run/consume apps — that's `ssh` (above).

### Auth

Author auth is the deploy **claim**: `pt claim` opens a Shoo browser login that
binds the deploy to an owner. There is no separate `pt auth login` — possession
of the claim token (saved in `.plumtree/deploy.json`) authorizes later updates,
secrets, and egress for that app.

```
pt claim                        # browser-claim the current deploy to your owner
pt whoami                       # show the claimed app namespace
pt secret set KEY               # server-side secret; never committed (claimed apps)
pt egress add HOST              # gated egress allowlist (claimed apps)
```

### Build & deploy

```
pt new <name> --tui|--cli       # scaffold the standard Go app shape
pt dev                          # compile to WASM + run locally in wazero (loopback)
pt deploy                       # compile + deploy to a claimed namespace.
                                #   auth unlocks env + gated egress
pt deploy --anonymous-preview   # later: only after abuse controls exist
```

### Inspect

```
pt logs <app>
pt inspect <deploy-id-or-handle>
pt db list | dump               # scoped app storage
```


## Config

- **`plumtree.json`** (committed) — `{ "deployId": "..." }` once claimed; lets
  CI/CD update the same app. This is a pointer, not a credential; every update
  still requires owner auth or a scoped CI token.
- **Claiming** — anonymous preview deploys, once enabled, must be claimable only
  by possession of a one-time claim token or the original deploy credential.
- **Secrets** — server-only secrets are managed with `pt secret set/list/rm`,
  injected at run-time for claimed apps, and never present at build-time.
  `.env.plumtree.server.local` is only an optional local import file and must be
  generated into `.gitignore`.
- The **app shape + SDK contract is the build/run contract** — the platform
  compiles `app/main.go` to WASM. V1 intentionally supports SDK-shaped apps
  only; arbitrary existing CLIs/TUIs are not a compatibility target.


## App — Server (platform)

- **Build pipeline** — receive source → compile Go → WASM *in a sandboxed build
  worker* with restricted modules, no secrets, bounded CPU/memory/disk, isolated
  caches, and no default network → store the `.wasm` artifact.
- **Run** — instantiate the WASM module per session in an isolated runner
  process/container, with reusable compiled-module caching where useful; import
  the `ctx` capability host functions; mediate I/O through the versioned ABI:
  keystrokes → `KeyMsg`, guest structured frame → terminal output rendered and
  sanitized host-side.
- **SSH layer** — Plumtree's own SSH server (Go `crypto/ssh` + PTY); resize,
  signals, disconnects, concurrent users of one app.
- **Auth & authz** — SSH-key auth; stricter gate for *deploy* than *run*;
  per-app ownership and `<owner>/<app>` namespaces.
- **Storage** — scoped `ctx.DB`/`ctx.KV` per app; never shared across tenants.
- **Limits & abuse** — per-app time/mem/CPU/output/storage/concurrency limits,
  egress allowlist, deploy quotas, reputation controls, moderation hooks, and
  kill switches.


## MVP

Live status is tracked in `PLAN.md`. Summary as of 2026-06-25:

1. ✅ `pt new` / `pt dev` — scaffold a Go app and run it locally in wazero
   (terminal, headless-scripted, or local SSH).
2. ✅ `pt deploy` — upload source, build server-side to WASM in a sandboxed
   build worker, run in wazero with resource limits; `pt claim` + Shoo browser
   auth claims ownership (this *is* author auth — no separate `pt auth login`).
3. ✅ `ssh <app>@plumtree.dev` — connect (no install) to the sandboxed app via
   the control plane's local SSH gateway.
4. ✅ `pt secret set` + `pt egress add` — `ctx.Env` (server-side secrets) and
   `ctx.Fetch` (default-deny gated egress) unlocked for claimed apps.

The host capabilities beyond render/input are wired end to end, all sharing the
host-import + ptr/len ABI pattern: **KV** (durable per-app state), **pub/sub**
(live cross-session messaging, ABI v2), **Auth** (SSH-key identity), **Env**
(claimed-only secrets), and **Fetch** (claimed-only gated egress). `ctx.DB`
(richer scoped storage) is the remaining future capability.

**Prototype-first risk to retire:** confirm the Plumtree runtime + a real TUI
loop compile and run under `wasip1`/TinyGo in wazero, with input/render bridged
through the host renderer to an SSH PTY. ✅ Retired in the Phase 1 spike (see
findings below).

Defer: fully anonymous public deploy, native binaries / microVMs, non-Go
languages, preview environments, teams/orgs, billing, custom handles, and
compatibility with arbitrary existing terminal apps.


## Phase 1 findings (WASM feasibility spike)

The load-bearing assumption is **validated**. A counter TUI built on the
extracted `plumtree-tui` runtime compiles to `wasip1` WASM, runs in wazero with
no ambient authority, exchanges input/frames with the host over a `ptr/len` ABI,
and is rendered host-side. See `spike/` for the prototype and `spike/README.md`
for the full write-up. Confirmed on Go 1.26.2 + wazero v1.12.0.

What the spike established:

- **Reactor build is required.** The host-driven frame loop needs the guest to
  export functions callable repeatedly, so guests build as WASI *reactors*
  (`GOOS=wasip1 GOARCH=wasm -buildmode=c-shared` + `//go:wasmexport`), not as
  one-shot commands. The host calls `_initialize` once, then `frame` per event.
- **The render core is WASM-clean.** `screen`, `layout`, and `components`
  compile to `wasip1` unchanged; only `terminal`/`app` (raw mode, signals) are
  host-only — exactly the guest/host split hosted mode needs. The guest renders
  into a cell buffer and returns a snapshot; it never flushes or touches a TTY.
- **No raw ANSI by construction.** The ABI frame carries `rune + RGB + decor
  bits`, never escape strings; the host builds all terminal output itself and
  sanitizes every rune (C0/C1/DEL → space). A hostile guest has no escape path.
- **Cancellation works.** A per-frame wall-clock deadline plus wazero
  `WithCloseOnContextDone` reliably aborts a guest stuck in an infinite compute
  loop; the session is then torn down.
- **Limits enforced.** `WithMemoryLimitPages` caps linear memory; guest
  stdout/stderr are captured as logs, never forwarded to the terminal; WASI is
  configured with no filesystem, env, args, or network.

Known limitations / follow-ups carried forward:

- **Color storage.** `screen.Cell` holds colors as ANSI SGR *strings*; the guest
  parses them back to RGB at the ABI edge. The runtime should store structured
  color so hosted mode needs no parsing round-trip.
- **Guest allocator.** The spike pins host-visible buffers in a `map[ptr][]byte`.
  The runner wants a bump/arena allocator or fixed double-buffer instead.
- **Frame granularity.** One synchronous `frame()` per event, full-grid each
  time. Guest-initiated ticks/streaming and guest-side diffing are future work.
- **Memory floor / TinyGo.** Stock Go's `wasip1` module declares a ~39-page
  (~2.5 MiB) memory minimum and a 2.6 MiB binary — runtime overhead TinyGo
  should reduce, but TinyGo's `wasip1` reactor support must first be confirmed
  against the Go 1.26 features this runtime uses. Comparison deferred.
- **Unicode width.** Wide/combining runes are still treated as one cell; a
  runtime concern unaffected by the spike.


## Glossary

- **Plumtree / pt** — the platform and its CLI.
- **App** — an SDK-written Go terminal program, namespaced `<owner>/<app>`,
  compiled to WASM.
- **SDK** — `github.com/Ceinl/plumtree/sdk`; the runtime + the only surface an app reaches the
  world through.
- **ctx** — the capability object (host functions) handed to an app: db, auth,
  env, fetch, io.
- **Sandbox** — the wazero WASM instance an app runs in (server-side).
- **Claim** — authenticating ownership to unlock higher-trust capabilities.
- **Deploy** — publish an app via `pt` (the privileged author action).
- **Run** — connect to an app with plain `ssh` (no install); the platform
  executes it server-side.
