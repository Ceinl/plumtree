//go:build wasip1

// startspin is a deliberately misbehaving guest used to test that startup
// compute is bounded: it spins forever in main before making any host call, so
// neither the per-frame nor the session re-arm path ever runs on its behalf.
package main

func main() {
	x := 0
	for i := 0; ; i++ { // never returns; never calls recv or present
		x += i
		_ = x
	}
}
