package terminal

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

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
	WriteEvent(&out, "connection \x1b[31mopen\x00\nforged", false)
	got := out.String()
	if !strings.Contains(got, "• connection  [31mopen  forged") {
		t.Fatalf("event line missing marker and message: %q", got)
	}
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x00') {
		t.Fatalf("event line contains terminal controls: %q", got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("event message injected an extra log line: %q", got)
	}
}
