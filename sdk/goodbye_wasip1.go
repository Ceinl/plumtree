//go:build wasip1

package sdk

import (
	"runtime"
	"unsafe"
)

//go:wasmimport plumtree goodbye_set
func hostGoodbyeSet(ptr, length int32)

func goodbyeSet(msg string) {
	if len(msg) == 0 || len(msg) > MaxGoodbyeLen {
		return
	}
	b := []byte(msg)
	hostGoodbyeSet(int32(uintptr(unsafe.Pointer(&b[0]))), int32(len(b)))
	runtime.KeepAlive(b)
}
