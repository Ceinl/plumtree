package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/sqlite"
)

func openSuspensionTestBackend(t *testing.T) *SQLiteBackend {
	t.Helper()
	repository, err := sqlite.OpenRepository(filepath.Join(t.TempDir(), "suspensions.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return NewSQLiteBackend(repository)
}

// A suspension committed before the kill switch registers must be delivered
// during registration, exactly once even if several events coalesce, so no
// early kill switch request is lost.
func TestSQLiteBackendDeliversSuspensionsCommittedBeforeWatcher(t *testing.T) {
	backend := openSuspensionTestBackend(t)
	for range 3 {
		if err := backend.committed(sqlite.CommitEvent{Operation: "deployment-suspension", ID: "dep-1"}); err != nil {
			t.Fatalf("commit listener: %v", err)
		}
	}
	backend.mu.Lock()
	pending := len(backend.pending)
	backend.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending deliveries = %d, want 1 (duplicates coalesced)", pending)
	}

	delivered := make(chan Suspension, 4)
	watch := func(_ context.Context, event Suspension) error { delivered <- event; return nil }
	done := make(chan error, 1)
	go func() { done <- backend.StartSuspensionWatcher(context.Background(), watch) }()
	select {
	case event := <-delivered:
		if event.Scope != KillDeploy || event.ID != "dep-1" {
			t.Fatalf("delivered %+v, want deploy dep-1", event)
		}
	case <-time.After(time.Second):
		t.Fatal("pre-registration suspension was never delivered")
	}
	select {
	case extra := <-delivered:
		t.Fatalf("duplicate delivery %v", extra)
	case <-time.After(50 * time.Millisecond):
	}
	backend.mu.Lock()
	pending = len(backend.pending)
	backend.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d deliveries left pending after registration", pending)
	}
}

// A failing kill switch must not drop a committed suspension: the delivery
// stays pending and is retried until the handler acknowledges.
func TestSQLiteBackendRetriesFailedDeliveryUntilAcknowledged(t *testing.T) {
	backend := openSuspensionTestBackend(t)
	backend.retryBase = time.Millisecond
	var attempts atomic.Int32
	first := make(chan struct{})
	closeFirst := sync.OnceFunc(func() { close(first) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := backend.StartSuspensionWatcher(ctx, func(_ context.Context, _ Suspension) error {
		if attempts.Add(1) == 1 {
			closeFirst() // fail the first pass, acknowledge afterwards
			return context.DeadlineExceeded
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := backend.committed(sqlite.CommitEvent{Operation: "deployment-suspension", ID: "dep-2"}); err != nil {
		t.Fatalf("commit listener: %v", err)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("failed delivery was dropped instead of retried")
	}
	for attempts.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	backend.mu.Lock()
	pending := len(backend.pending)
	backend.mu.Unlock()
	if pending != 0 {
		t.Fatalf("%d deliveries left pending after acknowledgement", pending)
	}
}

func TestSQLiteBackendIgnoresNonSuspensionCommitEvents(t *testing.T) {
	backend := openSuspensionTestBackend(t)
	if err := backend.committed(sqlite.CommitEvent{Operation: "session-end", Kind: "session", ID: "s-1"}); err != nil {
		t.Fatalf("commit listener: %v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.pending) != 0 {
		t.Fatalf("non-suspension event was queued: %v", backend.pending)
	}
}

func TestSQLiteBackendRejectsDeploymentFromAnotherApp(t *testing.T) {
	backend := openSuspensionTestBackend(t)
	repository := backend.Repository
	author, _, err := repository.RegisterAuthor(context.Background(), sqlite.RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop",
		PublicKey: "key", Fingerprint: "fingerprint", RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"app-1", "app-2"} {
		if _, err := repository.CreateApp(context.Background(), sqlite.AppInput{ID: id, AuthorID: author.ID, Name: id, Kind: "cli", AccessMode: "public"}); err != nil {
			t.Fatal(err)
		}
	}
	wasm := []byte("wasm")
	digest := sha256.Sum256(wasm)
	artifact, err := repository.PutArtifact(context.Background(), sqlite.ArtifactInput{ID: "artifact-1", Digest: "sha256:" + hex.EncodeToString(digest[:]), WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repository.CreateDeployment(context.Background(), sqlite.DeploymentInput{ID: "deployment-1", AppID: "app-1", ArtifactID: artifact.ID, DeployedByDeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	if sessionID, err := backend.StartSession("app-2", deployment.ID); sessionID != "" || !errors.Is(err, sqlite.ErrNotFound) {
		t.Fatalf("cross-app session = %q, %v; want not found", sessionID, err)
	}
}
