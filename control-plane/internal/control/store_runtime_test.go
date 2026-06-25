package control

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionsRequireActiveDeploy(t *testing.T) {
	store := NewStore()
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ArtifactInput{Digest: artifactDigest, SizeBytes: 1})
	if err != nil {
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
	if _, err := store.StartSession(app.ID, deploy.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("StartSession inactive deploy error = %v, want ErrInvalid", err)
	}
	if _, err := store.ActivateDeploy(app.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}
	session, err := store.StartSession(app.ID, deploy.ID)
	if err != nil {
		t.Fatal(err)
	}
	ended, err := store.EndSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ended.EndedAt == nil {
		t.Fatal("ended session missing EndedAt")
	}
}

func TestRecordSessionLogPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane-state.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ArtifactInput{Digest: artifactDigest, SizeBytes: 1})
	if err != nil {
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
	session, err := store.StartSession(app.ID, deploy.ID)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.RecordSessionLog(session.ID, "hello\nworld\n", true); err != nil {
		t.Fatalf("RecordSessionLog: %v", err)
	}
	if _, err := store.RecordSessionLog("ses_nope", "x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RecordSessionLog unknown id err = %v, want ErrNotFound", err)
	}

	reloaded, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := reloaded.ListSessionsForApp(app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if got := sessions[0]; got.Log != "hello\nworld\n" || !got.LogTruncated {
		t.Fatalf("persisted session log = %q truncated=%v, want %q true", got.Log, got.LogTruncated, "hello\\nworld\\n")
	}
}

func TestPerAppDailySessionCap(t *testing.T) {
	now := time.Unix(1_000_000, 0).UTC()
	store := NewStore(
		WithClock(func() time.Time { return now }),
		WithMaxSessionsPerAppPerDay(3),
	)
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(ArtifactInput{Digest: artifactDigest, SizeBytes: 1})
	if err != nil {
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

	// The first 3 sessions in the window are allowed; the 4th is rejected.
	for i := 0; i < 3; i++ {
		if _, err := store.StartSession(app.ID, deploy.ID); err != nil {
			t.Fatalf("session %d should be allowed: %v", i, err)
		}
	}
	if _, err := store.StartSession(app.ID, deploy.ID); !errors.Is(err, ErrQuota) {
		t.Fatalf("4th session err = %v, want ErrQuota", err)
	}

	// Past sessions age out of the rolling window: 25h later the cap resets.
	now = now.Add(25 * time.Hour)
	if _, err := store.StartSession(app.ID, deploy.ID); err != nil {
		t.Fatalf("session after window should be allowed: %v", err)
	}
}
