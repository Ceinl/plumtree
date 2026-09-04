//go:build qualification

package qualification_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// TestNativeReleaseJourney qualifies only public product seams. The controller
// receives already-built release binaries and observes commands, SSH sessions,
// exit status, and durable operator state. It does not import product packages.
func TestNativeReleaseJourney(t *testing.T) {
	pt := requiredExecutable(t, "PLUMTREE_QUALIFY_PT")
	plumtree := requiredExecutable(t, "PLUMTREE_QUALIFY_SERVER")
	runnerWorker := requiredExecutable(t, "PLUMTREE_QUALIFY_RUNNER_WORKER")
	ssh := requiredTool(t, "ssh")
	sshKeygen := requiredTool(t, "ssh-keygen")

	root := t.TempDir()
	configPath := filepath.Join(root, "server", "config.json")
	databasePath := filepath.Join(root, "server", "plumtree.db")
	kvRoot := filepath.Join(root, "server", "kv")
	hostKeyPath := filepath.Join(root, "server", "host-key")
	databaseKeyPath := filepath.Join(root, "server", "database.key")
	runnerTokenPath := filepath.Join(root, "server", "runner.token")
	runnerSocketDir, err := os.MkdirTemp("/tmp", "plumtree-q-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runnerSocketDir) })
	runnerEndpoint := "unix://" + filepath.Join(runnerSocketDir, "runner.sock")
	serversPath := filepath.Join(root, "author", "servers.json")
	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(filepath.Dir(databaseKeyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	databaseKey := make([]byte, 32)
	if _, err := rand.Read(databaseKey); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(databaseKeyPath, databaseKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runnerTokenPath, []byte("qualification-isolated-runner-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}

	baseEnv := append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+filepath.Join(root, "xdg"), "PLUMTREE_PT_SERVERS_FILE="+serversPath)
	configure := func(field, value string) {
		t.Helper()
		runOK(t, command{path: plumtree, env: baseEnv, args: []string{"config", "set", "--config", configPath, field, value}})
	}
	configure("storage.databasePath", databasePath)
	configure("storage.kvRoot", kvRoot)
	configure("storage.sshIdentity", hostKeyPath)
	configure("exposure.ssh.address", availableTCPAddress(t))
	configure("secrets.databaseKeyFile", databaseKeyPath)
	configure("secrets.gatewayTokenFile", runnerTokenPath)
	configure("secrets.runnerTokenFile", runnerTokenPath)
	configure("runtime.runnerEndpoint", runnerEndpoint)
	configure("runtime.runnerWorker", runnerWorker)
	configure("runtime.runnerScratchRoot", filepath.Join(root, "server", "runner-scratch"))
	configure("runtime.production", "true")

	bootstrapJSON := runOK(t, command{path: plumtree, env: baseEnv, args: []string{"bootstrap", "--config", configPath, "-handle", "alice", "-device", "laptop", "--json"}})
	var bootstrap struct {
		ID     string `json:"bootstrapID"`
		Secret string `json:"secret"`
	}
	decodeJSON(t, bootstrapJSON, &bootstrap)
	if bootstrap.ID == "" || bootstrap.Secret == "" {
		t.Fatalf("bootstrap returned an incomplete authority: %s", bootstrapJSON)
	}

	server := startServer(t, plumtree, runnerWorker, configPath, baseEnv)
	t.Cleanup(func() { server.stop(t) })
	endpoint := server.endpoint
	pairOut := runOK(t, command{path: pt, env: baseEnv, args: []string{"pair", "--bootstrap", bootstrap.ID, "--secret", bootstrap.Secret, "--next-recovery-secret", "qualification-recovery-secret-000001", "--name", "native", "--device", "laptop", "--yes", "--json", endpoint}})
	assertContains(t, pairOut, `"authorHandle": "alice"`)
	runOK(t, command{path: pt, env: baseEnv, args: []string{"status", "--json"}})

	assertRemovedCommands(t, pt, baseEnv)
	runOK(t, command{path: pt, dir: projects, env: baseEnv, args: []string{"new", "counter", "--tui", "--access", "public"}})
	runOK(t, command{path: pt, dir: projects, env: baseEnv, args: []string{"new", "hello-cli", "--cli", "--access", "restricted"}})
	tuiProject := filepath.Join(projects, "counter")
	cliProject := filepath.Join(projects, "hello-cli")
	tuiDev := runOK(t, command{path: pt, dir: tuiProject, env: baseEnv, args: []string{"dev", "--headless", "--script", "up,up,q"}})
	assertContains(t, tuiDev, "Count: 2")
	cliDev := runOK(t, command{path: pt, dir: cliProject, env: baseEnv, args: []string{"dev", "Dima"}})
	assertContains(t, cliDev, "Hello Dima")
	runOK(t, command{path: pt, dir: tuiProject, env: baseEnv, args: []string{"build"}})
	runOK(t, command{path: pt, dir: cliProject, env: baseEnv, args: []string{"build", "--json"}})
	runOK(t, command{path: pt, dir: tuiProject, env: baseEnv, args: []string{"deploy", "--yes", "--json"}})
	runOK(t, command{path: pt, dir: cliProject, env: baseEnv, args: []string{"deploy", "--yes", "--json"}})

	apps := listApps(t, pt, baseEnv)
	counterID, helloID := apps["counter"], apps["hello-cli"]
	if counterID == "" || helloID == "" {
		t.Fatalf("deployed apps = %#v", apps)
	}
	record := readCurrentServer(t, serversPath)
	deviceKey := filepath.Join(filepath.Dir(serversPath), "keys", record.KeyRef)

	cliSSH := runOK(t, command{path: ssh, env: baseEnv, timeout: 15 * time.Second, args: sshArgs(endpoint, deviceKey, "alice/hello-cli", "Dima")})
	assertContains(t, cliSSH, "Hello Dima")
	tuiSSH := runTUISSH(t, endpoint, deviceKey, "alice/counter", "Count: 0")
	assertContains(t, tuiSSH, "Count: 0")

	runManagementJourney(t, pt, sshKeygen, baseEnv, endpoint, root, helloID)
	logs := runOK(t, command{path: pt, env: baseEnv, args: []string{"logs", helloID}})
	assertContains(t, logs, "sessions")

	server.stop(t)
	server = startServer(t, plumtree, runnerWorker, configPath, baseEnv)
	runOK(t, command{path: pt, env: baseEnv, args: []string{"status", "--json"}})
	runOK(t, command{path: ssh, env: baseEnv, timeout: 15 * time.Second, args: sshArgs(server.endpoint, deviceKey, "alice/hello-cli", "restart")})

	server.stop(t)
	inventory := runOK(t, command{path: plumtree, env: baseEnv, args: []string{"state", "inventory", "--config", configPath}})
	assertContains(t, inventory, `"encrypted":true`)
	databasePrefix, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(databasePrefix, []byte("SQLite format 3\x00")) {
		t.Fatal("production database has a plaintext SQLite header")
	}
	bundle := filepath.Join(root, "backup")
	runOK(t, command{path: plumtree, env: baseEnv, args: []string{"state", "backup", "--config", configPath, "--output", bundle}})

	server = startServer(t, plumtree, runnerWorker, configPath, baseEnv)
	runOK(t, command{path: pt, env: baseEnv, args: []string{"secret", "rm", helloID, "API_TOKEN", "--yes"}})
	server.stop(t)
	runOK(t, command{path: plumtree, env: baseEnv, args: []string{"state", "restore", "--config", configPath, "--input", bundle, "--yes"}})
	server = startServer(t, plumtree, runnerWorker, configPath, baseEnv)
	defer server.stop(t)
	if got := listApps(t, pt, baseEnv); got["counter"] != counterID || got["hello-cli"] != helloID {
		t.Fatalf("restored apps = %#v, want original IDs", got)
	}
	secrets := runOK(t, command{path: pt, env: baseEnv, args: []string{"secret", "list", helloID}})
	assertContains(t, secrets, "API_TOKEN")
	runOK(t, command{path: ssh, env: baseEnv, timeout: 15 * time.Second, args: sshArgs(server.endpoint, deviceKey, "alice/hello-cli", "restored")})
}

func runTUISSH(t *testing.T, endpoint, keyPath, user, readyText string) string {
	t.Helper()
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", endpoint, &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("dial TUI leaf: %v", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.RequestPty("xterm", 12, 40, ssh.TerminalModes{}); err != nil {
		t.Fatalf("request TUI PTY: %v", err)
	}
	output := newSessionOutput(readyText)
	session.Stdout, session.Stderr = output, output
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(""); err != nil {
		t.Fatalf("start TUI leaf: %v", err)
	}
	output.wait(5 * time.Second)
	if _, err := stdin.Write([]byte("q")); err != nil {
		t.Fatalf("quit TUI leaf: %v", err)
	}
	if err := session.Wait(); err != nil {
		t.Fatalf("run TUI leaf: %v\n%s", err, output.String())
	}
	return output.String()
}

type sessionOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	readyText []byte
	ready     chan struct{}
	once      sync.Once
}

func newSessionOutput(readyText string) *sessionOutput {
	return &sessionOutput{readyText: []byte(readyText), ready: make(chan struct{})}
}

func (o *sessionOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	n, err := o.buffer.Write(p)
	ready := bytes.Contains(o.buffer.Bytes(), o.readyText)
	o.mu.Unlock()
	if ready {
		o.once.Do(func() { close(o.ready) })
	}
	return n, err
}

func (o *sessionOutput) wait(timeout time.Duration) {
	select {
	case <-o.ready:
	case <-time.After(timeout):
	}
}

func (o *sessionOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buffer.String()
}

func runManagementJourney(t *testing.T, pt, sshKeygen string, env []string, endpoint, root, appID string) {
	t.Helper()
	runOK(t, command{path: pt, env: env, args: []string{"secret", "set", appID, "API_TOKEN", "private-value"}})
	secrets := runOK(t, command{path: pt, env: env, args: []string{"secret", "list", appID}})
	assertContains(t, secrets, "API_TOKEN")
	if strings.Contains(secrets, "private-value") {
		t.Fatal("secret list exposed secret value")
	}
	runOK(t, command{path: pt, env: env, args: []string{"egress", "add", appID, "api.example.com"}})
	assertContains(t, runOK(t, command{path: pt, env: env, args: []string{"egress", "list", appID}}), "api.example.com")

	guestKey := filepath.Join(root, "guest-key")
	runOK(t, command{path: sshKeygen, env: env, args: []string{"-q", "-t", "ed25519", "-N", "", "-f", guestKey}})
	publicKeyBytes, err := os.ReadFile(guestKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fingerprintOutput := runOK(t, command{path: sshKeygen, env: env, args: []string{"-lf", guestKey + ".pub", "-E", "sha256"}})
	fingerprintFields := strings.Fields(fingerprintOutput)
	if len(fingerprintFields) < 2 {
		t.Fatalf("ssh-keygen fingerprint = %q", fingerprintOutput)
	}
	runOK(t, command{path: pt, env: env, args: []string{"access", "add", appID, "guest", strings.TrimSpace(string(publicKeyBytes)), fingerprintFields[1]}})
	access := runOK(t, command{path: pt, env: env, args: []string{"access", "list", appID}})
	assertContains(t, access, "guest")
	var accessList struct {
		Keys []struct{ ID string }
	}
	decodeJSON(t, access, &accessList)
	if len(accessList.Keys) != 1 || accessList.Keys[0].ID == "" {
		t.Fatalf("access list = %s", access)
	}
	runOK(t, command{path: pt, env: env, args: []string{"access", "rm", appID, accessList.Keys[0].ID, "--yes"}})

	invitationJSON := runOK(t, command{path: pt, env: env, args: []string{"device", "invite", "phone"}})
	var invitation struct {
		Invitation struct {
			ID     string
			Secret string
		} `json:"invitation"`
	}
	decodeJSON(t, invitationJSON, &invitation)
	phoneStore := filepath.Join(root, "phone", "servers.json")
	phoneEnv := replaceEnvironment(env, "PLUMTREE_PT_SERVERS_FILE", phoneStore)
	runOK(t, command{path: pt, env: phoneEnv, args: []string{"pair", "--token", invitation.Invitation.ID, "--secret", invitation.Invitation.Secret, "--name", "native", "--device", "phone", "--yes", endpoint}})
	devicesJSON := runOK(t, command{path: pt, env: env, args: []string{"device", "list"}})
	var devices struct {
		Devices []struct {
			ID   string
			Name string
		}
	}
	decodeJSON(t, devicesJSON, &devices)
	phoneID := ""
	for _, device := range devices.Devices {
		if device.Name == "phone" {
			phoneID = device.ID
		}
	}
	if phoneID == "" {
		t.Fatalf("second device is absent: %s", devicesJSON)
	}
	runOK(t, command{path: pt, env: env, args: []string{"device", "revoke", phoneID, "--yes"}})
	assertContains(t, runOK(t, command{path: pt, env: env, args: []string{"audit"}}), "events")
}

func assertRemovedCommands(t *testing.T, pt string, env []string) {
	t.Helper()
	help := runOK(t, command{path: pt, env: env, args: []string{"--help"}})
	for _, removed := range []string{"claim", "preview", "dash" + "board", "bearer", "source" + " build"} {
		if strings.Contains(strings.ToLower(help), removed) {
			t.Fatalf("release help restored removed behavior %q: %s", removed, help)
		}
	}
	runFails(t, command{path: pt, env: env, args: []string{"claim"}})
}

type command struct {
	path    string
	args    []string
	dir     string
	env     []string
	input   string
	timeout time.Duration
}

func runOK(t *testing.T, item command) string {
	t.Helper()
	stdout, stderr, err := run(item)
	if err != nil {
		t.Fatalf("%s %s failed: %v\nstdout:\n%s\nstderr:\n%s", item.path, strings.Join(item.args, " "), err, stdout, stderr)
	}
	return stdout
}

func runFails(t *testing.T, item command) {
	t.Helper()
	if stdout, stderr, err := run(item); err == nil {
		t.Fatalf("%s %s unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", item.path, strings.Join(item.args, " "), stdout, stderr)
	}
}

func run(item command) (string, string, error) {
	timeout := item.timeout
	if timeout == 0 {
		timeout = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, item.path, item.args...)
	cmd.Dir, cmd.Env = item.dir, item.env
	cmd.Stdin = strings.NewReader(item.input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("deadline exceeded after %s", timeout)
	}
	return stdout.String(), stderr.String(), err
}

type liveServer struct {
	control  *managedProcess
	runner   *managedProcess
	endpoint string
	waited   bool
}

type managedProcess struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	waited bool
}

func startServer(t *testing.T, binary, runnerWorker, config string, env []string) *liveServer {
	t.Helper()
	runner, _ := startReadyProcess(t, binary, []string{"serve", "--config", config, "--roles-control=false", "--roles-gateway=false", "--roles-runner=true", "--runtime-runner-worker", runnerWorker, "--product-version", "qualification"}, env, "plumtree runner ready on ")
	t.Cleanup(func() { _ = runner.stop() })
	control, line := startReadyProcess(t, binary, []string{"serve", "--config", config, "--product-version", "qualification"}, env, " ready on ")
	fields := strings.Fields(line)
	endpoint := fields[len(fields)-1]
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		_ = control.stop()
		_ = runner.stop()
		t.Fatalf("invalid ready endpoint %q: %v", endpoint, err)
	}
	return &liveServer{control: control, runner: runner, endpoint: endpoint}
}

func startReadyProcess(t *testing.T, binary string, args, env []string, readiness string) (*managedProcess, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		sent := false
		for scanner.Scan() {
			line := scanner.Text()
			if !sent && strings.Contains(line, readiness) {
				lines <- line
				sent = true
			}
		}
		close(lines)
	}()
	select {
	case line := <-lines:
		if line == "" {
			_ = cmd.Process.Kill()
			t.Fatalf("process did not announce %q; stderr: %s", readiness, stderr)
		}
		return &managedProcess{cmd: cmd, stderr: stderr}, line
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		t.Fatalf("process readiness %q timed out; stderr: %s", readiness, stderr)
	}
	return nil, ""
}

