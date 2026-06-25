package abi

// Env host function exposes claimed-app secrets to the guest, read-only. It uses
// the same ptr/len + grow convention as kv_get: the guest passes a key and an
// output buffer; the host writes the value and returns its length (or the needed
// length when the buffer is too small).

const (
	// EnvMaxKey caps an env/secret key length in bytes.
	EnvMaxKey = 256
	// EnvMaxValue caps an env/secret value length in bytes.
	EnvMaxValue = 64 * 1024
)

// Env result codes. A non-negative env_get return is the value length.
const (
	EnvOk          int32 = 0
	EnvErrNotFound int32 = -1 // no secret set for the key
	EnvErrTooLarge int32 = -2 // key exceeds its cap
	EnvErrInternal int32 = -3 // host-side failure or absent capability
)
