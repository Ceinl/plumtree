// kvguest is a minimal WASI guest that exercises the Plumtree KV host imports
// directly (no SDK), so the runner can test the host functions and the kv_get
// size protocol in isolation. It prints one line per operation for the host to
// assert on.
package main

import (
	"fmt"
	"runtime"
	"unsafe"
)

//go:wasmimport plumtree kv_set
func kvSet(keyPtr, keyLen, valPtr, valLen int32) int32

//go:wasmimport plumtree kv_get
func kvGet(keyPtr, keyLen, outPtr, outCap int32) int32

//go:wasmimport plumtree kv_delete
func kvDelete(keyPtr, keyLen int32) int32

func bptr(b []byte) int32 { return int32(uintptr(unsafe.Pointer(&b[0]))) }

func set(key, val string) int32 {
	k, v := []byte(key), []byte(val)
	r := kvSet(bptr(k), int32(len(k)), bptr(v), int32(len(v)))
	runtime.KeepAlive(k)
	runtime.KeepAlive(v)
	return r
}

// get returns the host's raw return value and, when the value fit in capBytes,
// the bytes read.
func get(key string, capBytes int) (int32, string) {
	k := []byte(key)
	buf := make([]byte, capBytes)
	var outPtr, outCap int32
	if capBytes > 0 {
		outPtr, outCap = bptr(buf), int32(capBytes)
	}
	n := kvGet(bptr(k), int32(len(k)), outPtr, outCap)
	runtime.KeepAlive(k)
	runtime.KeepAlive(buf)
	if n >= 0 && int(n) <= capBytes {
		return n, string(buf[:n])
	}
	return n, ""
}

func del(key string) int32 {
	k := []byte(key)
	r := kvDelete(bptr(k), int32(len(k)))
	runtime.KeepAlive(k)
	return r
}

func main() {
	fmt.Printf("set=%d\n", set("greeting", "hello world")) // 11-byte value

	n, s := get("greeting", 64)
	fmt.Printf("get=%d:%s\n", n, s)

	// Buffer too small: the host returns the needed length and writes nothing.
	grow, _ := get("greeting", 4)
	fmt.Printf("grow=%d\n", grow)

	miss, _ := get("nope", 64)
	fmt.Printf("miss=%d\n", miss)

	fmt.Printf("del=%d\n", del("greeting"))

	after, _ := get("greeting", 64)
	fmt.Printf("after=%d\n", after)
}
