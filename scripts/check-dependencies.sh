#!/usr/bin/env bash
set -euo pipefail

workspace_root=${GUARDRAIL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
manifest=${DEPENDENCY_MANIFEST:-$workspace_root/testdata/consolidation/dependencies.tsv}

if [[ ! -f "$manifest" ]]; then
  echo "missing dependency allowlist: $manifest" >&2
  exit 1
fi

workfile=off
if [[ -f "$workspace_root/go.work" ]]; then
  workfile=$workspace_root/go.work
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
expected=$tmpdir/expected.tsv
actual=$tmpdir/actual.tsv

LC_ALL=C sed '/^[[:space:]]*#/d; /^[[:space:]]*$/d' "$manifest" | sort -u > "$expected"
cut -f1 "$expected" | sort -u | while IFS= read -r module_dir; do
  if [[ ! -f "$workspace_root/$module_dir/go.mod" ]]; then
    echo "allowlisted module has no go.mod: $module_dir" >&2
    exit 1
  fi
  (
    cd "$workspace_root/$module_dir"
    GOWORK="$workfile" go list -deps -test -f '{{if .Module}}{{.Module.Path}}{{end}}' ./...
  ) | sed '/^$/d' | sort -u | while IFS= read -r dependency; do
    printf '%s\t%s\n' "$module_dir" "$dependency"
  done
done | LC_ALL=C sort -u > "$actual"

if ! diff -u "$expected" "$actual"; then
  echo "dependency graph differs from the exact allowlist" >&2
  echo "remove obsolete rows when dependencies disappear; do not allowlist unplanned edges" >&2
  exit 1
fi

printf 'dependency allowlist: %s module edges verified\n' "$(wc -l < "$actual" | tr -d ' ')"
