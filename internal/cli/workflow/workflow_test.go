package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/cli/scaffold"
	"golang.org/x/crypto/ssh"
)

func TestNewAcceptsNameBeforeOrAfterFlags(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	var out bytes.Buffer
	r := Runner{Out: &out}
	if err := r.Run([]string{"new", "first", "--tui", "--access", "public"}); err != nil {
		t.Fatalf("name before flags: %v", err)
	}
	if err := r.Run([]string{"new", "--cli", "--access", "restricted", "second"}); err != nil {
		t.Fatalf("name after flags: %v", err)
	}
	for _, project := range []string{"first", "second"} {
		if _, err := ReadManifest(filepath.Join(root, project)); err != nil {
			t.Fatalf("read %s manifest: %v", project, err)
		}
	}
}

func TestDevPassesCLIArguments(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project, err := NewScaffold(root, "greeter", scaffold.CLI, "public")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	var out bytes.Buffer
	r := Runner{Out: &out, Workspace: repoRoot}
	if err := r.Run([]string{"dev", "Alice"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Hello Alice") {
		t.Fatalf("dev output = %q", out.String())
	}
}

func TestDevHeadlessAcceptsExplicitRuntimeControls(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project, err := NewScaffold(root, "counter", scaffold.TUI, "public")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	var out bytes.Buffer
	r := Runner{Out: &out, Workspace: repoRoot}
	if err := r.Run([]string{"dev", "--headless", "--script", "q", "--w", "18", "--h", "6", "--mem-pages", "256", "--frame-timeout", "1s", "--max-fps", "30"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "18x6") || !strings.Contains(got, "256 pages") || !strings.Contains(got, "1s") {
		t.Fatalf("headless summary = %q", got)
	}
}

func TestDevTUIRequiresRealTerminalOrExplicitAlternateMode(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project, err := NewScaffold(root, "counter", scaffold.TUI, "public")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	r := Runner{In: strings.NewReader("q"), Workspace: repoRoot}
	err = r.Run([]string{"dev"})
	if err == nil || !strings.Contains(err.Error(), "--headless") || !strings.Contains(err.Error(), "--ssh") {
		t.Fatalf("error = %v", err)
	}
}

func TestDevSSHExplicitNonLoopbackServesCLIAndPassesExecArguments(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project, err := NewScaffold(root, "greeter", scaffold.CLI, "public")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Context: ctx, Out: &out, Workspace: repoRoot}).Run([]string{"dev", "--ssh", "--no-ssh-config", "--allow-nonloopback-ssh", "--addr", "0.0.0.0:0"})
	}()

	addressPattern := regexp.MustCompile(`(?:0\.0\.0\.0|\[::\]):[0-9]+`)
	deadline := time.Now().Add(30 * time.Second)
	var address string
	for time.Now().Before(deadline) {
		address = addressPattern.FindString(out.String())
		if address != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if address == "" {
		t.Fatalf("SSH server did not become ready: %q", out.String())
	}

	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	connectAddress := net.JoinHostPort("127.0.0.1", port)
	client, err := ssh.Dial("tcp", connectAddress, &ssh.ClientConfig{User: "greeter", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	result, err := session.Output("Alice")
	client.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "Hello Alice") {
		t.Fatalf("SSH CLI output = %q", result)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SSH server shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSH server did not stop after cancellation")
	}
}

func TestDevSSHRejectsNonLoopbackListener(t *testing.T) {
	err := (Runner{}).Run([]string{"dev", "--ssh", "--addr", "0.0.0.0:2222"})
	if err == nil || !strings.Contains(err.Error(), "loopback") || !strings.Contains(err.Error(), "--allow-nonloopback-ssh") {
		t.Fatalf("error = %v", err)
	}
}

func TestDevSSHRejectsInvalidHostAlias(t *testing.T) {
	err := (Runner{}).Run([]string{"dev", "--ssh", "--host", "bad alias"})
	if err == nil || !strings.Contains(err.Error(), "whitespace") {
		t.Fatalf("error = %v", err)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestDestructiveCommandsAcceptConfirmationOrYes(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "secret", args: []string{"secret", "rm", "app", "KEY"}},
		{name: "egress", args: []string{"egress", "rm", "app", "example.test"}},
		{name: "access", args: []string{"access", "rm", "app", "key"}},
	} {
		t.Run(test.name+" interactive", func(t *testing.T) {
			confirmed := false
			r := Runner{StorePath: filepath.Join(t.TempDir(), "servers.json"), Confirm: func(string) bool {
				confirmed = true
				return true
			}}
			err := r.Run(test.args)
			if !confirmed || errors.Is(err, ErrConfirm) {
				t.Fatalf("confirmed=%t err=%v", confirmed, err)
			}
		})
		t.Run(test.name+" yes", func(t *testing.T) {
			r := Runner{StorePath: filepath.Join(t.TempDir(), "servers.json")}
			args := append(append([]string(nil), test.args...), "--yes")
			if err := r.Run(args); errors.Is(err, ErrConfirm) || (err != nil && strings.Contains(err.Error(), "usage:")) {
				t.Fatalf("--yes was not accepted: %v", err)
			}
		})
	}
}

func TestConfirmationRequiredExplainsNonInteractiveRecovery(t *testing.T) {
	r := Runner{StorePath: filepath.Join(t.TempDir(), "servers.json")}
	err := r.Run([]string{"secret", "rm", "app", "KEY"})
	if !errors.Is(err, ErrConfirm) || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeployAcceptsConfirmationOrYes(t *testing.T) {
	t.Chdir(t.TempDir())
	confirmed := false
	r := Runner{Confirm: func(string) bool {
		confirmed = true
		return true
	}}
	if err := r.Run([]string{"deploy"}); !confirmed || errors.Is(err, ErrConfirm) {
		t.Fatalf("confirmed=%t err=%v", confirmed, err)
	}
	if err := (Runner{}).Run([]string{"deploy", "--yes"}); errors.Is(err, ErrConfirm) {
		t.Fatalf("--yes required another confirmation: %v", err)
	}
	if err := (Runner{}).Run([]string{"deploy"}); !errors.Is(err, ErrConfirm) || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("non-interactive error = %v", err)
	}
}

func TestCommandHelpDescribesTheTestedDevContract(t *testing.T) {
	var out bytes.Buffer
	r := Runner{Out: &out}
	if err := r.Run([]string{"dev", "--help"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pt dev [flags] [--] [args...]", "--headless", "--ssh", "--mem-pages", "--frame-timeout", "--max-fps"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help does not contain %q: %s", want, out.String())
		}
	}
}

