package runner

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/abi"
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

	wantArgs := []string{"one", "two words"}
	gotLim, cli, gotMask, gotArgs, gotWasm, err := decodeStart(encodeStart(lim, true, mask, wantArgs, wasm))
	if err != nil {
		t.Fatalf("decodeStart: %v", err)
	}
	if !cli {
		t.Error("cli flag flipped")
	}
	if gotLim != lim {
		t.Errorf("limits = %+v, want %+v", gotLim, lim)
	}
	if gotMask != mask {
		t.Errorf("capMask = %#b, want %#b", gotMask, mask)
	}
	if len(gotArgs) != len(wantArgs) || gotArgs[0] != wantArgs[0] || gotArgs[1] != wantArgs[1] {
		t.Errorf("args = %q, want %q", gotArgs, wantArgs)
	}
	if string(gotWasm) != string(wasm) {
		t.Errorf("wasm = %x, want %x", gotWasm, wasm)
	}
}

func TestReadWorkerMessageRejectsOperationOversizeBeforePayload(t *testing.T) {
	var header [5]byte
	header[0] = byte(opKVGet)
	binary.LittleEndian.PutUint32(header[1:], abi.KVMaxKey+1)
	if _, _, err := readMsgBounded(bytes.NewReader(header[:]), maxWorkerPayload); !errors.Is(err, errProtocol) {
		t.Fatalf("oversized key error = %v, want protocol error", err)
	}

	header[0] = 0xff
	binary.LittleEndian.PutUint32(header[1:], 1)
	if _, _, err := readMsgBounded(bytes.NewReader(header[:]), maxWorkerPayload); !errors.Is(err, errProtocol) {
		t.Fatalf("unknown operation error = %v, want protocol error", err)
	}
}

func TestEncodeDecodeDoneRoundTrip(t *testing.T) {
	payload := encodeDone("guest failed", "thanks", []byte("logs"))
	errText, goodbye, logs, ok := decodeDone(payload)
	if !ok || errText != "guest failed" || goodbye != "thanks" || string(logs) != "logs" {
		t.Fatalf("decodeDone = %q, %q, %q, %v", errText, goodbye, logs, ok)
	}
	if _, _, _, ok := decodeDone(payload[:len(payload)-1-len("thanks")]); ok {
		t.Fatal("truncated done payload accepted")
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
		Exec:  LocalCommander{},
	}
	if m, want := capMask(full), capKV|capBus|capAuth|capEnv|capFetch|capExec; m != want {
		t.Errorf("full caps mask = %#b, want %#b", m, want)
	}
	if m := capMask(Capabilities{KV: NewMemStore(0, 0)}); m != capKV {
		t.Errorf("kv-only mask = %#b, want %#b", m, capKV)
	}
}