func (s *liveServer) stop(t *testing.T) {
	t.Helper()
	if s == nil || s.waited {
		return
	}
	s.waited = true
	controlErr := s.control.stop()
	runnerErr := s.runner.stop()
	if controlErr != nil || runnerErr != nil {
		t.Fatalf("stop server: control=%v runner=%v", controlErr, runnerErr)
	}
}

func (p *managedProcess) stop() error {
	if p == nil || p.waited {
		return nil
	}
	p.waited = true
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("process exit: %w; stderr: %s", err, p.stderr)
		}
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		return fmt.Errorf("process did not stop; stderr: %s", p.stderr)
	}
	return nil
}

func listApps(t *testing.T, pt string, env []string) map[string]string {
	t.Helper()
	output := runOK(t, command{path: pt, env: env, args: []string{"app", "list"}})
	var value struct {
		Apps []struct{ ID, Name string }
	}
	decodeJSON(t, output, &value)
	result := make(map[string]string, len(value.Apps))
	for _, app := range value.Apps {
		result[app.Name] = app.ID
	}
	return result
}

type savedServer struct {
	KeyRef string `json:"keyRef"`
}

func readCurrentServer(t *testing.T, path string) savedServer {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var store struct {
		Current string        `json:"current"`
		Servers []savedServer `json:"servers"`
	}
	decodeJSON(t, string(data), &store)
	if len(store.Servers) != 1 || store.Current == "" || store.Servers[0].KeyRef == "" {
		t.Fatalf("paired server store is incomplete: %s", data)
	}
	return store.Servers[0]
}

func sshArgs(endpoint, key, user string, remote ...string) []string {
	host, port, _ := net.SplitHostPort(endpoint)
	args := []string{"-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", "-i", key, "-p", port, user + "@" + host}
	return append(args, remote...)
}

func availableTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func requiredExecutable(t *testing.T, name string) string {
	t.Helper()
	path := os.Getenv(name)
	if path == "" {
		t.Fatalf("%s must point to a built release binary; the qualification gate cannot skip it", name)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not an executable release binary: %q", name, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func requiredTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("required qualification tool %s is unavailable; this gate cannot skip: %v", name, err)
	}
	return path
}

func decodeJSON(t *testing.T, value string, out any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(value))
	if err := decoder.Decode(out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, value)
	}
}

func assertContains(t *testing.T, value, expected string) {
	t.Helper()
	if !strings.Contains(value, expected) {
		t.Fatalf("output does not contain %q:\n%s", expected, value)
	}
}

func replaceEnvironment(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+value)
}
