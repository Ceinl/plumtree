#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workfile=$workspace_root/go.work

run_tests() {
  local module=$1
  local package=$2
  shift 2

  local alternatives
  alternatives=$(printf '%s|' "$@")
  local pattern="^(${alternatives%|})$"
  local listed
  listed=$(
    cd "$workspace_root/$module"
    GOWORK="$workfile" go test -list "$pattern" "$package" | grep '^Test' || true
  )

  local expected
  for expected in "$@"; do
    if ! grep -F -x -q -- "$expected" <<<"$listed"; then
      echo "missing consolidation journey test in $module/$package: $expected" >&2
      exit 1
    fi
  done
  if [[ $(printf '%s\n' "$listed" | sed '/^$/d' | wc -l | tr -d ' ') != $# ]]; then
    echo "consolidation journey selection in $module/$package is not exact" >&2
    exit 1
  fi

  (
    cd "$workspace_root/$module"
    GOWORK="$workfile" go test -run "$pattern" "$package"
  )
}

run_tests internal/runner . \
  TestBaselineABIV4CounterArtifact \
  TestControlFilterStripsEscapes \
  TestControlFilterDropsC1AndKeepsUTF8 \
  TestTextSinkRendersGrid \
  TestInvalidLimitsReturnErrorsInsteadOfPanicking \
  TestRemoteProcessRunnerRejectsWrongToken \
  TestProcessRunnerProxiesAllCapabilities

run_tests pt . \
  TestTerminalSafeTextStripsTerminalControls \
  TestWriteGoodbyeSanitizesTerminalControls \
  TestDeploySelectsConfiguredServerAlias

run_tests internal/gateway . \
  TestOptionalAuthRequiresProofBeforeRecordingFingerprint \
  TestOptionalAuthHandshakeDoesNotTrustInvalidKeyProof \
  TestRunSessionProductionCLIUsesWorker

run_tests control-plane ./internal/httpapi \
  TestDevPingLogsLifecycleWithoutCredentials \
  TestDevSecretsRequireClaimToken
