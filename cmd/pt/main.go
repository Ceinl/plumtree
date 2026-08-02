// Command pt is the staged root author CLI entrypoint.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/cli"
)

// These variables keep the private-release linker injection surface on the
// command package while the implementation lives in internal/cli.
var (
	defaultServerURL string
	defaultDevToken  string
	devRoot          string
)

func main() {
	cli.DevRoot = devRoot
	cli.DefaultServerURL = defaultServerURL
	cli.DefaultDevToken = defaultDevToken
	os.Exit(cli.Run(os.Args[1:]))
}
