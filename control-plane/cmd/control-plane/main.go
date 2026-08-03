// Command control-plane is the temporary legacy caller for the root-owned
// control server role.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/server/controlrole"
)

func main() {
	controlrole.Run(os.Args[1:])
}
