package sdk

import "errors"

// Identity is who is connected to the current session, returned by Whoami. User
// is a stable opaque id — an SSH public-key fingerprint when the client offered
// a key, otherwise an ephemeral per-session id — suitable for distinguishing
// and labelling users (e.g. a real handle in a chat instead of "user1").
// Authenticated reports whether the platform verified the identity against a
// claimed owner key.
type Identity struct {
	User          string
	Authenticated bool
}

// ErrAuthUnavailable means the running context provides no auth capability.
var ErrAuthUnavailable = errors.New("sdk: auth capability unavailable")

// Whoami returns the identity of the user connected to this session. In
// `go run .` it returns a fixed local identity so app code behaves the same.
func Whoami() (Identity, error) { return whoami() }
