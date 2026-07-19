package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/runner"
)

func TestWriteGoodbyeSanitizesTerminalControls(t *testing.T) {
	message := "thanks\x1b[31m"
	var out bytes.Buffer
	writeGoodbye(&out, runner.Capabilities{Goodbye: &message})
	if got := out.String(); !strings.Contains(got, "thanks [31m") || strings.ContainsRune(got, '\x1b') {
		t.Fatalf("goodbye output = %q", got)
	}
}
