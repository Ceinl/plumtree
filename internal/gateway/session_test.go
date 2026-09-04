package gateway

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Ceinl/plumtree/internal/runner"
	"github.com/Ceinl/plumtree/internal/terminal"
	"golang.org/x/crypto/ssh"
)

func TestParseTerminalDimensions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		columns uint32
		rows    uint32
		valid   bool
	}{
		{"minimum", terminal.MinWidth, terminal.MinHeight, true},
		{"maximum", terminal.MaxWidth, terminal.MaxHeight, true},
		{"zero columns", 0, 24, false},
		{"zero rows", 80, 0, false},
		{"too wide", terminal.MaxWidth + 1, 24, false},
		{"too tall", 80, terminal.MaxHeight + 1, false},
		{"uint32 maximum", ^uint32(0), ^uint32(0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ptyPayload := ssh.Marshal(ptyRequest{Term: "xterm", Columns: tc.columns, Rows: tc.rows})
			if _, err := parsePTYRequest(ptyPayload); (err == nil) != tc.valid {
				t.Fatalf("parsePTYRequest(%dx%d) error = %v, valid = %v", tc.columns, tc.rows, err, tc.valid)
			}

			windowPayload := ssh.Marshal(windowChange{Columns: tc.columns, Rows: tc.rows})
			if _, err := parseWindowChange(windowPayload); (err == nil) != tc.valid {
				t.Fatalf("parseWindowChange(%dx%d) error = %v, valid = %v", tc.columns, tc.rows, err, tc.valid)
			}
		})
	}

	if _, err := parsePTYRequest([]byte{0, 0, 0}); err == nil {
		t.Fatal("malformed pty request was accepted")
	}
	if _, err := parseWindowChange([]byte{0, 0, 0}); err == nil {
		t.Fatal("malformed window-change request was accepted")
	}
}

// TestRunSessionProductionCLIUsesWorker guards the production configuration:
// when RunnerWorker is set, CLI apps must cross the same process boundary as
// TUI apps. The wrapper marker proves that the configured executable spawned;
// the guest output proves the CLI protocol completed end-to-end.
func TestRunSessionProductionCLIUsesWorker(t *testing.T) {
	worker := buildTestBinary(t, "../..", "./cmd/runner-worker", nil)
	wasmPath := buildTestBinary(t, "../../internal/runner/testdata/kvguest", ".", []string{"GOOS=wasip1", "GOARCH=wasm", "GOWORK=off"})
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "spawned")
	wrapper := filepath.Join(t.TempDir(), "runner-worker-wrapper")
	script := fmt.Sprintf("#!/bin/sh\n: > %q\nexec %q\n", marker, worker)
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	ch := &testChannel{}
	s := mustNewServer(t, Config{Backend: &stubBackend{}, Runner: runner.New(), RunnerWorker: wrapper})
	s.runSession(context.Background(), ch, wasm, "cli", runner.Capabilities{KV: runner.NewMemStore(0, 0)}, nil, nil)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("configured runner worker was not spawned: %v", err)
	}
	if got := ch.String(); !strings.Contains(got, "get=11:hello world") {
		t.Fatalf("CLI output did not cross worker protocol:\n%s", got)
	}
}

func TestRunSessionTUIEnablesAndDisablesMouse(t *testing.T) {
	wasmPath := buildTestBinary(t, "../../sdk/examples/counter", ".", []string{"GOOS=wasip1", "GOARCH=wasm"})
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	ch := &testChannel{}
	goodbye := "safe\x1b[31mtext"
	s := mustNewServer(t, Config{Backend: &stubBackend{}, Runner: runner.New(), MaxFPS: 60})
	s.runSession(context.Background(), ch, wasm, "tui", runner.Capabilities{Goodbye: &goodbye}, func() (int, int) { return 30, 10 }, nil)
	got := ch.String()
	if !strings.Contains(got, "\x1b[?1006h") || !strings.Contains(got, "\x1b[?1006l") {
		t.Fatalf("mouse setup/teardown missing: %q", got)
	}
	if !strings.Contains(got, "safe [31mtext") || strings.Contains(got, "safe\x1b[31mtext") {
		t.Fatalf("goodbye was not displayed safely: %q", got)
	}
}

func TestRunSessionCleanCLIUsesCLIForTUIApp(t *testing.T) {
	wasmPath := buildTestBinary(t, "../../examples/agentboard/app", ".", []string{"GOOS=wasip1", "GOARCH=wasm"})
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	ch := &testChannel{}
	caps := runner.Capabilities{
		KV: runner.NewMemStore(0, 0), Bus: runner.NewMemBus(),
		Auth: runner.StaticAuth{Identity: runner.Identity{User: "SHA256:owner-key-0123456789012345", Kind: runner.IdentitySSHKey, OwnsApp: true}},
	}
	goodbye := "clean CLI complete"
	caps.Goodbye = &goodbye
	s := mustNewServer(t, Config{Backend: &stubBackend{}, Runner: runner.New()})
	s.runSessionArgs(context.Background(), ch, wasm, "tui", caps, nil, nil, []string{"get_identity"})
	if got := ch.String(); !strings.Contains(got, "authenticated=false") || strings.Contains(got, terminal.OPEN_ALT) || !strings.Contains(got, goodbye) {
		t.Fatalf("clean CLI output = %q", got)
	}
}

