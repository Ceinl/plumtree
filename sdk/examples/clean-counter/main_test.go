package main

import (
	"testing"

	"github.com/Ceinl/plumtree/sdk/plumtest"
)

func TestCleanCounter(t *testing.T) {
	runtime := plumtest.Start(t, &counter{}, plumtest.Viewport(40, 8))
	runtime.ExpectText("Count: 0")
	runtime.ClickButton("+")
	runtime.ExpectText("Count: 1")
	runtime.Key('q')
	runtime.ExpectQuit()
	runtime.ExpectGoodbye("Thanks for using clean-counter.")
}
