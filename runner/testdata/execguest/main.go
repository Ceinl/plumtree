package main

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"unsafe"
)

//go:wasmimport plumtree exec
func hostExec(reqPtr, reqLen, outPtr, outCap int32) int32

func ptr(b []byte) int32 { return int32(uintptr(unsafe.Pointer(&b[0]))) }

func appendString(b []byte, s string) []byte {
	b = binary.LittleEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

func main() {
	req := appendString(nil, "sh")
	req = binary.LittleEndian.AppendUint32(req, 2)
	req = appendString(req, "-c")
	req = appendString(req, "printf host-ok")
	out := make([]byte, 4096)
	n := hostExec(ptr(req), int32(len(req)), ptr(out), int32(len(out)))
	runtime.KeepAlive(req)
	runtime.KeepAlive(out)
	if n < 12 || int(n) > len(out) {
		fmt.Printf("exec-error=%d\n", n)
		return
	}
	b := out[:n]
	exitCode := int32(binary.LittleEndian.Uint32(b[:4]))
	stdoutLen := int(binary.LittleEndian.Uint32(b[4:8]))
	if stdoutLen > len(b)-12 {
		fmt.Println("decode-error")
		return
	}
	stdout := string(b[8 : 8+stdoutLen])
	fmt.Printf("exit=%d stdout=%s\n", exitCode, stdout)
}
