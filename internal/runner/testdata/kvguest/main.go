// kvguest is a minimal WASI guest that exercises the Plumtree KV host imports
// directly (no SDK), so the runner can test the host functions and the kv_get
// size protocol in isolation. It prints one line per operation for the host to
// assert on.
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"runtime"
	"strings"
	"unsafe"
)

//go:wasmimport plumtree kv_set
func kvSet(keyPtr, keyLen, valPtr, valLen int32) int32

//go:wasmimport plumtree kv_get
func kvGet(keyPtr, keyLen, outPtr, outCap int32) int32

//go:wasmimport plumtree kv_delete
func kvDelete(keyPtr, keyLen int32) int32

//go:wasmimport plumtree kv_list
func kvList(prefixPtr, prefixLen, limit, outPtr, outCap int32) int32

//go:wasmimport plumtree kv_compare_and_swap
func kvCAS(keyPtr, keyLen, expectedPtr, valPtr, valLen int32) int32

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

func cas(key string, expected [sha256.Size]byte, value string) int32 {
	k, v := []byte(key), []byte(value)
	r := kvCAS(bptr(k), int32(len(k)), bptr(expected[:]), bptr(v), int32(len(v)))
	runtime.KeepAlive(k)
	runtime.KeepAlive(expected)
	runtime.KeepAlive(v)
	return r
}

func list(prefix string, limit int) (int32, string) {
	p := []byte(prefix)
	buf := make([]byte, 2048)
	var pptr int32
	if len(p) > 0 {
		pptr = bptr(p)
	}
	n := kvList(pptr, int32(len(p)), int32(limit), bptr(buf), int32(len(buf)))
	runtime.KeepAlive(p)
	runtime.KeepAlive(buf)
	if n < 0 || int(n) > len(buf) {
		return n, ""
	}
	var keys []string
	raw := buf[:n]
	for len(raw) >= 2 {
		l := int(binary.LittleEndian.Uint16(raw[:2]))
		raw = raw[2:]
		if l > len(raw) {
			return -99, ""
		}
		keys = append(keys, string(raw[:l]))
		raw = raw[l:]
	}
	return n, strings.Join(keys, ",")
}

func main() {
	fmt.Printf("set=%d\n", set("greeting", "hello world")) // 11-byte value
	var absent [sha256.Size]byte
	fmt.Printf("cas-create=%d\n", cas("created", absent, "one"))
	fmt.Printf("cas-stale=%d\n", cas("created", absent, "bad"))
	fmt.Printf("cas-replace=%d\n", cas("created", sha256.Sum256([]byte("one")), "two"))
	_, keys := list("", 10)
	fmt.Printf("list=%s\n", keys)

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
