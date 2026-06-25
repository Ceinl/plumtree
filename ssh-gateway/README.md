# Plumtree SSH Gateway

SSH server that lets users run deployed apps with plain `ssh`.

Owns:

- Go `crypto/ssh` server.
- app handle parsing.
- PTY/session lifecycle.
- key input, resize, signal, and disconnect forwarding.
- streaming host-rendered terminal output.

Does not own:

- app execution internals.
- deploy/build APIs.
- persistent control-plane state.
