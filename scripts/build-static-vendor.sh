#!/usr/bin/env bash
set -euo pipefail

# Builds the static SQLCipher + OpenSSL vendor prefix for one darwin target
# so release binaries are self-contained (no runtime brew/ports dylibs).
# Windows uses the msys2 mingw-w64-sqlcipher static package and Linux uses
# the pinned apt libsqlcipher-dev packages, so neither needs this script.
#
# Usage: build-static-vendor.sh OS/ARCH BUILD_DIR
# Pins  sqlcipher v4.11.0 and openssl 3.5.4 by sha256. Repeat runs reuse the
# staged prefix whenever both static libs exist.
#
# Caller pins: CC (clang), AR (ar), RANLIB (ranlib) for the target arch.

openssl_version=3.5.4
openssl_sha256=967311f84955316969bdb1d8d4b983718ef42338639c621ec4c34fddef355e99
sqlcipher_version=4.11.0
sqlcipher_sha256=bfa85505001dfb6c7f4ab532a39af0ed11d255cd11763bb070d6bb6ac6739a64

target=$1
build_dir=$2
[[ ${target%/*} == darwin ]] || {
  echo "build-static-vendor.sh only vendors darwin targets; got $target" >&2
  exit 1
}
: "${CC:?build-static-vendor.sh: CC must be clang for $target}"
: "${AR:?build-static-vendor.sh: AR must be the archiver for $target}"
: "${RANLIB:?build-static-vendor.sh: RANLIB must be the archiver for $target}"
[[ -n "$(command -v curl)" && -n "$(command -v tar)" && -n "$(command -v make)" ]] || {
  echo "build-static-vendor.sh needs curl, tar, make" >&2
  exit 1
}

mkdir -p "$build_dir/prefix/sqlcipher" "$build_dir/prefix/openssl" "$build_dir/src"
prefix_dir=$(cd "$build_dir/prefix" && pwd)
src_dir=$(cd "$build_dir/src" && pwd)
cd "$src_dir"

if [[ ! -s "$prefix_dir/openssl/lib/libcrypto.a" ]]; then
  echo "==> build static openssl $openssl_version for $target"
  curl -fsSLo "openssl-$openssl_version.tar.gz" \
    "https://github.com/openssl/openssl/releases/download/openssl-$openssl_version/openssl-$openssl_version.tar.gz"
  echo "$openssl_sha256  openssl-$openssl_version.tar.gz" | shasum -a 256 -c - > /dev/null
  tar xf "openssl-$openssl_version.tar.gz"
  (
    cd "openssl-$openssl_version"
    ./config no-shared --prefix="$prefix_dir/openssl"
    make -j "$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc)" build_sw
    make install_sw
  )
fi

if [[ ! -s "$prefix_dir/sqlcipher/lib/libsqlcipher.a" ]]; then
  echo "==> build static sqlcipher $sqlcipher_version for $target"
  curl -fsSLo "sqlcipher-$sqlcipher_version.tar.gz" \
    "https://github.com/sqlcipher/sqlcipher/archive/v$sqlcipher_version.tar.gz"
  echo "$sqlcipher_sha256  sqlcipher-$sqlcipher_version.tar.gz" | shasum -a 256 -c - > /dev/null
  tar xf "sqlcipher-$sqlcipher_version.tar.gz"
  (
    cd "sqlcipher-$sqlcipher_version"
    ./configure --disable-shared \
      --prefix="$prefix_dir/sqlcipher" \
      CFLAGS="-I$prefix_dir/openssl/include" \
      LDFLAGS="-L$prefix_dir/openssl/lib"
    make -j "$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc)"
    make install
  )
fi

[[ -s "$prefix_dir/sqlcipher/lib/libsqlcipher.a" && -s "$prefix_dir/openssl/lib/libssl.a" ]] || {
  echo "build-static-vendor.sh did not produce the expected artifacts" >&2
  exit 1
}
echo "static openssl and sqlcipher staged in $prefix_dir"
