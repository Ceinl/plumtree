// Command plumtree is the root server entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/Ceinl/plumtree/internal/server/cleanrole"
)

func main() {
	if err := cleanrole.Run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
