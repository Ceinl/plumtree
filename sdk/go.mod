module github.com/Ceinl/plumtree/sdk

go 1.26

require github.com/Ceinl/plumtree/tui-runtime v0.0.0

require golang.org/x/sys v0.43.0 // indirect

require golang.org/x/term v0.42.0 // indirect

// The runtime is a sibling module in this workspace.
replace github.com/Ceinl/plumtree/tui-runtime => ../tui-runtime
