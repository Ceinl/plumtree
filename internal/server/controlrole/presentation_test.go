package controlrole

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteStartupSummaryIsCompactAndPlainWhenColorDisabled(t *testing.T) {
	var out bytes.Buffer
	writeStartupSummary(&out, startupSummary{
		Mode: "development", Dashboard: "http://localhost:8080/dashboard",
		SSH: "ssh alice/app@plumtree.dev", Stack: "state · build · auth",
		Limits: "25 apps · 50/app/day", Next: "pt deploy → pt claim",
	}, false)
	got := out.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("plain summary contains ANSI escapes: %q", got)
	}
	for _, want := range []string{"plumtree", "● ready", "web", "ssh", "limits", "pt deploy → pt claim"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q:\n%s", want, got)
		}
	}
	if lines := strings.Count(strings.TrimSpace(got), "\n") + 1; lines != 6 {
		t.Fatalf("summary uses %d lines, want 6:\n%s", lines, got)
	}
}

func TestWriteStartupSummaryUsesANSIWhenColorEnabled(t *testing.T) {
	var out bytes.Buffer
	writeStartupSummary(&out, startupSummary{Mode: "production", Dashboard: "https://plumtree.test/dashboard", Stack: "durable", Limits: "bounded", Next: "ready"}, true)
	if !strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("colored summary has no ANSI styling: %q", out.String())
	}
}

func TestBriefDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{{0, "∞"}, {30 * time.Minute, "30m"}, {2 * time.Hour, "2h"}, {1500 * time.Millisecond, "1.5s"}} {
		if got := briefDuration(tc.in); got != tc.want {
			t.Fatalf("briefDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
