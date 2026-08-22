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
		if !strings.Contains(message, "ready on") {
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
		return
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
