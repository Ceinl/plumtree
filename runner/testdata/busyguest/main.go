//go:build wasip1

// busyguest is a deliberately misbehaving guest used to test that the dev host
// terminates a runaway frame: after receiving its first event it spins forever
// in a compute loop and never presents, so the per-frame watchdog must kill it.
package main

import "unsafe"

//go:wasmimport plumtree recv
func recv(ptr, capBytes int32) int32

//go:wasmimport plumtree present
func present(ptr, length int32)

var buf [64]byte

func main() {
	for {
		n := recv(int32(uintptr(unsafe.Pointer(&buf[0]))), int32(len(buf)))
		if n < 0 {
			return
		}
		x := 0
		for i := 0; ; i++ { // never returns; never calls present
			x += i
			_ = x
		}
	}
}
