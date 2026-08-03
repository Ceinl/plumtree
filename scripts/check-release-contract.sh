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

echo "release asset contract: ${#expected[@]} canonical assets verified"
