//go:build !wasip1

// This stub keeps `go build ./...` / `go vet ./...` green on the host platform.
// The real guest (main_wasip1.go) only builds for GOOS=wasip1, where
// //go:wasmexport and the linear-memory pointer arithmetic are valid. Build the
// guest with: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared ./guest
package main

func main() {}
