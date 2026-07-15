package runner

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/Ceinl/plumtree/sdk/abi"
)

// ProcessRunner runs a guest in a separate worker process and serves its host
// calls from this process. It is a drop-in for the in-process TUI and CLI
// paths: the worker owns the wazero sandbox (limits, watchdog, the untrusted
// guest) while the parent owns the Source, Sink, and capabilities. The two speak
// the lock-step procproto over the worker's stdin/stdout.
//
// This is the production isolation split: a bug in the WASM runtime or a host
// function lives in a disposable child process, not in the control plane.
type ProcessRunner struct {
	// WorkerPath is the runner-worker executable to spawn (see
	// cmd/plumtree-runner-worker).
	WorkerPath string
	// WorkerEndpoint is a remote runner-broker endpoint. Supported forms are
	// unix:///path/to/socket and tcp://host:port. Production uses a Unix socket
	// into a separate, networkless container so a native WASM-runtime escape
	// cannot inherit the gateway's credentials, filesystem, or network.
	WorkerEndpoint string
	// WorkerToken authenticates the gateway to a remote runner broker.
	WorkerToken string
}

// NewProcessRunner returns a ProcessRunner that spawns workerPath per session.
func NewProcessRunner(workerPath string) *ProcessRunner {
	return &ProcessRunner{WorkerPath: workerPath}
}

// NewRemoteProcessRunner returns a ProcessRunner backed by a runner broker.
// The broker owns the disposable worker process; this process retains all app
// capabilities and communicates with it over the existing lock-step protocol.
func NewRemoteProcessRunner(endpoint, token string) *ProcessRunner {
	return &ProcessRunner{WorkerEndpoint: endpoint, WorkerToken: token}
}

// Run spawns a worker for one TUI session and serves its capability calls until
// the guest exits. It returns the same errors as the in-process Run (a guest
// failure, ErrFrameDeadline surfaced by the worker, or ctx.Err()).
func (pr *ProcessRunner) Run(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, src Source, sink Sink, logs io.Writer) error {
	return pr.run(ctx, wasm, lim, caps, false, nil, src, sink, nil, logs)
}

// RunCLI spawns a worker for one non-interactive CLI invocation. Guest output
// is filtered inside the worker and streamed back to out over the process
// protocol; args become the guest's command-line arguments.
func (pr *ProcessRunner) RunCLI(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, args []string, out io.Writer) error {
	return pr.run(ctx, wasm, lim, caps, true, args, nil, nil, out, nil)
}

func (pr *ProcessRunner) run(ctx context.Context, wasm []byte, lim Limits, caps Capabilities, cli bool, args []string, src Source, sink Sink, out, logs io.Writer) error {
	if err := validateLimits(lim); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	worker, err := pr.startWorker(ctx)
	if err != nil {
		cancel()
		return err
	}
	// Cancel before closing/waiting so a parent-side I/O/protocol error also
	// terminates a worker that may be blocked waiting for its opResp.
	defer func() {
		cancel()
		worker.close()
	}()

	// Bus delivery rides the recv channel: bind the real subscription to the
	// real Source so src.Next returns KindMessage events, exactly as in-process.
	var sub Subscriber
	if !cli && caps.Bus != nil {
		sub = caps.Bus.Open()
		defer sub.Close()
		if bb, ok := src.(BusBinder); ok {
			bb.BindBus(sub.Events())
		}
	}

	if err := writeMsg(worker.in, opStart, encodeStart(lim, cli, capMask(caps), args, wasm)); err != nil {
		return err
	}

	for {
		o, payload, err := readMsgBounded(worker.out, maxWorkerPayload)
		if err != nil {
			// Worker exited or pipe closed. Prefer the caller's cancellation cause.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("runner worker exited unexpectedly: %s", worker.failure())
			}
			return err
		}
		if o == opDone {
			errStr, logBytes, ok := decodeDone(payload)
			if !ok || len(errStr) > maxWorkerError || len(logBytes) > maxSessionLog {
				return errProtocol
			}
			if logs != nil && len(logBytes) > 0 {
				_, _ = logs.Write(logBytes)
			}
			if errStr != "" {
				if errStr == ErrFrameDeadline.Error() {
					return ErrFrameDeadline
				}
				return errors.New(errStr)
			}
			return nil
		}
		if err := pr.serve(ctx, worker.in, o, payload, caps, src, sink, sub, out); err != nil {
			return err
		}
	}
}

