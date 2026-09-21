#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture=${ABI_FIXTURE:-$workspace_root/internal/runner/testdata/compat/abi-v4-counter.wasm.gz}
want_size=3350824
want_sha256=b6c898405cc3526b2b6f0395ada7af5d2ea7344987c822778360938acdd3b482

if [[ ! -f "$fixture" ]]; then
  echo "missing frozen ABI fixture: $fixture" >&2
  exit 1
fi

size=$(gzip -cd "$fixture" | wc -c | tr -d ' ')
sha256=$(gzip -cd "$fixture" | shasum -a 256 | awk '{print $1}')

if [[ "$size" != "$want_size" ]]; then
  echo "frozen ABI fixture size is $size, want $want_size" >&2
  exit 1
fi
if [[ "$sha256" != "$want_sha256" ]]; then
  echo "frozen ABI fixture SHA-256 is $sha256, want $want_sha256" >&2
  exit 1
fi

printf 'frozen ABI-v4 fixture: %s bytes, sha256:%s\n' "$size" "$sha256"
