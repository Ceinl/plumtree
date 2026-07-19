//go:build unix

package main

import (
	"os"
	"syscall"
)

func resizeSignal() os.Signal { return syscall.SIGWINCH }
