# Plumtree SQLCipher build contract

Plumtree links the root-internal `mattn/go-sqlite3` binding against the
official SQLCipher Community Edition `v4.17.0` engine (published as the
prefix's `libsqlite3`) and an OpenSSL 3 provider. The release builder supplies
those libraries from a target-native, checksummed prefix; the Go package does
not load a system SQLite library or a runtime extension.

The SQLCipher prefix must be configured with these definitions:

```text
SQLITE_HAS_CODEC
SQLITE_TEMP_STORE=2
SQLITE_EXTRA_INIT=sqlcipher_extra_init
SQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown
SQLITE_THREADSAFE=1
```

Use the SQLCipher 4 defaults, including an encrypted header, AES-256-CBC,
random per-write IVs, and HMAC-SHA-512. `scripts/check-sqlcipher-target.sh`
is the target-native qualification entrypoint. It expects `SQLCIPHER_PREFIX`,
`OPENSSL_PREFIX`, and a native `CC` for the selected target; callers may set
`SQLCIPHER_CFLAGS` and `SQLCIPHER_LDFLAGS` when their platform's static-link
flags differ.

The source pin is deliberately recorded here rather than selecting a new
repository or exposing SQLCipher through the SDK. The SQLCipher and OpenSSL
source archives, checksums, compiler, and static-link flags are release-input
artifacts owned by the native build environment.

Pinned source inputs:

```text
https://github.com/sqlcipher/sqlcipher/archive/refs/tags/v4.17.0.tar.gz
79c0e164b9c059e7487bf8f29272f601cca5f3312cc267461f81e349962a5058

https://www.openssl.org/source/openssl-3.6.2.tar.gz
aaf51a1fe064384f811daeaeb4ec4dce7340ec8bd893027eee676af31e83a04f
```
