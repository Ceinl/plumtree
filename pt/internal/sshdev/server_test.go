package sshdev

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/runner"
	"golang.org/x/crypto/ssh"
)

// Race instrumentation substantially slows wazero compilation on CI runners.
// Keep the end-to-end SSH assertion bounded while allowing the instrumented
// runtime enough time to produce its first frame.
const sshIntegrationTimeout = 30 * time.Second

// buildCounter compiles the SDK counter example to WASM, skipping if the
// toolchain build fails.
func buildCounter(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "counter.wasm")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "../../../sdk/examples/counter"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("wasm build failed (%v):\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func buildAgentboard(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "agentboard.wasm")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "../../../examples/agentboard/app"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wasm build failed (%v):\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// End-to-end over a real SSH connection: handshake, pty-req, shell, a streamed
// frame, and keystroke delivery (the 'q' that quits).
func TestServeOverSSH(t *testing.T) {
	wasm := buildCounter(t)

	srv := &Server{Wasm: wasm, Limits: runner.DefaultLimits, AppType: "tui", AppName: "counter"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addrCh := make(chan string, 1)
	go srv.ListenAndServe(ctx, "127.0.0.1:0", func(a net.Addr) { addrCh <- a.String() })

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(sshIntegrationTimeout):
		t.Fatal("server did not start")
	}

	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            "dev",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out safeBuffer
	sess.Stdout = &out

	if err := sess.RequestPty("xterm", 10, 30, ssh.TerminalModes{}); err != nil {
		t.Fatalf("pty: %v", err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatalf("shell: %v", err)
	}

	// The initial frame must arrive over the SSH stream.
	if !waitForContains(&out, "Count: 0", sshIntegrationTimeout) {
		t.Fatalf("did not receive initial frame; got %q", out.String())
	}
	if !strings.Contains(out.String(), "\x1b[?1006h") {
		t.Fatalf("mouse reporting was not enabled: %q", out.String())
	}

	// Drive input, then quit. If keystrokes weren't wired, the session would
	// hang until the test times out.
	stdin.Write([]byte("\x1b[A\x1b[A")) // two up arrows
	time.Sleep(100 * time.Millisecond)
	stdin.Write([]byte("q"))

	done := make(chan struct{})
	go func() { sess.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(sshIntegrationTimeout):
		t.Fatal("session did not end after 'q' — input not delivered?")
	}
	if !strings.Contains(out.String(), "\x1b[?1006l") {
		t.Fatalf("mouse reporting was not disabled: %q", out.String())
	}
}

func TestServeActionOverSSHExec(t *testing.T) {
	srv := &Server{
		Wasm: buildAgentboard(t), Limits: runner.DefaultLimits, AppType: "tui", AppName: "agentboard",
		Caps: runner.Capabilities{
			KV: runner.NewMemStore(0, 0), Bus: runner.NewMemBus(),
			Auth: runner.StaticAuth{Identity: runner.Identity{User: "local", Kind: runner.IdentitySSHKey, OwnsApp: true, Authenticated: true}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addrCh := make(chan string, 1)
	go srv.ListenAndServe(ctx, "127.0.0.1:0", func(a net.Addr) { addrCh <- a.String() })
	addr := <-addrCh
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "dev", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err := session.Output(`action get_identity {}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.Contains(got, `"ok":true`) || !strings.Contains(got, `"owns_app":true`) {
		t.Fatalf("action = %s", got)
	}
}

func waitForContains(b *safeBuffer, sub string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), sub) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return strings.Contains(b.String(), sub)
}
