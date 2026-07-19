package gateway

import (
	"testing"

	"github.com/Ceinl/plumtree/runner"
)

func TestAppRelativeIdentity(t *testing.T) {
	owner := appRelativeIdentity(runner.Identity{User: "key-owner", Kind: runner.IdentitySSHKey, Authenticated: true, OwnerID: "own_1"}, "own_1")
	if !owner.OwnsApp || owner.OwnerID != "" {
		t.Fatalf("owner = %+v", owner)
	}
	nonOwner := appRelativeIdentity(runner.Identity{User: "key-other", Kind: runner.IdentitySSHKey, Authenticated: true, OwnerID: "own_2"}, "own_1")
	if nonOwner.OwnsApp || nonOwner.OwnerID != "" {
		t.Fatalf("non-owner = %+v", nonOwner)
	}
	proved := appRelativeIdentity(runner.Identity{User: "SHA256:proved", Kind: runner.IdentitySSHKey}, "own_1")
	if proved.OwnsApp || proved.Authenticated || proved.Kind != runner.IdentitySSHKey {
		t.Fatalf("proved key = %+v", proved)
	}
	anon := appRelativeIdentity(runner.Identity{User: "anonymous:1", Kind: runner.IdentityAnonymous}, "own_1")
	if anon.OwnsApp || anon.Kind != runner.IdentityAnonymous {
		t.Fatalf("anonymous = %+v", anon)
	}
}
