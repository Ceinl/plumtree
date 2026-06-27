---
name: dmux-project
description: What dmux is, its design, and how it reuses plumtree / herdr
metadata:
  type: project
---

`/Users/c/code/dmux` is a NEW project (started 2026-06-25; `SPEC.md` + `PLAN.md`
written and a Phase-0 Go scaffold builds: module `github.com/Ceinl/dmux`,
`cmd/dmux` with `server`/`host`/`attach` subcommands stubbed, `internal/proto`
control messages, `internal/{hub,host,client}` stubs): a **device
multiplexer**. One device runs `dmux server` (the hub/relay); every other device
connects and joins as a **host** (shares terminal sessions others can attach to),
a **user** (browses + attaches to host sessions to view/control), or both. Think
"tmux across devices" / herdr's server-client model but the multiplexed unit is a
*device's session*, not a local pane.

Design decisions (see `dmux/SPEC.md`, `dmux/PLAN.md`):
- Transport + identity: reuse plumtree's `crypto/ssh` hub pattern
  (`control-plane/internal/sshgateway`: server.go, session.go, hostkey.go,
  killswitch, concurrency). Device identity = SSH key fingerprint
  (`PublicKeyCallback`). SSH **channels** multiplex control + per-session data.
- Live presence/status: reuse `runner.Bus`/`MemBus` pub/sub (no polling); the
  client `Source` selects on input + bus, like plumtree's `runner.TTYSource`.
- Client TUI (herdr UI reference): `tui-runtime` (flexbox `layout`, `components`
  Div/Text/Button, diff `screen`, `keyboard` incl. **mouse**). Layout = sidebar
  list of devices/sessions with live status + main attached-terminal pane +
  workspaces/tabs.

Reference: **herdr** (github.com/ogulcancelik/herdr, herdr.dev) — Rust "agent
multiplexer that lives in your terminal": server/client, workspaces/tabs/panes,
sidebar with blocked/working/done/idle status, detach/reattach, mouse-native,
works over SSH, socket API. dmux borrows its UI + server/client shape.
See [[plumtree-platform]] for the reusable runtime/gateway.
