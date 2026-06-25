// Package control owns the Phase 4 control-plane model: owners, apps, deploys,
// auth metadata, and deploy pointers. It deliberately avoids transport and
// database choices so HTTP handlers and persistence can be layered on later.
package control

import "time"

// Visibility controls who may run an app. Deploy remains owner-gated either way.
type Visibility string

const (
	VisibilityPrivate Visibility = "private"
	VisibilityPublic  Visibility = "public"
)

// Owner is an authenticated Plumtree namespace owner.
type Owner struct {
	ID            string
	Handle        string
	HandleClaimed bool
	// Suspended is an operator kill switch: when true, none of the owner's apps
	// resolve to a runnable session.
	Suspended bool
	CreatedAt time.Time
}

type AuthProvider string

const (
	ProviderShoo AuthProvider = "shoo"
)

// AuthIdentity binds an external verified identity to a Plumtree owner.
type AuthIdentity struct {
	Provider  AuthProvider
	Subject   string
	OwnerID   string
	CreatedAt time.Time
}

// App is the durable namespace record for <owner>/<name>.
type App struct {
	ID             string
	OwnerID        string
	Name           string
	Visibility     Visibility
	ActiveDeployID string
	// Suspended is an operator kill switch: when true, the app does not resolve
	// to a runnable session.
	Suspended bool
	CreatedAt time.Time
}

// Artifact describes a content-addressed WASM artifact produced by build
// workers. The control plane stores metadata only, never raw source or WASM.
type Artifact struct {
	ID            string
	Digest        string
	SizeBytes     int64
	ABIVersion    uint8
	BuildMetadata map[string]string
	CreatedAt     time.Time
}

// Deploy is an immutable release record for an app.
type Deploy struct {
	ID               string
	AppID            string
	AppName          string
	AppType          string
	Visibility       Visibility
	ArtifactID       string
	SourceDigest     string
	CreatedByOwnerID string
	ClaimTokenHash   string
	CreatedAt        time.Time
	ClaimExpiresAt   *time.Time
	ClaimedAt        *time.Time
}

// SSHKey is login metadata for owner authentication.
type SSHKey struct {
	ID          string
	OwnerID     string
	Name        string
	PublicKey   string
	Fingerprint string
	CreatedAt   time.Time
}

// TokenScope limits CI token use. Tokens are stored by hash only.
type TokenScope string

const (
	ScopeDeploy  TokenScope = "deploy"
	ScopeInspect TokenScope = "inspect"
	ScopeLogs    TokenScope = "logs"
	ScopeSecrets TokenScope = "secrets"
)

// CIToken is metadata for an owner-scoped automation token.
type CIToken struct {
	ID        string
	OwnerID   string
	Name      string
	TokenHash string
	Scopes    []TokenScope
	CreatedAt time.Time
	RevokedAt *time.Time
}

// SecretMetadata records the existence/version of a server-side app secret.
// Secret values are intentionally outside this package.
type SecretMetadata struct {
	AppID     string
	Key       string
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Session records a runner session selected by the gateway/control plane.
type Session struct {
	ID        string
	AppID     string
	DeployID  string
	StartedAt time.Time
	EndedAt   *time.Time
	// Log is the guest's captured stdout/stderr for the session, size-capped by
	// the runner. LogTruncated reports that output exceeded the cap and the tail
	// was dropped.
	Log          string
	LogTruncated bool
}

// Quotas are owner-level abuse and cost limits. Zero values mean "unset" in
// this in-memory implementation.
type Quotas struct {
	MaxApps          int
	MaxDeploysPerApp int
	MaxSecretsPerApp int
	MaxSessions      int
}

type AppInput struct {
	OwnerID    string
	Name       string
	Visibility Visibility
}

type ArtifactInput struct {
	Digest        string
	SizeBytes     int64
	ABIVersion    uint8
	BuildMetadata map[string]string
}

type DeployInput struct {
	AppID            string
	ArtifactID       string
	SourceDigest     string
	CreatedByOwnerID string
}

type DeployClaimInput struct {
	AppName        string
	AppType        string
	Visibility     Visibility
	ArtifactID     string
	SourceDigest   string
	ClaimTokenHash string
}

type SSHKeyInput struct {
	OwnerID     string
	Name        string
	PublicKey   string
	Fingerprint string
}

type CITokenInput struct {
	OwnerID   string
	Name      string
	TokenHash string
	Scopes    []TokenScope
}

type SecretInput struct {
	AppID string
	Key   string
	Value []byte
}

type IdentityInput struct {
	Provider AuthProvider
	Subject  string
}
