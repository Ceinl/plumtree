//go:build sqlcipher && cgo

package sqlite

/*
#cgo CFLAGS: -DSQLITE_HAS_CODEC -DSQLITE_TEMP_STORE=2 -DSQLITE_THREADSAFE=1 -DSQLITE_EXTRA_INIT=sqlcipher_extra_init -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown
// The libsqlite3 name is intentional: the target prefix's libsqlite3 is the
// pinned SQLCipher build, not the host's ordinary SQLite library.
#cgo LDFLAGS: -lcrypto -lssl

// Keep the SQLCipher build contract visible to the compiler. The actual
// library is supplied by the target-native release toolchain; this package
// deliberately does not load a library or extension at runtime.
#include <sqlite3.h>
#if !defined(SQLITE_HAS_CODEC)
#error SQLCipher builds must define SQLITE_HAS_CODEC
#endif
#if SQLITE_TEMP_STORE != 2
#error SQLCipher builds must use SQLITE_TEMP_STORE=2
#endif
enum { plumtree_sqlcipher_build = 1 };
*/
import "C"

const (
	compiledSQLCipher = true
	engineVariant     = "sqlcipher-openssl"
)

var _ = C.plumtree_sqlcipher_build
