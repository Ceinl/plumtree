package abi

import (
	"encoding/binary"
	"errors"
)

// KV host functions provide per-app scoped key/value storage. They follow the
// same ptr/len convention as recv/present: the guest passes pointers into its
// own linear memory and the host reads/writes raw key/value bytes.

const (
	// KVMaxKey caps a key's length in bytes.
	KVMaxKey = 256
	// KVMaxValue caps a value's length in bytes.
	KVMaxValue = 64 * 1024
	// KVMaxList caps one prefix-list operation. Listing is deliberately bounded
	// so a guest cannot force an unbounded host allocation or response frame.
	KVMaxList = 256
	// KVHashSize is the SHA-256 digest size used by compare-and-swap.
	KVHashSize = 32
)

// KV result codes. Non-negative kv_get returns are value lengths, not codes;
// only negative returns are errors.
const (
	KVOk          int32 = 0
	KVErrNotFound int32 = -1 // kv_get: no entry for the key
	KVErrTooLarge int32 = -2 // key or value exceeds its cap
	KVErrQuota    int32 = -3 // per-app storage quota would be exceeded
	KVErrInternal int32 = -4 // host-side failure (store I/O, bad memory access)
	KVErrConflict int32 = -5 // compare-and-swap expected value did not match
)

// EncodeKVList serializes ordered keys as repeated [u16 length][key].
func EncodeKVList(keys []string) []byte {
	var out []byte
	for _, key := range keys {
		out = binary.LittleEndian.AppendUint16(out, uint16(len(key)))
		out = append(out, key...)
	}
	return out
}

// DecodeKVList decodes the bounded list payload returned by kv_list.
func DecodeKVList(raw []byte) ([]string, error) {
	keys := make([]string, 0)
	for len(raw) > 0 {
		if len(raw) < 2 {
			return nil, errors.New("abi: invalid kv list")
		}
		n := int(binary.LittleEndian.Uint16(raw[:2]))
		raw = raw[2:]
		if n == 0 || n > KVMaxKey || len(raw) < n || len(keys) >= KVMaxList {
			return nil, errors.New("abi: invalid kv list")
		}
		keys = append(keys, string(raw[:n]))
		raw = raw[n:]
	}
	return keys, nil
}
