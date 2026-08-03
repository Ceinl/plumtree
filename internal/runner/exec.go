package runner

import (
	"context"
	"errors"
	"os/exec"

	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Commander is the high-authority capability that executes a local process.
// It must never be installed for an untrusted app or by default.
type Commander interface {
	Run(context.Context, abi.ExecRequest) (abi.ExecResponse, error)
}

// LocalCommander executes programs as the server OS user, inheriting the
// server's working directory and environment. This is intentionally powerful:
// operators opt into it for trusted apps on private/self-hosted machines.
type LocalCommander struct{}

var ErrExecTooLarge = errors.New("runner: host command output too large")

func (LocalCommander) Run(ctx context.Context, req abi.ExecRequest) (abi.ExecResponse, error) {
	if !validExecRequest(req) {
		return abi.ExecResponse{}, ErrExecTooLarge
	}
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, req.Name, req.Args...)
	configureCommandGroup(cmd)
	stdout := &execBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	stderr := &execBuffer{max: abi.ExecMaxOutput, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.overflow || stderr.overflow {
		return abi.ExecResponse{}, ErrExecTooLarge
	}
	if ctx.Err() != nil {
		return abi.ExecResponse{}, ctx.Err()
	}
	resp := abi.ExecResponse{ExitCode: 0, Stdout: stdout.b, Stderr: stderr.b}
	if err == nil {
		return resp, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		resp.ExitCode = exitErr.ExitCode()
		return resp, nil
	}
	return abi.ExecResponse{}, err
}

type execBuffer struct {
	max      int
	b        []byte
	overflow bool
	cancel   context.CancelFunc
}

func (b *execBuffer) Write(p []byte) (int, error) {
	n := min(max(b.max-len(b.b), 0), len(p))
	if n > 0 {
		b.b = append(b.b, p[:n]...)
	}
	if n < len(p) {
		b.overflow = true
		b.cancel()
	}
	return len(p), nil
}

func validExecRequest(req abi.ExecRequest) bool {
	if req.Name == "" || len(req.Name) > abi.ExecMaxName || len(req.Args) > abi.ExecMaxArgs {
		return false
	}
	for _, arg := range req.Args {
		if len(arg) > abi.ExecMaxArg {
			return false
		}
	}
	return true
}

func registerExec(b wazero.HostModuleBuilder, commander Commander) wazero.HostModuleBuilder {
	return b.NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, reqPtr, reqLen, outPtr, outCap int32) int32 {
			if commander == nil {
				return abi.ExecErrUnavailable
			}
			if reqLen <= 0 || outCap < 0 {
				return abi.ExecErrTooLarge
			}
			raw, ok := m.Memory().Read(uint32(reqPtr), uint32(reqLen))
			if !ok {
				return abi.ExecErrFailed
			}
			req, err := abi.DecodeExecRequest(raw)
			if err != nil || !validExecRequest(req) {
				return abi.ExecErrTooLarge
			}
			resp, err := commander.Run(ctx, req)
			if errors.Is(err, ErrExecTooLarge) {
				return abi.ExecErrTooLarge
			}
			if err != nil {
				return abi.ExecErrFailed
			}
			if len(resp.Stdout) > abi.ExecMaxOutput || len(resp.Stderr) > abi.ExecMaxOutput {
				return abi.ExecErrTooLarge
			}
			enc := abi.EncodeExecResponse(resp)
			n := int32(len(enc))
			if n > outCap {
				return n
			}
			if n > 0 && !m.Memory().Write(uint32(outPtr), enc) {
				return abi.ExecErrFailed
			}
			return n
		}).
		Export("exec")
}
