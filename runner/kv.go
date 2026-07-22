package runner

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Store is per-app scoped key/value storage, handed to a guest as the KV
// capability. Sessions of the same app share one Store, so implementations must
// be safe for concurrent use. Keys and values are raw bytes; the host enforces
// the abi.KVMaxKey / abi.KVMaxValue size caps before reaching a Store, so a
// Store only needs to enforce its own aggregate quota.
type Store interface {
	// Get returns the value for key. found is false when no entry exists; err is
	// reserved for backing-store failures (e.g. disk I/O).
	Get(key string) (value []byte, found bool, err error)
	// Set stores value under key, replacing any existing entry. It returns
	// ErrQuota if the write would exceed the store's aggregate quota.
	Set(key string, value []byte) error
	// Delete removes key. Deleting a missing key is not an error.
	Delete(key string) error
	// List returns at most limit matching keys in lexicographic order.
	List(prefix string, limit int) ([]string, error)
	// CompareAndSwap atomically stores value when the current value hash equals
	// expected. The zero hash means the key must be absent.
	CompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error
}

// Capabilities are the host services exposed to a guest for one session. The
// zero value exposes nothing: guest calls into an absent capability fail
// cleanly (kv_* returns abi.KVErrInternal) rather than trapping.
type Capabilities struct {
	// KV is the scoped key/value store, or nil if the app has no storage.
	KV Store
	// Bus is the scoped pub/sub bus shared across the app's sessions, or nil if
	// the app has no live messaging.
	Bus Bus
	// Auth is the connected user's identity for this session, or nil if the host
	// provides no identity (auth_whoami then fails cleanly).
	Auth Auth
	// Env exposes the app's server-side secrets read-only, or nil for unclaimed
	// apps (env_get then reports the capability as unavailable).
	Env Env
	// Fetch is the gated outbound-HTTP capability, or nil for default-deny egress
	// (the common case; only claimed apps with an allowlist get one).
	Fetch Fetcher
	// Exec runs local programs as the server OS user. It is nil by default and
	// should only be installed for claimed apps on an explicitly trusted host.
	Exec Commander
	// Goodbye is an optional message set by the guest, displayed on the user's
	// terminal after the session ends (after the alt-screen is closed). The
	// host allocates Goodbye = new(string) before calling Run and reads the
	// result after Run returns.
	Goodbye *string
}

// ErrQuota reports that a Set would exceed the store's aggregate key or byte
// quota. The host maps it to abi.KVErrQuota.
var ErrQuota = errors.New("kv: store quota exceeded")

// ErrConflict reports a failed conditional write. State is unchanged.
var ErrConflict = errors.New("kv: compare-and-swap conflict")

// Default per-app KV quotas used by the dev host. 0 means unlimited.
const (
	DefaultMaxKeys  = 1000
	DefaultMaxBytes = 4 * 1024 * 1024 // 4 MiB of key+value bytes per app
)

