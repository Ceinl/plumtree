package runner

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestMemStoreGetSetDelete(t *testing.T) {
	s := NewMemStore(0, 0)

	if _, ok, err := s.Get("missing"); ok || err != nil {
		t.Fatalf("Get missing: ok=%v err=%v", ok, err)
	}
	if err := s.Set("k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.Get("k"); !ok || string(v) != "v1" {
		t.Fatalf("Get k = %q ok=%v, want v1", v, ok)
	}
	// Returned slice is a copy: mutating it must not change the store.
	v, _, _ := s.Get("k")
	v[0] = 'X'
	if again, _, _ := s.Get("k"); string(again) != "v1" {
		t.Fatalf("store mutated through returned slice: %q", again)
	}
	if err := s.Set("k", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.Get("k"); string(v) != "v2" {
		t.Fatalf("after overwrite = %q, want v2", v)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get("k"); ok {
		t.Fatal("key present after delete")
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("delete of missing key errored: %v", err)
	}
}

func TestMemStoreKeyQuota(t *testing.T) {
	s := NewMemStore(2, 0)
	if err := s.Set("a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b", []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("c", []byte("3")); !errors.Is(err, ErrQuota) {
		t.Fatalf("third key err = %v, want ErrQuota", err)
	}
	// Overwriting an existing key does not add a key, so it is allowed.
	if err := s.Set("a", []byte("11")); err != nil {
		t.Fatalf("overwrite within key quota errored: %v", err)
	}
}

func TestMemStoreByteQuota(t *testing.T) {
	// Budget = 10 bytes of key+value. "k1"(2) + "12345"(5) = 7.
	s := NewMemStore(0, 10)
	if err := s.Set("k1", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	// Growing the same key to exceed the budget is rejected and the old value
	// is retained.
	if err := s.Set("k1", []byte("123456789")); !errors.Is(err, ErrQuota) {
		t.Fatalf("oversize overwrite err = %v, want ErrQuota", err)
	}
	if v, _, _ := s.Get("k1"); string(v) != "12345" {
		t.Fatalf("value changed after rejected write: %q", v)
	}
	// Deleting frees the budget so a new entry fits.
	if err := s.Delete("k1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("longkey", []byte("xyz")); err != nil {
		t.Fatalf("set after delete errored: %v", err)
	}
}

func TestFileStorePersistsAcrossInstances(t *testing.T) {
	path := t.TempDir() + "/kv.json"

	s1, err := NewFileStore(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Set("greeting", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := s1.Set("count", []byte("3")); err != nil {
		t.Fatal(err)
	}
	if err := s1.Delete("count"); err != nil {
		t.Fatal(err)
	}

	// A fresh instance over the same file sees the persisted state.
	s2, err := NewFileStore(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s2.Get("greeting"); !ok || string(v) != "hello" {
		t.Fatalf("reloaded greeting = %q ok=%v", v, ok)
	}
	if _, ok, _ := s2.Get("count"); ok {
		t.Fatal("deleted key reappeared after reload")
	}
}

func TestFileStoreSetRollsBackOnPersistenceFailure(t *testing.T) {
	for _, failure := range []struct {
		name   string
		inject func(*FileStore, error)
	}{
		{"write", func(s *FileStore, err error) { s.writeFile = func(string, []byte, os.FileMode) error { return err } }},
		{"rename", func(s *FileStore, err error) { s.rename = func(string, string) error { return err } }},
	} {
		t.Run(failure.name, func(t *testing.T) {
			path := t.TempDir() + "/kv.json"
			s, err := NewFileStore(path, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Set("key", []byte("old")); err != nil {
				t.Fatal(err)
			}
			beforeBytes := s.bytes
			injected := errors.New("injected persistence failure")
			failure.inject(s, injected)

			if err := s.Set("key", []byte("new value")); !errors.Is(err, injected) {
				t.Fatalf("Set error = %v, want injected failure", err)
			}
			if value, found, _ := s.Get("key"); !found || string(value) != "old" {
				t.Fatalf("in-memory value after failed Set = %q, found=%v", value, found)
			}
			if s.bytes != beforeBytes {
				t.Fatalf("byte count after failed Set = %d, want %d", s.bytes, beforeBytes)
			}
			persisted, err := NewFileStore(path, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if value, found, _ := persisted.Get("key"); !found || string(value) != "old" {
				t.Fatalf("persisted value after failed Set = %q, found=%v", value, found)
			}
		})
	}
}

func TestFileStoreDeleteRollsBackOnPersistenceFailure(t *testing.T) {
	for _, failure := range []struct {
		name   string
		inject func(*FileStore, error)
	}{
		{"write", func(s *FileStore, err error) { s.writeFile = func(string, []byte, os.FileMode) error { return err } }},
		{"rename", func(s *FileStore, err error) { s.rename = func(string, string) error { return err } }},
	} {
		t.Run(failure.name, func(t *testing.T) {
			path := t.TempDir() + "/kv.json"
			s, err := NewFileStore(path, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Set("key", []byte("value")); err != nil {
				t.Fatal(err)
			}
			beforeBytes := s.bytes
			injected := errors.New("injected persistence failure")
			failure.inject(s, injected)

			if err := s.Delete("key"); !errors.Is(err, injected) {
				t.Fatalf("Delete error = %v, want injected failure", err)
			}
			if value, found, _ := s.Get("key"); !found || string(value) != "value" {
				t.Fatalf("in-memory value after failed Delete = %q, found=%v", value, found)
			}
			if s.bytes != beforeBytes {
				t.Fatalf("byte count after failed Delete = %d, want %d", s.bytes, beforeBytes)
			}
			persisted, err := NewFileStore(path, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			if value, found, _ := persisted.Get("key"); !found || string(value) != "value" {
				t.Fatalf("persisted value after failed Delete = %q, found=%v", value, found)
			}
		})
	}
}

// TestKVHostFunctions drives a raw-wasmimport guest through the KV host imports,
// covering set, get, the kv_get grow-and-retry size protocol, not-found, and
// delete.
func TestKVHostFunctions(t *testing.T) {
	wasm := buildGuest(t, "testdata/kvguest", "GOWORK=off")

	store := NewMemStore(0, 0)
	var out strings.Builder
	if err := RunCLI(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, nil, &out); err != nil {
		t.Fatalf("RunCLI: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		"set=0",              // KVOk
		"get=11:hello world", // full value read back
		"grow=11",            // small buffer -> needed length, no write
		"miss=-1",            // KVErrNotFound
		"del=0",              // KVOk
		"after=-1",           // gone after delete
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; full output:\n%s", want, got)
		}
	}
	if _, ok, _ := store.Get("greeting"); ok {
		t.Error("key still present in store after guest delete")
	}
}

// TestKVUnavailable verifies that without a KV capability the host imports fail
// cleanly with KVErrInternal instead of trapping, so a guest that kept the
// imports still instantiates.
func TestKVUnavailable(t *testing.T) {
	wasm := buildGuest(t, "testdata/kvguest", "GOWORK=off")

	var out strings.Builder
	if err := RunCLI(context.Background(), wasm, DefaultLimits, Capabilities{}, nil, &out); err != nil {
		t.Fatalf("RunCLI: %v", err)
	}
	want := "set=" + strconv.Itoa(int(abi.KVErrInternal))
	if !strings.Contains(out.String(), want) {
		t.Errorf("want %q with nil KV; output:\n%s", want, out.String())
	}
}
