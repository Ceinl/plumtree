module github.com/Ceinl/plumtree

go 1.26.5

require (
	github.com/Ceinl/plumtree/sdk v0.0.0
	github.com/mattn/go-sqlite3 v1.14.49
	github.com/tetratelabs/wazero v1.12.0
	golang.org/x/crypto v0.54.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
)

replace github.com/Ceinl/plumtree/sdk => ./sdk
