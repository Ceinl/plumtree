//go:build wasip1

package app

func runPlatform(runtime *Runtime) { <-runtime.Done() }
