// Command pt is the temporary legacy caller for the root-owned author CLI.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/cli"
)

var (
	defaultServerURL string
	defaultDevToken  string
	devRoot          string
)

func main() {
	configureCLI()
	os.Exit(cli.Run(os.Args[1:]))
}

func configureCLI() {
	cli.DevRoot = devRoot
	cli.DefaultServerURL = defaultServerURL
	cli.DefaultDevToken = defaultDevToken
}
