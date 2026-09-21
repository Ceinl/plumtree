package kv

import (
	"context"
	"crypto/sha256"
	"sort"
	"strings"
	"sync"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// Adapter is the context-scoped storage boundary used by deterministic tests
// and future host integrations. Production callers normally use the selected
// native or hosted adapter behind the operation constructors.
type Adapter interface {
	Get(string) ([]byte, bool, error)
	Set(string, []byte) error
	Delete(string) error
	List(string, int) ([]string, error)
	CompareAndSwap(string, [sha256.Size]byte, []byte) error
}

type adapterContextKey struct{}

// WithAdapter scopes KV operations to adapter for this context only.
func WithAdapter(ctx context.Context, adapter Adapter) context.Context {
	return context.WithValue(ctx, adapterContextKey{}, adapter)
}

func adapterFor(ctx context.Context) Adapter {
	if ctx == nil {
		return nil
	}
	adapter, _ := ctx.Value(adapterContextKey{}).(Adapter)
	return adapter
}

// Memory is an isolated, concurrency-safe KV fixture for plumtest.
type Memory struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewMemory(initial map[string][]byte) *Memory {
	memory := &Memory{data: make(map[string][]byte, len(initial))}
	for key, value := range initial {
		memory.data[key] = append([]byte(nil), value...)
	}
	return memory
}

func (memory *Memory) Get(key string) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	value, found := memory.data[key]
	return append([]byte(nil), value...), found, nil
}

func (memory *Memory) Set(key string, value []byte) error {
	if err := validateKeyValue(key, value); err != nil {
		return err
	}
	memory.mu.Lock()
	memory.data[key] = append([]byte(nil), value...)
	memory.mu.Unlock()
	return nil
}

func (memory *Memory) Delete(key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	memory.mu.Lock()
	delete(memory.data, key)
	memory.mu.Unlock()
	return nil
}

func (memory *Memory) List(prefix string, limit int) ([]string, error) {
	if len(prefix) > abi.KVMaxKey || limit < 1 || limit > abi.KVMaxList {
		return nil, ErrTooLarge
	}
	memory.mu.RLock()
	defer memory.mu.RUnlock()
	keys := make([]string, 0, limit)
	for key := range memory.data {
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

func (memory *Memory) CompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	if err := validateKeyValue(key, value); err != nil {
		return err
	}
	memory.mu.Lock()
	defer memory.mu.Unlock()
	current, found := memory.data[key]
	var actual [sha256.Size]byte
	if found {
		actual = sha256.Sum256(current)
	}
	if actual != expected {
		return ErrConflict
	}
	memory.data[key] = append([]byte(nil), value...)
	return nil
}

// Value reads a copied fixture value for test assertions.
func (memory *Memory) Value(key string) []byte {
	value, _, _ := memory.Get(key)
	return value
}
