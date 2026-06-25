//go:build !wasip1

package sdk

// Native whoami returns a fixed local identity so `go run .` and tests behave
// like a hosted session. There is no SSH layer locally, so the user is a stable
// placeholder rather than a key fingerprint.
func whoami() (Identity, error) {
	return Identity{User: "local", Authenticated: false}, nil
}
