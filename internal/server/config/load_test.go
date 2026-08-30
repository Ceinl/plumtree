package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMaterializesFlagEnvironmentConfigDefaultPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	stored := Default()
	stored.Storage.DatabasePath = "from-config.db"
	stored.Limits.MaxSessions = 70
	if err := Write(path, stored); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(LoadOptions{
		Path: path,
		Environment: map[string]string{
			"PLUMTREE_STORAGE_DATABASE_PATH": "from-environment.db",
			"PLUMTREE_LIMITS_MAX_SESSIONS":   "71",
		},
		Flags: map[string]string{
			"storage.databasePath": "from-flag.db",
		},
		ReadFile:   os.ReadFile,
		HostMemory: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Storage.DatabasePath != "from-flag.db" {
		t.Fatalf("database path = %q", loaded.Config.Storage.DatabasePath)
	}
	if loaded.Sources["storage.databasePath"] != SourceFlag {
		t.Fatalf("database path source = %q", loaded.Sources["storage.databasePath"])
	}
	if loaded.Config.Limits.MaxSessions != 71 || loaded.Sources["limits.maxSessions"] != SourceEnvironment {
		t.Fatalf("max sessions = %d source=%q", loaded.Config.Limits.MaxSessions, loaded.Sources["limits.maxSessions"])
	}
	if loaded.Config.Limits.MaxFPS != Default().Limits.MaxFPS || loaded.Sources["limits.maxFPS"] != SourceConfig {
		t.Fatalf("max FPS = %d source=%q", loaded.Config.Limits.MaxFPS, loaded.Sources["limits.maxFPS"])
	}
	if loaded.Config.Resources.Capacity.MaxWorkers != 8 {
		t.Fatalf("capacity = %+v", loaded.Config.Resources.Capacity)
	}
}

func TestProductionValidationFailsClosed(t *testing.T) {
	c := Default()
	c.Roles.Gateway = false
	c.Runtime.Production = true
	if err := c.ValidateProduction(); err == nil {
		t.Fatal("production without a database key was accepted")
	}
	c.Secrets.DatabaseKeyFile = filepath.Join(t.TempDir(), "database.key")
	c.Limits.MaxSessions = 0
	if err := c.ValidateProduction(); err == nil {
		t.Fatal("production with an unlimited critical limit was accepted")
	}
	c.Runtime.AcknowledgeUnlimitedLimits = true
	if err := c.ValidateProduction(); err != nil {
		t.Fatalf("explicit unlimited policy was rejected: %v", err)
	}
}

func TestProductionValidationRejectsEveryUnlimitedCriticalLimit(t *testing.T) {
	tests := []struct {
		name string
		zero func(*Config)
	}{
		{name: "max queued builds", zero: func(c *Config) { c.Limits.MaxQueuedBuilds = 0 }},
		{name: "max FPS", zero: func(c *Config) { c.Limits.MaxFPS = 0 }},
		{name: "rate burst", zero: func(c *Config) { c.Limits.RateBurst = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := Default()
			c.Runtime.Production = true
			c.Secrets.DatabaseKeyFile = filepath.Join(t.TempDir(), "database.key")
			test.zero(&c)
			if err := c.ValidateProduction(); err == nil {
				t.Fatal("production accepted an unlimited critical limit")
			}
		})
	}
}

func TestValidationRequiresShutdownTimeout(t *testing.T) {
	c := Default()
	c.Roles.Gateway = false
	c.Runtime.ShutdownTimeout = ""
	if err := c.Validate(); err == nil {
		t.Fatal("empty shutdown timeout was accepted")
	}
}

func TestControlProjectionDoesNotReadDisabledRoleSecrets(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "database.key")
	key := []byte("0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.Roles.Control = true
	c.Roles.Gateway = false
	c.Secrets.DatabaseKeyFile = keyPath
	c.Secrets.GatewayTokenFile = filepath.Join(dir, "missing-gateway-token")

	projection, err := MaterializeRole(c, RoleControl)
	if err != nil {
		t.Fatal(err)
	}
	if string(projection.Secret()) != string(key) {
		t.Fatalf("database key = %q", projection.Secret())
	}
	if _, err := MaterializeRole(c, RoleGateway); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled gateway error = %v", err)
	}
}

func TestLoadRejectsInvalidEnvironmentInsteadOfFallingBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Write(path, Default()); err != nil {
		t.Fatal(err)
	}
	_, err := Load(LoadOptions{Path: path, Environment: map[string]string{
		"PLUMTREE_LIMITS_MAX_SESSIONS": "many",
	}, HostMemory: 1 << 30})
	if err == nil {
		t.Fatal("invalid environment value was accepted")
	}
}

// Production must refuse the cleartext tcp:// runner transport by name while
// accepting both encrypted transports.
func TestProductionValidationRefusesPlainTCPRunnerEndpoint(t *testing.T) {
	tests := []struct {
		name, endpoint string
		accepted       bool
	}{
		{name: "unix socket", endpoint: "unix:///run/plumtree/runner.sock", accepted: true},
		{name: "tls", endpoint: "tls://broker.internal:7947", accepted: true},
		{name: "plain tcp", endpoint: "tcp://broker.internal:7947"},
	}
	for _, gatewayRole := range []bool{true, false} {
		for _, test := range tests {
			c := Default()
			c.Runtime.Production = true
			c.Roles = Roles{Control: false, Gateway: gatewayRole, Runner: !gatewayRole}
			c.Secrets.DatabaseKeyFile = filepath.Join(t.TempDir(), "database.key")
			c.Secrets.RunnerTokenFile = filepath.Join(t.TempDir(), "runner.token")
			if gatewayRole {
				c.Secrets.GatewayTokenFile = filepath.Join(t.TempDir(), "gateway.token")
			}
			c.Runtime.RunnerWorker = "/usr/local/bin/runner-worker"
			c.Runtime.RunnerEndpoint = test.endpoint
			err := c.ValidateProduction()
			if test.accepted && err != nil {
				t.Errorf("%s (gateway=%t): %v", test.name, gatewayRole, err)
			}
			if !test.accepted {
				if err == nil {
					t.Errorf("%s (gateway=%t): plain tcp:// was accepted", test.name, gatewayRole)
				} else if !strings.Contains(err.Error(), "tcp://") {
					t.Errorf("%s (gateway=%t): error %q does not name tcp://", test.name, gatewayRole, err)
				}
			}
		}
	}
}
