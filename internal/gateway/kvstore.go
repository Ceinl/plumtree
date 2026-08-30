package gateway

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/Ceinl/plumtree/internal/runner"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

// KVSource is implemented by backends that expose durable per-app guest KV
// backed by the encrypted repository, replacing the legacy per-app JSON files.
type KVSource interface {
	// KVStore returns the shared runner.Store for one app's sessions.
	KVStore(appID string) (runner.Store, error)
}

var _ runner.Store = (*sqliteKVStore)(nil)

// sqliteKVStore adapts the repository's KV tables to the runner.Store
// contract for a single app. All state lives in the repository, so sessions
// of the same app share data across processes and inherit SQLCipher at-rest
// encryption from the storage engine itself.
type sqliteKVStore struct {
	repo  *sqlite.Repository
	appID string
}

func (s *sqliteKVStore) Get(key string) ([]byte, bool, error) {
	return s.repo.KVGet(context.Background(), s.appID, key)
}

func (s *sqliteKVStore) Set(key string, value []byte) error {
	err := s.repo.KVSet(context.Background(), s.appID, key, value)
	if errors.Is(err, sqlite.ErrQuota) {
		return runner.ErrQuota
	}
	return err
}

func (s *sqliteKVStore) Delete(key string) error {
	return s.repo.KVDelete(context.Background(), s.appID, key)
}

func (s *sqliteKVStore) List(prefix string, limit int) ([]string, error) {
	keys, err := s.repo.KVList(context.Background(), s.appID, prefix, limit)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), keys...), nil
}

func (s *sqliteKVStore) CompareAndSwap(key string, expected [sha256.Size]byte, value []byte) error {
	err := s.repo.KVCompareAndSwap(context.Background(), s.appID, key, expected, value)
	switch {
	case errors.Is(err, sqlite.ErrQuota):
		return runner.ErrQuota
	case errors.Is(err, sqlite.ErrConflict):
		return runner.ErrConflict
	default:
		return err
	}
}

// KVStore returns the repository-backed store for one app.
func (b *SQLiteBackend) KVStore(appID string) (runner.Store, error) {
	if b.Repository == nil {
		return nil, errors.New("gateway: backend has no repository")
	}
	return &sqliteKVStore{repo: b.Repository, appID: appID}, nil
}
