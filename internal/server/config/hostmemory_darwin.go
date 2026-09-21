//go:build darwin

package config

import "golang.org/x/sys/unix"

func HostMemory() int64 {
	value, err := unix.SysctlUint64("hw.memsize")
	if err != nil || value > uint64(^uint64(0)>>1) {
		return 2 << 30
	}
	return int64(value)
}
