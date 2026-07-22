package abi

import "encoding/binary"

// Exec is the opt-in trusted-host command capability. It is deliberately not
// ambient WASI authority: the server must install the host function for a
// claimed app, and every invocation remains bounded by the session context.
const (
	ExecMaxName   = 4096
	ExecMaxArgs   = 256
	ExecMaxArg    = 64 << 10
	ExecMaxOutput = 1 << 20 // per stdout/stderr stream
)

const (
	ExecErrUnavailable int32 = -1
	ExecErrTooLarge    int32 = -2
	ExecErrFailed      int32 = -3
)

type ExecRequest struct {
	Name string
	Args []string
}

type ExecResponse struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

func EncodeExecRequest(r ExecRequest) []byte {
	b := make([]byte, 0, len(r.Name)+8)
	b = appendU32(b, len(r.Name))
	b = append(b, r.Name...)
	b = appendU32(b, len(r.Args))
	for _, arg := range r.Args {
		b = appendU32(b, len(arg))
		b = append(b, arg...)
	}
	return b
}

func DecodeExecRequest(b []byte) (ExecRequest, error) {
	var r ExecRequest
	name, rest, ok := takeU32Bytes(b)
	if !ok || len(rest) < 4 {
		return r, ErrShort
	}
	r.Name = string(name)
	n := binary.LittleEndian.Uint32(rest[:4])
	rest = rest[4:]
	if n > ExecMaxArgs {
		return ExecRequest{}, ErrShort
	}
	r.Args = make([]string, 0, n)
	for range n {
		arg, next, ok := takeU32Bytes(rest)
		if !ok {
			return ExecRequest{}, ErrShort
		}
		r.Args = append(r.Args, string(arg))
		rest = next
	}
	if len(rest) != 0 {
		return ExecRequest{}, ErrShort
	}
	return r, nil
}

func EncodeExecResponse(r ExecResponse) []byte {
	b := make([]byte, 0, len(r.Stdout)+len(r.Stderr)+12)
	b = binary.LittleEndian.AppendUint32(b, uint32(int32(r.ExitCode)))
	b = appendU32(b, len(r.Stdout))
	b = append(b, r.Stdout...)
	b = appendU32(b, len(r.Stderr))
	b = append(b, r.Stderr...)
	return b
}

func DecodeExecResponse(b []byte) (ExecResponse, error) {
	if len(b) < 4 {
		return ExecResponse{}, ErrShort
	}
	exitCode := int(int32(binary.LittleEndian.Uint32(b[:4])))
	stdout, rest, ok := takeU32Bytes(b[4:])
	if !ok {
		return ExecResponse{}, ErrShort
	}
	stderr, rest, ok := takeU32Bytes(rest)
	if !ok || len(rest) != 0 {
		return ExecResponse{}, ErrShort
	}
	return ExecResponse{ExitCode: exitCode, Stdout: append([]byte(nil), stdout...), Stderr: append([]byte(nil), stderr...)}, nil
}
