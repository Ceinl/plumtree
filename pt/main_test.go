package main

import (
	"strings"
	"testing"
)

func TestReplaceManagedSSHBlockAppends(t *testing.T) {
	existing := "Host github.com\n    User git\n"
	block := devSSHConfigBlock("plumtree.dev", "127.0.0.1", "2222")
	got := replaceManagedSSHBlock(existing, block)

	if !strings.Contains(got, "Host github.com\n    User git") {
		t.Fatalf("existing config was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "Host plumtree.dev\n    HostName 127.0.0.1\n    Port 2222") {
		t.Fatalf("managed block missing:\n%s", got)
	}
	if strings.Count(got, sshConfigBegin) != 1 || strings.Count(got, sshConfigEnd) != 1 {
		t.Fatalf("managed markers count wrong:\n%s", got)
	}
}

func TestReplaceManagedSSHBlockUpdates(t *testing.T) {
	oldBlock := devSSHConfigBlock("plumtree.dev", "127.0.0.1", "2222")
	newBlock := devSSHConfigBlock("plumtree.dev", "127.0.0.1", "3333")
	existing := "Host github.com\n    User git\n\n" + oldBlock + "\nHost example.com\n    User me\n"
	got := replaceManagedSSHBlock(existing, newBlock)

	if strings.Contains(got, "Port 2222") {
		t.Fatalf("old port survived:\n%s", got)
	}
	if !strings.Contains(got, "Port 3333") {
		t.Fatalf("new port missing:\n%s", got)
	}
	if !strings.Contains(got, "Host github.com") || !strings.Contains(got, "Host example.com") {
		t.Fatalf("surrounding config not preserved:\n%s", got)
	}
	if strings.Count(got, sshConfigBegin) != 1 || strings.Count(got, sshConfigEnd) != 1 {
		t.Fatalf("managed markers count wrong:\n%s", got)
	}
}

func TestLocalConnectHost(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", "127.0.0.1"},
		{"0.0.0.0", "127.0.0.1"},
		{"::", "127.0.0.1"},
		{"127.0.0.1", "127.0.0.1"},
	} {
		if got := localConnectHost(tc.in); got != tc.want {
			t.Fatalf("localConnectHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
