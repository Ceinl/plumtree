//go:build qualification

package qualification_test

import (
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestComposeReleaseJourney qualifies the shipped production topology. It
// uses the public Compose file, a built author binary, SSH, and offline state
// commands. A selected gate fails when Docker or Compose is not available.
func TestComposeReleaseJourney(t *testing.T) {
	pt := requiredExecutable(t, "PLUMTREE_QUALIFY_PT")
	docker := requiredTool(t, "docker")
	ssh := requiredTool(t, "ssh")
	sshKeygen := requiredTool(t, "ssh-keygen")

	repository := requiredDirectory(t, "PLUMTREE_QUALIFY_REPOSITORY")
	composeFile := filepath.Join(repository, "deploy", "docker-compose.yml")
	root := t.TempDir()
	databaseKeyPath := filepath.Join(root, "database.key")
	runnerTokenPath := filepath.Join(root, "runner.token")
	databaseKey := make([]byte, 32)
	if _, err := rand.Read(databaseKey); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databaseKeyPath, databaseKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runnerTokenPath, []byte("qualification-compose-runner-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	endpoint := availableTCPAddress(t)
	project := fmt.Sprintf("plumtreeq%d", time.Now().UnixNano())
	composeEnv := append(os.Environ(),
		"COMPOSE_PROJECT_NAME="+project,
		"PLUMTREE_DATABASE_KEY_FILE="+databaseKeyPath,
		"PLUMTREE_RUNNER_TOKEN_FILE="+runnerTokenPath,
		"PLUMTREE_SSH_PUBLISH_ADDR="+endpoint,
		"PLUMTREE_PRODUCT_VERSION=qualification",
	)
	compose := func(timeout time.Duration, args ...string) command {
		base := []string{"compose", "-f", composeFile}
		return command{path: docker, dir: repository, env: composeEnv, args: append(base, args...), timeout: timeout}
	}

	runOK(t, command{path: docker, env: composeEnv, args: []string{"info"}})
	runOK(t, command{path: docker, env: composeEnv, args: []string{"compose", "version"}})
	t.Cleanup(func() {
		stdout, stderr, err := run(compose(2*time.Minute, "down", "--volumes", "--remove-orphans"))
		if err != nil {
			t.Logf("Compose cleanup failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	resolved := runOK(t, compose(time.Minute, "config"))
	assertContains(t, resolved, "network_mode: none")
	if strings.Count(resolved, "published:") != 1 {
		t.Fatalf("Compose must publish only the public SSH port:\n%s", resolved)
	}
	runOK(t, compose(10*time.Minute, "build", "--pull"))

	bootstrapJSON := runOK(t, compose(2*time.Minute, "run", "--rm", "--no-deps", "plumtree", "bootstrap", "--config", "/etc/plumtree/config.json", "-handle", "alice", "-device", "laptop"))
	var bootstrap struct {
		ID     string `json:"bootstrapID"`
		Secret string `json:"secret"`
	}
	decodeJSON(t, bootstrapJSON, &bootstrap)
	if bootstrap.ID == "" || bootstrap.Secret == "" {
		t.Fatalf("bootstrap returned an incomplete authority: %s", bootstrapJSON)
	}

	runOK(t, compose(2*time.Minute, "up", "-d"))
	waitForTCP(t, endpoint)
	runnerID := strings.TrimSpace(runOK(t, compose(time.Minute, "ps", "-q", "runner")))
	if runnerID == "" {
		t.Fatal("Compose runner container is absent")
	}
	mode := strings.TrimSpace(runOK(t, command{path: docker, env: composeEnv, args: []string{"inspect", "--format", "{{.HostConfig.NetworkMode}}", runnerID}}))
	if mode != "none" {
		t.Fatalf("runner network mode = %q, want none", mode)
	}

	serversPath := filepath.Join(root, "author", "servers.json")
	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	authorEnv := append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+filepath.Join(root, "xdg"), "PLUMTREE_PT_SERVERS_FILE="+serversPath)
	runOK(t, command{path: pt, env: authorEnv, args: []string{"pair", "--bootstrap", bootstrap.ID, "--secret", bootstrap.Secret, "--next-recovery-secret", "qualification-recovery-secret-000002", "--name", "compose", "--device", "laptop", "--yes", "--json", endpoint}})
	waitForStatus(t, pt, authorEnv)
	assertRemovedCommands(t, pt, authorEnv)

	runOK(t, command{path: pt, dir: projects, env: authorEnv, args: []string{"new", "counter", "--tui", "--access", "public"}})
	runOK(t, command{path: pt, dir: projects, env: authorEnv, args: []string{"new", "hello-cli", "--cli", "--access", "restricted"}})
	tuiProject := filepath.Join(projects, "counter")
	cliProject := filepath.Join(projects, "hello-cli")
	assertContains(t, runOK(t, command{path: pt, dir: tuiProject, env: authorEnv, args: []string{"dev", "--headless", "--script", "up,up,q"}}), "Count: 2")
	assertContains(t, runOK(t, command{path: pt, dir: cliProject, env: authorEnv, args: []string{"dev", "Dima"}}), "Hello Dima")
	runOK(t, command{path: pt, dir: tuiProject, env: authorEnv, args: []string{"build"}})
	runOK(t, command{path: pt, dir: cliProject, env: authorEnv, args: []string{"build", "--json"}})
	runOK(t, command{path: pt, dir: tuiProject, env: authorEnv, args: []string{"deploy", "--yes", "--json"}})
	runOK(t, command{path: pt, dir: cliProject, env: authorEnv, args: []string{"deploy", "--yes", "--json"}})

	apps := listApps(t, pt, authorEnv)
	counterID, helloID := apps["counter"], apps["hello-cli"]
	if counterID == "" || helloID == "" {
		t.Fatalf("deployed apps = %#v", apps)
	}
	record := readCurrentServer(t, serversPath)
	deviceKey := filepath.Join(filepath.Dir(serversPath), "keys", record.KeyRef)
	assertContains(t, runOK(t, command{path: ssh, env: authorEnv, timeout: 20 * time.Second, args: sshArgs(endpoint, deviceKey, "alice/hello-cli", "Dima")}), "Hello Dima")
	assertContains(t, runTUISSH(t, endpoint, deviceKey, "alice/counter", "Count: 0"), "Count: 0")
	runManagementJourney(t, pt, sshKeygen, authorEnv, endpoint, root, helloID)
	assertContains(t, runOK(t, command{path: pt, env: authorEnv, args: []string{"logs", helloID}}), "sessions")

	runOK(t, compose(2*time.Minute, "restart", "runner", "plumtree"))
	waitForStatus(t, pt, authorEnv)
	runOK(t, command{path: ssh, env: authorEnv, timeout: 20 * time.Second, args: sshArgs(endpoint, deviceKey, "alice/hello-cli", "restart")})

	runOK(t, compose(time.Minute, "stop", "plumtree"))
	inventory := runOK(t, compose(2*time.Minute, "run", "--rm", "--no-deps", "plumtree", "state", "inventory", "--config", "/etc/plumtree/config.json"))
	assertContains(t, inventory, `"encrypted":true`)
	runOK(t, compose(2*time.Minute, "run", "--rm", "--no-deps", "plumtree", "state", "backup", "--config", "/etc/plumtree/config.json", "--output", "/data/qualification-backup"))

	runOK(t, compose(time.Minute, "start", "plumtree"))
	waitForStatus(t, pt, authorEnv)
	runOK(t, command{path: pt, env: authorEnv, args: []string{"secret", "rm", helloID, "API_TOKEN", "--yes"}})
	runOK(t, compose(time.Minute, "stop", "plumtree"))
	runOK(t, compose(2*time.Minute, "run", "--rm", "--no-deps", "plumtree", "state", "restore", "--config", "/etc/plumtree/config.json", "--input", "/data/qualification-backup", "--yes"))
	runOK(t, compose(time.Minute, "start", "plumtree"))
	waitForStatus(t, pt, authorEnv)
	if got := listApps(t, pt, authorEnv); got["counter"] != counterID || got["hello-cli"] != helloID {
		t.Fatalf("restored apps = %#v, want original IDs", got)
	}
	assertContains(t, runOK(t, command{path: pt, env: authorEnv, args: []string{"secret", "list", helloID}}), "API_TOKEN")
	runOK(t, command{path: ssh, env: authorEnv, timeout: 20 * time.Second, args: sshArgs(endpoint, deviceKey, "alice/hello-cli", "restored")})
}

func requiredDirectory(t *testing.T, name string) string {
	t.Helper()
	path := os.Getenv(name)
	if path == "" {
		t.Fatalf("%s must point to the repository; the qualification gate cannot skip it", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s is not a directory: %q", name, path)
	}
	return abs
}

func waitForTCP(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", endpoint, 500*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("Compose SSH endpoint %s did not become ready", endpoint)
}

func waitForStatus(t *testing.T, pt string, env []string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var stdout, stderr string
	var err error
	for time.Now().Before(deadline) {
		stdout, stderr, err = run(command{path: pt, env: env, args: []string{"status", "--json"}, timeout: 5 * time.Second})
		if err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("author API did not become ready: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
}
