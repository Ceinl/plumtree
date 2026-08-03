package sqlite

import "errors"

var (
	// ErrSQLCipherUnavailable means that a keyed database was requested but the
	// binary was not built with the SQLCipher variant of the engine.
	ErrSQLCipherUnavailable = errors.New("sqlite: SQLCipher engine is unavailable")

	// ErrKeyRequired is returned when an encrypted database is opened without a
	// key. The database file is never inspected before this check.
	ErrKeyRequired = errors.New("sqlite: encryption key is required")

	// ErrInvalidKey means that the supplied raw key is not the required size.
	ErrInvalidKey = errors.New("sqlite: encryption key must be exactly 32 bytes")

	// ErrKeyRejected covers missing, wrong, and tampered SQLCipher keys without
	// echoing key material or a DSN in the error.
	ErrKeyRejected = errors.New("sqlite: encryption key rejected")
)
