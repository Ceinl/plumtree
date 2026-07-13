# Agent Notes

This is a Plumtree cli app named `goodbye-cli`.

## Commands

- Run locally: `pt dev`
- Deterministic smoke check: `pt dev Alice`
- Serve over SSH for manual testing: `pt dev --ssh`
- Deploy: set `PLUMTREE_SERVER_URL` and `PLUMTREE_DEV_TOKEN` in the environment, then `pt deploy` (no flags)

## Project Rules

- Keep app code under `app/` unless a broader structure is needed.
- Keep `plumtree.json` committed and do not store secrets in it.
- Do not commit `.plumtree/`, `.env.plumtree.server.local`, `dist/`, or generated `*.wasm` files.
- Use the Plumtree SDK imports already present in `app/main.go`; avoid adding terminal-specific output for TUI apps because the host owns rendering.
- For TUI changes, prefer state updates in `Update` and component tree changes in `View`.
- For CLI changes, write user output through `sdk.Ctx` rather than directly to stdout.
- This app demonstrates `sdk.SetGoodbye`: it sets a goodbye message that the SSH
  gateway displays after the session ends.
- Run the deterministic smoke check before handing work back.
