//go:build unix

package cli

import (
	"os"
	"syscall"
)

func resizeSignal() os.Signal { return syscall.SIGWINCH }
