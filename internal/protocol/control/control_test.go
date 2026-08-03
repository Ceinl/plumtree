package control

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestValidateRequestRejectsSpoofedIdentityAndNonAPIPaths(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "http://plumtree/api/v1/status", nil)
	r.Header.Set("x-plumtree-device-id", "spoof")
	if !errors.Is(ValidateRequest(r), ErrIdentityHeader) {
		t.Fatal("spoofed identity header was accepted")
	}
	r.Header.Del("x-plumtree-device-id")
	r.URL.Path = "/admin"
	if !errors.Is(ValidateRequest(r), ErrInvalid) {
		t.Fatal("non-control path was accepted")
	}
}

func TestValidateRequestBoundsBody(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "http://plumtree/api/v1/state", strings.NewReader("x"))
	r.ContentLength = MaxBodyBytes + 1
	if !errors.Is(ValidateRequest(r), ErrBodyTooLarge) {
		t.Fatal("oversized body was accepted")
	}
}
