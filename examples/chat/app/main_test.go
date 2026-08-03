package main

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk/plumtest"
)

func TestChatCleanRuntime(t *testing.T) {
	runtime := plumtest.Start(t, &chat{}, plumtest.Viewport(40, 8))
	runtime.ExpectText("Clean chat")
	runtime.Key('q')
	runtime.ExpectQuit()
}
