package control

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	artifactDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sourceDigest   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	claimDigest    = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestOwnerAppDeployLifecycle(t *testing.T) {
	store := NewStore(WithClock(func() time.Time { return time.Unix(100, 0).UTC() }))

	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ArtifactInput{
		Digest:        artifactDigest,
		SizeBytes:     42,
		ABIVersion:    0,
		BuildMetadata: map[string]string{"go": "1.26.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact.BuildMetadata["go"] = "mutated"
	deploy, err := store.CreateDeploy(DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err = store.ActivateDeploy(app.ID, deploy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if app.ActiveDeployID != deploy.ID {
		t.Fatalf("active deploy = %q, want %q", app.ActiveDeployID, deploy.ID)
	}

	resolvedApp, resolvedDeploy, resolvedArtifact, err := store.ResolveActiveDeploy("alice", "counter")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedApp.ID != app.ID || resolvedDeploy.ID != deploy.ID || resolvedArtifact.ID != artifact.ID {
		t.Fatalf("resolved wrong records: app=%q deploy=%q artifact=%q", resolvedApp.ID, resolvedDeploy.ID, resolvedArtifact.ID)
	}
	if resolvedArtifact.BuildMetadata["go"] != "1.26.2" {
		t.Fatalf("artifact metadata leaked through return value: got %q", resolvedArtifact.BuildMetadata["go"])
	}

	if _, err := store.CreateOwner("alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate owner error = %v, want ErrConflict", err)
	}
	if _, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate app error = %v, want ErrConflict", err)
	}
}

func TestRejectsUnsafeNames(t *testing.T) {
	store := NewStore()
	for _, name := range []string{"", "../x", "Bad", "-bad", "bad-", "bad_name", "bad/name", "bad--name", strings.Repeat("a", 64)} {
		if _, err := store.CreateOwner(name); !errors.Is(err, ErrInvalid) {
			t.Fatalf("CreateOwner(%q) error = %v, want ErrInvalid", name, err)
		}
	}
}

func TestActivateDeployRejectsOtherApp(t *testing.T) {
	store := NewStore()
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ArtifactInput{Digest: artifactDigest, SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeploy(DeployInput{
		AppID:            first.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateDeploy(second.ID, deploy.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ActivateDeploy wrong app error = %v, want ErrInvalid", err)
	}
}

func TestDeployRequiresOwningAuthor(t *testing.T) {
	store := NewStore()
	alice, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateOwner("bob")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(AppInput{OwnerID: alice.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ArtifactInput{Digest: artifactDigest, SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateDeploy(DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: bob.ID,
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateDeploy with non-owner error = %v, want ErrInvalid", err)
	}
}

func TestEnsureOwnerForIdentityCreatesStableOwner(t *testing.T) {
	store := NewStore()
	owner, identity, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner.ID == "" || identity.OwnerID != owner.ID {
		t.Fatalf("owner=%+v identity=%+v", owner, identity)
	}
	if owner.Handle != "" {
		t.Fatalf("identity owner handle = %q, want unclaimed", owner.Handle)
	}
	again, againIdentity, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != owner.ID || againIdentity.OwnerID != identity.OwnerID {
		t.Fatalf("identity did not resolve stably: owner=%+v identity=%+v", again, againIdentity)
	}
}

func TestClaimOwnerHandle(t *testing.T) {
	store := NewStore()
	owner, _, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err = store.ClaimOwnerHandle(owner.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if owner.Handle != "alice" {
		t.Fatalf("handle = %q, want alice", owner.Handle)
	}
	if _, err := store.FindOwner("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOwnerHandle(owner.ID, "alice"); err != nil {
		t.Fatalf("idempotent claim failed: %v", err)
	}
	if _, err := store.ClaimOwnerHandle(owner.ID, "bad_name"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad handle error = %v, want ErrInvalid", err)
	}

	other, _, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOwnerHandle(other.ID, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting handle error = %v, want ErrConflict", err)
	}
}

func TestAutoClaimOwnerHandleIsReservedForInternalOwner(t *testing.T) {
	store := NewStore()
	if _, err := store.CreateOwner(AutoClaimOwnerHandle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateOwner(%q) error = %v, want ErrInvalid", AutoClaimOwnerHandle, err)
	}
	if _, err := store.EnsureOwner(AutoClaimOwnerHandle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("EnsureOwner(%q) error = %v, want ErrInvalid", AutoClaimOwnerHandle, err)
	}
	publicOwner, _, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_reserved",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOwnerHandle(publicOwner.ID, AutoClaimOwnerHandle); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ClaimOwnerHandle(%q) error = %v, want ErrInvalid", AutoClaimOwnerHandle, err)
	}

	internal, err := store.EnsureAutoClaimOwner()
	if err != nil {
		t.Fatal(err)
	}
	if internal.Handle != AutoClaimOwnerHandle || !internal.Internal {
		t.Fatalf("auto-claim owner = %+v", internal)
	}
	again, err := store.EnsureAutoClaimOwner()
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != internal.ID {
		t.Fatalf("EnsureAutoClaimOwner returned different owner: %q != %q", again.ID, internal.ID)
	}
	if _, err := store.ClaimOwnerHandle(internal.ID, "renamed"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("renaming internal owner error = %v, want ErrInvalid", err)
	}
}

func TestEnsureOwnerAndAppAreIdempotent(t *testing.T) {
	store := NewStore()
	owner, err := store.EnsureOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.EnsureOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != owner.ID {
		t.Fatalf("EnsureOwner returned different owner: %q != %q", again.ID, owner.ID)
	}

	app, err := store.EnsureApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	againApp, err := store.EnsureApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	if againApp.ID != app.ID {
		t.Fatalf("EnsureApp returned different app: %q != %q", againApp.ID, app.ID)
	}
}
