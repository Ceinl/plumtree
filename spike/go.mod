module github.com/Ceinl/plumtree/spike

go 1.26

require (
	github.com/Ceinl/plumtree/tui-runtime v0.0.0
	github.com/tetratelabs/wazero v1.12.0
)

require (
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/term v0.42.0 // indirect
)

// The TUI runtime lives in a sibling local repo during the spike.
replace github.com/Ceinl/plumtree/tui-runtime => ../tui-runtime
