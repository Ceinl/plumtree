// Familiar is a Plumtree leaf: a summonable terminal spirit backed by GLM.
// An interactive SSH session opens a chat with persistent per-user memory;
// SSH exec asks the same spirit one question at a time (`… ask "why wasm?"`).
package main

import (
	"github.com/Ceinl/plumtree/sdk/app"
)

func main() {
	app.Run(newModel(), app.WithCommands(buildCLI(realComplete, secretLookup)))
}

// newModel returns the chat model wired to the real capabilities. Every
// hook is a field so plumtest can substitute deterministic fakes.
func newModel() *model {
	return &model{
		complete: tuiComplete(realComplete),
		lookup:   secretLookup,
		whoami:   identityWhoami,
		w:        80,
		h:        24,
	}
}
