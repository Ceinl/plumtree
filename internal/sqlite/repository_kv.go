package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
)

// Guest KV limits mirror the per-app budgets the previous JSON file store
// enforced (runner.DefaultMaxKeys/DefaultMaxBytes): 0 means unlimited.
const (
	DefaultKVMaxKeys  = 1000
	DefaultKVMaxBytes = 4 * 1024 * 1024 // 4 MiB of key+value bytes per app

	// kvMaxKeyLen matches the guest ABI's key cap so a direct caller cannot
	// smuggle in keys the hosted capability would have rejected.
	kvMaxKeyLen = 256
)

// WithKVLimits overrides the per-app KV budget. Non-positive values are
// treated as unlimited, matching MemStore semantics.
func WithKVLimits(maxKeys, maxBytes int) RepositoryOption {
	return func(r *Repository) {
		r.kvMaxKeys = maxKeys
		r.kvMaxBytes = maxBytes
	}
}

func validateKVKey(key string) error {
	if key == "" || len(key) > kvMaxKeyLen {
		return fmt.Errorf("%w: kv key", ErrInvalid)
	}
	return nil
}

// KVGet returns the value for one of an app's KV entries. found is false when
// no entry exists under key.
func (r *Repository) KVGet(ctx context.Context, appID, key string) ([]byte, bool, error) {
	if err := validateID(appID); err != nil {
		return nil, false, err
	}
	if err := validateKVKey(key); err != nil {
		return nil, false, err
	}
	var value []byte
	err := r.db.QueryRowContext(ctx, `SELECT value FROM kv_entries WHERE app_id=? AND key=?`, appID, []byte(key)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, storageError(err)
	}
	return append([]byte(nil), value...), true, nil
}

// KVSet stores value under key for one app, replacing any existing entry and
// advancing its revision. It returns ErrQuota when the write would exceed the
// app's aggregate key-count or byte budget.
func (r *Repository) KVSet(ctx context.Context, appID, key string, value []byte) error {
	expected := (*[sha256.Size]byte)(nil)
	return r.kvWrite(ctx, appID, key, expected, value)
}

// KVCompareAndSwap atomically stores value when the SHA-256 hash of the
// current value equals expected. The zero hash requires the key to be absent.
// It returns ErrConflict on a stale expectation and ErrQuota when the write
// would exceed the app's budget.
func (r *Repository) KVCompareAndSwap(ctx context.Context, appID, key string, expected [sha256.Size]byte, value []byte) error {
	return r.kvWrite(ctx, appID, key, &expected, value)
}

func (r *Repository) kvWrite(ctx context.Context, appID, key string, expected *[sha256.Size]byte, value []byte) error {
	if err := validateID(appID); err != nil {
		return err
	}
	if err := validateKVKey(key); err != nil {
		return err
	}
	if value == nil {
		value = []byte{}
	}
	now := r.now().UnixNano()
	return r.mutate(ctx, "kv-write", CommitEvent{Operation: "kv-write", Kind: "app", ID: appID}, func(m *MutationTx) error {
		if err := appExists(ctx, m, appID); err != nil {
			return err
		}
		var previous []byte
		var revision int64 = 1
		row, _ := m.QueryRowContext(ctx, `SELECT value,revision FROM kv_entries WHERE app_id=? AND key=?`, appID, []byte(key))
		scanErr := row.Scan(&previous, &revision)
		existed := scanErr == nil
		if !existed && !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		}
		if existed {
			revision++
		}
		if expected != nil {
			var actual [sha256.Size]byte
			if existed {
				actual = sha256.Sum256(previous)
			}
			if actual != *expected {
				return ErrConflict
			}
		}
		var usedKeys, usedBytes int
		usageRow, _ := m.QueryRowContext(ctx, `SELECT keys,bytes FROM kv_usage WHERE app_id=?`, appID)
		usageScanErr := usageRow.Scan(&usedKeys, &usedBytes)
		if usageScanErr != nil && !errors.Is(usageScanErr, sql.ErrNoRows) {
			return usageScanErr
		}
		newBytes := usedBytes + len(value) - len(previous)
		newKeys := usedKeys
		if !existed {
			newBytes += len(key)
			newKeys++
			if r.kvMaxKeys > 0 && newKeys > r.kvMaxKeys {
				return ErrQuota
			}
		}
		if r.kvMaxBytes > 0 && newBytes > r.kvMaxBytes {
			return ErrQuota
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO kv_entries(app_id,key,value,revision,updated_at_ns) VALUES(?,?,?,?,?)
ON CONFLICT(app_id,key) DO UPDATE SET value=excluded.value,revision=excluded.revision,updated_at_ns=excluded.updated_at_ns`,
			appID, []byte(key), value, revision, now); err != nil {
			return err
		}
		_, err := m.ExecContext(ctx, `INSERT INTO kv_usage(app_id,keys,bytes) VALUES(?,?,?)
ON CONFLICT(app_id) DO UPDATE SET keys=excluded.keys,bytes=excluded.bytes`, appID, newKeys, newBytes)
		return err
	})
}

// KVDelete removes one entry. Deleting a missing key is not an error.
func (r *Repository) KVDelete(ctx context.Context, appID, key string) error {
	if err := validateID(appID); err != nil {
		return err
	}
	if err := validateKVKey(key); err != nil {
		return err
	}
	return r.mutate(ctx, "kv-delete", CommitEvent{Operation: "kv-delete", Kind: "app", ID: appID}, func(m *MutationTx) error {
		var value []byte
		row, _ := m.QueryRowContext(ctx, `SELECT value FROM kv_entries WHERE app_id=? AND key=?`, appID, []byte(key))
		scanErr := row.Scan(&value)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		if _, err := m.ExecContext(ctx, `DELETE FROM kv_entries WHERE app_id=? AND key=?`, appID, []byte(key)); err != nil {
			return err
		}
		_, err := m.ExecContext(ctx, `UPDATE kv_usage SET keys=keys-1,bytes=bytes-?
WHERE app_id=? AND keys>0 AND bytes>=?`, len(key)+len(value), appID, len(key)+len(value))
		return err
	})
}

// KVList returns at most limit matching keys in lexicographic order, mirroring
// the Store contract: limit must fall within 1..256 and an empty prefix lists
// the whole namespace.
func (r *Repository) KVList(ctx context.Context, appID, prefix string, limit int) ([]string, error) {
	if err := validateID(appID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 256 || len(prefix) > kvMaxKeyLen {
		return nil, fmt.Errorf("%w: kv list", ErrInvalid)
	}
	query := `SELECT key FROM kv_entries WHERE app_id=? AND substr(key,1,length(?))=? ORDER BY key LIMIT ?`
	rows, err := r.db.QueryContext(ctx, query, appID, []byte(prefix), []byte(prefix), limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, storageError(err)
		}
		out = append(out, key)
	}
	return out, storageError(rows.Err())
}

// KVUsage reports an app's current aggregate KV accounting, for operator
// diagnostics and quota tests.
func (r *Repository) KVUsage(ctx context.Context, appID string) (keys, bytes int, err error) {
	if err := validateID(appID); err != nil {
		return 0, 0, err
	}
	err = r.db.QueryRowContext(ctx, `SELECT keys,bytes FROM kv_usage WHERE app_id=?`, appID).Scan(&keys, &bytes)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, storageError(err)
	}
	return keys, bytes, nil
}
