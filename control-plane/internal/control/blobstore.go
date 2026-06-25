package control

import (
	"os"
	"path/filepath"
)

// BlobStore holds artifact bytes (compiled WASM). It is separated from the
// metadata store so large binaries need not live inside the JSON state file: a
// durable, filesystem-backed store keeps them on disk, while the default
// in-memory store embeds them in the snapshot for the all-in-one dev process.
type BlobStore interface {
	Put(id string, data []byte) error
	Get(id string) ([]byte, bool)
	Delete(id string)
	// snapshot returns the blobs to embed in the JSON state file. A durable
	// store returns nil — its bytes persist on disk independently of the
	// metadata snapshot.
	snapshot() map[string][]byte
}

// memBlobStore keeps artifact bytes in memory and persists them inside the JSON
// snapshot. It is the default, preserving the original single-file behavior.
type memBlobStore struct{ m map[string][]byte }

func newMemBlobStore() *memBlobStore { return &memBlobStore{m: make(map[string][]byte)} }

func (s *memBlobStore) Put(id string, data []byte) error {
	s.m[id] = cloneBytes(data)
	return nil
}

func (s *memBlobStore) Get(id string) ([]byte, bool) {
	b, ok := s.m[id]
	if !ok {
		return nil, false
	}
	return cloneBytes(b), true
}

func (s *memBlobStore) Delete(id string) { delete(s.m, id) }

func (s *memBlobStore) snapshot() map[string][]byte {
	out := make(map[string][]byte, len(s.m))
	for id, b := range s.m {
		out[id] = cloneBytes(b)
	}
	return out
}

// fsBlobStore stores each artifact as a file under dir, so compiled WASM lives
// on disk separately from the metadata state — durable artifact storage. Its
// snapshot is empty: the files are the source of truth. The directory is created
// lazily on first Put, so constructing the store cannot fail.
type fsBlobStore struct{ dir string }

func (s *fsBlobStore) path(id string) string { return filepath.Join(s.dir, id+".wasm") }

func (s *fsBlobStore) Put(id string, data []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := s.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(id))
}

func (s *fsBlobStore) Get(id string) ([]byte, bool) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, false
	}
	return b, true
}

func (s *fsBlobStore) Delete(id string) { _ = os.Remove(s.path(id)) }

func (s *fsBlobStore) snapshot() map[string][]byte { return nil }
