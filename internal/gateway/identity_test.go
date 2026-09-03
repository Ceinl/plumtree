package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
	"golang.org/x/crypto/ssh"
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

// Owner authority requires authentication: a backend or wire peer that reports
// an owner for an unauthenticated identity must not yield OwnsApp, even when
// the owner IDs collide.
func TestAppRelativeIdentityNeverTrustsOwnerWhenUnauthenticated(t *testing.T) {
	hostile := appRelativeIdentity(runner.Identity{
		User: "SHA256:hostile-key-0001", Kind: runner.IdentitySSHKey,
		OwnerID: "own_1",
	}, "own_1")
	if hostile.OwnsApp {
		t.Fatalf("unauthenticated identity with colliding owner gained OwnsApp: %+v", hostile)
	}
	if hostile.OwnerID != "" {
		t.Fatalf("owner metadata leaked into the session identity: %+v", hostile)
	}
	if hostile.Authenticated {
		t.Fatal("unauthenticated identity became authenticated")
	}

	anonWithOwner := appRelativeIdentity(runner.Identity{
		User: "anonymous:abcd", Kind: runner.IdentityAnonymous, OwnerID: "own_1",
	}, "own_1")
	if anonWithOwner.OwnsApp || anonWithOwner.OwnerID != "" {
		t.Fatalf("anonymous identity = %+v", anonWithOwner)
	}
}

// identityBackend replays a fixed resolution result so hostile backend replies
// can be driven without a repository.
type identityBackend struct {
	stubBackend
	identity runner.Identity
}

func (b *identityBackend) ResolveIdentity(context.Context, string) (runner.Identity, error) {
	return b.identity, nil
}

// A gateway must strip owner metadata when its backend reports an
// unauthenticated identity — never trust OwnerID on an unauthenticated reply.
func TestIdentityFromConnDropsOwnerOnUnauthenticatedBackendReply(t *testing.T) {
	var log strings.Builder
	s := &Server{
		Backend: &identityBackend{identity: runner.Identity{User: "fp", Kind: runner.IdentitySSHKey, OwnerID: "own_1"}},
		Logf:    func(format string, args ...any) { log.WriteString(fmt.Sprintf(format, args...)) },
	}
	c := &ssh.ServerConn{Permissions: &ssh.Permissions{Extensions: map[string]string{"pubkey-fp": "fp"}}}
	id := s.identityFromConn(context.Background(), c)
	if id.Authenticated || id.OwnerID != "" || id.OwnsApp {
		t.Fatalf("identity from hostile backend = %+v", id)
	}
}
