package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBootstrapStrictPermissionsAndCreateOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	first, created, err := Bootstrap(path)
	if err != nil || !created || first.Version != FormatVersion {
		t.Fatalf("bootstrap = %+v created=%v err=%v", first, created, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	second, created, err := Bootstrap(path)
	if err != nil || created || second.Version != first.Version {
		t.Fatalf("second bootstrap = %+v created=%v err=%v", second, created, err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown field error=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1}{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("trailing document error=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate field error=%v", err)
	}
}

func TestConcurrentAtomicUpdatesAndPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, _, err := Bootstrap(path); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Update(path, func(c *Config) error { c.Limits.MaxSessions++; return nil }); err != nil {
				t.Errorf("update: %v", err)
			}
		}()
	}
	wg.Wait()
	c, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.MaxSessions != 76 {
		t.Fatalf("max sessions=%d", c.Limits.MaxSessions)
	}
	if value, source := ResolveString("default", "config", "env", "flag"); value != "flag" || source != SourceFlag {
		t.Fatalf("string precedence=%q/%s", value, source)
	}
	if value, source := ResolveString("default", "config", "env", ""); value != "env" || source != SourceEnvironment {
		t.Fatalf("env precedence=%q/%s", value, source)
	}
	if value, source := ResolveInt(1, 2, 3, 4, true, true, false); value != 3 || source != SourceEnvironment {
		t.Fatalf("int precedence=%d/%s", value, source)
	}
	overridden, provenance, err := ApplyOverrides(Default(),
		map[string]string{"PLUMTREE_LIMITS_MAX_SESSIONS": "70"},
		map[string]string{"limits.maxSessions": "71"})
	if err != nil || overridden.Limits.MaxSessions != 71 || provenance["limits.maxSessions"] != SourceFlag {
		t.Fatalf("overrides=%+v provenance=%v err=%v", overridden.Limits, provenance["limits.maxSessions"], err)
	}
}

func TestCapacitySecretsAndRoleProjections(t *testing.T) {
	if got := CapacityFromMemory(1); got.MaxSessions != 16 || got.MaxWorkers != 4 || got.MaxBuilds != 1 {
		t.Fatalf("low capacity=%+v", got)
	}
	if got := CapacityFromMemory(64 << 30); got.MaxSessions != 256 || got.MaxWorkers != 64 || got.MaxBuilds != 16 {
		t.Fatalf("high capacity=%+v", got)
	}
	c := Default()
	c.Roles = Roles{Control: true, Gateway: true, Runner: true}
	c.Exposure.HTTP = ExposureGate{Enabled: true, Address: "127.0.0.1:8080"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	composition, err := Compose(c)
	if err != nil {
		t.Fatal(err)
	}
	if composition.Control().Name() != RoleControl || composition.Gateway().Name() != RoleGateway || composition.Runner().Name() != RoleRunner {
		t.Fatalf("roles=%s,%s,%s", composition.Control().Name(), composition.Gateway().Name(), composition.Runner().Name())
	}
	path := filepath.Join(t.TempDir(), "missing-secret")
	c.Secrets.GatewayTokenFile = path
	if value, err := SecretForRole(c, RoleControl); err != nil || value != nil {
		t.Fatalf("irrelevant secret read=%q err=%v", value, err)
	}
	if _, err := SecretForRole(c, RoleGateway); err == nil {
		t.Fatal("gateway missing secret accepted")
	}
	materialized, err := MaterializeCapacity(c, func(string) ([]byte, error) { return nil, os.ErrNotExist }, 1<<30)
	if err != nil || materialized.Resources.MemoryLimitBytes != 1<<30 || materialized.Resources.Capacity.MaxWorkers != 8 {
		t.Fatalf("materialized=%+v err=%v", materialized.Resources, err)
	}
	if got := Diagnostics(Default()); len(got) == 0 {
		t.Fatal("expected warning diagnostics")
	}
	if got := ChangesRequireRestart(c, materialized); !got.RestartRequired {
		t.Fatalf("expected restart-only change: %+v", got)
	}
}

type testComponent struct {
	name      string
	events    *[]string
	failReady bool
}

func (c testComponent) Start(context.Context) error {
	*c.events = append(*c.events, c.name+":start")
	return nil
}
func (c testComponent) Ready(context.Context) error {
	*c.events = append(*c.events, c.name+":ready")
	if c.failReady {
		return errors.New(c.name + " ready")
	}
	return nil
}
func (c testComponent) Stop(context.Context) error {
	*c.events = append(*c.events, c.name+":stop")
	return nil
}

func TestLifecycleReadinessRollbackAndBoundedStop(t *testing.T) {
	var events []string
	l := NewLifecycle(testComponent{name: "control", events: &events}, testComponent{name: "gateway", events: &events, failReady: true})
	if err := l.Start(context.Background()); err == nil {
		t.Fatal("failed readiness accepted")
	}
	want := []string{"control:start", "control:ready", "gateway:start", "gateway:ready", "gateway:stop", "control:stop"}
	if len(events) != len(want) {
		t.Fatalf("events=%v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
	l = NewLifecycle(testComponent{name: "runner", events: &events})
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := StopWithin(l, context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleRunDrainsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var events []string
	l := NewLifecycle(testComponent{name: "control", events: &events})
	cancel()
	if err := l.Run(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("run error=%v", err)
	}
	if len(events) == 0 || events[len(events)-1] != "control:stop" {
		t.Fatalf("events=%v", events)
	}
}

type failingComponent struct {
	testComponent
	failures chan error
}

func (c failingComponent) Errors() <-chan error { return c.failures }

func TestLifecycleRunFailsAsOneUnitOnComponentError(t *testing.T) {
	var events []string
	failures := make(chan error, 1)
	failures <- errors.New("listener failed")
	l := NewLifecycle(failingComponent{
		testComponent: testComponent{name: "control", events: &events},
		failures:      failures,
	})
	err := l.Run(context.Background(), time.Second)
	if err == nil || err.Error() != "listener failed" {
		t.Fatalf("run error=%v", err)
	}
	if len(events) == 0 || events[len(events)-1] != "control:stop" {
		t.Fatalf("events=%v", events)
	}
}

func TestConfigShowRedactsSecretReferences(t *testing.T) {
	c := Default()
	c.Secrets.DatabaseKeyFile = "/private/database.key"
	redacted := c.Redacted()
	if redacted.Secrets.DatabaseKeyFile == c.Secrets.DatabaseKeyFile || redacted.Secrets.DatabaseKeyFile != "<redacted>" {
		t.Fatalf("redacted=%+v", redacted.Secrets)
	}
}

func TestConfigCommandsUseTypedAtomicEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, _, err := Bootstrap(path); err != nil {
		t.Fatal(err)
	}
	if err := RunConfigCommand([]string{"set", "--path", path, "--field", "limits.maxSessions", "--value", "80"}, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunConfigCommand([]string{"show", "--path", path}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"maxSessions": 80`)) {
		t.Fatalf("show output=%s", output.Bytes())
	}
	if err := RunConfigCommand([]string{"unset", "--path", path, "--field", "limits.maxSessions"}, bytes.NewBuffer(nil)); err != nil {
		t.Fatal(err)
	}
	c, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.MaxSessions != Default().Limits.MaxSessions {
		t.Fatalf("unset max sessions=%d", c.Limits.MaxSessions)
	}
}
