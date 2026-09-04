package terminal

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Presentation constants and writers for operator-facing terminal output.
//
// This is the single source of truth for the compact startup summaries and
// timestamped runtime events first introduced for the operator presentation
// pass: one rule for color detection, one summary style, and one event line
// shape shared by the server, the runner, the gateway, and pt dev. Summaries
// stay minimal on purpose: a plum left edge, a green readiness marker, plain
// text otherwise, so the output reads the same on a TTY, in a pipe, and in a
// log file. Callers pass an explicit color flag resolved with Enabled or
// ColorFor so tests can force plain output with buffers while processes honor
// the terminal.
const (
	ansiReset = "\x1b[0m"
	ansiGreen = "\x1b[38;5;42m"
	ansiPlum  = "\x1b[38;5;133m"
	ansiDim   = "\x1b[2m"
)

// Enabled reports whether styled output should be used for f, honoring
// CLICOLOR_FORCE, NO_COLOR, and TERM=dumb ahead of TTY detection.
func Enabled(f *os.File) bool {
	if force := os.Getenv("CLICOLOR_FORCE"); force != "" && force != "0" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// ColorFor resolves the color flag for an arbitrary writer: real files honor
// Enabled, while buffers and pipes stay plain unless CLICOLOR_FORCE opts in.
// This keeps test output deterministic without extra parameters at call sites.
func ColorFor(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return Enabled(f)
	}
	force := os.Getenv("CLICOLOR_FORCE")
	return force != "" && force != "0"
}

func tone(text, code string, color bool) string {
	if !color {
		return text
	}
	return code + text + ansiReset
}

// displayPath keeps long absolute paths from wrapping in narrow terminals:
// paths under the working directory render relative to it, paths under home
// collapse to ~/..., and anything else (or a relative path) passes through
// untouched. Operators run servers from their checkout, so the common case
// shrinks to a basename while the stored configuration keeps full paths.
func displayPath(path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return path
	}
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "~" + string(filepath.Separator) + rel
		}
	}
	return path
}

