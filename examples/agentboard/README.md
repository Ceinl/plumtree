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

Actions are SSH exec requests, not shell commands. Each invocation prints one
JSON envelope and uses the same per-app KV, pub/sub, identity, env, and fetch
capabilities as the TUI:

```sh
ssh "${OWNER}/agentboard@plumtree.dev" 'action get_identity {}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action list_boards {}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action list_tasks {"board":{"type":"user"}}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action create_task {"board":{"type":"project","project":"plumtree"},"title":"Add smoke test","description":"Cover the release topology"}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action advance_task {"board":{"type":"project","project":"plumtree"},"task_id":"task-000001","expected_status":"todo"}'
```

The app owner creates and manages project boards with proved SSH fingerprint
strings. Fingerprints are hashed before membership metadata is persisted:

```sh
ssh "${OWNER}/agentboard@plumtree.dev" 'action create_project_board {"project":"plumtree","name":"Plumtree"}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action add_project_member {"project":"plumtree","identity":"SHA256:AbCdEf0123456789AbCdEf0123456789AbCdEf01234"}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action remove_project_member {"project":"plumtree","identity":"SHA256:AbCdEf0123456789AbCdEf0123456789AbCdEf01234"}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action rename_project_board {"project":"plumtree","name":"Plumtree Platform"}'
ssh "${OWNER}/agentboard@plumtree.dev" 'action archive_project_board {"project":"plumtree","archived":true}'
```

Personal boards accept only `{"type":"user"}`. Their opaque board ID is
derived from the proved caller identity; there is no target-user field, so an
agent—or the app owner—cannot select another identity's personal board.
Anonymous sessions are rejected. Project access is implicit for the app owner
and otherwise requires the caller's hashed identity in that project's
allowlist.

Workflow authority is shared deliberately:

- callers can move their own Personal-board tasks through the entire workflow;
- on project boards, action callers create tasks in `pending` and advance `todo → in-progress → in-review`;
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
