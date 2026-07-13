package abi

// KV host functions provide per-app scoped key/value storage. They follow the
// same ptr/len convention as recv/present: the guest passes pointers into its
// own linear memory and the host reads/writes raw key/value bytes.

// GoodbyeMaxLen caps the length of a goodbye message set by the guest.
const GoodbyeMaxLen = 4096

const (
	// KVMaxKey caps a key's length in bytes.
	KVMaxKey = 256
	// KVMaxValue caps a value's length in bytes.
	KVMaxValue = 64 * 1024
)

// KV result codes. Non-negative kv_get returns are value lengths, not codes;
// only negative returns are errors.
const (
	KVOk          int32 = 0
	KVErrNotFound int32 = -1 // kv_get: no entry for the key
	KVErrTooLarge int32 = -2 // key or value exceeds its cap
	KVErrQuota    int32 = -3 // per-app storage quota would be exceeded
	KVErrInternal int32 = -4 // host-side failure (store I/O, bad memory access)
)
