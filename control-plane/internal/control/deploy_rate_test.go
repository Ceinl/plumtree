package control

import (
	"errors"
	"testing"
	"time"
)

func TestDeployClaimRateLimit(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	store := NewStore(
		WithClock(func() time.Time { return now }),
		WithMaxDeployClaimsPerHour(2),
	)

	mkClaim := func() error {
		art, err := store.CreateArtifact(ArtifactInput{
			Digest: digestBytes([]byte("w" + now.String())), SizeBytes: int64(len("w" + now.String())),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateDeployClaim(DeployClaimInput{
			AppName: "counter", AppType: "tui", ArtifactID: art.ID,
			SourceDigest:   digestBytes([]byte("src" + now.String())),
			ClaimTokenHash: digestBytes([]byte("t" + now.String())),
		})
		return err
	}

	if err := mkClaim(); err != nil {
		t.Fatalf("claim 1: %v", err)
	}
	if err := mkClaim(); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	// Third claim within the hour is rejected.
	if err := mkClaim(); !errors.Is(err, ErrQuota) {
		t.Fatalf("claim 3 err = %v, want ErrQuota", err)
	}

	// After the window advances, claims are allowed again.
	now = now.Add(time.Hour + time.Minute)
	if err := mkClaim(); err != nil {
		t.Fatalf("claim after window: %v", err)
	}
}
