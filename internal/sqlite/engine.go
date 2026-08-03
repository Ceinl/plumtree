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
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
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

// EngineInfo is a non-secret identity and capability snapshot of the opened
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

	driver := &sqlite3.SQLiteDriver{
		BeforeConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return keyConnection(conn, key, encrypted)
		},
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return configureConnection(conn, encrypted, timeoutMS)
		},
	}
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
		return fmt.Sprintf("file:plumtree-memory-%d?mode=memory&cache=shared", id)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

func configureConnection(conn *sqlite3.SQLiteConn, encrypted bool, busyTimeoutMS int) error {
	// The key has already been applied by BeforeConnectHook. No key is placed in
	// the DSN; x'...' only exists in transient SQL text inside that hook.

	// A schema read validates a key before WAL or connection-level state is
	// changed. It also makes wrong-key and tamper failures deterministic.
	if _, err := pragmaValue(conn, "schema_version"); err != nil {
		if encrypted {
			return ErrKeyRejected
		}
		return redactError(err)
	}

	if compiledSQLCipher {
		if _, err := pragmaValue(conn, "cipher_version"); err != nil {
			return ErrSQLCipherUnavailable
		}
	} else if encrypted {
		return ErrSQLCipherUnavailable
	}

	return configurePragmas(conn, busyTimeoutMS)
}

func keyConnection(conn *sqlite3.SQLiteConn, key []byte, encrypted bool) error {
	if encrypted {
		statement := `PRAGMA key = "x'` + hex.EncodeToString(key) + `'";`
		if _, err := conn.Exec(statement, nil); err != nil {
			return ErrKeyRejected
		}
		if value, err := pragmaValue(conn, "cipher_status"); err != nil || value != "1" {
			return ErrKeyRejected
		}
	}
	return nil
}

func configurePragmas(conn *sqlite3.SQLiteConn, busyTimeoutMS int) error {
	pragmas := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMS),
		"PRAGMA foreign_keys = ON",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA secure_delete = ON",
		"PRAGMA synchronous = NORMAL",
	}
	for _, statement := range pragmas {
		if _, err := conn.Exec(statement, nil); err != nil {
			return redactError(err)
		}
	}

	// WAL is enabled only after key/schema validation. In-memory databases
	// correctly report "memory" and do not have sidecar files.
	if value, err := pragmaValue(conn, "journal_mode = WAL"); err != nil {
		return redactError(err)
	} else if value != "wal" && !strings.Contains(value, "memory") {
		return fmt.Errorf("sqlite: WAL unavailable: %s", value)
	}
	if _, err := conn.Exec("PRAGMA wal_autocheckpoint = 1000", nil); err != nil {
		return redactError(err)
	}
	return nil
}

func pragmaValue(conn *sqlite3.SQLiteConn, pragma string) (string, error) {
	rows, err := conn.Query("PRAGMA "+pragma, nil)
	if err != nil {
		return "", redactError(err)
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil {
		return "", redactError(err)
	}
	if values[0] == nil {
		return "", nil
	}
	return fmt.Sprint(values[0]), nil
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

	var backupErr error
	err = dstConn.Raw(func(dst any) error {
		dstConn, ok := dst.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("sqlite: unexpected destination driver connection")
		}
		return srcConn.Raw(func(src any) error {
			srcConn, ok := src.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("sqlite: unexpected source driver connection")
			}
			backup, err := dstConn.Backup("main", srcConn, "main")
			if err != nil {
				return redactError(err)
			}
			defer func() {
				if err := backup.Finish(); backupErr == nil && err != nil {
					backupErr = redactError(err)
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				done, err := backup.Step(backupPages)
				if err != nil {
					return redactError(err)
				}
				if done {
					return nil
				}
				runtime.Gosched()
			}
		})
	})
	if err != nil {
		return redactError(err)
	}
	dstConn.Close()
	srcConn.Close()
	if backupErr != nil {
		return backupErr
	}
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
