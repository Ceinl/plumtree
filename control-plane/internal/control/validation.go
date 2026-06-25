package control

import (
	"errors"
	"fmt"
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
)

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

func validateVisibility(v Visibility) (Visibility, error) {
	if v == "" {
		return VisibilityPrivate, nil
	}
	switch v {
	case VisibilityPrivate, VisibilityPublic:
		return v, nil
	default:
		return "", fmt.Errorf("%w: unknown visibility %q", ErrInvalid, v)
	}
}

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
