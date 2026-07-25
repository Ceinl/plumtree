package main

import (
	"strings"
	"testing"
)

func TestIdentityKeyDoesNotExposeFingerprint(t *testing.T) {
	fingerprint := "SHA256:private-public-key-fingerprint"
	got := identityKey(fingerprint)
	if len(got) != 32 || strings.Contains(got, fingerprint) {
		t.Fatalf("identityKey(%q) = %q", fingerprint, got)
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"Ada", "tree person", "żółw"} {
		if !validName(name) {
			t.Errorf("validName(%q) = false", name)
		}
	}
	for _, name := range []string{"", " ", "line\nbreak", strings.Repeat("x", maxNameRunes+1)} {
		if validName(name) {
			t.Errorf("validName(%q) = true", name)
		}
	}
}
