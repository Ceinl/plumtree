package sdk

import (
	"crypto/sha256"
	"reflect"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestKVNativeRoundTrip(t *testing.T) {
	const key = "greeting-test"
	t.Cleanup(func() { _ = KVDelete(key) })

	if _, ok, err := KVGet(key); ok || err != nil {
		t.Fatalf("Get before set: ok=%v err=%v", ok, err)
	}
	if err := KVSet(key, []byte("hello")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := KVGet(key)
	if err != nil || !ok || string(v) != "hello" {
		t.Fatalf("Get = %q ok=%v err=%v", v, ok, err)
	}
	// Returned slice is a copy.
	v[0] = 'X'
	if again, _, _ := KVGet(key); string(again) != "hello" {
		t.Fatalf("store mutated through returned slice: %q", again)
	}
	if err := KVDelete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := KVGet(key); ok {
		t.Fatal("key present after delete")
	}
}

func TestKVNativeSizeLimits(t *testing.T) {
	if err := KVSet("", []byte("v")); err != ErrKVTooLarge {
		t.Errorf("empty key err = %v, want ErrKVTooLarge", err)
	}
	if err := KVSet(strings.Repeat("k", abi.KVMaxKey+1), []byte("v")); err != ErrKVTooLarge {
		t.Errorf("oversize key err = %v, want ErrKVTooLarge", err)
	}
	big := make([]byte, abi.KVMaxValue+1)
	if err := KVSet("big", big); err != ErrKVTooLarge {
		t.Errorf("oversize value err = %v, want ErrKVTooLarge", err)
	}
}

func TestKVNativeListAndCompareAndSwap(t *testing.T) {
	for _, key := range []string{"native-list/b", "native-list/a", "native-list/c"} {
		_ = KVDelete(key)
		t.Cleanup(func() { _ = KVDelete(key) })
	}
	var absent [sha256.Size]byte
	if err := KVCompareAndSwap("native-list/b", absent, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := KVCompareAndSwap("native-list/b", absent, []byte("stale")); err != ErrKVConflict {
		t.Fatalf("stale err = %v", err)
	}
	if err := KVCompareAndSwap("native-list/b", KVHash([]byte("one")), []byte("two")); err != nil {
		t.Fatal(err)
	}
	_ = KVSet("native-list/a", []byte("a"))
	_ = KVSet("native-list/c", []byte("c"))
	keys, err := KVList("native-list/", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"native-list/a", "native-list/b"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("keys = %#v, want %#v", keys, want)
	}
}
