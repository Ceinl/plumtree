module github.com/Ceinl/plumtree/runner

go 1.26.5

require (
	github.com/Ceinl/plumtree/sdk v0.0.0
	github.com/Ceinl/plumtree/tui-runtime v0.0.0
	github.com/tetratelabs/wazero v1.12.0
)

require golang.org/x/sys v0.47.0 // indirect

replace (
	github.com/Ceinl/plumtree/sdk => ../sdk
	github.com/Ceinl/plumtree/tui-runtime => ../tui-runtime
)
