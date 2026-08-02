//go:build wasip1

package app

func runCLIIfRequested(*Runtime) bool { return false }

func runPlatform(runtime *Runtime) { <-runtime.Done() }
