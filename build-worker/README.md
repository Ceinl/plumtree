# Plumtree Build Worker

Sandboxed service that compiles uploaded Go app source into WASM artifacts.

Owns:

- isolated build execution.
- source size and build-time limits.
- module cache isolation.
- checksum/module policy enforcement.
- build logs.
- WASM artifact output.

Does not own:

- app metadata authority.
- runtime session execution.
- SSH connections.
