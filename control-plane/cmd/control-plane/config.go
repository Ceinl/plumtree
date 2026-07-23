package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// fileConfig holds operator settings loaded from a JSON config file. Every field
// is optional; an unset field falls back to the matching environment variable
// and then the built-in default. Overall precedence is:
//
//	command-line flag > environment variable > config file > built-in default
//
// so a config file is a convenient baseline that flags/env can still override
// per run. Keep this in sync with config.example.json.
type fileConfig struct {
	// AutoClaimOwner claims new deploys directly to this owner handle without
	// Shoo. It is intended for trusted self-hosted servers only.
	AutoClaimOwner string `json:"autoClaimOwner"`
	// AllowHostCommands lets claimed apps execute local programs as the server
	// OS user. It is for trusted apps on private/self-hosted servers only.
	AllowHostCommands bool `json:"allowHostCommands"`
	// PublicOrigin is the dashboard/claim base URL advertised to authors, e.g.
	// "https://plumtree.dev". Maps to -origin / PLUMTREE_PUBLIC_ORIGIN.
	PublicOrigin string `json:"publicOrigin"`
	// SSHHost is the hostname apps are reached at over SSH, e.g. "plumtree.dev".
	// Maps to -ssh-host / PLUMTREE_SSH_HOST.
	SSHHost string `json:"sshHost"`
	// DeployClaimTTL is how long an unclaimed deploy may exist before it is
	// garbage-collected, as a Go duration string ("30s", "15m", "24h"). Empty
	// keeps the default. Maps to -deploy-claim-ttl / PLUMTREE_DEPLOY_CLAIM_TTL.
	DeployClaimTTL string `json:"deployClaimTtl"`
	// MaxAppsPerOwner caps how many apps a single owner may create. 0 means
	// unlimited. Maps to -max-apps-per-owner / PLUMTREE_MAX_APPS_PER_OWNER.
	MaxAppsPerOwner int `json:"maxAppsPerOwner,omitempty"` // default 25
	// MaxSessionsPerAppPerDay caps new sessions (SSH connections) per app in any
	// rolling 24h. 0 means unlimited; unset falls back to the built-in default.
	// Maps to -max-sessions-per-app-day / PLUMTREE_MAX_SESSIONS_PER_APP_DAY.
	MaxSessionsPerAppPerDay int `json:"maxSessionsPerAppPerDay"`
	// MaxDeploysPerHour caps new deploy claims across the platform per rolling
	// hour. 0 means unlimited. Maps to -max-deploys-per-hour.
	MaxDeploysPerHour int `json:"maxDeploysPerHour"`
}

// loadConfig reads the JSON config at path. An empty path returns a zero config
// (all defaults) with no error, so the file is entirely optional.
func loadConfig(path string) (fileConfig, error) {
	var cfg fileConfig
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %q: %w", path, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse config %q: %w", path, err)
	}
	if _, err := cfg.deployClaimTTL(); err != nil {
		return cfg, fmt.Errorf("config %q: %w", path, err)
	}
	return cfg, nil
}

// deployClaimTTL parses DeployClaimTTL, returning 0 when unset so callers can
// substitute their own default.
func (c fileConfig) deployClaimTTL() (time.Duration, error) {
	if strings.TrimSpace(c.DeployClaimTTL) == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(c.DeployClaimTTL)
	if err != nil {
		return 0, fmt.Errorf("invalid deployClaimTtl %q: %w", c.DeployClaimTTL, err)
	}
	if d <= 0 {
		return 0, errors.New("deployClaimTtl must be positive")
	}
	return d, nil
}

// configPathFromArgs resolves the config file path, honoring -config / --config
// on the command line (with or without "=") and falling back to PLUMTREE_CONFIG.
// It runs before flag.Parse so the file's values can seed flag defaults.
func configPathFromArgs(args []string, envPath string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-config="):
			return strings.TrimPrefix(a, "-config=")
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return envPath
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
