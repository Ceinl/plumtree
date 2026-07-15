//go:build !unix

package main

import "os"

// Windows has no SIGWINCH. This keeps the local TTY path buildable; terminal
// resize notifications are only supported on Unix terminals.
func resizeSignal() os.Signal { return os.Interrupt }
