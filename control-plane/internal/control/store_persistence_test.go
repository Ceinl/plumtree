package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestDurableMutationsRollbackOnSnapshotFailure exercises every store mutation
// family against the same fault boundary. The writer fails after the candidate
// snapshot has been staged; each operation must leave the live snapshot exactly
// at the last successfully persisted checkpoint.
func TestDurableMutationsRollbackOnSnapshotFailure(t *testing.T) {
	fail := errors.New("injected snapshot failure")
	tests := []struct {
		name   string
		mutate func(*Store, Owner, App, Artifact, Deploy) error
	}{
		{"owner", func(s *Store, _ Owner, _ App, _ Artifact, _ Deploy) error { _, err := s.CreateOwner("bob"); return err }},
		{"app", func(s *Store, o Owner, _ App, _ Artifact, _ Deploy) error {
			_, err := s.CreateApp(AppInput{OwnerID: o.ID, Name: "other"})
			return err
		}},
		{"artifact", func(s *Store, _ Owner, _ App, _ Artifact, _ Deploy) error {
			_, err := s.CreateArtifact(ArtifactInput{Digest: digestBytes([]byte("new wasm")), SizeBytes: int64(len("new wasm"))})
			return err
		}},
		{"deploy", func(s *Store, o Owner, a App, art Artifact, _ Deploy) error {
			_, err := s.CreateDeploy(DeployInput{AppID: a.ID, ArtifactID: art.ID, SourceDigest: digestBytes([]byte("src-2")), CreatedByOwnerID: o.ID})
			return err
		}},
		{"credentials", func(s *Store, o Owner, _ App, _ Artifact, _ Deploy) error {
			_, err := s.RegisterSSHKey(SSHKeyInput{OwnerID: o.ID, Name: "laptop", PublicKey: "ssh-ed25519 TEST", Fingerprint: "SHA256:test"})
			return err
		}},
		{"secrets", func(s *Store, _ Owner, a App, _ Artifact, _ Deploy) error {
			_, err := s.UpsertSecret(SecretInput{AppID: a.ID, Key: "API_KEY", Value: []byte("secret")})
			return err
		}},
		{"egress", func(s *Store, _ Owner, a App, _ Artifact, _ Deploy) error {
			_, err := s.AddEgressHost(a.ID, "api.example.com")
			return err
		}},
		{"sessions", func(s *Store, _ Owner, a App, _ Artifact, d Deploy) error {
			_, err := s.StartSession(a.ID, d.ID)
			return err
		}},
		{"suspensions", func(s *Store, o Owner, _ App, _ Artifact, _ Deploy) error {
			_, err := s.SetOwnerSuspended(o.ID, true)
			return err
		}},
		{"deploy_claims", func(s *Store, _ Owner, _ App, art Artifact, _ Deploy) error {
			_, err := s.CreateDeployClaim(DeployClaimInput{AppName: "claim", AppType: "tui", ArtifactID: art.ID, SourceDigest: digestBytes([]byte("claim-src")), ClaimTokenHash: digestBytes([]byte("claim-token"))})
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, owner, app, artifact, deploy := persistentMutationFixture(t)
			s.mu.RLock()
			before := s.snapshotLocked()
			s.mu.RUnlock()
			s.writeSnapshot = func(string, storeSnapshot) error { return fail }

			if err := tt.mutate(s, owner, app, artifact, deploy); !errors.Is(err, fail) {
				t.Fatalf("mutation error = %v, want injected failure", err)
			}
			s.mu.RLock()
			after := s.snapshotLocked()
			s.mu.RUnlock()
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("failed mutation leaked into memory\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestArtifactBytesRollbackOnSnapshotFailure(t *testing.T) {
	// Use the filesystem store: unlike the embedded in-memory blob map, its
	// bytes are a separate durable mutation and must be explicitly restored.
	path := filepath.Join(t.TempDir(), "state.json")
	blobDir := filepath.Join(t.TempDir(), "blobs")
	s, err := OpenStore(path, WithBlobDir(blobDir))
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm")
	artifact, err := s.CreateArtifact(ArtifactInput{Digest: digestBytes(wasm), SizeBytes: int64(len(wasm))})
	if err != nil {
		t.Fatal(err)
	}
	s.writeSnapshot = func(string, storeSnapshot) error { return errors.New("injected snapshot failure") }
	if err := s.PutArtifactBytes(artifact.ID, wasm); err == nil {
		t.Fatal("PutArtifactBytes succeeded despite snapshot failure")
	}
	if _, ok := s.blobs.Get(artifact.ID); ok {
		t.Fatal("artifact bytes survived failed durable mutation")
	}
}

func persistentMutationFixture(t *testing.T) (*Store, Owner, App, Artifact, Deploy) {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm")
	artifact, err := s.CreateArtifact(ArtifactInput{Digest: digestBytes(wasm), SizeBytes: int64(len(wasm))})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutArtifactBytes(artifact.ID, wasm); err != nil {
		t.Fatal(err)
	}
	deploy, err := s.CreateDeploy(DeployInput{AppID: app.ID, ArtifactID: artifact.ID, SourceDigest: digestBytes([]byte("src")), CreatedByOwnerID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateDeploy(app.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}
	return s, owner, app, artifact, deploy
}

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

func TestEncryptedSnapshotDoesNotExposeSecretAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	key := []byte("01234567890123456789012345678901")
	s, err := OpenStore(path, WithSnapshotEncryptionKey(key))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertSecret(SecretInput{AppID: app.ID, Key: "API_KEY", Value: []byte("not-on-disk")}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || bytes.Contains(raw, []byte("not-on-disk")) || !json.Valid(raw) {
		t.Fatalf("snapshot was not encrypted: %q", raw)
	}
	reopened, err := OpenStore(path, WithSnapshotEncryptionKey(key))
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.SecretsForApp(app.ID)["API_KEY"]; got != "not-on-disk" {
		t.Fatalf("secret = %q", got)
	}
}

func TestEncryptedSnapshotMigratesPreviousKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	oldKey := []byte("01234567890123456789012345678901")
	newKey := []byte("abcdefghijklmnopqrstuvwxyz012345")
	s, err := OpenStore(path, WithSnapshotEncryptionKey(oldKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOwner("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, WithSnapshotEncryptionKey(newKey), WithPreviousSnapshotEncryptionKey(oldKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, WithSnapshotEncryptionKey(newKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, WithSnapshotEncryptionKey(oldKey)); err == nil {
		t.Fatal("old key decrypted rotated snapshot")
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
	app, err := store.EnsureApp(AppInput{OwnerID: owner.ID, Name: "counter"})
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
