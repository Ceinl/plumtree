//go:build wasip1

// presentloop is a deliberately misbehaving guest used to test that the dev
// host terminates a guest that presents forever without ever calling recv: no
// frame watchdog ever arms, so only the total session budget can stop it.
package main

import "unsafe"

//go:wasmimport plumtree present
func present(ptr, length int32)

var buf [64]byte

func main() {
	for {
		present(int32(uintptr(unsafe.Pointer(&buf[0]))), int32(len(buf)))
	}
}