// registerKV adds the kv_get/kv_set/kv_delete host functions to b. They are
// installed even when kv is nil so a guest whose linker kept the KV imports can
// still instantiate; calls then return abi.KVErrInternal. Size caps are checked
// here, before the Store, so a hostile guest cannot exceed them.
func registerKV(b wazero.HostModuleBuilder, kv Store) wazero.HostModuleBuilder {
	readKey := func(m api.Module, ptr, length int32) ([]byte, int32) {
		if length <= 0 || length > abi.KVMaxKey {
			return nil, abi.KVErrTooLarge
		}
		key, ok := m.Memory().Read(uint32(ptr), uint32(length))
		if !ok {
			return nil, abi.KVErrInternal
		}
		return key, abi.KVOk
	}

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, keyPtr, keyLen, valPtr, valLen int32) int32 {
			if kv == nil {
				return abi.KVErrInternal
			}
			key, code := readKey(m, keyPtr, keyLen)
			if key == nil {
				return code
			}
			if valLen < 0 || valLen > abi.KVMaxValue {
				return abi.KVErrTooLarge
			}
			raw, ok := m.Memory().Read(uint32(valPtr), uint32(valLen))
			if !ok {
				return abi.KVErrInternal
			}
			// Copy: Read may alias guest linear memory, which the Store outlives.
			val := append([]byte(nil), raw...)
			if err := kv.Set(string(key), val); err != nil {
				if errors.Is(err, ErrQuota) {
					return abi.KVErrQuota
				}
				return abi.KVErrInternal
			}
			return abi.KVOk
		}).
		Export("kv_set")

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, keyPtr, keyLen, outPtr, outCap int32) int32 {
			if kv == nil {
				return abi.KVErrInternal
			}
			key, code := readKey(m, keyPtr, keyLen)
			if key == nil {
				return code
			}
			val, found, err := kv.Get(string(key))
			if err != nil {
				return abi.KVErrInternal
			}
			if !found {
				return abi.KVErrNotFound
			}
			n := int32(len(val))
			// Too big for the guest's buffer: report the needed length and write
			// nothing, so the guest grows and retries. Never truncate.
			if n > outCap {
				return n
			}
			if n > 0 && !m.Memory().Write(uint32(outPtr), val) {
				return abi.KVErrInternal
			}
			return n
		}).
		Export("kv_get")

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, keyPtr, keyLen int32) int32 {
			if kv == nil {
				return abi.KVErrInternal
			}
			key, code := readKey(m, keyPtr, keyLen)
			if key == nil {
				return code
			}
			if err := kv.Delete(string(key)); err != nil {
				return abi.KVErrInternal
			}
			return abi.KVOk
		}).
		Export("kv_delete")

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, prefixPtr, prefixLen, limit, outPtr, outCap int32) int32 {
			if kv == nil {
				return abi.KVErrInternal
			}
			if prefixLen < 0 || prefixLen > abi.KVMaxKey || limit < 1 || limit > abi.KVMaxList || outCap < 0 {
				return abi.KVErrTooLarge
			}
			var prefix []byte
			if prefixLen > 0 {
				var ok bool
				prefix, ok = m.Memory().Read(uint32(prefixPtr), uint32(prefixLen))
				if !ok {
					return abi.KVErrInternal
				}
			}
			keys, err := kv.List(string(prefix), int(limit))
			if err != nil || len(keys) > int(limit) || len(keys) > abi.KVMaxList {
				return abi.KVErrInternal
			}
			raw := abi.EncodeKVList(keys)
			n := int32(len(raw))
			if n > outCap {
				return n
			}
			if n > 0 && !m.Memory().Write(uint32(outPtr), raw) {
				return abi.KVErrInternal
			}
			return n
		}).
		Export("kv_list")

	b = b.NewFunctionBuilder().
		WithFunc(func(_ context.Context, m api.Module, keyPtr, keyLen, expectedPtr, valPtr, valLen int32) int32 {
			if kv == nil {
				return abi.KVErrInternal
			}
			key, code := readKey(m, keyPtr, keyLen)
			if key == nil {
				return code
			}
			if valLen < 0 || valLen > abi.KVMaxValue {
				return abi.KVErrTooLarge
			}
			expectedRaw, ok := m.Memory().Read(uint32(expectedPtr), abi.KVHashSize)
			if !ok {
				return abi.KVErrInternal
			}
			valueRaw, ok := m.Memory().Read(uint32(valPtr), uint32(valLen))
			if !ok {
				return abi.KVErrInternal
			}
			var expected [sha256.Size]byte
			copy(expected[:], expectedRaw)
			err := kv.CompareAndSwap(string(key), expected, append([]byte(nil), valueRaw...))
			switch {
			case err == nil:
				return abi.KVOk
			case errors.Is(err, ErrConflict):
				return abi.KVErrConflict
			case errors.Is(err, ErrQuota):
				return abi.KVErrQuota
			default:
				return abi.KVErrInternal
			}
		}).
		Export("kv_compare_and_swap")

	return b
}

// MemStore is an in-memory Store with optional key-count and byte quotas. It is
// safe for concurrent use and is the basis for FileStore.
type MemStore struct {
	mu       sync.RWMutex
	m        map[string][]byte
	bytes    int // sum of len(key)+len(value) across entries
	maxKeys  int // 0 = unlimited
	maxBytes int // 0 = unlimited
}

// NewMemStore returns an empty in-memory store. Non-positive limits are treated
// as unlimited.
func NewMemStore(maxKeys, maxBytes int) *MemStore {
	return &MemStore{m: make(map[string][]byte), maxKeys: maxKeys, maxBytes: maxBytes}
}

func (s *MemStore) Get(key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *MemStore) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.m[key]
	newBytes := s.bytes + len(value) - len(prev)
	if !existed {
		newBytes += len(key)
		if s.maxKeys > 0 && len(s.m)+1 > s.maxKeys {
			return ErrQuota
		}
	}
	if s.maxBytes > 0 && newBytes > s.maxBytes {
		return ErrQuota
	}
	s.m[key] = value
	s.bytes = newBytes
	return nil
}

func (s *MemStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[key]; ok {
		s.bytes -= len(key) + len(v)
		delete(s.m, key)
	}
	return nil
}

