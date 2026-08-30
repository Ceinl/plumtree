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
#include <string.h>
#if !defined(SQLITE_HAS_CODEC)
#error SQLCipher builds must define SQLITE_HAS_CODEC
#endif
#if SQLITE_TEMP_STORE != 2
#error SQLCipher builds must use SQLITE_TEMP_STORE=2
#endif
enum { plumtree_sqlcipher_build = 1 };

enum { plumtree_key_literal_size = 67 };
static _Thread_local unsigned char plumtree_key_literal[plumtree_key_literal_size];
static _Thread_local int plumtree_key_literal_len;

static int plumtree_apply_key(sqlite3 *db, char **error, const sqlite3_api_routines *api) {
	(void)error;
	(void)api;
	if (plumtree_key_literal_len == 0) {
		return SQLITE_OK;
	}
	return sqlite3_key(db, plumtree_key_literal, plumtree_key_literal_len);
}

static int plumtree_register_key_hook(void) {
	return sqlite3_auto_extension((void (*)(void))plumtree_apply_key);
}

static int plumtree_set_key(const unsigned char *key, int key_len) {
	if (key_len != plumtree_key_literal_size) {
		return SQLITE_MISUSE;
	}
	memcpy(plumtree_key_literal, key, key_len);
	plumtree_key_literal_len = key_len;
	return SQLITE_OK;
}

static void plumtree_clear_key(void) {
	volatile unsigned char *key = plumtree_key_literal;
	for (int i = 0; i < plumtree_key_literal_size; i++) {
		key[i] = 0;
	}
	plumtree_key_literal_len = 0;
}
*/
import "C"

import (
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

const (
	compiledSQLCipher = true
	engineVariant     = "sqlcipher-openssl"
)

var _ = C.plumtree_sqlcipher_build

var (
	registerKeyHookOnce sync.Once
	registerKeyHookErr  error
)

type keyedDriver struct {
	driver.Driver
	key []byte
}

func wrapKeyedDriver(base driver.Driver, key []byte) driver.Driver {
	return &keyedDriver{Driver: base, key: key}
}

func (d *keyedDriver) Open(name string) (driver.Conn, error) {
	registerKeyHookOnce.Do(func() {
		if code := C.plumtree_register_key_hook(); code != C.SQLITE_OK {
			registerKeyHookErr = fmt.Errorf("sqlite: register SQLCipher key hook: code %d", int(code))
		}
	})
	if registerKeyHookErr != nil {
		return nil, registerKeyHookErr
	}

	literal := make([]byte, int(C.plumtree_key_literal_size))
	copy(literal, "x'")
	hex.Encode(literal[2:66], d.key)
	literal[66] = '\''
	defer zero(literal)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if code := C.plumtree_set_key((*C.uchar)(unsafe.Pointer(&literal[0])), C.int(len(literal))); code != C.SQLITE_OK {
		return nil, fmt.Errorf("sqlite: stage SQLCipher key: code %d", int(code))
	}
	defer C.plumtree_clear_key()
	return d.Driver.Open(name)
}
