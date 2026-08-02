package runner

import (
	"encoding/binary"
	"io"
	"time"

	"github.com/Ceinl/plumtree/internal/protocol"
)

// Cross-process runner protocol. The gateway (parent) spawns a worker process
// that owns the wazero sandbox and the untrusted guest; the parent owns all
// capabilities and terminal I/O. Every host call the guest makes — recv,
// present, kv_*, bus_*, auth, env, fetch — is forwarded from the worker to the
// parent as a request frame, and the parent replies with a response frame. This
// is the production isolation boundary: a host-function or runtime bug in the
// worker cannot reach the control plane's state.
//
// Framing is strictly lock-step and worker-driven: the parent sends one opStart,
// then only ever replies (opResp) to a worker request. The worker sends exactly
// one request and reads exactly one reply at a time, so the two sides never
// deadlock writing at each other.
//
// All multi-byte integers are little-endian.

type op = protocol.Op

const (
	opStart        = protocol.OpStart
	opResp         = protocol.OpResp
	opRecv         = protocol.OpRecv
	opPresent      = protocol.OpPresent
	opKVGet        = protocol.OpKVGet
	opKVSet        = protocol.OpKVSet
	opKVDel        = protocol.OpKVDel
	opBusSub       = protocol.OpBusSub
	opBusPub       = protocol.OpBusPub
	opAuth         = protocol.OpAuth
	opEnv          = protocol.OpEnv
	opFetch        = protocol.OpFetch
	opDone         = protocol.OpDone
	opOutput       = protocol.OpOutput
	opKVList       = protocol.OpKVList
	opKVCAS        = protocol.OpKVCAS
	opExec         = protocol.OpExec
	opTimerStart   = protocol.OpTimerStart
	opTimerCancel  = protocol.OpTimerCancel
	opOutputStderr = protocol.OpOutputStderr
	maxFrame       = protocol.MaxFrame
)

var errProtocol = protocol.ErrProtocol

func writeMsg(w io.Writer, o op, payload []byte) error {
	return protocol.Write(w, o, payload)
}

func readMsg(r io.Reader) (op, []byte, error) {
	return protocol.Read(r)
}

func readMsgBounded(r io.Reader, maxFor func(op) uint32) (op, []byte, error) {
	return protocol.ReadBounded(r, maxFor)
}

// Capability-presence bits in the start payload. The parent sets a bit for each
// capability it actually holds and the worker installs a proxy only for those,
// so a capability the parent lacks is nil in the worker too — its host function
// then returns the same "unavailable" code as the in-process path instead of a
// proxy that silently reports not-found/empty/no-op. Without this the process
// (production) path would diverge from in-process for nil Env/Bus/Auth.
const (
	capKV    byte = 1 << 0
	capBus   byte = 1 << 1
	capAuth  byte = 1 << 2
	capEnv   byte = 1 << 3
	capFetch byte = 1 << 4
	capExec  byte = 1 << 5
)

// capMask returns the presence bitmask for caps.
func capMask(caps Capabilities) byte {
	var m byte
	if caps.KV != nil {
		m |= capKV
	}
	if caps.Bus != nil {
		m |= capBus
	}
	if caps.Auth != nil {
		m |= capAuth
	}
	if caps.Env != nil {
		m |= capEnv
	}
	if caps.Fetch != nil {
		m |= capFetch
	}
	if caps.Exec != nil {
		m |= capExec
	}
	return m
}

// startPayload encodes the session parameters the parent hands the worker.
// Layout: [appType u8][memPages u32][frameTimeoutNs i64][sessionTimeoutNs i64]
// [maxEventsPerSec u32][maxFramesPerSec u32][capMask u8][argc u32]
// repeated [argLen u32][arg], then [wasm...]. appType is 0 for TUI and 1 for
// CLI. Arguments are meaningful only in CLI mode.
func encodeStart(lim Limits, cli bool, caps byte, args []string, wasm []byte) []byte {
	b := make([]byte, 0, 34+len(wasm))
	var appType byte
	if cli {
		appType = 1
	}
	b = append(b, appType)
	b = binary.LittleEndian.AppendUint32(b, lim.MemoryPages)
	b = binary.LittleEndian.AppendUint64(b, uint64(lim.FrameTimeout))
	b = binary.LittleEndian.AppendUint64(b, uint64(lim.SessionTimeout))
	b = binary.LittleEndian.AppendUint32(b, uint32(lim.MaxEventsPerSec))
	b = binary.LittleEndian.AppendUint32(b, uint32(lim.MaxFramesPerSec))
	b = append(b, caps)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(args)))
	for _, arg := range args {
		b = binary.LittleEndian.AppendUint32(b, uint32(len(arg)))
		b = append(b, arg...)
	}
	b = append(b, wasm...)
	return b
}

