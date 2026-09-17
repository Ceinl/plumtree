package gateway

import (
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
)

func TestIdentityLogValueShortensFingerprints(t *testing.T) {
	full := "SHA256:abcdefghijklmnopqrstuvwxyz0123456789"
	got := IdentityLogValue(runner.Identity{User: full, Kind: runner.IdentitySSHKey})
	if got == full || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "SHA256:") {
		t.Fatalf("short identity = %q", got)
	}
	if got := IdentityLogValue(runner.Identity{User: "local"}); got != "local" {
		t.Fatalf("short identity changed simple user to %q", got)
	}
	if got := IdentityLogValue(runner.Identity{User: "anonymous:1234567890abcdef"}); got != "anonymous:12345678…" {
		t.Fatalf("anonymous identity = %q", got)
	}
}
