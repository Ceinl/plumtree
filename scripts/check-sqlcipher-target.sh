#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
target=${1:-"$(go env GOOS)/$(go env GOARCH)"}
target_os=${target%/*}
target_arch=${target#*/}

if [[ "$target_os" == "$target" || -z "$target_arch" ]]; then
  echo "usage: $0 GOOS/GOARCH" >&2
  exit 2
fi

: "${SQLCIPHER_PREFIX:?set SQLCIPHER_PREFIX to the pinned SQLCipher prefix}"
: "${OPENSSL_PREFIX:?set OPENSSL_PREFIX to the pinned OpenSSL prefix}"
: "${CC:?set CC to the target-native C compiler}"

default_cflags="-I${SQLCIPHER_PREFIX}/include -I${OPENSSL_PREFIX}/include -DSQLITE_HAS_CODEC -DSQLITE_TEMP_STORE=2 -DSQLITE_EXTRA_INIT=sqlcipher_extra_init -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown"
default_ldflags="-L${SQLCIPHER_PREFIX}/lib -L${OPENSSL_PREFIX}/lib -lsqlite3 -lcrypto -lssl"

export CGO_ENABLED=1
export CGO_CFLAGS="${SQLCIPHER_CFLAGS:-$default_cflags}"
export CGO_LDFLAGS="${SQLCIPHER_LDFLAGS:-$default_ldflags}"

cd "$workspace_root"
tags="sqlcipher libsqlite3"
if [[ "$target_os/$target_arch" == "$(go env GOOS)/$(go env GOARCH)" ]]; then
  go test -tags "$tags" ./internal/sqlite -count=1
else
  # Cross-target jobs can prove compilation and link identity here; execution
  # is intentionally reserved for the native runner for that target.
  output_dir=${TMPDIR:-/tmp}/plumtree-sqlcipher-target
  mkdir -p "$output_dir"
  output="$output_dir/sqlite-${target_os}-${target_arch}.test"
  GOOS="$target_os" GOARCH="$target_arch" CC="$CC" \
    go test -tags "$tags" -c ./internal/sqlite -o "$output"
  go tool nm "$output" | grep -E '(^|[[:space:]])(_?sqlite3_key|_?sqlcipher_version)([[:space:]]|$)'
fi
