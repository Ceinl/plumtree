package runner

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// buildWorker compiles the runner-worker binary for the process-isolation tests.
func buildWorker(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "runner-worker")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/plumtree-runner-worker")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build runner-worker failed (%v):\n%s", err, b)
	}
	return out
}

func shortUnixSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pt-runner-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "broker.sock")
}

// End-to-end across a process boundary: the counter guest runs in a worker
// subprocess while this process serves recv/present. Same observable result as
// the in-process Run — proving the worker is a faithful drop-in.
func TestProcessRunnerCounter(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../sdk/examples/counter")

	var sink capture
	src := NewScriptSource(24, 6, []string{"up", "up", "down", "q"})
	pr := NewProcessRunner(worker)
	if err := pr.Run(context.Background(), wasm, DefaultLimits, Capabilities{}, src, &sink, io.Discard); err != nil {
		t.Fatalf("ProcessRunner.Run: %v", err)
	}

	wantCounts := []string{"Count: 0", "Count: 1", "Count: 2", "Count: 1", "Count: 1"}
	if len(sink.frames) != len(wantCounts) {
		t.Fatalf("got %d frames, want %d", len(sink.frames), len(wantCounts))
	}
	for i, want := range wantCounts {
		if got := frameText(sink.frames[i]); !strings.Contains(got, want) {
			t.Errorf("frame %d missing %q:\n%s", i, want, got)
		}
	}
	if !sink.frames[len(sink.frames)-1].Quit {
		t.Error("final frame should carry the quit flag")
	}
}

func TestProcessRunnerProxiesHostCommands(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "testdata/execguest", "GOWORK=off")
	var out strings.Builder
	pr := NewProcessRunner(worker)
	if err := pr.RunCLI(context.Background(), wasm, DefaultLimits, Capabilities{Exec: LocalCommander{}}, nil, &out); err != nil {
		t.Fatalf("ProcessRunner.RunCLI: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "exit=0 stdout=host-ok") {
		t.Fatalf("host command did not cross worker protocol: %q", got)
	}
}

func TestProcessRunnerHostedSDKButtonMouseClick(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../sdk/examples/mousebutton")
	var sink capture
	pr := NewProcessRunner(worker)
	if err := pr.Run(context.Background(), wasm, DefaultLimits, Capabilities{}, &eventListSource{events: mouseClickEvents()}, &sink, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !frameWith(sink.frames, "clicked=1 events=2") {
		t.Fatalf("isolated hosted click missing; last frame:\n%s", frameText(sink.frames[len(sink.frames)-1]))
	}
}

func TestProcessRunnerTimersWakeAndRedraw(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../sdk/examples/timer")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var sink capture
	pr := NewProcessRunner(worker)
	err := pr.Run(ctx, wasm, DefaultLimits, Capabilities{}, &initialThenIdleSource{}, &sink, io.Discard)
	if err != nil && err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if !frameWith(sink.frames, "ticks: 2") {
		t.Fatalf("isolated recurring timer did not redraw; frames=%d", len(sink.frames))
	}
	if !frameWith(sink.frames, "one-shot fired: true") {
		t.Fatalf("isolated one-shot timer did not redraw; frames=%d", len(sink.frames))
	}
}

func TestProcessRunnerActionCapabilityParity(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../examples/agentboard/app")
	store := NewMemStore(0, 0)
	caps := Capabilities{
		KV: store, Bus: NewMemBus(),
		Auth: StaticAuth{Identity: Identity{User: "SHA256:owner-key-0123456789012345", Kind: IdentitySSHKey, OwnsApp: true, Authenticated: true}},
	}
	pr := NewProcessRunner(worker)
	var created strings.Builder
	if err := pr.RunCLI(context.Background(), wasm, DefaultLimits, caps, []string{abi.ActionArgPrefix, "create_project_board", `{"project":"runner","name":"Runner"}`}, &created); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(created.String(), `"ok":true`) {
		t.Fatalf("create = %s", created.String())
	}
	var listed strings.Builder
	if err := pr.RunCLI(context.Background(), wasm, DefaultLimits, caps, []string{abi.ActionArgPrefix, "list_boards", `{}`}, &listed); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listed.String(), `"project":"runner"`) {
		t.Fatalf("list = %s", listed.String())
	}
}

