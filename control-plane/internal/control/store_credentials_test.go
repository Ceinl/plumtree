package control

import (
	"errors"
	"testing"
	"time"
)

func TestAuthAndSecretMetadata(t *testing.T) {
	store := NewStore()
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.RegisterSSHKey(SSHKeyInput{
		OwnerID:     owner.ID,
		Name:        "laptop",
		PublicKey:   "ssh-ed25519 AAAATEST alice@host",
		Fingerprint: "SHA256:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.ID == "" {
		t.Fatal("empty SSH key ID")
	}
	if _, err := store.RegisterSSHKey(SSHKeyInput{
		OwnerID:     owner.ID,
		Name:        "desktop",
		PublicKey:   "ssh-ed25519 AAAATEST2 alice@host",
		Fingerprint: "SHA256:test",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate key error = %v, want ErrConflict", err)
	}

	scopes := []TokenScope{ScopeDeploy}
	token, err := store.CreateCIToken(CITokenInput{
		OwnerID:   owner.ID,
		Name:      "ci",
		TokenHash: "sha256:token",
		Scopes:    scopes,
	})
	if err != nil {
		t.Fatal(err)
	}
	scopes[0] = ScopeSecrets
	gotToken, err := store.GetCIToken(token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotToken.Scopes[0] != ScopeDeploy {
		t.Fatalf("token scopes were not cloned: got %q", gotToken.Scopes[0])
	}

	secret, err := store.UpsertSecret(SecretInput{AppID: app.ID, Key: "API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Version != 1 {
		t.Fatalf("secret version = %d, want 1", secret.Version)
	}
	secret, err = store.UpsertSecret(SecretInput{AppID: app.ID, Key: "API_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Version != 2 {
		t.Fatalf("secret version = %d, want 2", secret.Version)
	}
	if _, err := store.UpsertSecret(SecretInput{AppID: app.ID, Key: "bad-key"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad secret key error = %v, want ErrInvalid", err)
	}
}

func TestResolveSSHKey(t *testing.T) {
	store := NewStore()
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	registered, err := store.RegisterSSHKey(SSHKeyInput{
		OwnerID: owner.ID, Name: "laptop", PublicKey: "ssh-ed25519 AAAATEST", Fingerprint: "SHA256:registered",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, gotOwner, err := store.ResolveSSHKey(registered.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != registered.ID || gotOwner.ID != owner.ID {
		t.Fatalf("resolved key=%+v owner=%+v", key, gotOwner)
	}
	if _, _, err := store.ResolveSSHKey("SHA256:unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown fingerprint error = %v, want ErrNotFound", err)
	}
}

func TestSecretValues(t *testing.T) {
	store := NewStore()
	owner, _ := store.CreateOwner("alice")
	app, _ := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})

	if _, err := store.UpsertSecret(SecretInput{AppID: app.ID, Key: "API_KEY", Value: []byte("s3cr3t")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertSecret(SecretInput{AppID: app.ID, Key: "DB_URL", Value: []byte("postgres://x")}); err != nil {
		t.Fatal(err)
	}

	// SecretsForApp returns values for runtime injection.
	vals := store.SecretsForApp(app.ID)
	if vals["API_KEY"] != "s3cr3t" || vals["DB_URL"] != "postgres://x" {
		t.Fatalf("SecretsForApp = %v", vals)
	}

	// ListSecrets returns metadata only, never values.
	metas := store.ListSecrets(app.ID)
	if len(metas) != 2 || metas[0].Key != "API_KEY" || metas[1].Key != "DB_URL" {
		t.Fatalf("ListSecrets = %+v", metas)
	}

	// Updating a value bumps the version and replaces the value.
	if _, err := store.UpsertSecret(SecretInput{AppID: app.ID, Key: "API_KEY", Value: []byte("rotated")}); err != nil {
		t.Fatal(err)
	}
	if store.SecretsForApp(app.ID)["API_KEY"] != "rotated" {
		t.Fatalf("value not rotated")
	}

	// Delete removes both metadata and value.
	if err := store.DeleteSecret(app.ID, "API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.SecretsForApp(app.ID)["API_KEY"]; ok {
		t.Fatal("value remained after delete")
	}
	if err := store.DeleteSecret(app.ID, "API_KEY"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing = %v, want ErrNotFound", err)
	}
}

func TestCITokenLifecycle(t *testing.T) {
	now := time.Unix(500, 0).UTC()
	store := NewStore(WithClock(func() time.Time { return now }))
	alice, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateOwner("bob")
	if err != nil {
		t.Fatal(err)
	}

	token, err := store.CreateCIToken(CITokenInput{
		OwnerID:   alice.ID,
		Name:      "ci",
		TokenHash: "sha256:deadbeef",
		Scopes:    []TokenScope{ScopeDeploy},
	})
	if err != nil {
		t.Fatal(err)
	}

	// ListCITokens is owner-scoped.
	aliceTokens, err := store.ListCITokens(alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliceTokens) != 1 || aliceTokens[0].ID != token.ID {
		t.Fatalf("alice tokens = %+v", aliceTokens)
	}
	bobTokens, err := store.ListCITokens(bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bobTokens) != 0 {
		t.Fatalf("bob tokens = %+v, want none", bobTokens)
	}

	// An active token authenticates by hash.
	got, owner, err := store.AuthenticateCIToken("sha256:deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != token.ID || owner.ID != alice.ID {
		t.Fatalf("authenticated token = %+v owner = %+v", got, owner)
	}

	// Another owner cannot revoke it.
	if _, err := store.RevokeCIToken(bob.ID, token.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner revoke error = %v, want ErrNotFound", err)
	}

	revoked, err := store.RevokeCIToken(alice.ID, token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("revoked token has nil RevokedAt")
	}
	// Revoke is idempotent and keeps the original timestamp.
	again, err := store.RevokeCIToken(alice.ID, token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Fatalf("idempotent revoke changed timestamp: %v != %v", again.RevokedAt, revoked.RevokedAt)
	}

	// A revoked token no longer authenticates.
	if _, _, err := store.AuthenticateCIToken("sha256:deadbeef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token auth error = %v, want ErrNotFound", err)
	}
}
