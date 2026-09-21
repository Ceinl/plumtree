#!/usr/bin/env bash
set -euo pipefail

dist=${1:-dist}
if [[ ! -d "$dist" ]]; then
  echo "release asset directory does not exist: $dist" >&2
  exit 1
fi

expected=(
  checksums.txt
  pt-linux-amd64
  pt-linux-arm64
  pt-darwin-amd64
  pt-darwin-arm64
  pt-windows-amd64.exe
  plumtree-linux-amd64
  plumtree-linux-arm64
  plumtree-darwin-amd64
  plumtree-darwin-arm64
  plumtree-windows-amd64.exe
)

actual_count=$(find "$dist" -maxdepth 1 -type f -print | wc -l | tr -d ' ')
if [[ "$actual_count" -ne "${#expected[@]}" ]]; then
  printf 'release asset count: found %s, expected %s\n' "$actual_count" "${#expected[@]}" >&2
  exit 1
fi
for path in "$dist"/*; do
  [[ -f "$path" ]] || continue
  asset=${path##*/}
  case "$asset" in
    checksums.txt|pt-linux-amd64|pt-linux-arm64|pt-darwin-amd64|pt-darwin-arm64|pt-windows-amd64.exe|plumtree-linux-amd64|plumtree-linux-arm64|plumtree-darwin-amd64|plumtree-darwin-arm64|plumtree-windows-amd64.exe) ;;
    *) echo "unexpected release asset: $path" >&2; exit 1 ;;
  esac
done
for asset in "${expected[@]}"; do
  if [[ ! -s "$dist/$asset" ]]; then
    echo "missing or empty release asset: $dist/$asset" >&2
    exit 1
  fi
done

# Release-blocking stamp check: every binary must identify itself with the
# release tag it ships under, so version and update can trust what they see.
tag=${2:-}
if [[ -n "$tag" ]]; then
  for path in "$dist"/pt-* "$dist"/plumtree-*; do
    [[ -f "$path" ]] || continue
    if ! LC_ALL=C grep -aqF -- "$tag" "$path"; then
      echo "release asset ${path##*/} does not carry the release stamp $tag; rebuild with the stamped linker flags" >&2
      exit 1
    fi
  done
fi

echo "release asset contract: ${#expected[@]} canonical assets verified"
