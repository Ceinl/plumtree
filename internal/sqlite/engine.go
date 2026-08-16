// Package sqlite owns Plumtree's root-internal SQLCipher boundary.
//
// The package intentionally does not select a repository or know the control
// schema. Later storage work can depend on this boundary without exposing a
// database driver through the SDK or command protocols.
package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	KeySize            = 32
	DefaultBusyTimeout = 5 * time.Second
	backupPages        = 128
)

var nextDriverID atomic.Uint64

// Config controls one database handle. Key is a raw 32-byte key; it is copied
// when the handle is opened and is never included in the DSN.
type Config struct {
	Path         string
	Key          []byte
	BusyTimeout  time.Duration
	MaxOpenConns int
	MaxIdleConns int
}

// String is intentionally non-diagnostic so configs can be included in
// structured logs without exposing a raw key or a caller-supplied DSN.
func (Config) String() string { return "sqlite.Config{redacted}" }

// DB is a configured database pool. The embedded database handle is useful to
// the future repository while keeping the driver and its keying policy
// internal to the root module.
type DB struct {
	*sql.DB
	path          string
	encrypted     bool
	key           []byte
	driverName    string
	busyTimeoutMS int
}

// String keeps the key and driver details out of fmt/log output.
func (*DB) String() string { return "sqlite.DB{redacted}" }

// EngineInfo is a non-secret identity and capability description of the opened
// engine. It intentionally contains no path, DSN, or key data.
type EngineInfo struct {
	CipherVersion  string
	CompileOptions []string
	JournalMode    string
	Encrypted      bool
}

// Open opens and eagerly validates a database. A non-empty key always requires
// the SQLCipher build; ordinary SQLite can therefore never accidentally be
// used for production state.
func Open(path string, key []byte) (*DB, error) {
	return OpenWithConfig(Config{Path: path, Key: key})
}

// OpenWithConfig creates a private driver for this key policy, opens the pool,
// and pings it so key and tamper failures happen before the handle is returned.
func OpenWithConfig(cfg Config) (*DB, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, errors.New("sqlite: database path is required")
	}
	if strings.HasPrefix(cfg.Path, "file:") && cfg.Path != ":memory:" {
		return nil, errors.New("sqlite: database path must not be a DSN")
	}

	key := append([]byte(nil), cfg.Key...)
	encrypted := len(key) != 0
	if encrypted && len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	if encrypted && !compiledSQLCipher {
		return nil, ErrSQLCipherUnavailable
	}
	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = DefaultBusyTimeout
	}

	id := nextDriverID.Add(1)
	driverName := fmt.Sprintf("plumtree-sqlite-%d", id)
	timeoutMS := int(cfg.BusyTimeout / time.Millisecond)
	if timeoutMS < 1 {
		timeoutMS = 1
	}

	driver := newSQLiteDriver(key, encrypted, timeoutMS)
	sql.Register(driverName, driver)

	db, err := sql.Open(driverName, sqliteDSN(cfg.Path))
	if err != nil {
		zero(key)
		return nil, redactError(err)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}

	result := &DB{
		DB:            db,
		path:          cfg.Path,
		encrypted:     encrypted,
		key:           key,
		driverName:    driverName,
		busyTimeoutMS: timeoutMS,
	}
	if err := result.PingContext(context.Background()); err != nil {
		_ = db.Close()
		result.closeKey()
		if encrypted {
			return nil, ErrKeyRejected
		}
		return nil, redactError(err)
	}
	return result, nil
}

func sqliteDSN(path string) string {
	if path == ":memory:" {
		// A unique shared-memory URI lets pooled connections see one database
		// while avoiding user-controlled DSN parameters.
		id := nextDriverID.Add(1)
		return fmt.Sprintf("file:plumtree-memory-%d?mode=memory&cache=shared&_txlock=immediate", id)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: abs, RawQuery: "_txlock=immediate"}).String()
}

// Info returns the engine identity and connection policy without exposing
// secrets. Callers can use this at release qualification time.
func (db *DB) Info(ctx context.Context) (EngineInfo, error) {
	if db == nil || db.DB == nil {
		return EngineInfo{}, errors.New("sqlite: nil database")
	}
	var info EngineInfo
	err := db.QueryRowContext(ctx, "PRAGMA cipher_version").Scan(&info.CipherVersion)
	if err != nil {
		if db.encrypted || compiledSQLCipher {
			return EngineInfo{}, ErrSQLCipherUnavailable
		}
		info.CipherVersion = "unavailable (ordinary SQLite)"
	}
	rows, err := db.QueryContext(ctx, "PRAGMA compile_options")
	if err != nil {
		return EngineInfo{}, redactError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var option string
		if err := rows.Scan(&option); err != nil {
			return EngineInfo{}, redactError(err)
		}
		info.CompileOptions = append(info.CompileOptions, option)
	}
	if err := rows.Err(); err != nil {
		return EngineInfo{}, redactError(err)
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&info.JournalMode); err != nil {
		return EngineInfo{}, redactError(err)
	}
	info.Encrypted = db.encrypted
	return info, nil
}

// Backup performs SQLite's online backup between two already validated pools.
// Source and destination must use matching plaintext/encrypted modes; mode
// conversion belongs to sqlcipher_export and is intentionally not implicit.
func Backup(ctx context.Context, destination, source *DB) error {
	if destination == nil || source == nil {
		return errors.New("sqlite: backup requires source and destination")
	}
	if destination.encrypted != source.encrypted {
		return errors.New("sqlite: online backup requires matching encryption modes")
	}
	if err := Verify(ctx, destination); err != nil {
		return err
	}

	dstConn, err := destination.Conn(ctx)
	if err != nil {
		return redactError(err)
	}
	defer dstConn.Close()
	srcConn, err := source.Conn(ctx)
	if err != nil {
		return redactError(err)
	}
	defer srcConn.Close()

	err = backupConnections(ctx, dstConn, srcConn)
	if err != nil {
		return redactError(err)
	}
	dstConn.Close()
	srcConn.Close()
	return Verify(ctx, destination)
}

// Verify checks integrity without returning database contents.
func Verify(ctx context.Context, db *DB) error {
	if db == nil || db.DB == nil {
		return errors.New("sqlite: nil database")
	}
	for _, check := range []string{"integrity_check", "cipher_integrity_check"} {
		var result string
		if err := db.QueryRowContext(ctx, "PRAGMA "+check).Scan(&result); err != nil {
			// A newly created SQLCipher file has no page to inspect until its
			// first schema write. The key/schema validation already happened in
			// the connection hook, so an empty integrity result is acceptable
			// before an online backup initializes the destination.
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if check == "cipher_integrity_check" && !db.encrypted && !compiledSQLCipher {
				continue
			}
			return redactError(err)
		}
		if !strings.EqualFold(result, "ok") {
			return fmt.Errorf("sqlite: %s failed", check)
		}
	}
	return nil
}

func (db *DB) closeKey() {
	zero(db.key)
	db.key = nil
}

// Close releases the pool and clears the copied raw key.
func (db *DB) Close() error {
	if db == nil || db.DB == nil {
		return nil
	}
	err := db.DB.Close()
	db.closeKey()
	return redactError(err)
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}

func redactError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, driver.ErrBadConn) {
		return driver.ErrBadConn
	}
	return err
}
