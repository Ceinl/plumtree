package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	MinMemoryBytes int64 = 512 << 20
	MaxMemoryBytes int64 = 8 << 30
)

// CapacityFromMemory clamps host/cgroup memory to the supported range and
// derives bounded role capacities. The formulas are deterministic and do not
// inspect process-global flags or environment variables.
func CapacityFromMemory(memoryBytes int64) Capacity {
	if memoryBytes < MinMemoryBytes {
		memoryBytes = MinMemoryBytes
	}
	if memoryBytes > MaxMemoryBytes {
		memoryBytes = MaxMemoryBytes
	}
	units := memoryBytes / (128 << 20)
	if units < 4 {
		units = 4
	}
	return Capacity{MaxSessions: int(units * 4), MaxWorkers: int(units), MaxBuilds: int((units + 3) / 4)}
}

// MaterializeCapacity resolves adaptive capacity from the cgroup limit and,
// when no cgroup limit is available, the supplied host-memory value. An
// explicit capacity is preserved unchanged.
func MaterializeCapacity(c Config, read func(string) ([]byte, error), hostMemory int64) (Config, error) {
	if err := c.Validate(); err != nil {
		return c, err
	}
	if !c.Resources.AutoCapacity {
		return c, nil
	}
	memory := MemoryLimitFromCgroup(read)
	if memory == 0 {
		memory = hostMemory
	}
	if memory <= 0 {
		return c, fmt.Errorf("%w: unable to determine memory limit", ErrInvalid)
	}
	c.Resources.MemoryLimitBytes = memory
	c.Resources.Capacity = CapacityFromMemory(memory)
	return c, nil
}

func ReadSecretFile(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty secret path", ErrInvalid)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%w: secret file", ErrUnsafePath)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: empty secret file", ErrInvalid)
	}
	return b, nil
}

// SecretForRole is deliberately role-aware: irrelevant references are not
// opened, so a malformed secret for another role cannot break startup.
func SecretForRole(c Config, role RoleName) ([]byte, error) {
	var path string
	switch role {
	case RoleControl:
		path = c.Secrets.DatabaseKeyFile
	case RoleGateway:
		path = c.Secrets.GatewayTokenFile
	case RoleRunner:
		path = c.Secrets.RunnerTokenFile
	default:
		return nil, fmt.Errorf("%w: role", ErrInvalid)
	}
	if path == "" {
		return nil, nil
	}
	return ReadSecretFile(path)
}

func MemoryLimitFromCgroup(read func(string) ([]byte, error)) int64 {
	if read == nil {
		return 0
	}
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := read(path)
		if err != nil {
			continue
		}
		s := string(b)
		if s == "max" {
			continue
		}
		var value int64
		if _, err := fmt.Sscan(s, &value); err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func ValidatePathReference(path string) error {
	if path == "" {
		return nil
	}
	if filepath.Clean(path) == "." || filepath.IsAbs(path) == false && path == ".." {
		return fmt.Errorf("%w: path reference", ErrUnsafePath)
	}
	return nil
}
