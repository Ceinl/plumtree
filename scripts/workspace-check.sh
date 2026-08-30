#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
check=${1:-}

case "$check" in
  test)
    command=(go test ./...)
    ;;
  race)
    command=(go test -race ./...)
    ;;
  vet)
    command=(go vet ./...)
    ;;
  *)
    echo "usage: scripts/workspace-check.sh <test|race|vet>" >&2
    exit 2
    ;;
esac

# Keep this list aligned with go.work. Nested modules under internal/runner/testdata are
# build fixtures, while sdk/plums is an independent legacy module outside the
# workspace and therefore outside this repository-level CI contract.
workspace_modules=(
  .
  sdk
  examples/agentboard
  examples/ascii-saver
  examples/chat
  examples/tic-tac-toe
)

for module_dir in "${workspace_modules[@]}"; do
  echo "==> $check $module_dir"
  (
    cd "$workspace_root/$module_dir"
    if [[ "$module_dir" == . || "$module_dir" == sdk ]]; then
      # The root product and public SDK are independent release domains. Do
      # not let go.work conceal a dependency on another repository module.
      export GOWORK=off
    fi
    if [[ "$check" == race && "$module_dir" == . ]]; then
      # The root runner suite contains deliberate 150 ms wall-clock
      # cancellation budgets around Wazero. Race instrumentation slows those
      # guests by orders of magnitude, so retain them as normal-test
      # performance gates and race-check the shared mutable primitives here.
      listed_packages=$(go list ./...)
      mapfile -t race_packages < <(grep -v '^github.com/Ceinl/plumtree/internal/runner$' <<<"$listed_packages")
      if (( ${#race_packages[@]} == 0 )); then
        echo "workspace-check: go list produced no race packages" >&2
        exit 1
      fi
      go test -race "${race_packages[@]}"
      go test -race \
        -run '^(TestMemBus.*|TestMemStore.*|TestFileStore.*|TestTokenBucket.*)$' \
        ./internal/runner
    else
      "${command[@]}"
    fi
  )
done
