#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${1:-"$workspace_root/dist/native"}

: "${SQLCIPHER_PREFIX:?set SQLCIPHER_PREFIX to the pinned native SQLCipher prefix}"
: "${OPENSSL_PREFIX:?set OPENSSL_PREFIX to the pinned native OpenSSL prefix}"
: "${CC:?set CC to the native C compiler}"

mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)

sqlcipher_include=${SQLCIPHER_INCLUDE:-"$SQLCIPHER_PREFIX/include"}
sqlcipher_library=${SQLCIPHER_LIBRARY:-sqlite3}
export PLUMTREE_REAL_CC=$CC
export PLUMTREE_SQLCIPHER_INCLUDE=$sqlcipher_include
export PLUMTREE_SQLCIPHER_LIBRARY=$sqlcipher_library
export CC="$workspace_root/scripts/sqlcipher-cc.sh"
export CGO_ENABLED=1
export CGO_CFLAGS="${SQLCIPHER_CFLAGS:--I${sqlcipher_include} -I${OPENSSL_PREFIX}/include -DSQLITE_HAS_CODEC -DSQLITE_TEMP_STORE=2 -DSQLITE_EXTRA_INIT=sqlcipher_extra_init -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown}"
export CGO_LDFLAGS="${SQLCIPHER_LDFLAGS:--L${SQLCIPHER_PREFIX}/lib -L${OPENSSL_PREFIX}/lib -l${sqlcipher_library} -lcrypto -lssl}"

(
	cd "$workspace_root"
	go generate ./internal/build
	go build -tags "sqlcipher libsqlite3" -trimpath -o "$output_dir/pt" ./cmd/pt
	go build -tags "sqlcipher libsqlite3" -trimpath -o "$output_dir/plumtree" ./cmd/plumtree
	go build -tags "sqlcipher libsqlite3" -trimpath -o "$output_dir/runner-worker" ./cmd/runner-worker
)

"$workspace_root/scripts/check-sqlcipher-target.sh" "$(go env GOOS)/$(go env GOARCH)"
printf 'native release binaries written to %s\n' "$output_dir"
