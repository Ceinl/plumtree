#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${1:-"$workspace_root/dist"}

mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)

: "${SQLCIPHER_PREFIX:?set SQLCIPHER_PREFIX to the pinned SQLCipher prefix}"
: "${OPENSSL_PREFIX:?set OPENSSL_PREFIX to the pinned OpenSSL prefix}"
: "${CC:?set CC to the target-native C compiler (or a target-dispatching wrapper)}"

export CGO_ENABLED=1
default_cflags="-I${SQLCIPHER_PREFIX}/include -I${OPENSSL_PREFIX}/include -DSQLITE_HAS_CODEC -DSQLITE_TEMP_STORE=2 -DSQLITE_EXTRA_INIT=sqlcipher_extra_init -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown"
default_ldflags="-L${SQLCIPHER_PREFIX}/lib -L${OPENSSL_PREFIX}/lib -lsqlite3 -lcrypto -lssl"
export CGO_CFLAGS="${SQLCIPHER_CFLAGS:-$default_cflags}"
export CGO_LDFLAGS="${SQLCIPHER_LDFLAGS:-$default_ldflags}"
build_tags="sqlcipher libsqlite3"

# Regenerate the server's hermetic build bundle so every release contains the
# SDK and TUI runtime from the exact source revision being built.
(
  cd "$workspace_root"
  go generate ./internal/build
)

targets=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
)

for target in "${targets[@]}"; do
  target_os=${target%/*}
  target_arch=${target#*/}
  extension=""
  if [[ "$target_os" == windows ]]; then
    extension=.exe
  fi

  pt_output="$output_dir/pt-$target_os-$target_arch$extension"
  server_output="$output_dir/plumtree-$target_os-$target_arch$extension"

  echo "==> build pt $target"
  (
    cd "$workspace_root"
    GOOS="$target_os" GOARCH="$target_arch" CC="$CC" \
      go build -tags "$build_tags" -trimpath -ldflags="-s -w" -o "$pt_output" ./cmd/pt
  )

  echo "==> build plumtree $target"
  (
    cd "$workspace_root"
    GOOS="$target_os" GOARCH="$target_arch" CC="$CC" \
      go build -tags "$build_tags" -trimpath -ldflags="-s -w" -o "$server_output" ./cmd/plumtree
  )
done

(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum pt-* plumtree-* > checksums.txt
  else
    shasum -a 256 pt-* plumtree-* > checksums.txt
  fi
)

echo "release assets written to $output_dir"
