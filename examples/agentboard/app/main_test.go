package main

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk/plumtest"
)

func TestAgentboardCleanRuntime(t *testing.T) {
	runtime := plumtest.Start(t, &board{}, plumtest.Viewport(40, 8))
	runtime.ExpectText("Agentboard")
	runtime.Key('q')
	runtime.ExpectQuit()
}
