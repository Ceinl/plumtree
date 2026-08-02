module github.com/Ceinl/plumtree/pt

go 1.26.5

require (
	github.com/Ceinl/plumtree v0.0.0-20260802172637-f7a0d99b480f
	github.com/Ceinl/plumtree/build-worker v0.0.0
	github.com/Ceinl/plumtree/sdk v0.0.0
	github.com/Ceinl/plumtree/ssh-gateway v0.0.0
	golang.org/x/crypto v0.54.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
)

require (
	github.com/tetratelabs/wazero v1.12.0 // indirect
)

// Sibling modules resolved locally within this workspace.
replace (
	github.com/Ceinl/plumtree => ..
	github.com/Ceinl/plumtree/build-worker => ../build-worker
	github.com/Ceinl/plumtree/sdk => ../sdk
	github.com/Ceinl/plumtree/ssh-gateway => ../ssh-gateway
)
