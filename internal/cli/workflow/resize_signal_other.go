//go:build !unix

package workflow

import "os"

func resizeSignal() os.Signal { return os.Interrupt }
