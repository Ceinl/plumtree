package control

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

var (
	ErrInvalid   = errors.New("control: invalid input")
	ErrNotFound  = errors.New("control: not found")
	ErrConflict  = errors.New("control: conflict")
	ErrSuspended = errors.New("control: suspended")
	ErrQuota     = errors.New("control: quota exceeded")
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	secretPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
	// egressHostPattern matches a DNS hostname (no scheme/port/path). The optional
	// leading dot for the subdomain-wildcard form is stripped before matching.
	egressHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
)

// nonPublicTLDs are domain suffixes that name internal networks rather than the
// public internet. Allowing them as egress targets would let an app reach
// services behind the platform, so they are rejected at allowlist-add time.
var nonPublicTLDs = map[string]bool{
	"local": true, "localhost": true, "internal": true,
	"lan": true, "home": true, "intranet": true, "corp": true,
}

// ValidateEgressHost normalizes and validates an egress allowlist entry. It
// accepts a public DNS hostname (optionally with a leading dot for the
// subdomain-wildcard form) and rejects IP literals, single-label/internal names,
// and anything carrying a scheme, port, or path. This is the add-time half of
// the SSRF defense; the runner additionally blocks non-public resolved IPs at
// dial time to stop DNS rebinding.
func ValidateEgressHost(host string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return "", fmt.Errorf("%w: egress host is required", ErrInvalid)
	}
	bare := strings.TrimPrefix(h, ".")
	if strings.ContainsAny(bare, "/:@ \t") {
		return "", fmt.Errorf("%w: egress host %q must be a bare hostname (no scheme, port, or path)", ErrInvalid, host)
	}
	if net.ParseIP(bare) != nil {
		return "", fmt.Errorf("%w: egress host %q must be a domain name, not an IP address", ErrInvalid, host)
	}
	if !egressHostPattern.MatchString(bare) {
		return "", fmt.Errorf("%w: egress host %q is not a valid public domain name", ErrInvalid, host)
	}
	labels := strings.Split(bare, ".")
	if nonPublicTLDs[labels[len(labels)-1]] {
		return "", fmt.Errorf("%w: egress host %q uses a non-public domain suffix", ErrInvalid, host)
	}
	return h, nil
}

// ValidateName accepts the namespace-safe handles used for owners and apps.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%w: name must match [a-z0-9]([a-z0-9-]*[a-z0-9]) and be <=63 chars", ErrInvalid)
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("%w: name cannot contain consecutive hyphens", ErrInvalid)
	}
	return nil
}

func validateDigest(label, digest string) error {
	if !digestPattern.MatchString(digest) {
		return fmt.Errorf("%w: %s must be sha256:<64 lowercase hex chars>", ErrInvalid, label)
	}
	return nil
}

// ValidateDigest validates a SHA-256 digest supplied at an API boundary.
func ValidateDigest(label, digest string) error { return validateDigest(label, digest) }

func validateSecretKey(key string) error {
	if !secretPattern.MatchString(key) {
		return fmt.Errorf("%w: secret key must match [A-Z][A-Z0-9_]*", ErrInvalid)
	}
	return nil
}

func validateNonEmpty(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalid, label)
	}
	return nil
}

// ParseScope validates and normalizes a raw CI token scope string.
func ParseScope(raw string) (TokenScope, error) {
	scope := TokenScope(strings.TrimSpace(raw))
	if !validScope(scope) {
		return "", fmt.Errorf("%w: unknown token scope %q", ErrInvalid, raw)
	}
	return scope, nil
}

func validScope(scope TokenScope) bool {
	switch scope {
	case ScopeDeploy, ScopeInspect, ScopeLogs, ScopeSecrets:
		return true
	default:
		return false
	}
}

func validProvider(provider AuthProvider) bool {
	switch provider {
	case ProviderShoo:
		return true
	default:
		return false
	}
}
