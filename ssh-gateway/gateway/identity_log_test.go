package gateway

import (
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/runner"
)

func TestIdentityLogValueShortensFingerprints(t *testing.T) {
	full := "SHA256:abcdefghijklmnopqrstuvwxyz0123456789"
	got := identityLogValue(runner.Identity{User: full, Kind: runner.IdentitySSHKey})
	if got == full || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "SHA256:") {
		t.Fatalf("short identity = %q", got)
	}
	if got := identityLogValue(runner.Identity{User: "local"}); got != "local" {
		t.Fatalf("short identity changed simple user to %q", got)
	}
}
