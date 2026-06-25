//go:build !wasip1

package sdk

import (
	"sync"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// Native KV is an in-process, in-memory store so `go run .` and tests behave
// like the hosted store. It is process-local: it does not persist or share
// across processes (that is the hosted runner's job). Size limits mirror the
// host so app behavior matches; quota is not enforced in dev.
var (
	kvMu    sync.RWMutex
	kvStore = map[string][]byte{}
)

func kvGet(key string) ([]byte, bool, error) {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return nil, false, ErrKVTooLarge
	}
	kvMu.RLock()
	defer kvMu.RUnlock()
	v, ok := kvStore[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func kvSet(key string, value []byte) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey || len(value) > abi.KVMaxValue {
		return ErrKVTooLarge
	}
	kvMu.Lock()
	defer kvMu.Unlock()
	kvStore[key] = append([]byte(nil), value...)
	return nil
}

func kvDelete(key string) error {
	if len(key) == 0 || len(key) > abi.KVMaxKey {
		return ErrKVTooLarge
	}
	kvMu.Lock()
	defer kvMu.Unlock()
	delete(kvStore, key)
	return nil
}
