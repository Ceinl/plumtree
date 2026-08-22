//go:build !darwin && !linux

package config

// HostMemory uses the documented detection-failure capacity baseline on
// targets without a native memory query.
func HostMemory() int64 { return 2 << 30 }
