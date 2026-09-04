package terminal

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteServerSummaryIsCompactAndPlainWhenColorDisabled(t *testing.T) {
	var out bytes.Buffer
	WriteServerSummary(&out, ServerSummary{
		Mode: "development", Listen: "127.0.0.1:2222",
		Database: "plumtree.db", KVRoot: "plumtree-data", Next: "plumtree bootstrap -handle NAME",
	}, false)
	got := out.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("plain summary contains ANSI escapes: %q", got)
	}
	for _, want := range []string{"~ plumtree", "● ready", "(development)", "| ssh", "| store", "plumtree.db · plumtree-data", "| next", "plumtree bootstrap"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "limits") {
		t.Fatalf("summary should not carry limits; they live in config show:\n%s", got)
	}
	if lines := strings.Count(strings.TrimSpace(got), "\n") + 1; lines != 4 {
		t.Fatalf("summary uses %d lines, want 4:\n%s", lines, got)
	}
}

func TestWriteServerSummaryUsesPlumEdgeAndGreenMarkerOnly(t *testing.T) {
	var out bytes.Buffer
	WriteServerSummary(&out, ServerSummary{Mode: "production", Listen: ":2222", Database: "db", KVRoot: "kv", Next: "ready"}, true)
	got := out.String()
	for _, want := range []string{ansiPlum, ansiGreen, ansiReset} {
		if !strings.Contains(got, want) {
			t.Fatalf("colored summary missing %q: %q", want, got)
		}
	}
	for _, banned := range []string{"\x1b[1m", "\x1b[38;5;179m", "\x1b[38;5;73m"} {
		if strings.Contains(got, banned) {
			t.Fatalf("colored summary should stay minimal, found %q in %q", banned, got)
		}
	}
}

func TestWriteServerSummaryShortensPathsUnderTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	db := filepath.Join(dir, "plumtree.db")
	var out bytes.Buffer
	WriteServerSummary(&out, ServerSummary{Mode: "development", Listen: ":2222", Database: db, KVRoot: filepath.Join(dir, "kv"), Next: "n", ConfigPath: filepath.Join(dir, "config.json")}, false)
	got := out.String()
	if !strings.Contains(got, "| store plumtree.db · kv") {
		t.Fatalf("store paths not shortened:\n%s", got)
	}
	if !strings.Contains(got, "| note  config config.json") {
		t.Fatalf("config path not shortened:\n%s", got)
	}
}

func TestWriteServerSummaryRendersNoteOnItsOwnLine(t *testing.T) {
	var out bytes.Buffer
	WriteServerSummary(&out, ServerSummary{Mode: "development", Listen: ":2222", Database: "db", KVRoot: "kv", Next: "n", ConfigPath: "config.json"}, false)
	if !strings.Contains(out.String(), "| note  config config.json") {
		t.Fatalf("note line missing:\n%s", out.String())
	}
}

func TestWriteDevSSHSummaryKeepsGreppableConnect(t *testing.T) {
	var out bytes.Buffer
	WriteDevSSHSummary(&out, DevSSHSummary{Name: "greeter", AppType: "cli", Listen: "127.0.0.1:2222", Command: "ssh greeter@plumtree.dev"}, false)
	got := out.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("plain dev summary contains ANSI escapes: %q", got)
	}
	for _, want := range []string{"~ pt dev", "● ready", "greeter", "Connect: ssh greeter@plumtree.dev"} {
		if !strings.Contains(got, want) {
			t.Fatalf("dev summary missing %q:\n%s", want, got)
		}
	}
}

func TestWriteGatewaySummaryStaysPlainWithoutColor(t *testing.T) {
	var out bytes.Buffer
	WriteGatewaySummary(&out, "0.0.0.0:2222", "https://control", "in-process sandbox", false)
	if strings.ContainsRune(out.String(), '\x1b') {
		t.Fatalf("plain gateway summary contains ANSI escapes: %q", out.String())
	}
	if !strings.Contains(out.String(), "plumtree gateway") {
		t.Fatalf("gateway summary missing brand:\n%s", out.String())
	}
}

func TestWriteBootstrapSummaryIsCopyPasteReady(t *testing.T) {
	var out bytes.Buffer
	WriteBootstrapSummary(&out, BootstrapSummary{Handle: "dima", ID: "bootstrap_abc", Secret: "s3cr3t", Valid: 10 * time.Minute, Next: "pt pair --bootstrap bootstrap_abc localhost:2222"}, false)
	got := out.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("plain bootstrap summary contains ANSI escapes: %q", got)
	}
	for _, want := range []string{"~ plumtree bootstrap", "● ok", "dima", "bootstrap_abc", "s3cr3t", "10m", "pairing phrase shown once", "pt pair --bootstrap bootstrap_abc"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bootstrap summary missing %q:\n%s", want, got)
		}
	}
}

func TestCompactDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{{10 * time.Minute, "10m"}, {2 * time.Hour, "2h"}, {90 * time.Second, "90s"}} {
		if got := compactDuration(tc.in); got != tc.want {
			t.Fatalf("compactDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
func TestWriteEventIsTimestamped(t *testing.T) {
	var out bytes.Buffer
	WriteEvent(&out, "connection open", false)
	got := out.String()
	if !strings.Contains(got, "• connection open") {
		t.Fatalf("event line missing marker and message: %q", got)
	}
}