// SanitizeText neutralizes control characters and invalid UTF-8 in untrusted
// terminal text while preserving readable newlines and tabs.
func SanitizeText(s string) string {
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

// WriteEvent writes one timestamped runtime event line: a dim clock, a
// green marker, then the sanitized caller-supplied message.
func WriteEvent(w io.Writer, message string, color bool) {
	stamp := tone(time.Now().Format("15:04:05"), ansiDim, color)
	marker := tone("•", ansiGreen, color)
	message = strings.ReplaceAll(SanitizeText(message), "\n", " ")
	_, _ = io.WriteString(w, stamp+"  "+marker+" "+message+"\n")
}

// EventFunc adapts WriteEvent to the Logf shape used by gateway and runner
// components, formatting first and writing the event line to w.
func EventFunc(w io.Writer, color bool) func(format string, args ...any) {
	return func(format string, args ...any) {
		WriteEvent(w, fmt.Sprintf(format, args...), color)
	}
}

// ServerSummary is the operator-facing startup summary for the server. It
// answers: is it up, where does it listen, which store is it running
// against, and what is the next step. Limits live in
// `plumtree config show`, not here, so the startup output stays short.
// Database, KVRoot, and ConfigPath take raw configured paths; the writer
// shortens them for display (relative to the working directory, then ~/)
// so the summary fits narrow terminals while configuration keeps full paths.
type ServerSummary struct {
	Mode       string
	Listen     string
	Database   string
	KVRoot     string
	Next       string
	ConfigPath string
}

// WriteServerSummary renders the server startup summary in a single write so
// readiness streams that capture only the first chunk still see all of it.
// The left edge carries the only plum accent; the readiness marker stays
// green and everything else is plain text.
func WriteServerSummary(w io.Writer, s ServerSummary, color bool) {
	edge := tone("~", ansiPlum, color)
	bar := tone("|", ansiPlum, color)
	ready := tone("● ready", ansiGreen, color)

	var b strings.Builder
	fmt.Fprintf(&b, "%s plumtree %s (%s)\n", edge, ready, s.Mode)
	fmt.Fprintf(&b, "%s ssh   %s\n", bar, s.Listen)
	fmt.Fprintf(&b, "%s store %s · %s\n", bar, displayPath(s.Database), displayPath(s.KVRoot))
	fmt.Fprintf(&b, "%s next  %s\n", bar, s.Next)
	if s.ConfigPath != "" {
		fmt.Fprintf(&b, "%s note  config %s\n", bar, displayPath(s.ConfigPath))
	}
	_, _ = io.WriteString(w, b.String())
}

// RunnerSummary is the operator-facing startup summary for the isolated
// runner broker. Unlike ServerSummary, it does not imply an SSH listener or
// persistent application storage.
type RunnerSummary struct {
	Mode     string
	Endpoint string
	Worker   string
	Scratch  string
	Next     string
}

// WriteRunnerSummary renders the runner broker's role-specific startup state.
func WriteRunnerSummary(w io.Writer, s RunnerSummary, color bool) {
	edge := tone("~", ansiPlum, color)
	bar := tone("|", ansiPlum, color)
	ready := tone("● ready", ansiGreen, color)

	var b strings.Builder
	fmt.Fprintf(&b, "%s plumtree runner %s (%s)\n", edge, ready, s.Mode)
	fmt.Fprintf(&b, "%s broker  %s\n", bar, s.Endpoint)
	fmt.Fprintf(&b, "%s worker  %s\n", bar, displayPath(s.Worker))
	if s.Scratch != "" {
		fmt.Fprintf(&b, "%s scratch %s\n", bar, displayPath(s.Scratch))
	}
	fmt.Fprintf(&b, "%s next    %s\n", bar, s.Next)
	_, _ = io.WriteString(w, b.String())
}

// BootstrapSummary is the human-readable result of plumtree bootstrap.
// It shows the copy-paste values (ID and secret) plus the exact next command.
// Scripts should use --json instead; this shape is for terminals only.
type BootstrapSummary struct {
	Handle string
	ID     string
	Secret string
	Valid  time.Duration
	Next   string
}

// WriteBootstrapSummary renders the bootstrap result in a single write. The
// secret is shown once and never again, so it shares a row with its warning.
func WriteBootstrapSummary(w io.Writer, s BootstrapSummary, color bool) {
	edge := tone("~", ansiPlum, color)
	bar := tone("|", ansiPlum, color)
	ok := tone("● ok", ansiGreen, color)

	var b strings.Builder
	fmt.Fprintf(&b, "%s plumtree bootstrap %s\n", edge, ok)
	fmt.Fprintf(&b, "%s handle %s\n", bar, s.Handle)
	fmt.Fprintf(&b, "%s id     %s\n", bar, s.ID)
	fmt.Fprintf(&b, "%s secret %s\n", bar, s.Secret)
	fmt.Fprintf(&b, "%s valid  %s (pairing phrase shown once)\n", bar, compactDuration(s.Valid))
	fmt.Fprintf(&b, "%s next   %s\n", bar, s.Next)
	_, _ = io.WriteString(w, b.String())
}

// compactDuration renders whole durations without trailing zero units
// (10m, not 10m0s) for operator-facing validity spans.
func compactDuration(d time.Duration) string {
	if d <= 0 {
		return d.Truncate(time.Second).String()
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
	return d.Truncate(time.Second).String()
}

// DevSSHSummary is the operator-facing summary for pt dev --ssh.
type DevSSHSummary struct {
	Name       string
	AppType    string
	Listen     string
	Command    string
	ConfigPath string
}

// WriteDevSSHSummary renders the pt dev --ssh summary in a single write. The
// connect line keeps the "Connect: <command>" phrasing so existing tooling
// and tests can keep grepping for it.
func WriteDevSSHSummary(w io.Writer, s DevSSHSummary, color bool) {
	edge := tone("~", ansiPlum, color)
	bar := tone("|", ansiPlum, color)
	ready := tone("● ready", ansiGreen, color)

	var b strings.Builder
	fmt.Fprintf(&b, "%s pt dev %s\n", edge, ready)
	fmt.Fprintf(&b, "%s app     %s (%s)\n", bar, s.Name, s.AppType)
	fmt.Fprintf(&b, "%s listen  %s\n", bar, s.Listen)
	fmt.Fprintf(&b, "%s Connect: %s\n", bar, s.Command)
	if s.ConfigPath != "" {
		fmt.Fprintf(&b, "%s note    SSH alias installed in %s\n", bar, displayPath(s.ConfigPath))
	}
	_, _ = io.WriteString(w, b.String())
}
