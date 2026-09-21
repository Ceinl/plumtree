package main

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk/plumtest"
)

func TestGameCleanRuntime(t *testing.T) {
	runtime := plumtest.Start(t, &game{}, plumtest.Viewport(40, 8))
	runtime.ExpectText("Tic-tac-toe")
	runtime.Key('q')
	runtime.ExpectQuit()
}
