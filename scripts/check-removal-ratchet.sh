#!/usr/bin/env bash
set -euo pipefail

workspace_root=${GUARDRAIL_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}
manifest=${REMOVAL_MANIFEST:-$workspace_root/testdata/consolidation/removals.tsv}

if [[ ! -f "$manifest" ]]; then
  echo "missing removal inventory: $manifest" >&2
  exit 1
fi
if ! git -C "$workspace_root" rev-parse --git-dir >/dev/null 2>&1; then
  echo "removal inventory root is not a git repository: $workspace_root" >&2
  exit 1
fi

files=$(mktemp)
text_files=$(mktemp)
trap 'rm -f "$files" "$text_files"' EXIT
git -C "$workspace_root" ls-files --cached --others --exclude-standard > "$files"
grep -E -v '^(docs/consolidation/|testdata/consolidation/|scripts/(check-(abi-fixture|consolidation-guardrails|dependencies|external-consumers|journeys|removal-ratchet)|run-consolidation-journeys|test-guardrails)\.sh$)' \
  "$files" > "$text_files" || true

failed=0
while IFS=$'\t' read -r kind id planned_issue expected pattern; do
  [[ -z "$kind" || "$kind" == \#* ]] && continue
  case "$kind" in
    path)
      actual=$(grep -E -c -- "$pattern" "$files" || true)
      ;;
    text)
      matches=$(
        cd "$workspace_root"
        tr '\n' '\0' < "$text_files" | xargs -0 grep -I -E -h -o -- "$pattern" 2>/dev/null || true
      )
      if [[ -z "$matches" ]]; then
        actual=0
      else
        actual=$(printf '%s\n' "$matches" | wc -l | tr -d ' ')
      fi
      ;;
    *)
      echo "unknown removal inventory kind for $id: $kind" >&2
      failed=1
      continue
      ;;
  esac

  if [[ "$actual" != "$expected" ]]; then
    printf 'removal inventory %s: found %s, expected %s (planned issue #%s)\n' \
      "$id" "$actual" "$expected" "$planned_issue" >&2
    failed=1
  fi
done < "$manifest"

if [[ "$failed" != 0 ]]; then
  echo "removal inventory changed; reject additions and lower the recorded count for intentional removals" >&2
  exit 1
fi

printf 'removal inventory: all recorded counts match\n'
