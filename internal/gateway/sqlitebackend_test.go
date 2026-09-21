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

	"github.com/Ceinl/plumtree/internal/runner"
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
	if sessionID, err := backend.StartSession(context.Background(), "app-2", deployment.ID, artifact.Digest, "{}"); sessionID != "" || !errors.Is(err, sqlite.ErrNotFound) {
		t.Fatalf("cross-app session = %q, %v; want not found", sessionID, err)
	}
}

// The retained backend must satisfy one session-oriented contract: identity
// resolution, identity-aware runnable resolution with artifact digest,
// accounted session open, KV, capability reads, and session completion.
func TestSQLiteBackendSatisfiesSessionContract(t *testing.T) {
	repository, err := sqlite.OpenRepository(filepath.Join(t.TempDir(), "contract.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	author, device, err := repository.RegisterAuthor(context.Background(), sqlite.RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop",
		PublicKey: "owner-key", Fingerprint: "fp-owner",
		RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"),
	})
	if err != nil {
		t.Fatal(err)
	}
	wasm := []byte("wasm-bytes")
	digest := sha256.Sum256(wasm)
	artifact, err := repository.PutArtifact(context.Background(), sqlite.ArtifactInput{
		ID: "artifact-1", Digest: "sha256:" + hex.EncodeToString(digest[:]), WASM: wasm, ABIVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, err := repository.CreateApp(context.Background(), sqlite.AppInput{
		ID: "app-1", AuthorID: author.ID, Name: "tool", Kind: "cli",
		AccessMode: "public", CreatedByDeviceID: device.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repository.CreateDeployment(context.Background(), sqlite.DeploymentInput{
		ID: "deployment-1", AppID: app.ID, ArtifactID: artifact.ID, DeployedByDeviceID: device.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateDeployment(context.Background(), app.ID, deployment.ID); err != nil {
		t.Fatal(err)
	}
	backend := NewSQLiteBackend(repository)
	ctx := context.Background()
	identity := runner.Identity{User: "fp-owner", Kind: runner.IdentitySSHKey, Authenticated: true, OwnerID: author.ID}

	if id, err := backend.ResolveIdentity(ctx, "fp-owner"); err != nil || !id.Authenticated || id.OwnerID != author.ID {
		t.Fatalf("ResolveIdentity = %+v, %v; want authenticated owner", id, err)
	}
	if anon, err := backend.ResolveIdentity(ctx, "unknown-fp"); err != nil || anon.Authenticated || anon.Kind != runner.IdentitySSHKey {
		t.Fatalf("ResolveIdentity unknown = %+v, %v; want unauthenticated key identity", anon, err)
	}
	run, err := backend.ResolveRunnable(ctx, "alice/tool", identity)
	if err != nil || run.AppID != "app-1" || run.ArtifactDigest == "" || len(run.WASM) == 0 {
		t.Fatalf("ResolveRunnable = %+v, %v", run, err)
	}
	sessionID, err := backend.StartSession(ctx, run.AppID, run.DeployID, run.ArtifactDigest, `{"user":"fp-owner"}`)
	if err != nil || sessionID == "" {
		t.Fatalf("StartSession = %q, %v", sessionID, err)
	}
	if err := backend.RecordSessionLog(ctx, sessionID, "hello", false); err != nil {
		t.Fatalf("RecordSessionLog: %v", err)
	}
	store, err := backend.KVStore(ctx, run.AppID)
	if err != nil || store == nil {
		t.Fatalf("KVStore = %v, %v", store, err)
	}
	if err := store.Set("k", []byte("v")); err != nil {
		t.Fatalf("KV Set: %v", err)
	}
	if value, found, err := store.Get("k"); err != nil || !found || string(value) != "v" {
		t.Fatalf("KV Get = %q, %t, %v", value, found, err)
	}
	if _, err := backend.SecretsForApp(ctx, run.AppID); err != nil {
		t.Fatalf("SecretsForApp: %v", err)
	}
	if _, err := backend.EgressAllowlist(ctx, run.AppID); err != nil {
		t.Fatalf("EgressAllowlist: %v", err)
	}
	if err := backend.EndSession(ctx, sessionID); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
}

// Missing wiring and backend outages are distinct typed errors: a nil
// repository reports ErrNotConfigured, while unexpected storage failures
// report ErrCapsUnavailable.
func TestSQLiteBackendDistinguishesNotConfiguredFromUnavailable(t *testing.T) {
	bare := &SQLiteBackend{}
	ctx := context.Background()
	if _, err := bare.ResolveIdentity(ctx, "fp"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ResolveIdentity without repository = %v, want ErrNotConfigured", err)
	}
	if _, err := bare.ResolveRunnable(ctx, "alice/tool", runner.Identity{}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ResolveRunnable without repository = %v, want ErrNotConfigured", err)
	}
	if _, err := bare.KVStore(ctx, "app-1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("KVStore without repository = %v, want ErrNotConfigured", err)
	}
	if _, err := bare.StartSession(ctx, "app-1", "dep-1", "digest", "{}"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("StartSession without repository = %v, want ErrNotConfigured", err)
	}
	if _, err := bare.SecretsForApp(ctx, "app-1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("SecretsForApp without repository = %v, want ErrNotConfigured", err)
	}
	if _, err := bare.EgressAllowlist(ctx, "app-1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("EgressAllowlist without repository = %v, want ErrNotConfigured", err)
	}
	if err := bare.RecordSessionLog(ctx, "session_1", "log", false); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("RecordSessionLog without repository = %v, want ErrNotConfigured", err)
	}
	if err := bare.EndSession(ctx, "session_1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("EndSession without repository = %v, want ErrNotConfigured", err)
	}

	// A closed repository is a storage outage, not missing wiring.
	closed := openSuspensionTestBackend(t)
	if err := closed.Repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.SecretsForApp(ctx, "app-1"); !errors.Is(err, ErrCapsUnavailable) {
		t.Fatalf("SecretsForApp on closed store = %v, want ErrCapsUnavailable", err)
	}
}

// A gateway without its suspension dependency fails closed at construction, not by
// silently running without a kill switch.
func TestNewRequiresSuspensionSource(t *testing.T) {
	repository, err := sqlite.OpenRepository(filepath.Join(t.TempDir(), "susp-required.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	backend := NewSQLiteBackend(repository)
	if _, err := New(Config{Backend: backend}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("New without suspensions = %v, want ErrNotConfigured", err)
	}
}

func TestNewRequiresBackend(t *testing.T) {
	backend := openSuspensionTestBackend(t)
	if _, err := New(Config{Suspensions: backend}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("New without backend = %v, want ErrNotConfigured", err)
	}
}
