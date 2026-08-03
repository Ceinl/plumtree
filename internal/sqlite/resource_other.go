//go:build !linux

package sqlite

import "runtime"

// Other targets do not share Linux's stable /proc RSS interface. Sys is the
// bounded Go runtime memory proxy used by local qualification on those hosts.
func processMemoryBytes() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}
