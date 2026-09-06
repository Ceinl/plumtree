package config

import (
	"os"
	"testing"
)

func TestSessionCapacityReservesHostMemory(t *testing.T) {
	for _, tc := range []struct {
		name                string
		memory              int64
		pages, policy, want int
	}{
		{"512 MiB", 512 << 20, 512, 64, 4},
		{"1 GiB", 1 << 30, 512, 64, 8},
		{"larger guest", 1 << 30, 2048, 64, 3},
		{"lower policy", 1 << 30, 512, 2, 2},
		{"unlimited policy", 1 << 30, 512, 0, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Limits.MemoryPages, c.Limits.MaxSessions = tc.pages, tc.policy
			c, err := MaterializeCapacity(c, func(string) ([]byte, error) { return nil, os.ErrNotExist }, tc.memory)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.SessionCapacity(); got != tc.want {
				t.Fatalf("capacity=%d, want %d", got, tc.want)
			}
			if c.Limits.MaxSessions != tc.policy {
				t.Fatal("configured policy was overwritten")
			}
		})
	}
	c := Default()
	c.Resources.AutoCapacity = false
	c.Resources.Capacity = Capacity{MaxSessions: 7, MaxWorkers: 3}
	if c.SessionCapacity() != 7 || c.WorkerCapacity() != 3 {
		t.Fatal("explicit capacity was not enforced")
	}
}

func TestCapacityRejectsMemoryTooSmallForGuest(t *testing.T) {
	c := Default()
	_, err := MaterializeCapacity(c, func(string) ([]byte, error) { return []byte("67108864"), nil }, 1<<30)
	if err == nil {
		t.Fatal("cgroup too small for guest was accepted")
	}
}
