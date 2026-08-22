//go:build unix

package workflow

import (
	"os"
	"syscall"
)

func resizeSignal() os.Signal { return syscall.SIGWINCH }
