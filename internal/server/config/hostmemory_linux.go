//go:build linux

package config

import "golang.org/x/sys/unix"

func HostMemory() int64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 2 << 30
	}
	return int64(info.Totalram) * int64(info.Unit)
}
