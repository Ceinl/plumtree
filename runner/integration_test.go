package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// buildGuest compiles a Go package at dir to a WASI command module and returns
// its bytes. extraEnv augments the build environment (e.g. GOWORK=off for a
// standalone testdata module). The test skips if the toolchain build fails.
func buildGuest(t *testing.T, dir string, extraEnv ...string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "guest.wasm")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm"), extraEnv...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("wasm build in %s failed (%v):\n%s", dir, err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type capture struct{ frames []abi.Frame }

func (c *capture) Present(f abi.Frame) { c.frames = append(c.frames, f) }

func frameText(f abi.Frame) string {
	var b strings.Builder
	for y := 0; y < f.H; y++ {
		for x := 0; x < f.W; x++ {
			b.WriteRune(sanitizeRune(f.At(x, y).Ch))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// End-to-end: build the real SDK counter example and drive it through the dev
// host. This exercises the full guest-driven loop (recv/present), the ABI, and
// the SDK runtime — the same path `pt dev` uses.
func TestRunCounterExample(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/counter")

	var sink capture
	src := NewScriptSource(24, 6, []string{"up", "up", "down", "q"})
	if err := Run(context.Background(), wasm, DefaultLimits, Capabilities{}, src, &sink, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// initial + 4 events = 5 frames, with counts 0,1,2,1,1.
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

// End-to-end: the KV capability persists state across sessions. A first session
// of the real SDK kvcounter example increments and saves its count; a second,
// fresh guest instance sharing the same store loads and renders that value —
// the proof that two sessions of one app share durable state.
func TestKVPersistsAcrossSessions(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/kvcounter")
	store := NewMemStore(0, 0)

	// Session 1: increment twice, then quit. The count (2) is persisted.
	s1 := NewScriptSource(24, 6, []string{"up", "up", "q"})
	if err := Run(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, s1, &capture{}, io.Discard); err != nil {
		t.Fatalf("session 1: %v", err)
	}
	if v, ok, _ := store.Get("count"); !ok || string(v) != "2" {
		t.Fatalf("persisted count = %q ok=%v, want 2", v, ok)
	}

	// Session 2: a fresh instance loads the persisted count at startup.
	var sink capture
	s2 := NewScriptSource(24, 6, []string{"q"})
	if err := Run(context.Background(), wasm, DefaultLimits, Capabilities{KV: store}, s2, &sink, io.Discard); err != nil {
		t.Fatalf("session 2: %v", err)
	}
	if len(sink.frames) == 0 {
		t.Fatal("session 2 produced no frames")
	}
	last := frameText(sink.frames[len(sink.frames)-1])
	if !strings.Contains(last, "Count: 2") {
		t.Errorf("session 2 did not load persisted count; final frame:\n%s", last)
	}
}

// scriptedBusSource drives a guest through a fixed sequence of steps while also
// implementing BusBinder, so a step can block on the session's subscription and
// deliver whatever message arrives — the way TTYSource selects on the bus in
// production. It is the test rig for the pub/sub capability.
type scriptedBusSource struct {
	bus   <-chan abi.Event
	steps []busStep
	i     int
}

type busStep struct {
	ev      abi.Event // event to deliver when waitBus is false
	waitBus bool      // when true, block on the bus and deliver what arrives
	before  func()    // optional side effect run before the step
}

func (s *scriptedBusSource) BindBus(ev <-chan abi.Event) { s.bus = ev }

func (s *scriptedBusSource) Next(ctx context.Context) (abi.Event, bool) {
	if s.i >= len(s.steps) {
		return abi.Event{}, false
	}
	st := s.steps[s.i]
	s.i++
	if st.before != nil {
		st.before()
	}
	if st.waitBus {
		select {
		case ev := <-s.bus:
			return ev, true
		case <-ctx.Done():
			return abi.Event{}, false
		case <-time.After(2 * time.Second):
			return abi.Event{}, false
		}
	}
	return st.ev, true
}

func frameWith(frames []abi.Frame, want string) bool {
	for _, f := range frames {
		if strings.Contains(frameText(f), want) {
			return true
		}
	}
	return false
}

// End-to-end: a message published by another session of the same app is
// delivered live to a subscribed guest through recv, with no polling. The guest
// (the buschat example) subscribes on start; the test publishes on the shared
// bus — exactly what a second guest's bus_pub host call does — and the guest
// renders it.
func TestBusDeliversPublishedMessage(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/buschat")
	bus := NewMemBus()
	var sink capture
	src := &scriptedBusSource{steps: []busStep{
		{ev: abi.Event{Kind: abi.KindResize, W: 30, H: 8}},
		{before: func() { bus.Publish("room", []byte("hello")) }, waitBus: true},
		{ev: abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'q'}},
	}}
	caps := Capabilities{
		Bus:  bus,
		Auth: StaticAuth{Identity: Identity{User: "alice"}},
		Env:  MapEnv{"ROOM_NAME": "lobby"},
	}
	if err := Run(context.Background(), wasm, DefaultLimits, caps, src, &sink, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !frameWith(sink.frames, "hello") || !frameWith(sink.frames, "messages: 1") {
		t.Fatalf("guest did not render the delivered message; frames:\n%s",
			frameText(sink.frames[len(sink.frames)-1]))
	}
	// The Auth capability delivered the identity to the guest.
	if !frameWith(sink.frames, "user: alice") {
		t.Fatalf("guest did not render its identity; frames:\n%s",
			frameText(sink.frames[len(sink.frames)-1]))
	}
	// The Env capability delivered the secret to the guest.
	if !frameWith(sink.frames, "room: lobby") {
		t.Fatalf("guest did not render the injected secret; frames:\n%s",
			frameText(sink.frames[len(sink.frames)-1]))
	}
}

// End-to-end: a guest that publishes (key 'p' in buschat) receives its own
// message back, proving the full bus_pub -> bus -> recv loop within one session.
func TestBusEchoesPublisherOwnMessage(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/buschat")
	bus := NewMemBus()
	var sink capture
	src := &scriptedBusSource{steps: []busStep{
		{ev: abi.Event{Kind: abi.KindResize, W: 30, H: 8}},
		{ev: abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'p'}}, // guest publishes
		{waitBus: true}, // deliver the echo
		{ev: abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'q'}},
	}}
	if err := Run(context.Background(), wasm, DefaultLimits, Capabilities{Bus: bus}, src, &sink, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !frameWith(sink.frames, "ping") || !frameWith(sink.frames, "messages: 1") {
		t.Fatalf("guest did not receive its own published message; frames:\n%s",
			frameText(sink.frames[len(sink.frames)-1]))
	}
}

// End-to-end: a guest reaches an allowlisted host through the gated Fetch
// capability, and is denied when the host is not allowlisted. The fetchcheck
// example reads the target URL from a secret and renders the outcome.
func TestFetchGatedEgress(t *testing.T) {
	wasm := buildGuest(t, "../sdk/examples/fetchcheck")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	run := func(fetch Fetcher) string {
		var sink capture
		src := &scriptedBusSource{steps: []busStep{
			{ev: abi.Event{Kind: abi.KindResize, W: 40, H: 6}},
			{ev: abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'g'}},
			{ev: abi.Event{Kind: abi.KindKey, Key: abi.KeyRune, Ch: 'q'}},
		}}
		caps := Capabilities{Env: MapEnv{"FETCH_URL": srv.URL}, Fetch: fetch}
		if err := Run(context.Background(), wasm, DefaultLimits, caps, src, &sink, io.Discard); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return frameText(sink.frames[len(sink.frames)-1])
	}

	// Allowlisted: the request succeeds and the body is rendered. The test server
	// is on loopback, so allow private IPs here (production leaves this off).
	allowed := NewAllowlistFetcher([]string{"127.0.0.1"})
	allowed.AllowPrivateIPs = true
	if got := run(allowed); !strings.Contains(got, "status 200") || !strings.Contains(got, "ok") {
		t.Fatalf("allowed egress: %q", got)
	}
	// Default-deny (empty allowlist): the guest sees a denial.
	if got := run(NewAllowlistFetcher(nil)); !strings.Contains(got, "denied") {
		t.Fatalf("denied egress: %q", got)
	}
}

// A runaway guest that never presents is killed at the per-frame deadline.
func TestRunCancelsRunawayGuest(t *testing.T) {
	wasm := buildGuest(t, "testdata/busyguest", "GOWORK=off")

	lim := Limits{MemoryPages: 512, FrameTimeout: 150 * time.Millisecond}
	src := NewScriptSource(10, 3, []string{"up"})

	start := time.Now()
	err := Run(context.Background(), wasm, lim, Capabilities{}, src, &capture{}, io.Discard)
	if err != ErrFrameDeadline {
		t.Fatalf("Run err = %v, want ErrFrameDeadline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("cancellation took %s, deadline was 150ms", elapsed)
	}
}
