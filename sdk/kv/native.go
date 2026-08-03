//go:build !wasip1

package kv

import (
	"crypto/sha256"
	"sort"
	"strings"
	"sync"

	"github.com/Ceinl/plumtree/sdk/abi"
)

var (
	nativeMu    sync.RWMutex
	nativeStore = map[string][]byte{}
)

func kvGet(key string) ([]byte, bool, error) {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return nil, false, ErrTooLarge
	}
	nativeMu.RLock()
	defer nativeMu.RUnlock()
	value, found := nativeStore[key]
	return append([]byte(nil), value...), found, nil
}

func kvSet(key string, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrTooLarge
	}
	nativeMu.Lock()
	nativeStore[key] = append([]byte(nil), value...)
	nativeMu.Unlock()
	return nil
}

func kvDelete(key string) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return ErrTooLarge
	}
	nativeMu.Lock()
	delete(nativeStore, key)
	nativeMu.Unlock()
	return nil
}

func kvList(prefix string, limit int) ([]string, error) {
	if len(prefix) > abi.KVMaxKey || limit < 1 || limit > abi.KVMaxList {
		return nil, ErrTooLarge
	}
	nativeMu.RLock()
	defer nativeMu.RUnlock()
	keys := make([]string, 0, min(limit, len(nativeStore)))
	for key := range nativeStore {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

func kvCompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrTooLarge
	}
	nativeMu.Lock()
	defer nativeMu.Unlock()
	current, found := nativeStore[key]
	var actual [sha256.Size]byte
	if found {
		actual = sha256.Sum256(current)
	}
	if actual != expected {
		return ErrConflict
	}
	nativeStore[key] = append([]byte(nil), value...)
	return nil
}
