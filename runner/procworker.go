package runner

import (
	"context"
	"errors"
	"io"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// RunWorker is the entry point of the runner-worker process. It reads the
// session parameters from in, runs the guest in this process's wazero sandbox
// via the normal Run, and forwards every host call to the parent over out using
// the procproto. It returns when the guest finishes; the result is reported to
// the parent as the final opDone message.
//
// The worker owns only the sandbox; the parent owns the Source, Sink, and
// capabilities. That is the isolation boundary — see ProcessRunner.
func RunWorker(in io.Reader, out io.Writer) error {
	o, payload, err := readMsg(in)
	if err != nil {
		return err
	}
	if o != opStart {
		return errProtocol
	}
	lim, cli, wasm, err := decodeStart(payload)
	if err != nil {
		return err
	}
	if cli {
		// The process runner currently isolates the interactive TUI path; CLI
		// apps run in-process. Report a clean error to the parent.
		return writeMsg(out, opDone, encodeDone("process runner: CLI not supported", nil))
	}

	rpc := &workerRPC{in: in, out: out}
	caps := Capabilities{
		KV:    proxyKV{rpc},
		Bus:   proxyBus{rpc},
		Auth:  proxyAuth{rpc},
		Env:   proxyEnv{rpc},
		Fetch: proxyFetch{rpc},
	}
	logs := &boundedBuffer{max: maxSessionLog}
	runErr := Run(context.Background(), wasm, lim, caps, &proxySource{rpc}, &proxySink{rpc}, logs)

	errStr := ""
	if runErr != nil {
		errStr = runErr.Error()
	}
	return writeMsg(out, opDone, encodeDone(errStr, []byte(logs.String())))
}

// maxSessionLog bounds the guest log captured and shipped to the parent.
const maxSessionLog = 64 << 10

// workerRPC performs one lock-step request/response over the worker's pipes. The
// guest runs single-threaded, so calls are naturally serialized.
type workerRPC struct {
	in  io.Reader
	out io.Writer
}

func (r *workerRPC) call(o op, payload []byte) ([]byte, error) {
	if err := writeMsg(r.out, o, payload); err != nil {
		return nil, err
	}
	ro, rp, err := readMsg(r.in)
	if err != nil {
		return nil, err
	}
	if ro != opResp {
		return nil, errProtocol
	}
	return rp, nil
}

type proxySource struct{ rpc *workerRPC }

func (s *proxySource) Next(context.Context) (abi.Event, bool) {
	rp, err := s.rpc.call(opRecv, nil)
	if err != nil || len(rp) == 0 || rp[0] == 0 {
		return abi.Event{}, false
	}
	ev, err := abi.DecodeEvent(rp[1:])
	if err != nil {
		return abi.Event{}, false
	}
	return ev, true
}

type proxySink struct{ rpc *workerRPC }

func (s *proxySink) Present(f abi.Frame) { _, _ = s.rpc.call(opPresent, abi.EncodeFrame(f)) }

type proxyKV struct{ rpc *workerRPC }

func (k proxyKV) Get(key string) ([]byte, bool, error) {
	rp, err := k.rpc.call(opKVGet, []byte(key))
	if err != nil || len(rp) == 0 {
		return nil, false, errRPC
	}
	switch rp[0] {
	case 0:
		return append([]byte(nil), rp[1:]...), true, nil
	case 1:
		return nil, false, nil
	default:
		return nil, false, errRPC
	}
}

func (k proxyKV) Set(key string, value []byte) error {
	rp, err := k.rpc.call(opKVSet, encodeKeyValue(key, value))
	if err != nil || len(rp) == 0 {
		return errRPC
	}
	switch rp[0] {
	case 0:
		return nil
	case 1:
		return ErrQuota
	default:
		return errRPC
	}
}

func (k proxyKV) Delete(key string) error {
	rp, err := k.rpc.call(opKVDel, []byte(key))
	if err != nil || len(rp) == 0 || rp[0] != 0 {
		return errRPC
	}
	return nil
}

type proxyAuth struct{ rpc *workerRPC }

func (a proxyAuth) Whoami() Identity {
	rp, err := a.rpc.call(opAuth, nil)
	if err != nil || len(rp) == 0 || rp[0] == 0 {
		return Identity{}
	}
	id, err := abi.DecodeIdentity(rp[1:])
	if err != nil {
		return Identity{}
	}
	return Identity{User: id.User, Authenticated: id.Authenticated}
}

type proxyEnv struct{ rpc *workerRPC }

func (e proxyEnv) Get(key string) (string, bool) {
	rp, err := e.rpc.call(opEnv, []byte(key))
	if err != nil || len(rp) == 0 || rp[0] == 0 {
		return "", false
	}
	return string(rp[1:]), true
}

type proxyFetch struct{ rpc *workerRPC }

func (f proxyFetch) Fetch(_ context.Context, req abi.FetchRequest) (abi.FetchResponse, error) {
	rp, err := f.rpc.call(opFetch, abi.EncodeFetchRequest(req))
	if err != nil || len(rp) == 0 {
		return abi.FetchResponse{}, errRPC
	}
	switch rp[0] {
	case 0:
		resp, err := abi.DecodeFetchResponse(rp[1:])
		if err != nil {
			return abi.FetchResponse{}, errRPC
		}
		return resp, nil
	case 1:
		return abi.FetchResponse{}, ErrEgressDenied
	case 4:
		return abi.FetchResponse{}, errEgressUnavailable
	default:
		return abi.FetchResponse{}, errRPC
	}
}

type proxyBus struct{ rpc *workerRPC }

func (b proxyBus) Open() Subscriber { return &proxySubscriber{rpc: b.rpc} }

func (b proxyBus) Publish(topic string, data []byte) int {
	rp, err := b.rpc.call(opBusPub, encodeKeyValue(topic, data))
	if err != nil || len(rp) < 4 {
		return 0
	}
	return int(int32(uint32(rp[0]) | uint32(rp[1])<<8 | uint32(rp[2])<<16 | uint32(rp[3])<<24))
}

// proxySubscriber forwards Subscribe to the parent; delivery of messages rides
// the recv channel (the parent's Source multiplexes input + bus), so Events is
// never used here.
type proxySubscriber struct{ rpc *workerRPC }

func (s *proxySubscriber) Subscribe(topic string)   { _, _ = s.rpc.call(opBusSub, []byte(topic)) }
func (s *proxySubscriber) Events() <-chan abi.Event { return nil }
func (s *proxySubscriber) Close()                   {}

var (
	errRPC               = errors.New("runner: worker rpc failed")
	errEgressUnavailable = errors.New("runner: egress capability unavailable")
)
