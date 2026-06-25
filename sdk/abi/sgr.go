package abi

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseSGRColor extracts the RGB from a "\x1b[38;2;r;g;bm" or
// "\x1b[48;2;r;g;bm" sequence. ok is false for any other shape.
func ParseSGRColor(s string) (RGB, bool) {
	body, ok := sgrBody(s)
	if !ok {
		return RGB{}, false
	}
	parts := strings.Split(body, ";")
	if len(parts) != 5 || parts[1] != "2" || (parts[0] != "38" && parts[0] != "48") {
		return RGB{}, false
	}
	r, e1 := strconv.Atoi(parts[2])
	g, e2 := strconv.Atoi(parts[3])
	bl, e3 := strconv.Atoi(parts[4])
	if e1 != nil || e2 != nil || e3 != nil || !byteRange(r) || !byteRange(g) || !byteRange(bl) {
		return RGB{}, false
	}
	return RGB{uint8(r), uint8(g), uint8(bl)}, true
}

// ParseSGRDecor extracts decoration bits from a "\x1b[..m" sequence containing
// any of 1 (bold), 3 (italic), 4 (underline). Empty input means no decoration.
func ParseSGRDecor(s string) uint8 {
	if s == "" {
		return 0
	}
	body, ok := sgrBody(s)
	if !ok {
		return 0
	}
	var d uint8
	for _, p := range strings.Split(body, ";") {
		switch p {
		case "1":
			d |= DecorBold
		case "3":
			d |= DecorItalic
		case "4":
			d |= DecorUnderline
		}
	}
	return d
}

func sgrBody(s string) (string, bool) {
	const prefix = "\x1b["
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, "m") {
		return "", false
	}
	return s[len(prefix) : len(s)-1], true
}

func byteRange(v int) bool { return v >= 0 && v <= 255 }

// FgSGR renders a foreground truecolor SGR string. Host-side only: the host
// builds terminal output from validated RGB, never from guest-supplied bytes.
func FgSGR(c RGB) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B) }

// BgSGR renders a background truecolor SGR string (host-side only).
func BgSGR(c RGB) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B) }

// DecorSGR renders a decoration SGR string from decor bits (host-side only).
// Returns "" when no bits are set.
func DecorSGR(d uint8) string {
	if d == 0 {
		return ""
	}
	var parts []string
	if d&DecorBold != 0 {
		parts = append(parts, "1")
	}
	if d&DecorItalic != 0 {
		parts = append(parts, "3")
	}
	if d&DecorUnderline != 0 {
		parts = append(parts, "4")
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}
