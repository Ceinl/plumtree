package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigParsesLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{
	  "publicOrigin": "https://plumtree.dev",
	  "sshHost": "plumtree.dev",
	  "maxAppsPerOwner": 5,
	  "maxSessionsPerAppPerDay": 100,
	  "maxDeploysPerHour": 100
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxAppsPerOwner != 5 {
		t.Errorf("MaxAppsPerOwner = %d, want 5", cfg.MaxAppsPerOwner)
	}
	if cfg.MaxSessionsPerAppPerDay != 100 {
		t.Errorf("MaxSessionsPerAppPerDay = %d, want 100", cfg.MaxSessionsPerAppPerDay)
	}
}

// The committed example config must stay loadable (DisallowUnknownFields means a
// stale field name would fail) and reflect the public-server limits.
func TestExampleConfigIsValid(t *testing.T) {
	cfg, err := loadConfig("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxAppsPerOwner != 5 || cfg.MaxSessionsPerAppPerDay != 100 {
		t.Fatalf("example limits = %d apps / %d sessions, want 5 / 100",
			cfg.MaxAppsPerOwner, cfg.MaxSessionsPerAppPerDay)
	}
}

func TestFirstPositiveInt(t *testing.T) {
	if got := firstPositiveInt(0, 0, 50); got != 50 {
		t.Errorf("firstPositiveInt(0,0,50) = %d, want 50", got)
	}
	if got := firstPositiveInt(100, 50); got != 100 {
		t.Errorf("firstPositiveInt(100,50) = %d, want 100", got)
	}
	if got := firstPositiveInt(0, 0); got != 0 {
		t.Errorf("firstPositiveInt(0,0) = %d, want 0", got)
	}
}
