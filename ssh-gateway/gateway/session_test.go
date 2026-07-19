package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
	"golang.org/x/crypto/ssh"
)

func TestParseTerminalDimensions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		columns uint32
		rows    uint32
		valid   bool
	}{
		{"minimum", screen.MinWidth, screen.MinHeight, true},
		{"maximum", screen.MaxWidth, screen.MaxHeight, true},
		{"zero columns", 0, 24, false},
		{"zero rows", 80, 0, false},
		{"too wide", screen.MaxWidth + 1, 24, false},
		{"too tall", 80, screen.MaxHeight + 1, false},
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
	worker := buildTestBinary(t, "../../runner", "./cmd/plumtree-runner-worker", nil)
	wasmPath := buildTestBinary(t, "../../runner/testdata/kvguest", ".", []string{"GOOS=wasip1", "GOARCH=wasm", "GOWORK=off"})
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
	s := &Server{Runner: runner.New(), RunnerWorker: wrapper}
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
	s := &Server{Runner: runner.New(), MaxFPS: 60}
	s.runSession(context.Background(), ch, wasm, "tui", runner.Capabilities{}, func() (int, int) { return 30, 10 }, nil)
	got := ch.String()
	if !strings.Contains(got, "\x1b[?1006h") || !strings.Contains(got, "\x1b[?1006l") {
		t.Fatalf("mouse setup/teardown missing: %q", got)
	}
}

func TestRunSessionActionUsesCLIForTUIApp(t *testing.T) {
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
	s := &Server{Runner: runner.New()}
	s.runSessionArgs(context.Background(), ch, wasm, "tui", caps, nil, nil, []string{abi.ActionArgPrefix, "get_identity", `{}`})
	if got := ch.String(); !strings.Contains(got, `"ok":true`) || strings.Contains(got, terminal.OPEN_ALT) {
		t.Fatalf("action output = %q", got)
	}
}

func buildTestBinary(t *testing.T, dir, pkg string, extraEnv []string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "built")
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s failed (%v):\n%s", pkg, err, b)
	}
	return out
}

type testChannel struct {
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func (c *testChannel) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *testChannel) Write(p []byte) (int, error) { return c.stdout.Write(p) }
func (c *testChannel) String() string              { return c.stdout.String() }
func (c *testChannel) Close() error                { return nil }
func (c *testChannel) CloseWrite() error           { return nil }
func (c *testChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}
func (c *testChannel) Stderr() io.ReadWriter { return &c.stderr }
