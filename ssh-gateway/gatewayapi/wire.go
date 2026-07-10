// Package gatewayapi is the wire contract between a standalone SSH gateway and
// the control plane: the request/response DTOs, route prefix, auth header, and
// error codes shared by the control-plane HTTP handlers and the gateway's HTTP
// backend client.
package gatewayapi

// BasePath prefixes every gateway-facing control-plane endpoint. These routes
// are operator-internal (gateway <-> control plane) and guarded by a shared
// token, not by user auth.
const BasePath = "/internal/gateway"

// TokenHeader carries the shared gateway token on every request.
const TokenHeader = "X-Plumtree-Gateway-Token"

// Error codes returned in ErrorResponse.Code so the client can reconstruct the
// gateway's sentinel errors from an HTTP response.
const (
	CodeSuspended = "suspended"
	CodeQuota     = "quota"
)

// ResolveRequest asks the control plane to resolve an SSH app handle.
type ResolveRequest struct {
	Handle string `json:"handle"`
}

// IdentityRequest asks the control plane whether a public key whose possession
// was proved during the SSH handshake belongs to a registered owner.
type IdentityRequest struct {
	Fingerprint string `json:"fingerprint"`
}

type IdentityResponse struct {
	User          string `json:"user"`
	Authenticated bool   `json:"authenticated"`
}

// ResolveResponse is a resolved runnable app. WASM is JSON-encoded as base64.
type ResolveResponse struct {
	AppID    string `json:"appID"`
	AppName  string `json:"appName"`
	OwnerID  string `json:"ownerID"`
	DeployID string `json:"deployID"`
	AppType  string `json:"appType"`
	WASM     []byte `json:"wasm"`
}

// StartSessionRequest opens a session for an app's active deploy.
type StartSessionRequest struct {
	AppID    string `json:"appID"`
	DeployID string `json:"deployID"`
}

// StartSessionResponse returns the new session's id.
type StartSessionResponse struct {
	SessionID string `json:"sessionID"`
}

type RegisterSuspensionsRequest struct {
	GatewayID string `json:"gatewayID"`
}

type NextSuspensionRequest struct {
	GatewayID string `json:"gatewayID"`
}

type SuspensionResponse struct {
	DeliveryID string `json:"deliveryID"`
	Scope      string `json:"scope"`
	ID         string `json:"id"`
}

type AckSuspensionRequest struct {
	GatewayID  string `json:"gatewayID"`
	DeliveryID string `json:"deliveryID"`
}

// RecordLogRequest stores a finished session's captured output.
type RecordLogRequest struct {
	Log       string `json:"log"`
	Truncated bool   `json:"truncated"`
}

// SecretsResponse carries a claimed app's injected env/secret values.
type SecretsResponse struct {
	Secrets map[string]string `json:"secrets"`
}

// EgressResponse carries a claimed app's fetch allowlist.
type EgressResponse struct {
	Allow []string `json:"allow"`
}

// ErrorResponse is the JSON body for any non-2xx gateway-API response.
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}
