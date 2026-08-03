// Command pt is the staged root author CLI entrypoint.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/cli"
)

// These variables keep the private-release linker injection surface on the
// command package while the implementation lives in internal/cli.
var devRoot string

func main() {
	cli.DevRoot = devRoot
	os.Exit(cli.RunClean(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
