package main

import (
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/plumtest"
)

func TestAsciiSaverCleanRuntime(t *testing.T) {
	runtime := plumtest.Start(t, &saver{}, plumtest.Viewport(40, 8))
	runtime.ExpectText("PLUMTREE NIGHT GARDEN")
	runtime.Advance(90 * time.Millisecond)
	runtime.ExpectText("shimmer frame: 1")
	runtime.Key('q')
	runtime.ExpectQuit()
}
