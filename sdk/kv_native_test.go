package sdk

import (
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
