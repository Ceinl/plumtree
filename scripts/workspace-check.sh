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

# Keep this list aligned with go.work. Nested modules under runner/testdata are
# build fixtures, while sdk/plums is an independent legacy module outside the
# workspace and therefore outside this repository-level CI contract.
workspace_modules=(
  build-worker
  control-plane
  pt
  runner
  sdk
  ssh-gateway
  tui-runtime
  _devtest/goodbye-cli
  _devtest/goodbye-tui
  examples/agentboard
)

for module_dir in "${workspace_modules[@]}"; do
  echo "==> $check $module_dir"
  (
    cd "$workspace_root/$module_dir"
    if [[ "$check" == race && "$module_dir" == runner ]]; then
      # The runner's normal suite contains deliberate 150 ms wall-clock
      # cancellation budgets around Wazero. Race instrumentation slows those
      # guests by orders of magnitude, so retain them as normal-test
      # performance gates and race-check the shared mutable primitives here.
      go test -race \
        -run '^(TestMemBus.*|TestMemStore.*|TestFileStore.*|TestTokenBucket.*)$' \
        .
    else
      "${command[@]}"
    fi
  )
done
