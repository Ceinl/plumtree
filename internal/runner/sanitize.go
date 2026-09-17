package runner

import "github.com/Ceinl/plumtree/internal/terminal"

// SanitizeTerminalText neutralizes untrusted diagnostic/log text while keeping
// newlines and tabs readable. It is for text streams; structured frame cells
// use sanitizeRune instead.
func SanitizeTerminalText(s string) string {
	return terminal.SanitizeText(s)
}
