package main

import (
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk"
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

func TestCallerAllowed(t *testing.T) {
	owner := sdk.Identity{User: "SHA256:owner", Kind: sdk.IdentitySSHKey, Authenticated: true, OwnsApp: true}
	if !callerAllowed(owner, "") {
		t.Fatal("claimed owner denied")
	}
	autoClaimKey := sdk.Identity{User: "SHA256:auto-claim-key", Kind: sdk.IdentitySSHKey}
	if !callerAllowed(autoClaimKey, "SHA256:other, SHA256:auto-claim-key") {
		t.Fatal("allowlisted auto-claim key denied")
	}
	if callerAllowed(autoClaimKey, "SHA256:other") {
		t.Fatal("unlisted key allowed")
	}
	if callerAllowed(sdk.Identity{User: "SHA256:auto-claim-key", Kind: sdk.IdentityAnonymous}, "SHA256:auto-claim-key") {
		t.Fatal("anonymous identity allowed")
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