func TestStrictManifestAndExplicitScaffolds(t *testing.T) {
	root := t.TempDir()
	if _, err := NewScaffold(root, "demo", scaffold.TUI, "restricted"); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(filepath.Join(root, "demo"))
	if err != nil || manifest.Access != "restricted" || manifest.Type != "tui" {
		t.Fatalf("manifest=%+v err=%v", manifest, err)
	}
	if _, err := NewScaffold(root, "bad", scaffold.TUI, ""); err == nil {
		t.Fatal("missing access policy accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "demo", "plumtree.json"), []byte(`{"name":"demo","type":"tui","access":"public","future":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(filepath.Join(root, "demo")); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
}

func TestPersistentProfileReset(t *testing.T) {
	root := t.TempDir()
	caps, cleanup, err := OpenProfile(Profile{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := caps.KV.Set("persist", []byte("value")); err != nil {
		t.Fatal(err)
	}
	cleanup()
	caps, cleanup, err = OpenProfile(Profile{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := caps.KV.Get("persist")
	cleanup()
	if err != nil || !found || string(value) != "value" {
		t.Fatalf("value=%q found=%t err=%v", value, found, err)
	}
	_, cleanup, err = OpenProfile(Profile{Root: root, Reset: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	caps, cleanup, err = OpenProfile(Profile{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	_, found, err = caps.KV.Get("persist")
	cleanup()
	if err != nil || found {
		t.Fatalf("reset did not remove value: found=%t err=%v", found, err)
	}
}

func TestAPIUsesStableProblemAndMultipartDeployment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"product":"plumtree","version":"v1","apiVersion":1,"abiVersion":4}`)
			return
		}
		if r.URL.Path == "/api/v1/apps" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"code":"forbidden","detail":"bad\u001b[31m"}`)
			return
		}
		if r.URL.Path == "/api/v1/deployments" {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
				t.Error("deployment is not multipart")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"apiVersion":1,"app":{"id":"app"}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	baseURL, _ := url.Parse(server.URL)
	transport := server.Client().Transport
	api := &API{HTTP: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Scheme, request.URL.Host = baseURL.Scheme, baseURL.Host
		return transport.RoundTrip(request)
	})}}
	version, err := api.Version(context.Background())
	if err != nil || version.Version != "v1" {
		t.Fatalf("version=%+v err=%v", version, err)
	}
	_, err = api.Apps(context.Background())
	if err == nil || !strings.Contains(err.Error(), "forbidden:") || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("problem err=%v", err)
	}
	result, err := api.Deploy(context.Background(), ArtifactRequest{Name: "demo", Type: "tui", Access: "public", ABIVersion: 4, WASM: []byte("wasm")}, "")
	if err != nil || result.API != 1 {
		t.Fatalf("deploy=%+v err=%v", result, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestSSHInstructionRejectsTerminalInjection(t *testing.T) {
	if _, err := SSHInstruction(testServerRecord(), "owner/app\nmalicious"); err == nil {
		t.Fatal("unsafe handle accepted")
	}
}

func TestPairedServerCommandsListSwitchRenameAndForget(t *testing.T) {
	dir := t.TempDir()
	storePath, keyDir := filepath.Join(dir, "servers.json"), filepath.Join(dir, "keys")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	store := paired.NewStore()
	alpha, beta := testServerRecord(), testServerRecord()
	alpha.Name, alpha.ServerID, alpha.KeyRef = "alpha", "server-a", "a.ed25519"
	beta.Name, beta.ServerID, beta.KeyRef = "beta", "server-b", "b.ed25519"
	if err := store.Add(alpha); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(beta); err != nil {
		t.Fatal(err)
	}
	if err := paired.Save(storePath, store); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{alpha.KeyRef, beta.KeyRef} {
		if err := os.WriteFile(filepath.Join(keyDir, name), []byte("private"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	runner := Runner{Out: &out, StorePath: storePath, KeyDir: keyDir}
	if err := runner.Run([]string{"server", "list"}); err != nil || !strings.Contains(out.String(), `"current": "alpha"`) {
		t.Fatalf("list output=%s err=%v", out.String(), err)
	}
	if err := runner.Run([]string{"server", "use", "beta"}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run([]string{"server", "rename", "beta", "prod"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runner.Run([]string{"server", "current"}); err != nil || !strings.Contains(out.String(), `"name": "prod"`) {
		t.Fatalf("current output=%s err=%v", out.String(), err)
	}
	if err := runner.Run([]string{"server", "forget", "prod", "--yes"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := paired.Load(storePath)
	if err != nil || len(loaded.Servers) != 1 || loaded.Current != "alpha" {
		t.Fatalf("store=%+v err=%v", loaded, err)
	}
	if _, err := os.Stat(filepath.Join(keyDir, beta.KeyRef)); !os.IsNotExist(err) {
		t.Fatalf("forgotten key still exists: %v", err)
	}
}

func TestRemoteCommandExplainsHowToPairWhenStoreIsEmpty(t *testing.T) {
	runner := Runner{StorePath: filepath.Join(t.TempDir(), "servers.json")}
	err := runner.Run([]string{"status"})
	if err == nil || !strings.Contains(err.Error(), "run `pt pair") {
		t.Fatalf("error=%v", err)
	}
}

func testServerRecord() paired.ServerRecord {
	return paired.ServerRecord{Name: "main", ServerID: "server", Host: "example.test", Port: 2222,
		HostKeyAlgorithm: "ssh-ed25519", HostKeyFingerprint: "SHA256:host", ProductVersion: "v1", KeyRef: "key.ed25519"}
}

// TestDevSSHInstallsAliasInManagedBlock runs the real dev SSH path with HOME
// pointed at a scratch directory and checks that the resulting ssh config
// contains only the managed block.
func TestDevSSHInstallsAliasInManagedBlock(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project, err := NewScaffold(root, "greeter", scaffold.CLI, "public")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Context: ctx, Out: &out, Workspace: repoRoot}).Run([]string{"dev", "--ssh", "--host", "dev.test", "--addr", "127.0.0.1:0"})
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "Connect: ssh dev.test") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "Connect: ssh dev.test") {
		t.Fatalf("alias command not announced: %q", out.String())
	}

	config, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(config)
	if strings.Count(content, sshConfigBegin) != 1 || strings.Count(content, sshConfigEnd) != 1 {
		t.Fatalf("marker discipline violated: %q", content)
	}
	if !strings.Contains(content, "Host dev.test") || !strings.Contains(content, "StrictHostKeyChecking accept-new") {
		t.Fatalf("managed block incomplete: %q", content)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SSH server shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSH server did not stop after cancellation")
	}
}

// TestDevSSHNoSSHConfigLeavesDiskUntouched prints the raw command and never
// writes ~/.ssh/config.
func TestDevSSHNoSSHConfigLeavesDiskUntouched(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	project, err := NewScaffold(root, "greeter", scaffold.CLI, "public")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	home := t.TempDir()
	t.Setenv("HOME", home)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- (Runner{Context: ctx, Out: &out, Workspace: repoRoot}).Run([]string{"dev", "--ssh", "--no-ssh-config", "--addr", "127.0.0.1:0"})
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "Connect: ssh -p") {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "Connect: ssh -p") || !strings.Contains(out.String(), "@127.0.0.1") {
		t.Fatalf("raw ssh command not announced: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("~/.ssh was created without consent: %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSH server did not stop after cancellation")
	}
}

// TestPromptSecretPrintsTheFullPromptAndReadsOneLine covers the interactive
// prompt: the label is announced on stderr and one piped line is consumed.
func TestPromptSecretPrintsTheFullPromptAndReadsOneLine(t *testing.T) {
	errOut := &bytes.Buffer{}
	got, err := promptSecret(strings.NewReader("phrase-123\n"), errOut, "pairing phrase (secret from 'plumtree bootstrap')")
	if err != nil {
		t.Fatal(err)
	}
	if got != "phrase-123" {
		t.Fatalf("secret = %q", got)
	}
	if want := "pairing phrase (secret from 'plumtree bootstrap'): \n"; errOut.String() != want {
		t.Fatalf("prompt = %q, want %q", errOut.String(), want)
	}
}

// TestEveryDispatchableCommandHasHelpTopic mirrors the switch cases in
// Runner.Run so a new command cannot ship without documentation.
func TestEveryDispatchableCommandHasHelpTopic(t *testing.T) {
	commands := []string{
		"pair", "recover", "server", "device", "new", "dev", "build", "deploy",
		"status", "app", "logs", "secret", "egress", "access", "audit", "ssh",
	}
	r := Runner{Out: io.Discard}
	for _, command := range commands {
		if err := r.writeHelp(command); err != nil {
			t.Errorf("pt help %s: %v", command, err)
		}
		if err := (Runner{}).Run([]string{"help", command}); err != nil {
			t.Errorf("pt help %s via Run: %v", command, err)
		}
	}
	if err := r.writeHelp(""); err != nil {
		t.Fatalf("root help: %v", err)
	}
	if err := r.writeHelp("not-a-command"); err == nil {
		t.Fatal("unknown help topic unexpectedly resolved")
	}
}
