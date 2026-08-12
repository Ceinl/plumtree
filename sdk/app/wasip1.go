//go:build wasip1

package app

func runPlatform(runtime *Runtime) error {
	runtime.Stop()
	return ErrPlatformUnsupported
}
