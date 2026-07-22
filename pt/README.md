# pt

Plumtree author CLI.

Owns:

- `pt new` — scaffold the standard app shape.
- `pt dev` — compile the app to WASM and run it locally in wazero (the same ABI
  the platform uses), as a local TTY app, a scripted headless run, or a local
  SSH server.
- `pt deploy` — register/update a local control-plane deploy claim.
- `pt claim` — open the browser claim page for the current deploy. This is the
  author-auth step (Shoo browser login); there is no separate `pt auth login`.
- `pt whoami`, `pt inspect`, `pt logs` — inspect the current claimed deploy.
- `pt secret set|list|rm` — manage a claimed app's server-side secrets (`ctx.Env`).
- `pt egress add|list|rm` — manage a claimed app's default-deny egress allowlist
  (`ctx.Fetch`).

Does not own:

- hosted runner implementation.
- control-plane persistence.
- SSH gateway serving.

## Quickstart

```bash
pt new counter --tui      # scaffold counter/{go.mod,plumtree.json,.gitignore,README.md,AGENTS.md,app/main.go}
pt new hello --cli        # non-interactive CLI app
cd counter

pt dev                    # build to WASM, run in the local terminal
pt dev --headless --script "up,up,down,q"   # scripted, no PTY (deterministic)
pt dev --ssh              # serve over SSH; connect with ssh counter@plumtree.dev
```

`pt dev --ssh` runs a local SSH server (anonymous auth, stable local dev host
key) and streams the app to any `ssh` client — the same way users will reach
deployed apps. It writes a managed block to `~/.ssh/config` so the connection is
simple:

```
ssh counter@plumtree.dev
```

Action-enabled apps also accept the production-compatible exec form:

```bash
ssh counter@plumtree.dev 'action get_identity {}'
```

The action name is a single token and everything after it is passed unchanged
as the JSON request. Responses are a single JSON line and the SSH exit status
is non-zero only when the session itself cannot be started.

Use `--host <name>` to pick a different local alias:

```
pt dev --ssh --host apps.local
ssh counter@apps.local
```

Use `--no-ssh-config` if you do not want `pt` to update `~/.ssh/config`; in that
mode it prints the raw command with `-p` and host-key options.

Every connection gets a fresh wazero session wired to the SSH channel:
keystrokes in, host-rendered frames out. Resize (window-change) is forwarded;
the guest never emits raw ANSI.

Resource limits apply in all modes: a linear-memory page cap (`--mem-pages`) and
a per-frame wall-clock deadline (`--frame-timeout`) that terminates a runaway
guest.

The local sandbox lives in `github.com/Ceinl/plumtree/runner` (the wazero host + renderer)
and `internal/sshdev` (the local single-app SSH server); `internal/scaffold`
generates apps.

## Local dashboard deploy

Start the control plane with the local dev deploy API enabled:

```bash
cd /Users/c/code/plumtree/control-plane
go run ./cmd/control-plane \
  -addr 127.0.0.1:18080 \
  -origin http://localhost:18080 \
  -dev-token local-dev \
  -ssh-addr 127.0.0.1:2222
```

Configure the control-plane URL and deploy token once, then register the current
app/deploy:

```bash
pt configure --addr http://localhost:18080 --token
pt deploy
```

The shorter git-style form is equivalent:

```bash
pt --addr http://localhost:18080 --token
```

Run `pt configure` with no flags to show the saved address and whether a token
is configured. With an interactive terminal, `--token` prompts without echoing
the token; it also accepts a single line from standard input for secret managers
and CI (`--token-stdin` is an explicit alias). The token itself is never
printed. Use `--clear-addr` or `--clear-token` to remove a saved value.
Configuration is stored with mode `0600` under the OS user config directory
(`plumtree/pt.json`).

The first deploy prints `Claim: pt claim` and writes `.plumtree/deploy.json`.
Run `pt claim` within 5 minutes, sign in with Shoo in the browser, and choose a
handle if needed. Later `pt deploy` runs update the same claimed app by using
the saved deploy claim token.

Public releases are generic and contain no server address or deploy token.
`PLUMTREE_SERVER_URL` and `PLUMTREE_DEV_TOKEN` remain available as temporary
environment overrides, which is useful for CI. Environment values take
precedence over `pt configure`; `PLUMTREE_PT_CONFIG` selects an alternate config
file for isolated automation.

After claiming, these commands use the same saved local claim metadata:

```bash
pt whoami
pt inspect
pt logs
```

Refresh the dashboard and the app should appear with its active deploy ID. The
deploy uploads the WASM bytes to the control-plane store, so the SSH gateway can
run it:

```bash
ssh counter@plumtree.dev
```

The control plane backs this with durable on-disk artifact storage
(`--blob-dir`) and can run each session in an out-of-process WASM worker
(`--runner-worker`); see the control-plane and runner READMEs.
