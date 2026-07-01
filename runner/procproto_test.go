package runner

import (
	"testing"
	"time"
)

// The start payload must round-trip the limits, appType, capability mask, and
// wasm. The capability mask is what keeps the worker's installed capabilities in
// step with the parent's: a capability the parent lacks must decode as an absent
// bit so the worker installs nil (and the guest sees the in-process "unavailable"
// code) rather than a proxy.
func TestEncodeDecodeStartRoundTrip(t *testing.T) {
	lim := Limits{
		MemoryPages:     42,
		FrameTimeout:    250 * time.Millisecond,
		SessionTimeout:  90 * time.Second,
		MaxEventsPerSec: 1000,
		MaxFramesPerSec: 60,
	}
	wasm := []byte{0x00, 0x61, 0x73, 0x6d, 1, 2, 3}
	mask := capKV | capEnv // deliberately omit Bus/Auth/Fetch

	gotLim, cli, gotMask, gotWasm, err := decodeStart(encodeStart(lim, false, mask, wasm))
	if err != nil {
		t.Fatalf("decodeStart: %v", err)
	}
	if cli {
		t.Error("cli flag flipped")
	}
	if gotLim != lim {
		t.Errorf("limits = %+v, want %+v", gotLim, lim)
	}
	if gotMask != mask {
		t.Errorf("capMask = %#b, want %#b", gotMask, mask)
	}
	if string(gotWasm) != string(wasm) {
		t.Errorf("wasm = %x, want %x", gotWasm, wasm)
	}
}

// capMask reflects exactly the capabilities present in the set, so the worker
// installs a proxy for those and only those.
func TestCapMask(t *testing.T) {
	if m := capMask(Capabilities{}); m != 0 {
		t.Errorf("empty caps mask = %#b, want 0", m)
	}
	full := Capabilities{
		KV:    NewMemStore(0, 0),
		Bus:   NewMemBus(),
		Auth:  StaticAuth{},
		Env:   MapEnv{},
		Fetch: NewAllowlistFetcher([]string{"example.com"}),
	}
	if m, want := capMask(full), capKV|capBus|capAuth|capEnv|capFetch; m != want {
		t.Errorf("full caps mask = %#b, want %#b", m, want)
	}
	if m := capMask(Capabilities{KV: NewMemStore(0, 0)}); m != capKV {
		t.Errorf("kv-only mask = %#b, want %#b", m, capKV)
	}
}
