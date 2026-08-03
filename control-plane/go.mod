module github.com/Ceinl/plumtree/control-plane

go 1.26.5

require (
	github.com/Ceinl/plumtree v0.0.0-20260802172637-f7a0d99b480f
	github.com/Ceinl/plumtree/build-worker v0.0.0
	golang.org/x/crypto v0.54.0
)

require (
	github.com/Ceinl/plumtree/sdk v0.0.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
)

replace (
	github.com/Ceinl/plumtree => ..
	github.com/Ceinl/plumtree/build-worker => ../build-worker
	github.com/Ceinl/plumtree/sdk => ../sdk
)
