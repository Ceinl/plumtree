package control

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStoreMigratesLegacyIdentityHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	const legacyHandle = "user-0123456789abcdef"
	if err := writeSnapshotFile(path, storeSnapshot{
		Version: storeSnapshotVersion,
		Seq:     map[string]int{"own": 1},
		Owners: []Owner{{
			ID:     "own_000001",
			Handle: legacyHandle,
		}},
		Identities: []AuthIdentity{{
			Provider: ProviderShoo,
			Subject:  "ps_legacy",
			OwnerID:  "own_000001",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, identity, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.OwnerID != owner.ID || owner.ID != "own_000001" {
		t.Fatalf("owner=%+v identity=%+v", owner, identity)
	}
	if owner.Handle != "" {
		t.Fatalf("handle = %q, want unclaimed", owner.Handle)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap storeSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Owners) != 1 || snap.Owners[0].Handle != "" {
		t.Fatalf("migrated handle was not saved: %+v", snap.Owners)
	}
}

func TestOpenStoreKeepsClaimedLegacyLookingHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	const claimedHandle = "user-0123456789abcdef"
	if err := writeSnapshotFile(path, storeSnapshot{
		Version: storeSnapshotVersion,
		Seq:     map[string]int{"own": 1},
		Owners: []Owner{{
			ID:            "own_000001",
			Handle:        claimedHandle,
			HandleClaimed: true,
		}},
		Identities: []AuthIdentity{{
			Provider: ProviderShoo,
			Subject:  "ps_claimed",
			OwnerID:  "own_000001",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := store.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_claimed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner.Handle != claimedHandle {
		t.Fatalf("handle = %q, want %q", owner.Handle, claimedHandle)
	}
}

func TestOpenStorePersistsRunnableDeploy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	now := time.Unix(100, 0).UTC()
	store, err := OpenStore(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	owner, identity, err := store.EnsureOwnerForIdentity(IdentityInput{
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
	app, err := store.EnsureApp(AppInput{OwnerID: owner.ID, Name: "counter", Visibility: VisibilityPublic})
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm bytes")
	artifact, err := store.CreateArtifact(ArtifactInput{
		Digest:        digestBytes(wasm),
		SizeBytes:     int64(len(wasm)),
		ABIVersion:    0,
		BuildMetadata: map[string]string{"go": "1.26.2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeploy(DeployInput{
		AppID:            app.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateDeploy(app.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenStore(path, WithClock(func() time.Time { return time.Unix(200, 0).UTC() }))
	if err != nil {
		t.Fatal(err)
	}
	reloadedOwner, reloadedIdentity, err := reloaded.EnsureOwnerForIdentity(IdentityInput{
		Provider: ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reloadedOwner.ID != owner.ID || reloadedIdentity.OwnerID != identity.OwnerID {
		t.Fatalf("identity did not survive reload: owner=%+v identity=%+v", reloadedOwner, reloadedIdentity)
	}
	gotApp, gotDeploy, gotArtifact, gotWASM, err := reloaded.ResolveRunnable(owner.Handle + "/counter")
	if err != nil {
		t.Fatal(err)
	}
	if gotApp.ID != app.ID || gotDeploy.ID != deploy.ID || gotArtifact.ID != artifact.ID || string(gotWASM) != string(wasm) {
		t.Fatalf("reloaded app=%+v deploy=%+v artifact=%+v wasm=%q", gotApp, gotDeploy, gotArtifact, gotWASM)
	}

	nextWASM := []byte("next wasm")
	nextArtifact, err := reloaded.CreateArtifact(ArtifactInput{
		Digest:     digestBytes(nextWASM),
		SizeBytes:  int64(len(nextWASM)),
		ABIVersion: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextArtifact.ID != "art_000002" {
		t.Fatalf("next artifact ID = %q, want art_000002", nextArtifact.ID)
	}
	if err := reloaded.PutArtifactBytes(nextArtifact.ID, nextWASM); err != nil {
		t.Fatal(err)
	}
	nextDeploy, err := reloaded.CreateDeploy(DeployInput{
		AppID:            app.ID,
		ArtifactID:       nextArtifact.ID,
		SourceDigest:     sourceDigest,
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if nextDeploy.ID != "dep_000002" {
		t.Fatalf("next deploy ID = %q, want dep_000002", nextDeploy.ID)
	}
	if _, err := reloaded.ActivateDeploy(app.ID, nextDeploy.ID); err != nil {
		t.Fatal(err)
	}

	reloadedAgain, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, gotDeploy, _, gotWASM, err = reloadedAgain.ResolveRunnable(owner.Handle + "/counter")
	if err != nil {
		t.Fatal(err)
	}
	if gotDeploy.ID != nextDeploy.ID || string(gotWASM) != string(nextWASM) {
		t.Fatalf("auto-persisted deploy=%+v wasm=%q", gotDeploy, gotWASM)
	}
}

func TestOpenStorePersistsClaimedDeployAfterClaimWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	now := time.Unix(100, 0).UTC()
	store, err := OpenStore(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("claimed wasm")
	artifact, err := store.CreateArtifact(ArtifactInput{
		Digest:    digestBytes(wasm),
		SizeBytes: int64(len(wasm)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeployClaim(DeployClaimInput{
		AppName:        "counter",
		Visibility:     VisibilityPublic,
		ArtifactID:     artifact.ID,
		SourceDigest:   sourceDigest,
		ClaimTokenHash: claimDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.ClaimDeploy(deploy.ID, claimDigest, owner.ID); err != nil {
		t.Fatal(err)
	}

	now = now.Add(DeployClaimTTL + time.Second)
	reloaded, err := OpenStore(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	_, gotDeploy, _, gotWASM, err := reloaded.ResolveRunnable("alice/counter")
	if err != nil {
		t.Fatal(err)
	}
	if gotDeploy.ID != deploy.ID || string(gotWASM) != string(wasm) {
		t.Fatalf("reloaded deploy=%+v wasm=%q", gotDeploy, gotWASM)
	}
}

func TestOpenStoreDeletesExpiredUnclaimedDeploy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	now := time.Unix(100, 0).UTC()
	store, err := OpenStore(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("unclaimed wasm")
	artifact, err := store.CreateArtifact(ArtifactInput{
		Digest:    digestBytes(wasm),
		SizeBytes: int64(len(wasm)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeployClaim(DeployClaimInput{
		AppName:        "counter",
		Visibility:     VisibilityPublic,
		ArtifactID:     artifact.ID,
		SourceDigest:   sourceDigest,
		ClaimTokenHash: claimDigest,
	})
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(DeployClaimTTL)
	reloaded, err := OpenStore(path, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.GetDeploy(deploy.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired deploy error = %v, want ErrNotFound", err)
	}
	if _, err := reloaded.GetArtifact(artifact.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired artifact error = %v, want ErrNotFound", err)
	}
}
