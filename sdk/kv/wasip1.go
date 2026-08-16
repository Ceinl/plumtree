//go:build wasip1

package kv

import (
	"crypto/sha256"
	"runtime"
	"unsafe"

	"github.com/Ceinl/plumtree/sdk/abi"
)

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

func bytePtr(value []byte) int32 {
	if len(value) == 0 {
		return 0
	}
	return int32(uintptr(unsafe.Pointer(&value[0])))
}

func kvGet(key string) ([]byte, bool, error) {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return nil, false, ErrTooLarge
	}
	keyBytes := []byte(key)
	buffer := make([]byte, 256)
	for {
		n := hostKVGet(bytePtr(keyBytes), int32(len(keyBytes)), bytePtr(buffer), int32(len(buffer)))
		runtime.KeepAlive(keyBytes)
		switch {
		case n == abi.KVErrNotFound:
			return nil, false, nil
		case n < 0:
			return nil, false, kvError(n)
		case int(n) <= len(buffer):
			return append([]byte(nil), buffer[:n]...), true, nil
		default:
			buffer = make([]byte, n)
		}
	}
}

func kvSet(key string, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrTooLarge
	}
	keyBytes := []byte(key)
	result := hostKVSet(bytePtr(keyBytes), int32(len(keyBytes)), bytePtr(value), int32(len(value)))
	runtime.KeepAlive(keyBytes)
	runtime.KeepAlive(value)
	return kvError(result)
}

func kvDelete(key string) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return ErrTooLarge
	}
	keyBytes := []byte(key)
	result := hostKVDelete(bytePtr(keyBytes), int32(len(keyBytes)))
	runtime.KeepAlive(keyBytes)
	return kvError(result)
}

func kvList(prefix string, limit int) ([]string, error) {
	if len(prefix) > abi.KVMaxKey || limit < 1 || limit > abi.KVMaxList {
		return nil, ErrTooLarge
	}
	prefixBytes := []byte(prefix)
	buffer := make([]byte, 1024)
	for {
		n := hostKVList(bytePtr(prefixBytes), int32(len(prefixBytes)), int32(limit), bytePtr(buffer), int32(len(buffer)))
		runtime.KeepAlive(prefixBytes)
		if n < 0 {
			return nil, kvError(n)
		}
		if int(n) <= len(buffer) {
			keys, err := abi.DecodeKVList(buffer[:n])
			if err != nil {
				return nil, ErrUnavailable
			}
			return keys, nil
		}
		buffer = make([]byte, n)
	}
}

func kvCompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrTooLarge
	}
	keyBytes := []byte(key)
	result := hostKVCompareAndSwap(bytePtr(keyBytes), int32(len(keyBytes)), bytePtr(expected[:]), bytePtr(value), int32(len(value)))
	runtime.KeepAlive(keyBytes)
	runtime.KeepAlive(expected)
	runtime.KeepAlive(value)
	return kvError(result)
}

func kvError(code int32) error {
	switch code {
	case abi.KVOk, abi.KVErrNotFound:
		return nil
	case abi.KVErrTooLarge:
		return ErrTooLarge
	case abi.KVErrQuota:
		return ErrQuota
	case abi.KVErrConflict:
		return ErrConflict
	default:
		return ErrUnavailable
	}
}
