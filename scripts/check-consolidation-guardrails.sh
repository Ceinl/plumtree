#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

"$workspace_root/scripts/check-abi-fixture.sh"
"$workspace_root/scripts/check-dependencies.sh"
"$workspace_root/scripts/check-removal-ratchet.sh"
"$workspace_root/scripts/check-journeys.sh"
"$workspace_root/scripts/check-external-consumers.sh"
"$workspace_root/scripts/test-guardrails.sh"
"$workspace_root/scripts/run-consolidation-journeys.sh"
