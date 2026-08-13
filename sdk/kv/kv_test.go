package kv

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	legacy "github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
)

func TestUnavailableErrorMapping(t *testing.T) {
	if !errors.Is(normalize(legacy.ErrKVUnavailable), ErrUnavailable) {
		t.Fatal("unavailable host error was not mapped to the package contract")
	}
}

func TestOperationsAreTypedAndCopyValues(t *testing.T) {
	key := "typed-kv-test"
	_ = Delete(key).Run(context.Background())
	t.Cleanup(func() { _ = Delete(key).Run(context.Background()) })

	value := []byte("hello")
	if result := Set(key, value).Run(context.Background()); result.Err != nil {
		t.Fatal(result.Err)
	}
	value[0] = 'X'
	result := Get(key).Run(context.Background())
	if result.Err != nil || !result.Found || string(result.Value) != "hello" {
		t.Fatalf("get = %#v, want copied hello", result)
	}
	result.Value[0] = 'Y'
	if again := Get(key).Run(context.Background()); string(again.Value) != "hello" {
		t.Fatalf("result mutated stored value: %q", again.Value)
	}
}

func TestValidationAndCancellation(t *testing.T) {
	if result := Get("").Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("empty key err = %v", result.Err)
	}
	if result := Get(strings.Repeat("k", 257)).Run(context.Background()); !errors.Is(result.Err, ErrTooLarge) {
		t.Fatalf("large key err = %v", result.Err)
	}
	if result := List("", 0).Run(context.Background()); !errors.Is(result.Err, ErrInvalid) {
		t.Fatalf("zero list limit err = %v", result.Err)
	}
	if result := List("", abi.KVMaxList+1).Run(context.Background()); !errors.Is(result.Err, ErrTooLarge) {
		t.Fatalf("large list limit err = %v", result.Err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := Get("cancelled").Run(ctx); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("cancelled err = %v", result.Err)
	}
}

func TestCompareAndSwapContract(t *testing.T) {
	key := "typed-kv-cas"
	_ = Delete(key).Run(context.Background())
	t.Cleanup(func() { _ = Delete(key).Run(context.Background()) })
	var absent [sha256.Size]byte
	if result := CompareAndSwap(key, absent, []byte("one")).Run(context.Background()); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := CompareAndSwap(key, absent, []byte("two")).Run(context.Background()); !errors.Is(result.Err, ErrConflict) {
		t.Fatalf("stale CAS err = %v", result.Err)
	}
}
