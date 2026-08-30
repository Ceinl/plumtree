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
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func newSQLiteDriver(key []byte, encrypted bool, busyTimeoutMS, cacheSizeKB, walAutoCheckpointPages int, trace TraceFunc) driver.Driver {
	base := &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			// The upstream driver applies only connection-local defaults before
			// ConnectHook. Apply the SQLCipher key before Plumtree performs any
			// schema read or pager-changing PRAGMA.
			if err := keyConnection(conn, key, encrypted); err != nil {
				return err
			}
			if err := configureConnection(conn, encrypted, busyTimeoutMS, cacheSizeKB, walAutoCheckpointPages); err != nil {
				return err
			}
			return nil
		},
	}
	if trace == nil {
		return base
	}
	return &tracingDriver{Driver: base, trace: trace}
}

// tracingDriver keeps query observability in Plumtree instead of patching the
// dependency to force-enable go-sqlite3's optional sqlite_trace build tag.
// Bound values are intentionally never passed to the callback.
type tracingDriver struct {
	driver.Driver
	trace TraceFunc
}

func (d *tracingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &tracingConn{Conn: conn, trace: d.trace}, nil
}

type tracingConn struct {
	driver.Conn
	trace TraceFunc
}

func (c *tracingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	c.trace(TraceEvent{Kind: "statement", Statement: query})
	result, err := execer.ExecContext(ctx, query, args)
	c.trace(TraceEvent{Kind: "profile", Statement: query, Duration: time.Since(started)})
	return result, err
}

func (c *tracingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	started := time.Now()
	c.trace(TraceEvent{Kind: "statement", Statement: query})
	rows, err := queryer.QueryContext(ctx, query, args)
	c.trace(TraceEvent{Kind: "profile", Statement: query, Duration: time.Since(started)})
	return rows, err
}

func configureConnection(conn *sqlite3.SQLiteConn, encrypted bool, busyTimeoutMS, cacheSizeKB, walAutoCheckpointPages int) error {
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

	return configurePragmas(conn, busyTimeoutMS, cacheSizeKB, walAutoCheckpointPages)
}

func keyConnection(conn *sqlite3.SQLiteConn, key []byte, encrypted bool) error {
	if encrypted {
		statement := `PRAGMA key = "x'` + hex.EncodeToString(key) + `'";`
		if _, err := conn.Exec(statement, nil); err != nil {
			return ErrKeyRejected
		}
	}
	return nil
}

func configurePragmas(conn *sqlite3.SQLiteConn, busyTimeoutMS, cacheSizeKB, walAutoCheckpointPages int) error {
	pragmas := []string{
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeoutMS),
		"PRAGMA foreign_keys = ON",
		"PRAGMA temp_store = MEMORY",
		"PRAGMA secure_delete = ON",
		"PRAGMA synchronous = NORMAL",
		fmt.Sprintf("PRAGMA cache_size = -%d", cacheSizeKB),
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
	if _, err := conn.Exec(fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", walAutoCheckpointPages), nil); err != nil {
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
		dstConn, ok := unwrapSQLiteConn(dst)
		if !ok {
			return errors.New("sqlite: unexpected destination driver connection")
		}
		return source.Raw(func(src any) error {
			srcConn, ok := unwrapSQLiteConn(src)
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

func unwrapSQLiteConn(value any) (*sqlite3.SQLiteConn, bool) {
	if conn, ok := value.(*sqlite3.SQLiteConn); ok {
		return conn, true
	}
	if conn, ok := value.(*tracingConn); ok {
		sqliteConn, ok := conn.Conn.(*sqlite3.SQLiteConn)
		return sqliteConn, ok
	}
	return nil, false
}
