package cleanrole

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

type channelWriter chan string

func (w channelWriter) Write(data []byte) (int, error) {
	select {
	case w <- string(data):
	default:
	}
	return len(data), nil
}

func TestConfigCommandChangesTheNextSelectedRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, _, err := serverconfig.Bootstrap(path); err != nil {
		t.Fatal(err)
	}
	if err := Execute(context.Background(), []string{"config", "set", "--config", path, "limits.maxSessions", "70"}, nil, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveServe([]string{"serve", "--config", path, "--limits-max-sessions", "72"}, []string{
		"PLUMTREE_LIMITS_MAX_SESSIONS=71",
	}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Limits.MaxSessions != 72 || resolved.Sources["limits.maxSessions"] != serverconfig.SourceFlag {
		t.Fatalf("max sessions = %d source=%q", resolved.Config.Limits.MaxSessions, resolved.Sources["limits.maxSessions"])
	}
	stored, err := serverconfig.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Limits.MaxSessions != 70 {
		t.Fatalf("stored max sessions = %d", stored.Limits.MaxSessions)
	}
}

func TestResolveServeMakesConfigPathsAbsolute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := serverconfig.Default()
	c.Storage.DatabasePath = "state/plumtree.db"
	c.Storage.SSHIdentity = "state/host_key"
	if err := serverconfig.Write(path, c); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveServe([]string{"serve", "--config", path}, nil, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Storage.DatabasePath != filepath.Join(dir, "state/plumtree.db") {
		t.Fatalf("database path = %q", resolved.Config.Storage.DatabasePath)
	}
	if resolved.Config.Storage.SSHIdentity != filepath.Join(dir, "state/host_key") {
		t.Fatalf("host key path = %q", resolved.Config.Storage.SSHIdentity)
	}
}

func TestResolveServeAcceptsHostKeyFlagAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if _, _, err := serverconfig.Bootstrap(path); err != nil {
		t.Fatal(err)
	}

	flagResolved, err := ResolveServe([]string{"serve", "--config", path, "--host-key", "flag_key"}, []string{"PLUMTREE_HOST_KEY=env_key"}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got := flagResolved.Config.Storage.SSHIdentity; got != filepath.Join(dir, "flag_key") {
		t.Fatalf("flag host key path = %q", got)
	}

	envResolved, err := ResolveServe([]string{"serve", "--config", path}, []string{"PLUMTREE_HOST_KEY=env_key"}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got := envResolved.Config.Storage.SSHIdentity; got != filepath.Join(dir, "env_key") {
		t.Fatalf("environment host key path = %q", got)
	}
}

// The host-command allowlist resolves from the manual flag alias, the
// environment, and persisted config, in that precedence order.
func TestResolveServeAcceptsHostCommandAllowlistFlagAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := serverconfig.Default()
	cfg.Runtime.HostCommandAllowlist = "from-config"
	if err := serverconfig.Write(path, cfg); err != nil {
		t.Fatal(err)
	}

	flagResolved, err := ResolveServe([]string{
		"serve", "--config", path,
		"--host-command-allowlist", "/usr/bin/uptime, pt-status",
	}, []string{"PLUMTREE_HOST_COMMAND_ALLOWLIST=from-env"}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got := flagResolved.Config.Runtime.HostCommandAllowlist; got != "/usr/bin/uptime, pt-status" {
		t.Fatalf("flag allowlist = %q", got)
	}
	if sources := flagResolved.Sources["runtime.hostCommandAllowlist"]; sources != serverconfig.SourceFlag {
		t.Fatalf("allowlist provenance = %q", sources)
	}

	envResolved, err := ResolveServe([]string{"serve", "--config", path}, []string{"PLUMTREE_HOST_COMMAND_ALLOWLIST=from-env"}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got := envResolved.Config.Runtime.HostCommandAllowlist; got != "from-env" {
		t.Fatalf("environment allowlist = %q", got)
	}

	configResolved, err := ResolveServe([]string{"serve", "--config", path}, nil, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if got := configResolved.Config.Runtime.HostCommandAllowlist; got != "from-config" {
		t.Fatalf("persisted allowlist = %q", got)
	}
}

func TestResolveServeReportsGeneratedConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gen.json")
	first, err := ResolveServe([]string{"serve", "--config", path}, nil, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ConfigCreated {
		t.Fatal("first resolve did not report a generated config")
	}
	second, err := ResolveServe([]string{"serve", "--config", path}, nil, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if second.ConfigCreated {
		t.Fatal("second resolve reported a generated config for an existing file")
	}
}

func TestExecuteWarnsWhenConfigIsGenerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.json")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(channelWriter, 1)
	errOut := &bytes.Buffer{}
	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, []string{"serve", "--config", path, "--exposure-ssh-address", "127.0.0.1:0"}, nil, ready, errOut)
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not become ready")
	}
	if got := errOut.String(); !strings.Contains(got, "warning: no config found at "+path) {
		t.Fatalf("stderr missing generation warning: %q", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not drain")
	}
}

func TestLoadOrCreateHostKeyRefusesCorruptFileAndLeavesItUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host_key")
	corrupt := []byte("definitely not a private key\n")
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := loadOrCreateHostKey(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "remove the file") {
		t.Fatalf("corrupt host key error = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.Equal(got, corrupt) {
		t.Fatalf("corrupt file was modified: %q, %v", got, readErr)
	}
}

func TestExecuteAnnouncesReadinessAndDrainsOnCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := serverconfig.Default()
	c.Storage.DatabasePath = "plumtree.db"
	c.Storage.SSHIdentity = "host_key"
	c.Exposure.SSH.Address = "127.0.0.1:0"
	if err := serverconfig.Write(path, c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(channelWriter, 1)
	done := make(chan error, 1)
	go func() {
		done <- Execute(ctx, []string{"serve", "--config", path}, nil, ready, &bytes.Buffer{})
	}()
	select {
	case message := <-ready:
		if !strings.Contains(message, "● ready") {
			t.Fatalf("readiness message = %q", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not drain")
	}
	if _, err := os.Stat(filepath.Join(dir, "plumtree.db")); err != nil {
		t.Fatalf("database was not created: %v", err)
	}
}

func TestProductionRepositoryUsesConfiguredKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "database.key")
	if err := os.WriteFile(keyPath, []byte("0123456789abcdef0123456789abcdef"), 0600); err != nil {
		t.Fatal(err)
	}
	c := serverconfig.Default()
	c.Runtime.Production = true
	c.Secrets.DatabaseKeyFile = keyPath
	c.Storage.DatabasePath = filepath.Join(dir, "plumtree.db")
	projection, err := serverconfig.MaterializeRole(c, serverconfig.RoleControl)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := openRepository(projection)
	if errors.Is(err, sqlite.ErrSQLCipherUnavailable) {
		t.Skip("SQLCipher is not available in this build")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	header, err := os.ReadFile(c.Storage.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(header, []byte("SQLite format 3")) {
		t.Fatal("production repository was created as plaintext SQLite")
	}
}

func TestControlStopReturnsWhenConnectionDoesNotDrain(t *testing.T) {
	component := &controlComponent{}
	component.wg.Add(1)
	t.Cleanup(component.wg.Done)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- component.Stop(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stop error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop waited for a stuck connection after its deadline")
	}
}
