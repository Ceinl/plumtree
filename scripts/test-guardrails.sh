#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

expect_failure() {
  local label=$1
  shift
  if "$@" >"$tmpdir/$label.out" 2>&1; then
    echo "$label guard unexpectedly accepted an invalid fixture" >&2
    cat "$tmpdir/$label.out" >&2
    exit 1
  fi
  printf 'negative guard: %s rejected\n' "$label"
}

# Mutating the uncompressed guest must invalidate the pinned artifact digest.
gzip -cd "$workspace_root/runner/testdata/compat/abi-v4-counter.wasm.gz" > "$tmpdir/guest.wasm"
printf 'mutation' >> "$tmpdir/guest.wasm"
gzip -n -9 < "$tmpdir/guest.wasm" > "$tmpdir/mutated.wasm.gz"
expect_failure abi env ABI_FIXTURE="$tmpdir/mutated.wasm.gz" \
  "$workspace_root/scripts/check-abi-fixture.sh"

# A new SDK-to-product edge must differ from the exact module allowlist.
mkdir -p "$tmpdir/dependencies/sdk" "$tmpdir/dependencies/product/pkg"
cat > "$tmpdir/dependencies/sdk/go.mod" <<'EOF'
module example.com/sdk

go 1.26.0
EOF
cat > "$tmpdir/dependencies/sdk/sdk.go" <<'EOF'
package sdk

import _ "example.com/product/pkg"
EOF
cat > "$tmpdir/dependencies/product/go.mod" <<'EOF'
module example.com/product

go 1.26.0
EOF
cat > "$tmpdir/dependencies/product/pkg/pkg.go" <<'EOF'
package pkg
EOF
(
  cd "$tmpdir/dependencies"
  go work init ./sdk ./product
)
printf 'sdk\texample.com/product\nsdk\texample.com/sdk\n' > "$tmpdir/dependencies.tsv"
env GUARDRAIL_ROOT="$tmpdir/dependencies" DEPENDENCY_MANIFEST="$tmpdir/dependencies.tsv" \
  "$workspace_root/scripts/check-dependencies.sh" >/dev/null
printf 'sdk\texample.com/sdk\n' > "$tmpdir/dependencies.tsv"
expect_failure dependency env \
  GUARDRAIL_ROOT="$tmpdir/dependencies" \
  DEPENDENCY_MANIFEST="$tmpdir/dependencies.tsv" \
  "$workspace_root/scripts/check-dependencies.sh"

# Adding another occurrence of a concept scheduled for removal must fail.
mkdir -p "$tmpdir/removals"
(
  cd "$tmpdir/removals"
  git init -q
  git config user.email guardrails@example.invalid
  git config user.name Guardrails
  printf 'Shoo\n' > legacy.txt
  git add legacy.txt
)
printf 'text\tshoo\t73\t1\t[Ss][Hh][Oo][Oo]\n' > "$tmpdir/removals.tsv"
env GUARDRAIL_ROOT="$tmpdir/removals" REMOVAL_MANIFEST="$tmpdir/removals.tsv" \
  "$workspace_root/scripts/check-removal-ratchet.sh" >/dev/null
printf 'Shoo\n' >> "$tmpdir/removals/legacy.txt"
git -C "$tmpdir/removals" add legacy.txt
expect_failure removal env \
  GUARDRAIL_ROOT="$tmpdir/removals" \
  REMOVAL_MANIFEST="$tmpdir/removals.tsv" \
  "$workspace_root/scripts/check-removal-ratchet.sh"

# Removing a fixture while retaining its journey claim must fail.
mkdir -p "$tmpdir/journeys"
printf 'missing\tfixture\tproof_test.go\tTestProof\n' > "$tmpdir/journeys.tsv"
printf '# none\n' > "$tmpdir/consumers.txt"
expect_failure journey env \
  GUARDRAIL_ROOT="$tmpdir/journeys" \
  JOURNEY_MANIFEST="$tmpdir/journeys.tsv" \
  CONSUMER_MANIFEST="$tmpdir/consumers.txt" \
  "$workspace_root/scripts/check-journeys.sh"
