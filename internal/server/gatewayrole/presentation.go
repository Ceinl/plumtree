package gatewayrole

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func gatewayColorEnabled(f *os.File) bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func gatewayTone(text, code string, color bool) string {
	if !color {
		return text
	}
	return code + text + "\x1b[0m"
}

func writeGatewaySummary(w io.Writer, listen, control, runner string, color bool) {
	label := func(v string) string { return gatewayTone(fmt.Sprintf("%-8s", v), "\x1b[38;5;73m", color) }
	fmt.Fprintf(w, "╭─ %s  %s\n", gatewayTone("plumtree gateway", "\x1b[1;38;5;133m", color), gatewayTone("● ready", "\x1b[1;38;5;42m", color))
	fmt.Fprintf(w, "│ %s%s\n", label("listen"), gatewayTone(listen, "\x1b[1m", color))
	fmt.Fprintf(w, "│ %s%s\n", label("control"), control)
	fmt.Fprintf(w, "╰─ %s%s\n", label("runner"), runner)
}

func writeGatewayEvent(w io.Writer, message string, color bool) {
	fmt.Fprintf(w, "%s  %s %s\n",
		gatewayTone(time.Now().Format("15:04:05"), "\x1b[2m", color),
		gatewayTone("•", "\x1b[38;5;42m", color), message)
}
