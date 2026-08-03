// Command plumtree is the staged root server entrypoint.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/server/cleanrole"
)

func main() {
	cleanrole.Run(os.Args[1:])
}
