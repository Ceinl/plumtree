#!/usr/bin/env bash
# Build the Plumtree WASM feasibility spike: the guest counter (-> WASM) and the
# wazero host runner (native). Optionally builds a TinyGo guest for comparison
# when tinygo is on PATH.
set -euo pipefail
cd "$(dirname "$0")"

mkdir -p dist

echo ">> guest: GOOS=wasip1 GOARCH=wasm (stock Go, c-shared reactor)"
GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o dist/counter.wasm ./guest
printf "   dist/counter.wasm  %s bytes\n" "$(wc -c < dist/counter.wasm)"

if command -v tinygo >/dev/null 2>&1; then
  echo ">> guest: TinyGo wasip1 (comparison build)"
  if tinygo build -target=wasip1 -buildmode=c-shared -o dist/counter.tinygo.wasm ./guest 2>/tmp/tinygo.err; then
    printf "   dist/counter.tinygo.wasm  %s bytes\n" "$(wc -c < dist/counter.tinygo.wasm)"
  else
    echo "   TinyGo build failed (see /tmp/tinygo.err) — recorded as a finding."
  fi
else
  echo ">> TinyGo not installed; skipping comparison build."
fi

echo ">> host: native runner"
go build -o dist/host ./host
printf "   dist/host  %s bytes\n" "$(wc -c < dist/host)"

echo
echo "Run a scripted session:"
echo "  ./dist/host -headless -script \"up,up,down,q\""
echo "Interactive (needs a real terminal):"
echo "  ./dist/host"
