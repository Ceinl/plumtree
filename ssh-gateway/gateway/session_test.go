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
	"github.com/Ceinl/plumtree/tui-runtime/screen"
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
	bytes.Buffer
	stderr bytes.Buffer
}

func (c *testChannel) Close() error      { return nil }
func (c *testChannel) CloseWrite() error { return nil }
func (c *testChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}
func (c *testChannel) Stderr() io.ReadWriter { return &c.stderr }