func TestProcessRunnerGoodbyeCapabilityParity(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../_devtest/goodbye-cli/app")
	goodbye := ""
	pr := NewProcessRunner(worker)
	if err := pr.RunCLI(context.Background(), wasm, DefaultLimits, Capabilities{Goodbye: &goodbye}, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if goodbye != "Goodbye from goodbye-cli!" {
		t.Fatalf("isolated goodbye = %q", goodbye)
	}
}

// CLI mode carries guest arguments/output across the process boundary and
// proxies capabilities just like the interactive mode.
func TestProcessRunnerCLI(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "testdata/kvguest", "GOWORK=off")
	store := NewMemStore(0, 0)
	var out strings.Builder

	pr := NewProcessRunner(worker)
	if err := pr.RunCLI(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, []string{"unused"}, &out); err != nil {
		t.Fatalf("ProcessRunner.RunCLI: %v", err)
	}
	for _, want := range []string{"set=0", "get=11:hello world", "cas-stale=-5", "list=created,greeting", "del=0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q; full output:\n%s", want, out.String())
		}
	}
}

// The production path crosses a container boundary through a broker socket.
// Exercise that transport end to end, including capability RPC, rather than
// only testing the local subprocess path.
func TestRemoteProcessRunnerCLI(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "testdata/kvguest", "GOWORK=off")
	socket := shortUnixSocket(t)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker := &Broker{WorkerPath: worker, Token: "runner-secret", MaxSessions: 2}
	errCh := make(chan error, 1)
	go func() { errCh <- broker.Serve(ctx, ln) }()

	store := NewMemStore(0, 0)
	var out strings.Builder
	pr := NewRemoteProcessRunner("unix://"+socket, "runner-secret")
	if err := pr.RunCLI(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, nil, &out); err != nil {
		t.Fatalf("remote ProcessRunner.RunCLI: %v", err)
	}
	for _, want := range []string{"set=0", "get=11:hello world", "cas-stale=-5", "list=created,greeting", "del=0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q; full output:\n%s", want, out.String())
		}
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("broker shutdown: %v", err)
	}
}

func TestRemoteProcessRunnerRejectsWrongToken(t *testing.T) {
	worker := buildWorker(t)
	socket := shortUnixSocket(t)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go (&Broker{WorkerPath: worker, Token: "right", MaxSessions: 1}).Serve(ctx, ln)

	pr := NewRemoteProcessRunner("unix://"+socket, "wrong")
	err = pr.RunCLI(context.Background(), []byte("not reached"), DefaultLimits, Capabilities{}, nil, io.Discard)
	if err == nil {
		t.Fatal("wrong broker token was accepted")
	}
}

// The KV capability is proxied from the worker back to this process: a guest in
// the worker writes through to the parent-held store, and a second worker
// session reads it back.
func TestProcessRunnerProxiesKV(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../sdk/examples/kvcounter")
	store := NewMemStore(0, 0)
	pr := NewProcessRunner(worker)

	s1 := NewScriptSource(24, 6, []string{"up", "up", "q"})
	if err := pr.Run(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, s1, &capture{}, io.Discard); err != nil {
		t.Fatalf("session 1: %v", err)
	}
	if v, ok, _ := store.Get("count"); !ok || string(v) != "2" {
		t.Fatalf("persisted count = %q ok=%v, want 2", v, ok)
	}

	var sink capture
	s2 := NewScriptSource(24, 6, []string{"q"})
	if err := pr.Run(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, s2, &sink, io.Discard); err != nil {
		t.Fatalf("session 2: %v", err)
	}
	if last := frameText(sink.frames[len(sink.frames)-1]); !strings.Contains(last, "Count: 2") {
		t.Errorf("session 2 did not load KV count across the process boundary:\n%s", last)
	}
}

// All capabilities cross the boundary: buschat in a worker receives a bus
// message published in this process, and renders its proxied Auth identity and
// Env secret.
func TestProcessRunnerProxiesAllCapabilities(t *testing.T) {
	worker := buildWorker(t)
	wasm := buildGuest(t, "../sdk/examples/buschat")
	bus := NewMemBus()
	pr := NewProcessRunner(worker)

	var sink capture
	src := &scriptedBusSource{steps: []busStep{
		{ev: abi.Event{Kind: abi.KindResize, W: 30, H: 8}},
		{before: func() { bus.Publish("room", []byte("hello")) }, waitBus: true},
		{ev: abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'q'}},
	}}
	caps := Capabilities{
		Bus:  bus,
		Auth: StaticAuth{Identity: Identity{User: "alice", Kind: IdentitySSHKey, Authenticated: true, OwnsApp: true}},
		Env:  MapEnv{"ROOM_NAME": "lobby"},
	}
	if err := pr.Run(context.Background(), wasm, DefaultLimits, caps, src, &sink, io.Discard); err != nil {
		t.Fatalf("ProcessRunner.Run: %v", err)
	}
	for _, want := range []string{"hello", "messages: 1", "user: alice", "identity: ssh-key owner=true", "room: lobby"} {
		if !frameWith(sink.frames, want) {
			t.Fatalf("missing %q across the process boundary; last frame:\n%s",
				want, frameText(sink.frames[len(sink.frames)-1]))
		}
	}
}
