package sdk

import (
	"crypto/sha256"
	"errors"
)

// Scoped key/value storage — the first host capability. An app reads and writes
// a private namespace that the platform persists and shares across that app's
// sessions, so state survives reconnects and is visible to every user of the
// same app. Keys and values are bytes; size and quota limits are enforced by the
// host.
//
// The same API works in `go run .` (backed by an in-process store) and in the
// hosted WASM sandbox (backed by the host's KV capability), so app code is
// identical in both.

var (
	// ErrKVUnavailable means the running context provides no KV capability (or
	// the host reported an internal failure).
	ErrKVUnavailable = errors.New("sdk: kv capability unavailable")
	// ErrKVTooLarge means the key or value exceeds the host size limit.
	ErrKVTooLarge = errors.New("sdk: kv key or value too large")
	// ErrKVQuota means the write would exceed the app's storage quota.
	ErrKVQuota = errors.New("sdk: kv storage quota exceeded")
	// ErrKVConflict means a compare-and-swap observed a different value (or
	// key presence) than expected. No write was performed.
	ErrKVConflict = errors.New("sdk: kv compare-and-swap conflict")
)

// KVGet returns the value stored under key. ok is false when the key is absent.
func KVGet(key string) (value []byte, ok bool, err error) { return kvGet(key) }

// KVSet stores value under key, replacing any existing value.
func KVSet(key string, value []byte) error { return kvSet(key, value) }

// KVDelete removes key. Deleting a missing key is not an error.
func KVDelete(key string) error { return kvDelete(key) }

// KVList returns up to limit keys beginning with prefix in lexicographic order.
// limit must be between 1 and abi.KVMaxList. An empty prefix lists the namespace.
func KVList(prefix string, limit int) ([]string, error) { return kvList(prefix, limit) }

// KVHash returns the SHA-256 revision used by KVCompareAndSwap.
func KVHash(value []byte) [sha256.Size]byte { return sha256.Sum256(value) }

// KVCompareAndSwap atomically stores value when the current value hashes to
// expected. The all-zero hash means the key must be absent (create-if-absent).
func KVCompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	return kvCompareAndSwap(key, expected, value)
}
