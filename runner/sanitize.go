package runner

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SanitizeTerminalText neutralizes untrusted diagnostic/log text while keeping
// newlines and tabs readable. It is for text streams; structured frame cells
// use sanitizeRune instead.
func SanitizeTerminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r == utf8.RuneError || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
}
