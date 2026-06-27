module github.com/Ceinl/plumtree/control-plane

go 1.26

require (
	github.com/Ceinl/plumtree/build-worker v0.0.0
	github.com/Ceinl/plumtree/runner v0.0.0
	github.com/Ceinl/plumtree/ssh-gateway v0.0.0
)

require (
	github.com/Ceinl/plumtree/sdk v0.0.0 // indirect
	github.com/Ceinl/plumtree/tui-runtime v0.0.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/term v0.44.0 // indirect
)

replace (
	github.com/Ceinl/plumtree/build-worker => ../build-worker
	github.com/Ceinl/plumtree/runner => ../runner
	github.com/Ceinl/plumtree/sdk => ../sdk
	github.com/Ceinl/plumtree/ssh-gateway => ../ssh-gateway
	github.com/Ceinl/plumtree/tui-runtime => ../tui-runtime
)
