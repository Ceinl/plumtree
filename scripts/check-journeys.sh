#!/usr/bin/env bash
set -euo pipefail

workspace_root=${GUARDRAIL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
manifest=${JOURNEY_MANIFEST:-$workspace_root/testdata/consolidation/journeys.tsv}
consumers=${CONSUMER_MANIFEST:-$workspace_root/testdata/consolidation/external-consumers.txt}

if [[ ! -f "$manifest" ]]; then
  echo "missing retained-journey manifest: $manifest" >&2
  exit 1
fi
if [[ ! -f "$consumers" ]]; then
  echo "missing external-consumer manifest: $consumers" >&2
  exit 1
fi

failed=0
ids=$(mktemp)
trap 'rm -f "$ids"' EXIT
while IFS=$'\t' read -r id fixture proof_file proof_symbol; do
  [[ -z "$id" || "$id" == \#* ]] && continue
  printf '%s\n' "$id" >> "$ids"
  if [[ ! "$id" =~ ^[a-z0-9-]+$ ]]; then
    echo "invalid journey id: $id" >&2
    failed=1
  fi
  if [[ ! -e "$workspace_root/$fixture" ]]; then
    echo "journey $id is missing fixture: $fixture" >&2
    failed=1
  fi
  if [[ ! -f "$workspace_root/$proof_file" ]]; then
    echo "journey $id is missing proof file: $proof_file" >&2
    failed=1
  elif ! grep -F -q -- "$proof_symbol" "$workspace_root/$proof_file"; then
    echo "journey $id proof $proof_symbol is absent from $proof_file" >&2
    failed=1
  fi
done < "$manifest"

duplicates=$(sort "$ids" | uniq -d)
if [[ -n "$duplicates" ]]; then
  printf 'duplicate journey ids:\n%s\n' "$duplicates" >&2
  failed=1
fi

while IFS= read -r consumer; do
  [[ -z "$consumer" || "$consumer" == \#* ]] && continue
  if [[ ! -f "$workspace_root/$consumer/go.mod" ]]; then
    echo "external consumer has no go.mod: $consumer" >&2
    failed=1
  fi
done < "$consumers"

if [[ "$failed" != 0 ]]; then
  exit 1
fi

printf 'retained journeys: %s proofs and %s external consumers verified\n' \
  "$(wc -l < "$ids" | tr -d ' ')" \
  "$(sed '/^[[:space:]]*#/d; /^[[:space:]]*$/d' "$consumers" | wc -l | tr -d ' ')"
