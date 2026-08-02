// Command ssh-gateway is the temporary legacy entrypoint for the root-owned
// gateway role. The implementation lives under internal/server/gatewayrole.
package main

import (
	"os"

	"github.com/Ceinl/plumtree/internal/server/gatewayrole"
)

func main() {
	gatewayrole.Run(os.Args[1:])
}
