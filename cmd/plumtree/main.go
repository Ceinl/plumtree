// Command plumtree is the staged root server entrypoint.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/server/controlrole"
)

func main() {
	controlrole.Run(os.Args[1:])
}
