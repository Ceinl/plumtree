package control

import (
	"errors"
	"testing"
)

func TestCreateDeployClaimWithArtifactRollsBackPersistenceFailure(t *testing.T) {
	store := NewStore()
	store.persistPath = t.TempDir() // renaming the snapshot file over a directory fails
	reservation, release, err := store.ReserveDeployClaimQuota()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	wasm := []byte("wasm")
	_, _, err = store.CreateDeployClaimWithArtifact(reservation,
		ArtifactInput{Digest: digestBytes(wasm), SizeBytes: int64(len(wasm))}, wasm,
		DeployClaimInput{AppName: "counter", AppType: "tui", SourceDigest: digestBytes([]byte("src")), ClaimTokenHash: digestBytes([]byte("token"))},
	)
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if _, err := store.GetArtifact("art_000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("artifact survived failed transaction: %v", err)
	}
	if _, err := store.GetDeploy("dep_000001"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deploy survived failed transaction: %v", err)
	}
}

func TestDeployClaimReservationsCountAgainstRateLimit(t *testing.T) {
	store := NewStore(WithMaxDeployClaimsPerHour(1))
	_, release, err := store.ReserveDeployClaimQuota()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, err := store.ReserveDeployClaimQuota(); !errors.Is(err, ErrQuota) {
		t.Fatalf("second reservation error = %v, want ErrQuota", err)
	}
}

func TestUpdateDeployClaimWithArtifactRollsBackPersistenceFailure(t *testing.T) {
	store := NewStore()
	oldWASM := []byte("old")
	oldArtifact, err := store.CreateArtifact(ArtifactInput{Digest: digestBytes(oldWASM), SizeBytes: int64(len(oldWASM))})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutArtifactBytes(oldArtifact.ID, oldWASM); err != nil {
		t.Fatal(err)
	}
	tokenHash := digestBytes([]byte("token"))
	deploy, err := store.CreateDeployClaim(DeployClaimInput{
		AppName: "counter", AppType: "tui", ArtifactID: oldArtifact.ID,
		SourceDigest: digestBytes([]byte("old-src")), ClaimTokenHash: tokenHash,
	})
	if err != nil {
		t.Fatal(err)
	}

	store.persistPath = t.TempDir()
	newWASM := []byte("new")
	_, _, _, _, err = store.UpdateDeployClaimWithArtifact(deploy.ID,
		ArtifactInput{Digest: digestBytes(newWASM), SizeBytes: int64(len(newWASM))}, newWASM,
		DeployClaimInput{AppName: "counter", AppType: "tui", SourceDigest: digestBytes([]byte("new-src")), ClaimTokenHash: tokenHash},
	)
	if err == nil {
		t.Fatal("expected persistence error")
	}
	got, err := store.GetDeploy(deploy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArtifactID != oldArtifact.ID {
		t.Fatalf("deploy artifact = %q, want %q", got.ArtifactID, oldArtifact.ID)
	}
	if _, err := store.GetArtifact("art_000002"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new artifact survived failed update: %v", err)
	}
}
