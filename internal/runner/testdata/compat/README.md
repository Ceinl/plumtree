# Baseline compatibility artifacts

`abi-v4-counter.wasm.gz` is the counter example built from Plumtree commit
`9ac60460f3ed65ce98f0ef23276c0e0278875c50` with Go 1.26.5:

```sh
cd sdk/examples/counter
GOOS=wasip1 GOARCH=wasm go build -trimpath -buildvcs=false -o abi-v4-counter.wasm .
gzip -n -9 abi-v4-counter.wasm
```

The uncompressed artifact is 3,350,824 bytes and has SHA-256 digest
`b6c898405cc3526b2b6f0395ada7af5d2ea7344987c822778360938acdd3b482`.

This file represents an already-built ABI-v4 app. Do not regenerate it when the
SDK or runner changes. Consolidation tests must continue executing these exact
uncompressed bytes through both the in-process and isolated-worker runners until
the coordinated clean SDK/ABI cutover explicitly replaces this baseline.
