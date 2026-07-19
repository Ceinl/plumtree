package main

import (
	"github.com/Ceinl/plumtree/runner"
)

// terminalSafeText neutralizes untrusted server/compiler/app-log text before
// the author CLI writes it to a terminal. Newlines and tabs remain useful for
// logs; every other control (including ESC and C1 CSI/OSC/DCS) becomes a space.
func terminalSafeText(s string) string {
	return runner.SanitizeTerminalText(s)
}
