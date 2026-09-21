package sqlite

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
)

// Rekey changes the key of an already encrypted SQLCipher database. Empty to
// empty is a deliberate plaintext no-op; changing encryption mode requires a
// SQLCipher export and is not silently attempted by the engine boundary.
func Rekey(ctx context.Context, db *DB, newKey []byte) error {
	if db == nil || db.DB == nil {
		return errors.New("sqlite: nil database")
	}
	if len(newKey) != 0 && len(newKey) != KeySize {
		return ErrInvalidKey
	}
	if len(newKey) == 0 {
		if db.encrypted {
			return ErrSQLCipherUnavailable
		}
		return nil
	}
	if !compiledSQLCipher {
		return ErrSQLCipherUnavailable
	}
	encoded := hex.EncodeToString(newKey)
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA rekey = "x'%s'"`, encoded)); err != nil {
		return ErrKeyRejected
	}
	db.closeKey()
	db.key = append([]byte(nil), newKey...)
	db.encrypted = true
	return nil
}
