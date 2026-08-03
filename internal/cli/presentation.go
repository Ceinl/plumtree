package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func devColorEnabled(f *os.File) bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func devTone(text, code string, color bool) string {
	if !color {
		return text
	}
	return code + text + "\x1b[0m"
}

func writeDevSSHSummary(w io.Writer, name, appType, command, configPath string, color bool) {
	label := func(v string) string { return devTone(fmt.Sprintf("%-7s", v), "\x1b[38;5;73m", color) }
	fmt.Fprintf(w, "╭─ %s  %s\n", devTone("pt dev", "\x1b[1;38;5;133m", color), devTone("● ready", "\x1b[1;38;5;42m", color))
	fmt.Fprintf(w, "│ %s%s %s\n", label("app"), devTone(name, "\x1b[1m", color), devTone("("+appType+")", "\x1b[2m", color))
	fmt.Fprintf(w, "╰─ %s%s\n", label("connect"), devTone(command, "\x1b[1;38;5;179m", color))
	if configPath != "" {
		fmt.Fprintf(w, "  %s SSH alias installed in %s\n", devTone("note", "\x1b[2m", color), configPath)
	}
}

func writeDevEvent(w io.Writer, message string, color bool) {
	fmt.Fprintf(w, "%s  %s %s\n",
		devTone(time.Now().Format("15:04:05"), "\x1b[2m", color),
		devTone("•", "\x1b[38;5;42m", color), message)
}
