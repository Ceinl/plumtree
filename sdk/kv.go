package sdk

import "errors"

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
)

// KVGet returns the value stored under key. ok is false when the key is absent.
func KVGet(key string) (value []byte, ok bool, err error) { return kvGet(key) }

// KVSet stores value under key, replacing any existing value.
func KVSet(key string, value []byte) error { return kvSet(key, value) }

// KVDelete removes key. Deleting a missing key is not an error.
func KVDelete(key string) error { return kvDelete(key) }