// Every session end — success, guest failure, and early reject — must report a
// real exit status so SSH clients can script against the gateway.
func TestRunSessionReportsGuestFailureExitStatus(t *testing.T) {
	wasmPath := buildTestBinary(t, "../../examples/agentboard/app", ".", []string{"GOOS=wasip1", "GOARCH=wasm"})
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	ch := &testChannel{}
	s := mustNewServer(t, Config{Backend: &stubBackend{}, Runner: runner.New()})
	// The guest exits nonzero for an unknown command; the channel must carry it.
	log, _, status := s.runSessionArgsStatus(context.Background(), ch, wasm, "cli", runner.Capabilities{}, nil, nil, []string{"not-a-command"})
	if status == 0 {
		t.Fatalf("guest failure reported exit status 0 (log=%q)", log)
	}
	if got := ch.exitStatuses(); len(got) != 0 { // runSessionArgsStatus does not send; the caller does
		t.Fatalf("unexpected statuses %v before the caller sends one", got)
	}
}

func TestStartSessionRejectsSendNonzeroExitStatus(t *testing.T) {
	tests := []struct {
		name    string
		server  func(backend *stubBackend) *Server
		backend *stubBackend
	}{
		{"runner at capacity", func(b *stubBackend) *Server {
			s := mustNewServer(t, Config{Backend: b, MaxConcurrentSessions: 1})
			if !s.acquireSlot() { // occupy the only runner slot
				t.Fatal("could not occupy the only runner slot")
			}
			return s
		}, &stubBackend{}},
		{"resolve failure", func(b *stubBackend) *Server {
			return mustNewServer(t, Config{Backend: b})
		}, &stubBackend{resolveErr: errors.New("no such app")}},
		{"start session failure", func(b *stubBackend) *Server {
			return mustNewServer(t, Config{Backend: b})
		}, &stubBackend{startErr: errors.New("quota exceeded")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ch := &testChannel{}
			s := test.server(test.backend)
			ctx, cancel := context.WithCancel(context.Background())
			s.startSession(ctx, cancel, ch, "alice/tool", runner.Identity{}, nil, nil)
			got := ch.exitStatuses()
			if len(got) != 1 || got[0] == 0 {
				t.Fatalf("exit statuses = %v, want one nonzero status", got)
			}
		})
	}
}

// stubBackend fails resolution or admission on demand so early-reject paths can
// be driven without a repository.
type stubBackend struct {
	resolveErr error
	startErr   error
}

func (*stubBackend) ResolveIdentity(_ context.Context, fingerprint string) (runner.Identity, error) {
	return runner.Identity{User: fingerprint, Kind: runner.IdentitySSHKey}, nil
}
func (b *stubBackend) ResolveRunnable(context.Context, string, runner.Identity) (Runnable, error) {
	if b.resolveErr != nil {
		return Runnable{}, b.resolveErr
	}
	return Runnable{AppID: "app-1", AppName: "tool", OwnerID: "author-1", DeployID: "deployment-1", ArtifactDigest: "digest-1"}, nil
}
func (b *stubBackend) StartSession(context.Context, string, string, string, string) (string, error) {
	if b.startErr != nil {
		return "", b.startErr
	}
	return "session-1", nil
}
func (*stubBackend) RecordSessionLog(context.Context, string, string, bool) error { return nil }
func (*stubBackend) EndSession(context.Context, string) error                     { return nil }
func (*stubBackend) SecretsForApp(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (*stubBackend) EgressAllowlist(context.Context, string) ([]string, error) {
	return nil, nil
}
func (*stubBackend) KVStore(context.Context, string) (runner.Store, error) {
	return nil, ErrCapsUnavailable
}
func (*stubBackend) StartSuspensionWatcher(context.Context, func(context.Context, Suspension) error) error {
	return nil
}

func buildTestBinary(t *testing.T, dir, pkg string, extraEnv []string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "built")
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	if !hasEnv(extraEnv, "GOWORK") {
		root, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(cmd.Env, "GOWORK="+filepath.Join(root, "go.work"))
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s failed (%v):\n%s", pkg, err, b)
	}
	return out
}

func hasEnv(env []string, key string) bool {
	prefix := key + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

type testChannel struct {
	stdout   lockedBuffer
	stderr   lockedBuffer
	mu       sync.Mutex
	statuses []uint32
}

func (c *testChannel) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *testChannel) Write(p []byte) (int, error) { return c.stdout.Write(p) }
func (c *testChannel) String() string              { return c.stdout.String() }
func (c *testChannel) Close() error                { return nil }
func (c *testChannel) CloseWrite() error           { return nil }

func (c *testChannel) SendRequest(name string, wantReply bool, payload []byte) (bool, error) {
	if name == "exit-status" && len(payload) == 4 {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.statuses = append(c.statuses, binary.LittleEndian.Uint32(payload))
	}
	return false, nil
}

// exitStatuses returns every exit-status request sent over the channel.
func (c *testChannel) exitStatuses() []uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint32(nil), c.statuses...)
}

func (c *testChannel) Stderr() io.ReadWriter { return &c.stderr }

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Read(p)
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
