// Command runner-worker is the root-owned process boundary used when a
// hosted WASM session must leave the server process.
package main

import (
	"log"
	"os"

	"github.com/Ceinl/plumtree/internal/runner"
)

func main() {
	if err := runner.RunWorker(os.Stdin, os.Stdout); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