func decodeStart(b []byte) (lim Limits, cli bool, caps byte, args []string, wasm []byte, err error) {
	if len(b) < 34 || b[0] > 1 {
		return Limits{}, false, 0, nil, nil, errProtocol
	}
	cli = b[0] == 1
	lim.MemoryPages = binary.LittleEndian.Uint32(b[1:5])
	lim.FrameTimeout = time.Duration(binary.LittleEndian.Uint64(b[5:13]))
	lim.SessionTimeout = time.Duration(binary.LittleEndian.Uint64(b[13:21]))
	lim.MaxEventsPerSec = int(binary.LittleEndian.Uint32(b[21:25]))
	lim.MaxFramesPerSec = int(binary.LittleEndian.Uint32(b[25:29]))
	caps = b[29]
	argc := binary.LittleEndian.Uint32(b[30:34])
	b = b[34:]
	if uint64(argc) > uint64(len(b))/4 {
		return Limits{}, false, 0, nil, nil, errProtocol
	}
	args = make([]string, 0, argc)
	for range argc {
		if len(b) < 4 {
			return Limits{}, false, 0, nil, nil, errProtocol
		}
		n := binary.LittleEndian.Uint32(b[:4])
		b = b[4:]
		if uint64(n) > uint64(len(b)) {
			return Limits{}, false, 0, nil, nil, errProtocol
		}
		args = append(args, string(b[:n]))
		b = b[n:]
	}
	wasm = append([]byte(nil), b...)
	return lim, cli, caps, args, wasm, nil
}

// keyValuePayload encodes [u16 keyLen][key][value] for kv_set / bus_pub.
func encodeKeyValue(key string, value []byte) []byte {
	b := make([]byte, 0, 2+len(key)+len(value))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(key)))
	b = append(b, key...)
	b = append(b, value...)
	return b
}

func decodeKeyValue(b []byte) (key string, value []byte, ok bool) {
	if len(b) < 2 {
		return "", nil, false
	}
	n := int(binary.LittleEndian.Uint16(b[0:2]))
	if len(b) < 2+n {
		return "", nil, false
	}
	return string(b[2 : 2+n]), b[2+n:], true
}

func encodeKVListRequest(prefix string, limit int) []byte {
	b := binary.LittleEndian.AppendUint16(nil, uint16(limit))
	return append(b, prefix...)
}

func decodeKVListRequest(b []byte) (prefix string, limit int, ok bool) {
	if len(b) < 2 {
		return "", 0, false
	}
	return string(b[2:]), int(binary.LittleEndian.Uint16(b[:2])), true
}

func encodeKVCAS(key string, expected [32]byte, value []byte) []byte {
	b := binary.LittleEndian.AppendUint16(nil, uint16(len(key)))
	b = append(b, key...)
	b = append(b, expected[:]...)
	return append(b, value...)
}

func decodeKVCAS(b []byte) (key string, expected [32]byte, value []byte, ok bool) {
	if len(b) < 2 {
		return "", expected, nil, false
	}
	n := int(binary.LittleEndian.Uint16(b[:2]))
	if n == 0 || len(b) < 2+n+len(expected) {
		return "", expected, nil, false
	}
	key = string(b[2 : 2+n])
	copy(expected[:], b[2+n:2+n+len(expected)])
	return key, expected, b[2+n+len(expected):], true
}

// donePayload encodes [u32 errLen][err][u32 goodbyeLen][goodbye][logs].
func encodeDone(errStr, goodbye string, logs []byte) []byte {
	b := make([]byte, 0, 8+len(errStr)+len(goodbye)+len(logs))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(errStr)))
	b = append(b, errStr...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(goodbye)))
	b = append(b, goodbye...)
	b = append(b, logs...)
	return b
}

func decodeDone(b []byte) (errStr string, goodbye string, logs []byte, ok bool) {
	if len(b) < 4 {
		return "", "", nil, false
	}
	n := int(binary.LittleEndian.Uint32(b[0:4]))
	if len(b) < 4+n {
		return "", "", nil, false
	}
	errStr = string(b[4 : 4+n])
	b = b[4+n:]
	if len(b) < 4 {
		return "", "", nil, false
	}
	n = int(binary.LittleEndian.Uint32(b[0:4]))
	if len(b) < 4+n {
		return "", "", nil, false
	}
	goodbye = string(b[4 : 4+n])
	logs = b[4+n:]
	return errStr, goodbye, logs, true
}
