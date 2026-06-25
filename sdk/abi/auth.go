package abi

import "encoding/binary"

// Auth host function exposes the connected user's identity to the guest. Like
// kv_get it uses the ptr/len + grow convention: the guest passes an output
// buffer and the host writes an encoded Identity, returning the byte length (or
// the needed length when the buffer is too small).

const (
	// AuthMaxUser caps the encoded user identifier length in bytes.
	AuthMaxUser = 256
	// AuthErrInternal is returned when no auth capability is present or the host
	// failed to write the identity.
	AuthErrInternal int32 = -1
)

// Identity describes who is connected for the current session. User is a stable
// opaque identifier (an SSH public-key fingerprint) when the client offered a
// key, or an ephemeral per-session id otherwise. Authenticated reports whether
// the platform has verified the identity against a claimed owner key.
type Identity struct {
	User          string
	Authenticated bool
}

// EncodeIdentity serializes an Identity. Layout (LE):
//
//	[0] flags (bit0 = authenticated)  [1:3] userLen uint16  [3:] user bytes
func EncodeIdentity(id Identity) []byte {
	u := id.User
	if len(u) > AuthMaxUser {
		u = u[:AuthMaxUser]
	}
	b := make([]byte, 3+len(u))
	if id.Authenticated {
		b[0] = 1
	}
	binary.LittleEndian.PutUint16(b[1:3], uint16(len(u)))
	copy(b[3:], u)
	return b
}

// DecodeIdentity parses bytes produced by EncodeIdentity.
func DecodeIdentity(b []byte) (Identity, error) {
	if len(b) < 3 {
		return Identity{}, ErrShort
	}
	ulen := int(binary.LittleEndian.Uint16(b[1:3]))
	if len(b) < 3+ulen {
		return Identity{}, ErrShort
	}
	return Identity{
		Authenticated: b[0]&1 != 0,
		User:          string(b[3 : 3+ulen]),
	}, nil
}
