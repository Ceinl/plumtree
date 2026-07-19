package main

import (
	"strings"
	"testing"
)

func TestTerminalSafeTextStripsTerminalControls(t *testing.T) {
	in := "ok\x1b]0;pwned\a\nnext\u009b31m\rrewrite\xff"
	got := terminalSafeText(in)
	for _, forbidden := range []rune{'\x1b', '\a', '\u009b', '\r'} {
		if strings.ContainsRune(got, forbidden) {
			t.Fatalf("terminalSafeText retained control %#x in %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "\nnext") {
		t.Fatalf("terminalSafeText lost normal text: %q", got)
	}
}
