//go:build cgo

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func newSQLiteDriver(key []byte, encrypted bool, busyTimeoutMS int) driver.Driver {
	return &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			// The upstream driver applies only connection-local defaults before
			// ConnectHook. Apply the SQLCipher key before Plumtree performs any
			// schema read or pager-changing PRAGMA.
			if err := keyConnection(conn, key, encrypted); err != nil {
				return err
			}
			return configureConnection(conn, encrypted, busyTimeoutMS)
		},
	}
}

func configureConnection(conn *sqlite3.SQLiteConn, encrypted bool, busyTimeoutMS int) error {
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

func backupConnections(ctx context.Context, destination, source *sql.Conn) error {
	var finishErr error
	err := destination.Raw(func(dst any) error {
		dstConn, ok := dst.(*sqlite3.SQLiteConn)
		if !ok {
			return errors.New("sqlite: unexpected destination driver connection")
		}
		return source.Raw(func(src any) error {
			srcConn, ok := src.(*sqlite3.SQLiteConn)
			if !ok {
				return errors.New("sqlite: unexpected source driver connection")
			}
			backup, err := dstConn.Backup("main", srcConn, "main")
			if err != nil {
				return redactError(err)
			}
			defer func() {
				if err := backup.Finish(); finishErr == nil && err != nil {
					finishErr = redactError(err)
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
		return err
	}
	return finishErr
}
