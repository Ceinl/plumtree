#!/usr/bin/env bash
set -euo pipefail

# Builds release assets for one or all five contract platforms.
#
# Usage: build-release.sh DIST [OS/ARCH ...]
#
# Without explicit targets the classic full-desktop build runs: all five
# platforms, checksums.txt, and the full release contract check. Release
# matrix runners call it with exactly one target and leave checksum
# assembly to the publish job, which owns every artifact.
#
# The environment is pinned by the caller:
#   CC               target-native C compiler for this runner
#   SQLCIPHER_PREFIX pinned SQLCipher prefix (apt / msys2 / vendor build)
#   OPENSSL_PREFIX   pinned OpenSSL prefix
# plus optional SQLCIPHER_INCLUDE, SQLCIPHER_LIBRARY,

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${1:-"$workspace_root/dist"}
shift 2>/dev/null || true

platforms=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
)

targets=()
if [[ $# -gt 0 ]]; then
  for wanted in "$@"; do
    case " ${platforms[*]} " in
      *" $wanted "*) targets+=("$wanted") ;;
      *) echo "unknown release target: $wanted" >&2; exit 1 ;;
    esac
  done
else
  targets=("${platforms[@]}")
fi

: "${SQLCIPHER_PREFIX:?set SQLCIPHER_PREFIX to the pinned SQLCipher prefix}"
: "${OPENSSL_PREFIX:?set OPENSSL_PREFIX to the pinned OpenSSL prefix}"
: "${CC:?set CC to the target-native C compiler (or a target-dispatching wrapper)}"

export CGO_ENABLED=1
sqlcipher_include=${SQLCIPHER_INCLUDE:-"$SQLCIPHER_PREFIX/include"}
sqlcipher_library=${SQLCIPHER_LIBRARY:-sqlite3}
default_cflags="-I${sqlcipher_include} -I${OPENSSL_PREFIX}/include -DSQLITE_HAS_CODEC -DSQLITE_TEMP_STORE=2 -DSQLITE_EXTRA_INIT=sqlcipher_extra_init -DSQLITE_EXTRA_SHUTDOWN=sqlcipher_extra_shutdown"
default_ldflags="-L${SQLCIPHER_PREFIX}/lib -L${OPENSSL_PREFIX}/lib -l${sqlcipher_library} -lcrypto -lssl"
export CGO_CFLAGS="${SQLCIPHER_CFLAGS:-$default_cflags}"
export CGO_LDFLAGS="${SQLCIPHER_LDFLAGS:-$default_ldflags}"
build_tags="sqlcipher libsqlite3"

# Stamp every command package with the release version when the release is
# tagged, so version and update can trust what they report. The release
# contract check rejects unstamped release binaries.
stamp=${PLUMTREE_VERSION:-$(cd "$workspace_root" && git describe --tags --exact-match HEAD 2>/dev/null || true)}
if [[ -n "$stamp" ]]; then
  stamp_ldflags="-X main.version=$stamp"
else
  stamp_ldflags=""
  echo "==> no release version stamp (not on an exact tag); binaries will report dev"
fi

# Regenerate the server's hermetic build bundle so every release contains the
# SDK and TUI runtime from the exact source revision being built.
(
  cd "$workspace_root"
  go generate ./internal/build
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
      go build -tags "$build_tags" -trimpath -ldflags="-s -w $stamp_ldflags" -o "$pt_output" ./cmd/pt
  )

  echo "==> build plumtree $target"
  (
    cd "$workspace_root"
    GOOS="$target_os" GOARCH="$target_arch" CC="$CC" \
      go build -tags "$build_tags" -trimpath -ldflags="-s -w $stamp_ldflags" -o "$server_output" ./cmd/plumtree
  )
done

# checksums.txt covers every contract asset, so only the all-platform path
# writes it here; release matrix publish jobs assemble it.
if [[ ${#targets[@]} -eq ${#platforms[@]} ]]; then
  (
    cd "$output_dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum pt-* plumtree-* > checksums.txt
    else
      shasum -a 256 pt-* plumtree-* > checksums.txt
    fi
  )
fi

echo "release assets written to $output_dir"
