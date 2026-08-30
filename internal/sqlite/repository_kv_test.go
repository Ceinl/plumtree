package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
)

func newKVTestApp(t *testing.T, options ...RepositoryOption) (*Repository, string) {
	t.Helper()
	r := newTestRepository(t, options...)
	a, _ := registerTestAuthor(t, r)
	if _, err := r.CreateApp(context.Background(), AppInput{ID: "app-kv", AuthorID: a.ID, Name: "demo", Kind: "tui", AccessMode: "public"}); err != nil {
		t.Fatalf("create app: %v", err)
	}
	return r, "app-kv"
}

func TestRepositoryKVCrudRoundTrip(t *testing.T) {
	r, app := newKVTestApp(t)
	ctx := context.Background()

	if _, found, err := r.KVGet(ctx, app, "missing"); err != nil || found {
		t.Fatalf("get missing = %t, %v", found, err)
	}
	if err := r.KVSet(ctx, app, "greeting", []byte("hello")); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, found, err := r.KVGet(ctx, app, "greeting")
	if err != nil || !found || string(value) != "hello" {
		t.Fatalf("get = %q, %t, %v", value, found, err)
	}
	if err := r.KVSet(ctx, app, "greeting", []byte("again")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if keys, err := r.KVList(ctx, app, "", 10); err != nil || len(keys) != 1 || keys[0] != "greeting" {
		t.Fatalf("list = %v, %v", keys, err)
	}
	if err := r.KVSet(ctx, app, "prefixed-a", []byte("1")); err != nil {
		t.Fatalf("set prefixed: %v", err)
	}
	if keys, err := r.KVList(ctx, app, "prefixed", 10); err != nil || len(keys) != 1 || keys[0] != "prefixed-a" {
		t.Fatalf("prefix list = %v, %v", keys, err)
	}
	if keys, err := r.KVList(ctx, app, "", 1); err != nil || len(keys) != 1 {
		t.Fatalf("bounded list = %v, %v", keys, err)
	}
	if err := r.KVDelete(ctx, app, "greeting"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, err := r.KVGet(ctx, app, "greeting"); err != nil || found {
		t.Fatalf("get after delete = %t, %v", found, err)
	}
	if err := r.KVDelete(ctx, app, "greeting"); err != nil {
		t.Fatalf("deleting a missing key must not error: %v", err)
	}
	keys, bytes, err := r.KVUsage(ctx, app)
	if err != nil || keys != 1 || bytes != len("prefixed-a")+1 {
		t.Fatalf("usage = %d/%d, %v", keys, bytes, err)
	}
}

func TestRepositoryKVQueryFaultsReturnErrors(t *testing.T) {
	tests := []struct {
		name   string
		failAt int
		run    func(*Repository, string) error
	}{
		{name: "existing value", failAt: 2, run: func(r *Repository, app string) error {
			return r.KVSet(context.Background(), app, "key", []byte("value"))
		}},
		{name: "usage", failAt: 3, run: func(r *Repository, app string) error {
			return r.KVSet(context.Background(), app, "key", []byte("value"))
		}},
		{name: "delete value", failAt: 1, run: func(r *Repository, app string) error { return r.KVDelete(context.Background(), app, "key") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r, app := newKVTestApp(t)
			calls := 0
			r.faults.Statement = func(string) error {
				calls++
				if calls == test.failAt {
					return errors.New("boom")
				}
				return nil
			}
			if err := test.run(r, app); !errors.Is(err, ErrInjectedStatement) {
				t.Fatalf("query fault error = %v, want ErrInjectedStatement", err)
			}
		})
	}
}

func TestRepositoryKVCasSemantics(t *testing.T) {
	r, app := newKVTestApp(t)
	ctx := context.Background()
	var zero [sha256.Size]byte

	// Create-only via the zero hash.
	value := []byte("first")
	hash := sha256.Sum256(value)
	if err := r.KVCompareAndSwap(ctx, app, "state", zero, value); err != nil {
		t.Fatalf("cas create: %v", err)
	}
	if err := r.KVCompareAndSwap(ctx, app, "state", zero, []byte("dup")); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create with zero hash = %v, want ErrConflict", err)
	}
	// Stale expectation leaves state unchanged.
	if err := r.KVCompareAndSwap(ctx, app, "state", zero, []byte("stale")); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale cas = %v, want ErrConflict", err)
	}
	got, found, err := r.KVGet(ctx, app, "state")
	if err != nil || !found || string(got) != "first" {
		t.Fatalf("post-conflict state = %q, %t, %v", got, found, err)
	}
	// Matching expectation swaps atomically.
	next := []byte("second")
	if err := r.KVCompareAndSwap(ctx, app, "state", hash, next); err != nil {
		t.Fatalf("cas swap: %v", err)
	}
	got, _, _ = r.KVGet(ctx, app, "state")
	if string(got) != "second" {
		t.Fatalf("value after swap = %q", got)
	}
	// Delete-then-recreate through the zero hash.
	if err := r.KVDelete(ctx, app, "state"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := r.KVCompareAndSwap(ctx, app, "state", zero, []byte("recreated")); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

func TestRepositoryKVEnforcesBudgets(t *testing.T) {
	r, app := newKVTestApp(t, WithKVLimits(2, 12))
	ctx := context.Background()

	if err := r.KVSet(ctx, app, "a", []byte("12345")); err != nil { // 6 bytes used
		t.Fatalf("set a: %v", err)
	}
	if err := r.KVSet(ctx, app, "b", []byte("12345")); err != nil { // 12 bytes
		t.Fatalf("set b: %v", err)
	}
	if err := r.KVSet(ctx, app, "c", []byte("x")); !errors.Is(err, ErrQuota) {
		t.Fatalf("third key = %v, want ErrQuota (key budget)", err)
	}
	if err := r.KVSet(ctx, app, "a", []byte("123456")); !errors.Is(err, ErrQuota) {
		t.Fatalf("oversized overwrite = %v, want ErrQuota (byte budget)", err)
	}
	// Replacing an existing key within the byte budget stays allowed even at
	// the key-count ceiling.
	if err := r.KVSet(ctx, app, "b", []byte("54321")); err != nil {
		t.Fatalf("replace at key ceiling: %v", err)
	}
	if err := r.KVCompareAndSwap(ctx, app, "c", [sha256.Size]byte{}, []byte("x")); !errors.Is(err, ErrQuota) {
		t.Fatalf("cas beyond budget = %v, want ErrQuota", err)
	}
	if err := r.KVDelete(ctx, app, "a"); err != nil {
		t.Fatalf("delete frees budget: %v", err)
	}
	if err := r.KVSet(ctx, app, "c", []byte("x")); err != nil {
		t.Fatalf("set after delete: %v", err)
	}
}

func TestRepositoryKVRejectsInvalidInput(t *testing.T) {
	r, app := newKVTestApp(t)
	ctx := context.Background()
	if err := r.KVSet(ctx, app, "", []byte("x")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty key = %v, want ErrInvalid", err)
	}
	if err := r.KVSet(ctx, app, string(make([]byte, 257)), []byte("x")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized key = %v, want ErrInvalid", err)
	}
	if _, err := r.KVList(ctx, app, "", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero limit list = %v, want ErrInvalid", err)
	}
	if _, found, err := r.KVGet(ctx, "no-such-app", "k"); err != nil || found {
		t.Fatalf("unknown app get = %t, %v", found, err)
	}
	if err := r.KVSet(ctx, "no-such-app", "k", []byte("v")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("kv for unknown app = %v, want ErrNotFound", err)
	}
}

// KV state lives in the encrypted repository, so it survives reopen exactly
// like every other repository record.
func TestRepositoryKVPersistsAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/kv-repo.db"
	ctx := context.Background()
	r, err := OpenRepository(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := registerTestAuthor(t, r)
	if _, err := r.CreateApp(ctx, AppInput{ID: "app-kv", AuthorID: a.ID, Name: "demo", Kind: "tui", AccessMode: "public"}); err != nil {
		t.Fatal(err)
	}
	if err := r.KVSet(ctx, "app-kv", "durable", []byte("yes")); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRepository(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	value, found, err := reopened.KVGet(ctx, "app-kv", "durable")
	if err != nil || !found || string(value) != "yes" {
		t.Fatalf("reopened get = %q, %t, %v", value, found, err)
	}
}
