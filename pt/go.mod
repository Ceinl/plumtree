module github.com/Ceinl/plumtree/pt

go 1.26

require (
	github.com/Ceinl/plumtree/build-worker v0.0.0
	github.com/Ceinl/plumtree/runner v0.0.0
	github.com/Ceinl/plumtree/tui-runtime v0.0.0
	golang.org/x/crypto v0.53.0
)

require (
	github.com/Ceinl/plumtree/sdk v0.0.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.44.0 // indirect
)

// During the spike the SDK and runtime live in sibling local repos.
replace (
	github.com/Ceinl/plumtree/build-worker => ../build-worker
	github.com/Ceinl/plumtree/runner => ../runner
	github.com/Ceinl/plumtree/sdk => ../sdk
	github.com/Ceinl/plumtree/tui-runtime => ../tui-runtime
)
