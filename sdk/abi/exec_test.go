package abi

import (
	"encoding/binary"
	"testing"
)

func TestExecRequestAndResponseRoundTrip(t *testing.T) {
	req := ExecRequest{Name: "sh", Args: []string{"-lc", "printf hello"}}
	gotReq, err := DecodeExecRequest(EncodeExecRequest(req))
	if err != nil || gotReq.Name != req.Name || len(gotReq.Args) != 2 || gotReq.Args[1] != req.Args[1] {
		t.Fatalf("request round trip = %#v, %v", gotReq, err)
	}
	resp := ExecResponse{ExitCode: 7, Stdout: []byte("out"), Stderr: []byte("err")}
	gotResp, err := DecodeExecResponse(EncodeExecResponse(resp))
	if err != nil || gotResp.ExitCode != 7 || string(gotResp.Stdout) != "out" || string(gotResp.Stderr) != "err" {
		t.Fatalf("response round trip = %#v, %v", gotResp, err)
	}
}

func TestDecodeExecRejectsOversizedFields(t *testing.T) {
	name := make([]byte, ExecMaxName+1)
	request := make([]byte, 4+len(name)+4)
	binary.LittleEndian.PutUint32(request, uint32(len(name)))
	copy(request[4:], name)
	if _, err := DecodeExecRequest(request); err == nil {
		t.Fatal("DecodeExecRequest accepted an oversized name")
	}

	arg := make([]byte, ExecMaxArg+1)
	request = make([]byte, 4+1+4+4+len(arg))
	binary.LittleEndian.PutUint32(request, 1)
	request[4] = 'x'
	binary.LittleEndian.PutUint32(request[5:], 1)
	binary.LittleEndian.PutUint32(request[9:], uint32(len(arg)))
	copy(request[13:], arg)
	if _, err := DecodeExecRequest(request); err == nil {
		t.Fatal("DecodeExecRequest accepted an oversized argument")
	}
}

func TestDecodeExecRejectsMalformedInput(t *testing.T) {
	for _, b := range [][]byte{nil, {1, 0, 0, 0}, EncodeExecRequest(ExecRequest{Name: "x", Args: []string{"y"}})[:9]} {
		if _, err := DecodeExecRequest(b); err == nil {
			t.Fatalf("DecodeExecRequest(%v) accepted malformed input", b)
		}
	}
}