func (s *MemStore) List(prefix string, limit int) ([]string, error) {
	if limit < 1 || limit > abi.KVMaxList {
		return nil, errors.New("kv: invalid list limit")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, min(limit, len(s.m)))
	for key := range s.m {
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

func (s *MemStore) CompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compareAndSwapLocked(key, expected, value)
}

func (s *MemStore) compareAndSwapLocked(key string, expected [sha256.Size]byte, value []byte) error {
	previous, existed := s.m[key]
	var actual [sha256.Size]byte
	if existed {
		actual = sha256.Sum256(previous)
	}
	if actual != expected {
		return ErrConflict
	}
	newBytes := s.bytes + len(value) - len(previous)
	if !existed {
		newBytes += len(key)
		if s.maxKeys > 0 && len(s.m)+1 > s.maxKeys {
			return ErrQuota
		}
	}
	if s.maxBytes > 0 && newBytes > s.maxBytes {
		return ErrQuota
	}
	s.m[key] = append([]byte(nil), value...)
	s.bytes = newBytes
	return nil
}

// snapshot returns a copy of the current contents, for persistence.
func (s *MemStore) snapshot() map[string][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]byte, len(s.m))
	for k, v := range s.m {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// FileStore is a MemStore that persists its contents to a JSON file after every
// mutation, so the data survives across sessions and processes — this is what
// lets two `pt dev --ssh` connections to the same app share state. Keys are
// expected to be UTF-8 (JSON object keys); values may be arbitrary bytes.
type FileStore struct {
	*MemStore
	txnMu     sync.Mutex // serializes mutations, snapshots, and rollback
	path      string
	writeFile func(string, []byte, os.FileMode) error
	rename    func(string, string) error
}

// NewFileStore opens (or creates) a file-backed store at path, loading any
// existing contents. Parent directories are created as needed.
func NewFileStore(path string, maxKeys, maxBytes int) (*FileStore, error) {
	ms := NewMemStore(maxKeys, maxBytes)
	if data, err := os.ReadFile(path); err == nil {
		var raw map[string][]byte
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		for k, v := range raw {
			ms.m[k] = v
			ms.bytes += len(k) + len(v)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return &FileStore{
		MemStore:  ms,
		path:      path,
		writeFile: os.WriteFile,
		rename:    os.Rename,
	}, nil
}

func (s *FileStore) Set(key string, value []byte) error {
	s.txnMu.Lock()
	defer s.txnMu.Unlock()

	s.MemStore.mu.Lock()
	defer s.MemStore.mu.Unlock()
	previous, existed := s.m[key]
	previousBytes := s.bytes
	newBytes := s.bytes + len(value) - len(previous)
	if !existed {
		newBytes += len(key)
		if s.maxKeys > 0 && len(s.m)+1 > s.maxKeys {
			return ErrQuota
		}
	}
	if s.maxBytes > 0 && newBytes > s.maxBytes {
		return ErrQuota
	}
	s.m[key] = value
	s.bytes = newBytes
	if err := s.persistLocked(); err != nil {
		if existed {
			s.m[key] = previous
		} else {
			delete(s.m, key)
		}
		s.bytes = previousBytes
		return err
	}
	return nil
}

func (s *FileStore) Delete(key string) error {
	s.txnMu.Lock()
	defer s.txnMu.Unlock()

	s.MemStore.mu.Lock()
	defer s.MemStore.mu.Unlock()
	previous, existed := s.m[key]
	if !existed {
		return nil
	}
	previousBytes := s.bytes
	delete(s.m, key)
	s.bytes -= len(key) + len(previous)
	if err := s.persistLocked(); err != nil {
		s.m[key] = previous
		s.bytes = previousBytes
		return err
	}
	return nil
}

func (s *FileStore) CompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	s.txnMu.Lock()
	defer s.txnMu.Unlock()
	s.MemStore.mu.Lock()
	defer s.MemStore.mu.Unlock()

	previous, existed := s.m[key]
	previousCopy := append([]byte(nil), previous...)
	previousBytes := s.bytes
	if err := s.compareAndSwapLocked(key, expected, value); err != nil {
		return err
	}
	if err := s.persistLocked(); err != nil {
		if existed {
			s.m[key] = previousCopy
		} else {
			delete(s.m, key)
		}
		s.bytes = previousBytes
		return err
	}
	return nil
}

// persistLocked writes the current in-memory snapshot atomically. Both txnMu
// and MemStore.mu must be held by the caller, making mutation and snapshot one
// transaction: a failed write can restore the exact previous entry and count.
func (s *FileStore) persistLocked() error {
	raw := make(map[string][]byte, len(s.m))
	for k, v := range s.m {
		raw[k] = append([]byte(nil), v...)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	defer os.Remove(tmp)
	if err := s.writeFile(tmp, data, 0o600); err != nil {
		return err
	}
	return s.rename(tmp, s.path)
}
