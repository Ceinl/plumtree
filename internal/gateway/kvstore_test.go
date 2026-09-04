package gateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

func openKVTestServer(t *testing.T) (*Server, *SQLiteBackend) {
	t.Helper()
	repository, err := sqlite.OpenRepository(filepath.Join(t.TempDir(), "kv-gateway.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	author, _, err := repository.RegisterAuthor(context.Background(), sqlite.RegistrationInput{
		AuthorID: "author-kv", Handle: "kvalice", DeviceID: "device-kv", DeviceName: "laptop",
		PublicKey: "ssh-ed25519-key", Fingerprint: "fp-kv", RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"),
	})
	if err != nil {
		t.Fatalf("register author: %v", err)
	}
	if _, err := repository.CreateApp(context.Background(), sqlite.AppInput{ID: "app-kv", AuthorID: author.ID, Name: "demo", Kind: "tui", AccessMode: "public"}); err != nil {
		t.Fatalf("create app: %v", err)
	}
	backend := NewSQLiteBackend(repository)
	s := mustNewServer(t, Config{Backend: backend})
	return s, backend
}

// The SQLite backend's KV adapter must satisfy the full runner.Store contract:
// CRUD, prefix list, CAS conflict/quota sentinels, and cross-store sharing.
func TestSQLiteKVStoreSatisfiesRunnerStoreContract(t *testing.T) {
	_, backend := openKVTestServer(t)
	store, err := backend.KVStore(context.Background(), "app-kv")
	if err != nil {
		t.Fatalf("KVStore: %v", err)
	}

	if _, found, err := store.Get("missing"); err != nil || found {
		t.Fatalf("missing key = %t, %v", found, err)
	}
	if err := store.Set("greeting", []byte("hello")); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, found, err := store.Get("greeting")
	if err != nil || !found || string(value) != "hello" {
		t.Fatalf("get = %q, %t, %v", value, found, err)
	}
	if keys, err := store.List("greet", 10); err != nil || len(keys) != 1 || keys[0] != "greeting" {
		t.Fatalf("list = %v, %v", keys, err)
	}

	var zero [sha256.Size]byte
	if err := store.CompareAndSwap("cas", zero, []byte("v1")); err != nil {
		t.Fatalf("cas create: %v", err)
	}
	hash := sha256.Sum256([]byte("v1"))
	if err := store.CompareAndSwap("cas", zero, []byte("v2")); !errors.Is(err, runner.ErrConflict) {
		t.Fatalf("stale cas = %v, want runner.ErrConflict", err)
	}
	if err := store.CompareAndSwap("cas", hash, []byte("v2")); err != nil {
		t.Fatalf("cas swap: %v", err)
	}
	if err := store.Delete("greeting"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := store.Get("greeting"); found {
		t.Fatal("key survived delete")
	}

	// Quota exhaustion surfaces as runner.ErrQuota.
	for i := range 1000 {
		key := []byte{byte(i / 256), byte(i % 256)}
		if err := store.Set(string(key), nil); errors.Is(err, runner.ErrQuota) {
			break
		} else if err != nil {
			t.Fatalf("quota fill set %d: %v", i, err)
		}
	}
	if err := store.Set("overflow", make([]byte, 5<<20)); !errors.Is(err, runner.ErrQuota) {
		t.Fatalf("oversized value = %v, want runner.ErrQuota", err)
	}
}

func TestSQLiteKVStorePreservesContextCancellation(t *testing.T) {
	_, backend := openKVTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := backend.KVStore(ctx, "app-kv"); !errors.Is(err, context.Canceled) {
		t.Fatalf("KVStore error = %v, want context.Canceled", err)
	}
}

// Hosted sessions must receive the repository-backed KV store, so state is
// durable and encrypted at rest by the storage engine rather than living in
// plaintext per-app JSON files.
func TestAssembleHostCapabilitiesUsesRepositoryKV(t *testing.T) {
	s, backend := openKVTestServer(t)
	app := Runnable{AppID: "app-kv", OwnerID: "owner-kv"}
	caps, err := assembleWith(s, app, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if caps.KV == nil {
		t.Fatal("hosted session ran without a KV store")
	}
	if err := caps.KV.Set("from-session", []byte("persisted")); err != nil {
		t.Fatalf("session set: %v", err)
	}
	// A second session of the same app shares one store through the repository.
	second, err := assembleWith(s, app, runner.Identity{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if value, found, err := second.KV.Get("from-session"); err != nil || !found || string(value) != "persisted" {
		t.Fatalf("second session get = %q, %t, %v", value, found, err)
	}
	// The bytes live in the repository itself, not a StateDir/kv JSON file.
	if _, _, err := backend.Repository.KVGet(context.Background(), "app-kv", "from-session"); err != nil {
		t.Fatalf("repository read-back: %v", err)
	}
}

// A KV source failure fails closed: the capability is absent and the
// assembler reports why instead of hiding unavailable durable state.
func TestAssembleHostCapabilitiesKVUnavailableFailsClosed(t *testing.T) {
	backend := &capsBackend{kvErr: ErrCapsUnavailable}
	s := mustNewServer(t, Config{Backend: backend})
	caps, err := assembleWith(s, Runnable{AppID: "app-1", OwnerID: "owner-1"}, runner.Identity{})
	if caps.KV != nil {
		t.Fatalf("KV = %T, want nil when the KV source fails", caps.KV)
	}
	if !errors.Is(err, ErrCapsUnavailable) {
		t.Fatalf("error = %v, want ErrCapsUnavailable", err)
	}
}
