# Agentboard capability example

Agentboard is a deployable Plumtree example for exercising framework
capabilities with humans and external software agents. It is not automatically
hosted or claimed by the repository. It has five columns: `pending`, `todo`,
`in-progress`, `in-review`, and `done`.

Deploy and claim it with `pt`; the resulting SSH handle is determined by the
owner account used during the claim. Set `OWNER` to that handle for the examples
below:

```sh
pt deploy
pt claim
export OWNER=your-handle
ssh "${OWNER}/agentboard@plumtree.dev"
```

The clean CLI is available through SSH exec. It uses the same identity and
durable KV capabilities as the interactive app:

```sh
ssh "${OWNER}/agentboard@plumtree.dev" 'get_identity'
```

The interactive app retains the board workflow and uses typed commands and
capability operations; the CLI currently exposes identity as its stable
machine-facing smoke path.

Workflow authority is shared deliberately:

- callers can move their own Personal-board tasks through the entire workflow;
- in the TUI, clicking a task's left edge moves it back and clicking the rest moves it forward;
- on project boards, command callers create tasks in `pending` and advance `todo → in-progress → in-review`;
- only the app owner, using TUI controls, advances project tasks through `pending → todo` and `in-review → done`;
- callers pass `expected_status` on advancement, so concurrent stale updates
  return a typed `conflict` rather than chaining or losing an update.

Build and test:

```sh
go test ./...
GOOS=wasip1 GOARCH=wasm go build -o agentboard.wasm ./app
pt dev --headless --script 'right,left,q'
pt dev --ssh
```

Issue #9 (self-service SSH-key enrollment) is deferred because a proved,
unregistered key still supplies a stable personal identity and can be added to
a project by fingerprint. Issue #10 (timers/async commands) is deferred because
board refresh uses board-scoped pub/sub events.
