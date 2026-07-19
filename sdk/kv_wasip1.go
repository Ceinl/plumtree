//go:build wasip1

package sdk

import (
	"crypto/sha256"
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// KV host imports. They follow the recv/present ptr/len convention: the guest
// passes pointers into its own linear memory and the host reads/writes them.

//go:wasmimport plumtree kv_get
func hostKVGet(keyPtr, keyLen, outPtr, outCap int32) int32

//go:wasmimport plumtree kv_set
func hostKVSet(keyPtr, keyLen, valPtr, valLen int32) int32

//go:wasmimport plumtree kv_delete
func hostKVDelete(keyPtr, keyLen int32) int32

//go:wasmimport plumtree kv_list
func hostKVList(prefixPtr, prefixLen, limit, outPtr, outCap int32) int32

//go:wasmimport plumtree kv_compare_and_swap
func hostKVCompareAndSwap(keyPtr, keyLen, expectedPtr, valPtr, valLen int32) int32

func bytePtr(b []byte) int32 {
	if len(b) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&b[0])))
}

func kvGet(key string) ([]byte, bool, error) {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return nil, false, ErrKVTooLarge
	}
	k := []byte(key)
	buf := make([]byte, 256) // first guess; grown on demand
	for {
		n := hostKVGet(bytePtr(k), int32(len(k)), bytePtr(buf), int32(len(buf)))
		runtime.KeepAlive(k)
		switch {
		case n == abi.KVErrNotFound:
			return nil, false, nil
		case n < 0:
			return nil, false, kvErr(n)
		case int(n) <= len(buf):
			out := make([]byte, n)
			copy(out, buf[:n])
			runtime.KeepAlive(buf)
			return out, true, nil
		default:
			// Value did not fit; the host returned the needed length. Grow and
			// retry — the host wrote nothing, so no data was lost.
			buf = make([]byte, n)
		}
	}
}

func kvSet(key string, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrKVTooLarge
	}
	k := []byte(key)
	r := hostKVSet(bytePtr(k), int32(len(k)), bytePtr(value), int32(len(value)))
	runtime.KeepAlive(k)
	runtime.KeepAlive(value)
	return kvErr(r)
}

func kvDelete(key string) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return ErrKVTooLarge
	}
	k := []byte(key)
	r := hostKVDelete(bytePtr(k), int32(len(k)))
	runtime.KeepAlive(k)
	return kvErr(r)
}

func kvList(prefix string, limit int) ([]string, error) {
	if len(prefix) > abi.KVMaxKey || limit < 1 || limit > abi.KVMaxList {
		return nil, ErrKVTooLarge
	}
	p := []byte(prefix)
	buf := make([]byte, 1024)
	for {
		n := hostKVList(bytePtr(p), int32(len(p)), int32(limit), bytePtr(buf), int32(len(buf)))
		runtime.KeepAlive(p)
		switch {
		case n < 0:
			return nil, kvErr(n)
		case int(n) <= len(buf):
			keys, err := abi.DecodeKVList(buf[:n])
			runtime.KeepAlive(buf)
			if err != nil {
				return nil, ErrKVUnavailable
			}
			return keys, nil
		default:
			buf = make([]byte, n)
		}
	}
}

func kvCompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrKVTooLarge
	}
	k := []byte(key)
	r := hostKVCompareAndSwap(bytePtr(k), int32(len(k)), bytePtr(expected[:]), bytePtr(value), int32(len(value)))
	runtime.KeepAlive(k)
	runtime.KeepAlive(expected)
	runtime.KeepAlive(value)
	return kvErr(r)
}

// kvErr maps a host result code to an SDK error. KVOk and KVErrNotFound map to
// nil (callers handle not-found via the ok return of kvGet).
func kvErr(code int32) error {
	switch code {
	case abi.KVOk, abi.KVErrNotFound:
		return nil
	case abi.KVErrTooLarge:
		return ErrKVTooLarge
	case abi.KVErrQuota:
		return ErrKVQuota
	case abi.KVErrConflict:
		return ErrKVConflict
	default:
		return ErrKVUnavailable
	}
}
