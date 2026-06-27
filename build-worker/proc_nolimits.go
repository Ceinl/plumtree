//go:build !linux

package buildworker

import "time"

// applySandboxLimits is a no-op on platforms without prlimit-based rlimit
// support wired up. The wall-clock timeout still bounds builds, and production
// runs on Linux where the real limits apply.
func applySandboxLimits(pid int, maxMemoryBytes int64, timeout time.Duration) {}
