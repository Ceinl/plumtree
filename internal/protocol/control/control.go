// Package control defines the versioned HTTP contract carried over an
// authenticated plumtree-control-v1 SSH subsystem.
package control

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	ProtocolName         = "plumtree-control-v1"
	APIPrefix            = "/api/v1"
	VersionHeader        = "Plumtree-Version"
	MaxHeaderBytes       = 64 << 10
	MaxBodyBytes   int64 = 32 << 20
)

var (
	ErrInvalid         = errors.New("control: invalid request")
	ErrVersionMismatch = errors.New("control: product version mismatch")
	ErrIdentityHeader  = errors.New("control: client identity header is forbidden")
	ErrBodyTooLarge    = errors.New("control: request body too large")
)

var identityHeaders = []string{
	"X-Plumtree-Principal",
	"X-Plumtree-Author-ID",
	"X-Plumtree-Device-ID",
}

func ValidateVersion(actual, expected string) error {
	if expected == "" || actual != expected {
		return fmt.Errorf("%w: want %q got %q", ErrVersionMismatch, expected, actual)
	}
	return nil
}

func ValidateRequest(r *http.Request) error {
	if r == nil || r.URL == nil || !strings.HasPrefix(r.URL.Path, APIPrefix) ||
		(r.URL.Path != APIPrefix && !strings.HasPrefix(r.URL.Path, APIPrefix+"/")) {
		return fmt.Errorf("%w: path must start with %s", ErrInvalid, APIPrefix)
	}
	for _, name := range identityHeaders {
		if r.Header.Get(name) != "" {
			return fmt.Errorf("%w: %s", ErrIdentityHeader, name)
		}
	}
	if r.Body != nil && r.ContentLength > MaxBodyBytes {
		return ErrBodyTooLarge
	}
	return nil
}

func ValidateResponse(r *http.Response, expectedVersion string) error {
	if r == nil {
		return fmt.Errorf("%w: nil response", ErrInvalid)
	}
	return ValidateVersion(r.Header.Get(VersionHeader), expectedVersion)
}

func LimitBody(r io.Reader) io.Reader { return io.LimitReader(r, MaxBodyBytes+1) }

func IsIdentityHeader(name string) bool {
	for _, forbidden := range identityHeaders {
		if strings.EqualFold(name, forbidden) {
			return true
		}
	}
	return false
}
