//go:build !cgo

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func newSQLiteDriver([]byte, bool, int) driver.Driver {
	return &sqlite3.SQLiteDriver{}
}

func backupConnections(context.Context, *sql.Conn, *sql.Conn) error {
	return ErrSQLCipherUnavailable
}
