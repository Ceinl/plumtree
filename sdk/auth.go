package sdk

import "errors"

// Identity is who is connected to the current session, returned by Whoami. User
// is a stable opaque id — a proved SSH public-key fingerprint when the client
// authenticated with a key, otherwise an explicit "anonymous:" ephemeral id — suitable for distinguishing
// and labelling users (e.g. a real handle in a chat instead of "user1").
// Authenticated reports whether the platform verified the identity against a
// claimed owner key.
type Identity struct {
	User          string
	Authenticated bool
	Kind          IdentityKind
	OwnsApp       bool
}

type IdentityKind string

const (
	IdentitySSHKey    IdentityKind = "ssh-key"
	IdentityAnonymous IdentityKind = "anonymous"
)

// ErrAuthUnavailable means the running context provides no auth capability.
var ErrAuthUnavailable = errors.New("sdk: auth capability unavailable")

// Whoami returns the identity of the user connected to this session. In
// `go run .` it returns a fixed local identity so app code behaves the same.
func Whoami() (Identity, error) { return whoami() }
