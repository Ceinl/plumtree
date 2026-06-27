//go:build linux

package buildworker

import (
	"syscall"
	"time"
	"unsafe"
)

// applySandboxLimits caps the build process group's address space (RLIMIT_AS)
// and CPU-seconds (RLIMIT_CPU) right after start. Limits are inherited by the
// compile/link children the go driver spawns, so a pathological or hostile
// source cannot exhaust the worker's memory or peg a core indefinitely. The
// limits are applied via prlimit on the live process; the sub-millisecond window
// before they take effect is immaterial for memory and CPU-seconds.
//
// This is defense in depth: production should additionally run the worker in a
// container with no network and a tmpfs work dir. Here we enforce what we can
// from the worker process itself.
func applySandboxLimits(pid int, maxMemoryBytes int64, timeout time.Duration) {
	if maxMemoryBytes > 0 {
		lim := syscall.Rlimit{Cur: uint64(maxMemoryBytes), Max: uint64(maxMemoryBytes)}
		prlimit(pid, rlimitAS, &lim)
	}
	// Give CPU-seconds a hard ceiling a bit above the wall-clock budget so a
	// busy-looping compile is killed even if it never yields for the wall clock.
	cpuSecs := uint64(timeout/time.Second) + 5
	cpuLim := syscall.Rlimit{Cur: cpuSecs, Max: cpuSecs}
	prlimit(pid, rlimitCPU, &cpuLim)
}

const (
	rlimitCPU = 0 // RLIMIT_CPU
	rlimitAS  = 9 // RLIMIT_AS
)

// prlimit sets a resource limit on another live process via the prlimit64
// syscall. Best effort: failures (e.g. the process already exited) are ignored.
func prlimit(pid, resource int, newLimit *syscall.Rlimit) {
	_, _, _ = syscall.Syscall6(
		syscall.SYS_PRLIMIT64,
		uintptr(pid),
		uintptr(resource),
		uintptr(unsafe.Pointer(newLimit)),
		0, 0, 0,
	)
}
