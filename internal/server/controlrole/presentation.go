package controlrole

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type startupSummary struct {
	Mode      string
	Dashboard string
	SSH       string
	Stack     string
	Limits    string
	Next      string
	Note      string
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiPlum  = "\x1b[38;5;133m"
	ansiGreen = "\x1b[38;5;42m"
	ansiGold  = "\x1b[38;5;179m"
	ansiBlue  = "\x1b[38;5;73m"
)

func colorEnabled(f *os.File) bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func tone(text, code string, color bool) string {
	if !color {
		return text
	}
	return code + text + ansiReset
}

func writeStartupSummary(w io.Writer, s startupSummary, color bool) {
	brand := tone("plumtree", ansiBold+ansiPlum, color)
	ready := tone("● ready", ansiBold+ansiGreen, color)
	mode := tone(s.Mode, ansiDim, color)
	label := func(value string) string { return tone(fmt.Sprintf("%-7s", value), ansiBlue, color) }

	fmt.Fprintf(w, "╭─ %s  %s %s\n", brand, ready, mode)
	fmt.Fprintf(w, "│ %s%s\n", label("web"), s.Dashboard)
	if s.SSH != "" {
		fmt.Fprintf(w, "│ %s%s\n", label("ssh"), tone(s.SSH, ansiBold, color))
	}
	fmt.Fprintf(w, "│ %s%s\n", label("stack"), s.Stack)
	fmt.Fprintf(w, "│ %s%s\n", label("limits"), s.Limits)
	fmt.Fprintf(w, "╰─ %s%s\n", label("next"), tone(s.Next, ansiGold, color))
	if s.Note != "" {
		fmt.Fprintf(w, "  %s %s\n", tone("note", ansiDim, color), s.Note)
	}
}

func writeRuntimeEvent(w io.Writer, message string, color bool) {
	stamp := tone(time.Now().Format("15:04:05"), ansiDim, color)
	marker := tone("•", ansiGreen, color)
	fmt.Fprintf(w, "%s  %s %s\n", stamp, marker, message)
}

func briefDuration(d time.Duration) string {
	if d <= 0 {
		return "∞"
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return d.String()
}
