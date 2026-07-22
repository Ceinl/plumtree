package sdk

import "errors"

// Exec runs a program on the server host. Hosted apps receive this capability
// only when the operator explicitly enables allowHostCommands, and only after
// the app is claimed. It is intended for trusted apps on private/self-hosted
// servers (for example, invoking a shell or an installed AI-agent CLI).
//
// The command is executed directly, without a shell. Use Exec("sh", "-lc",
// script) when shell syntax is intentionally required.
func Exec(name string, args ...string) (ExecResult, error) { return execCommand(name, args) }

type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

var (
	ErrExecUnavailable = errors.New("sdk: host command capability unavailable")
	ErrExecTooLarge    = errors.New("sdk: host command request or output too large")
	ErrExecFailed      = errors.New("sdk: host command failed to start or was cancelled")
)
