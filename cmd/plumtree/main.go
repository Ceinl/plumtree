// Command plumtree is the root server entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/internal/selfupdate"
	"github.com/Ceinl/plumtree/internal/server/cleanrole"
)

// version keeps the release linker injection surface on the command package.
var version string

func main() {
	selfupdate.StampedVersion = version
	if err := cleanrole.Run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
