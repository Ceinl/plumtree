#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
workfile=$workspace_root/go.work

(
  cd "$workspace_root/runner"
  GOWORK="$workfile" go test -run '^(TestBaselineABIV4CounterArtifact|TestControlFilterStripsEscapes|TestControlFilterDropsC1AndKeepsUTF8|TestTextSinkRendersGrid|TestInvalidLimitsReturnErrorsInsteadOfPanicking|TestRemoteProcessRunnerRejectsWrongToken|TestProcessRunnerProxiesAllCapabilities)$' .
)
(
  cd "$workspace_root/pt"
  GOWORK="$workfile" go test -run '^(TestTerminalSafeTextStripsTerminalControls|TestWriteGoodbyeSanitizesTerminalControls|TestDeploySelectsConfiguredServerAlias)$' .
)
(
  cd "$workspace_root/ssh-gateway"
  GOWORK="$workfile" go test -run '^(TestOptionalAuthRequiresProofBeforeRecordingFingerprint|TestOptionalAuthHandshakeDoesNotTrustInvalidKeyProof|TestRunSessionProductionCLIUsesWorker)$' ./gateway
)
(
  cd "$workspace_root/control-plane"
  GOWORK="$workfile" go test -run '^(TestDevPingLogsLifecycleWithoutCredentials|TestDevSecretsRequireClaimToken)$' ./internal/httpapi
)
