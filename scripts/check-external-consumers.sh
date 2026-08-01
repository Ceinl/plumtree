#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
manifest=$workspace_root/testdata/consolidation/external-consumers.txt
workfile=$workspace_root/go.work
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT

while IFS= read -r consumer; do
  [[ -z "$consumer" || "$consumer" == \#* ]] && continue
  echo "==> external consumer $consumer"
  (
    cd "$workspace_root/$consumer"
    GOWORK="$workfile" go test -run '^$' ./...

    GOWORK="$workfile" go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... |
      sed '/^$/d' |
      while IFS= read -r package; do
        output=$(printf '%s' "$consumer/$package" | tr '/.' '__')
        GOWORK="$workfile" GOOS=wasip1 GOARCH=wasm \
          go build -trimpath -buildvcs=false -o "$build_dir/$output.wasm" "$package"
      done
  )
done < "$manifest"

printf 'external consumers: native tests compiled and WASI commands built\n'
