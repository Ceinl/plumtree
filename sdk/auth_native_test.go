//go:build !wasip1

package sdk

import "testing"

func TestNativeIdentityDefaultsAndEnvironment(t *testing.T) {
	t.Setenv("PLUMTREE_IDENTITY_USER", "")
	t.Setenv("PLUMTREE_IDENTITY_KIND", "")
	t.Setenv("PLUMTREE_IDENTITY_AUTHENTICATED", "")
	t.Setenv("PLUMTREE_IDENTITY_OWNS_APP", "")
	id, err := Whoami()
	if err != nil || id.User != "local" || id.Kind != IdentitySSHKey || !id.Authenticated || !id.OwnsApp {
		t.Fatalf("default identity = %+v, %v", id, err)
	}
	t.Setenv("PLUMTREE_IDENTITY_USER", "anonymous:test")
	t.Setenv("PLUMTREE_IDENTITY_KIND", string(IdentityAnonymous))
	t.Setenv("PLUMTREE_IDENTITY_AUTHENTICATED", "false")
	t.Setenv("PLUMTREE_IDENTITY_OWNS_APP", "false")
	id, err = Whoami()
	if err != nil || id.User != "anonymous:test" || id.Kind != IdentityAnonymous || id.Authenticated || id.OwnsApp {
		t.Fatalf("environment identity = %+v, %v", id, err)
	}
}
