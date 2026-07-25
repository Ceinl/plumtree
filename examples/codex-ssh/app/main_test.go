package main

import (
	"strings"
	"testing"
)

func TestCodexArgsAreNarrowAndSafeByDefault(t *testing.T) {
	got, err := codexArgs("summarize this repo", "/srv/project", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--color", "never", "--ephemeral", "--sandbox", "read-only", "-C", "/srv/project", "summarize this repo"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestCodexArgsRejectUnsafeSandbox(t *testing.T) {
	if _, err := codexArgs("hello", "", "danger-full-access"); err == nil {
		t.Fatal("danger-full-access sandbox accepted")
	}
	if _, err := codexArgs("", "", ""); err == nil {
		t.Fatal("empty prompt accepted")
	}
}