const (
	maxEncodedFrame = 8 + 150_000*11
	maxWorkerOutput = 64 << 10
	maxWorkerError  = 64 << 10
	maxEncodedFetch = 2 + abi.FetchMaxMethod + 2 + abi.FetchMaxURL + 4 + abi.FetchMaxBody
)

func maxWorkerPayload(o op) uint32 {
	switch o {
	case opRecv, opAuth:
		return 0
	case opPresent:
		return maxEncodedFrame
	case opKVGet, opKVDel:
		return abi.KVMaxKey
	case opKVSet:
		return 2 + abi.KVMaxKey + abi.KVMaxValue
	case opBusSub:
		return abi.BusMaxTopic
	case opBusPub:
		return 2 + abi.BusMaxTopic + abi.BusMaxData
	case opEnv:
		return abi.EnvMaxKey
	case opFetch:
		return maxEncodedFetch
	case opDone:
		return 4 + maxWorkerError + maxSessionLog
	case opOutput:
		return maxWorkerOutput
	default:
		return 0
	}
}

type workerTransport struct {
	in      io.Writer
	out     io.Reader
	close   func()
	failure func() string
}

func (pr *ProcessRunner) startWorker(ctx context.Context) (*workerTransport, error) {
	if pr.WorkerPath != "" && pr.WorkerEndpoint != "" {
		return nil, errors.New("runner: configure either a local worker path or a remote worker endpoint, not both")
	}
	if pr.WorkerEndpoint != "" {
		return pr.dialWorker(ctx)
	}
	if pr.WorkerPath == "" {
		return nil, errors.New("runner: worker path or endpoint is required")
	}

	workDir, err := os.MkdirTemp("", "plumtree-runner-*")
	if err != nil {
		return nil, fmt.Errorf("runner: create worker scratch dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, pr.WorkerPath)
	cmd.Dir = workDir
	// Never inherit gateway credentials or operator-controlled loader settings.
	// This is defense in depth for local process mode; production additionally
	// places the broker and workers in their own networkless container.
	cmd.Env = []string{
		"HOME=" + workDir,
		"TMPDIR=" + workDir,
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(workDir)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(workDir)
		return nil, err
	}
	stderr := &boundedBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(workDir)
		return nil, err
	}
	return &workerTransport{
		in:  stdin,
		out: stdout,
		close: func() {
			_ = stdin.Close()
			_ = stdout.Close()
			_ = cmd.Wait()
			_ = os.RemoveAll(workDir)
		},
		failure: stderr.String,
	}, nil
}

func (pr *ProcessRunner) dialWorker(ctx context.Context) (*workerTransport, error) {
	network, address, ok := strings.Cut(pr.WorkerEndpoint, "://")
	if !ok || (network != "unix" && network != "tcp") || address == "" {
		return nil, fmt.Errorf("runner: invalid worker endpoint %q (want unix:///path or tcp://host:port)", pr.WorkerEndpoint)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("runner: connect to broker: %w", err)
	}
	if err := writeBrokerAuth(conn, pr.WorkerToken); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("runner: authenticate to broker: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return &workerTransport{
		in:  conn,
		out: conn,
		close: func() {
			stop()
			_ = conn.Close()
		},
		failure: func() string { return "remote broker closed the session" },
	}, nil
}

// serve handles one worker request and writes the opResp reply.
func (pr *ProcessRunner) serve(ctx context.Context, w io.Writer, o op, payload []byte, caps Capabilities, src Source, sink Sink, sub Subscriber, out io.Writer) error {
	switch o {
	case opRecv:
		if src == nil {
			return errProtocol
		}
		ev, ok := src.Next(ctx)
		if !ok {
			return writeMsg(w, opResp, []byte{0})
		}
		return writeMsg(w, opResp, append([]byte{1}, abi.EncodeEvent(ev)...))

	case opPresent:
		if sink == nil {
			return errProtocol
		}
		f, err := abi.DecodeFrame(payload)
		if err != nil || !validFrame(f) {
			return errProtocol
		}
		sink.Present(f)
		return writeMsg(w, opResp, nil)

	case opKVGet:
		if caps.KV == nil || len(payload) == 0 || len(payload) > abi.KVMaxKey {
			return writeMsg(w, opResp, []byte{2})
		}
		val, found, err := caps.KV.Get(string(payload))
		switch {
		case err != nil:
			return writeMsg(w, opResp, []byte{2})
		case !found:
			return writeMsg(w, opResp, []byte{1})
		default:
			return writeMsg(w, opResp, append([]byte{0}, val...))
		}

	case opKVSet:
		key, val, ok := decodeKeyValue(payload)
		if !ok || caps.KV == nil || len(key) == 0 || len(key) > abi.KVMaxKey || len(val) > abi.KVMaxValue {
			return writeMsg(w, opResp, []byte{2})
		}
		if err := caps.KV.Set(key, val); err != nil {
			if errors.Is(err, ErrQuota) {
				return writeMsg(w, opResp, []byte{1})
			}
			return writeMsg(w, opResp, []byte{2})
		}
		return writeMsg(w, opResp, []byte{0})

	case opKVDel:
		if caps.KV == nil || len(payload) == 0 || len(payload) > abi.KVMaxKey {
			return writeMsg(w, opResp, []byte{2})
		}
		if err := caps.KV.Delete(string(payload)); err != nil {
			return writeMsg(w, opResp, []byte{2})
		}
		return writeMsg(w, opResp, []byte{0})

	case opBusSub:
		if len(payload) == 0 || len(payload) > abi.BusMaxTopic {
			return errProtocol
		}
		if sub != nil {
			sub.Subscribe(string(payload))
		}
		return writeMsg(w, opResp, nil)

	case opBusPub:
		topic, data, ok := decodeKeyValue(payload)
		n := 0
		if !ok || len(topic) == 0 || len(topic) > abi.BusMaxTopic || len(data) > abi.BusMaxData {
			return errProtocol
		}
		if caps.Bus != nil {
			n = caps.Bus.Publish(topic, data)
		}
		var out [4]byte
		binary.LittleEndian.PutUint32(out[:], uint32(n))
		return writeMsg(w, opResp, out[:])

	case opAuth:
		if len(payload) != 0 {
			return errProtocol
		}
		if caps.Auth == nil {
			return writeMsg(w, opResp, []byte{0})
		}
		id := caps.Auth.Whoami()
		enc := abi.EncodeIdentity(abi.Identity{User: id.User, Authenticated: id.Authenticated})
		return writeMsg(w, opResp, append([]byte{1}, enc...))

	case opEnv:
		if len(payload) == 0 || len(payload) > abi.EnvMaxKey {
			return errProtocol
		}
		if caps.Env == nil {
			return writeMsg(w, opResp, []byte{0})
		}
		val, found := caps.Env.Get(string(payload))
		if !found {
			return writeMsg(w, opResp, []byte{0})
		}
		return writeMsg(w, opResp, append([]byte{1}, val...))

	case opFetch:
		return pr.serveFetch(ctx, w, payload, caps)

	case opOutput:
		if out == nil || len(payload) > maxWorkerOutput {
			return errProtocol
		}
		if _, err := out.Write(payload); err != nil {
			return err
		}
		return writeMsg(w, opResp, nil)

	default:
		return errProtocol
	}
}

func validFrame(f abi.Frame) bool {
	return f.W >= 1 && f.W <= 500 && f.H >= 1 && f.H <= 300 && f.W <= 150_000/f.H && len(f.Cells) == f.W*f.H
}

// boundedBuffer captures up to max bytes of worker stderr (panics, fatal logs)
// to surface on an unexpected exit, discarding the rest.
type boundedBuffer struct {
	max int
	b   []byte
}

func (bb *boundedBuffer) Write(p []byte) (int, error) {
	if room := bb.max - len(bb.b); room > 0 {
		if len(p) > room {
			bb.b = append(bb.b, p[:room]...)
		} else {
			bb.b = append(bb.b, p...)
		}
	}
	return len(p), nil
}

func (bb *boundedBuffer) String() string { return string(bb.b) }

func (pr *ProcessRunner) serveFetch(ctx context.Context, w io.Writer, payload []byte, caps Capabilities) error {
	// Fetch status bytes: 0=ok,1=denied,2=toolarge,3=internal,4=unavail.
	if caps.Fetch == nil {
		return writeMsg(w, opResp, []byte{4})
	}
	req, err := abi.DecodeFetchRequest(payload)
	if err != nil || len(req.Method) > abi.FetchMaxMethod || len(req.URL) > abi.FetchMaxURL || len(req.Body) > abi.FetchMaxBody {
		return writeMsg(w, opResp, []byte{3})
	}
	resp, err := caps.Fetch.Fetch(ctx, req)
	switch {
	case errors.Is(err, ErrEgressDenied):
		return writeMsg(w, opResp, []byte{1})
	case err != nil:
		return writeMsg(w, opResp, []byte{3})
	default:
		return writeMsg(w, opResp, append([]byte{0}, abi.EncodeFetchResponse(resp)...))
	}
}
