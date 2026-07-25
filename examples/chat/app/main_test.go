package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk"
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

func TestNewChatLoadsHistoryWhenIdentityIsUnavailable(t *testing.T) {
	key := fmt.Sprintf("%s%020d", messagePrefix, 1)
	raw, err := json.Marshal(message{ID: 1, From: "Ada", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sdk.KVSet(key, raw); err != nil {
		t.Fatal(err)
	}
	defer sdk.KVDelete(key)

	originalWhoami := whoami
	whoami = func() (sdk.Identity, error) {
		return sdk.Identity{}, errors.New("unavailable")
	}
	t.Cleanup(func() { whoami = originalWhoami })

	c := newChat()
	if len(c.messages) != 1 || c.messages[0].Text != "hello" {
		t.Fatalf("history = %+v, want the existing message", c.messages)
	}
	if c.userKey != "" {
		t.Fatalf("userKey = %q, want empty without an identity", c.userKey)
	}
	if !c.rememberName("Grace") {
		t.Fatal("valid session-only name was rejected")
	}
	profiles, err := sdk.KVList(profilePrefix, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("profile was persisted without an identity under %v", profiles)
	}
}

func TestAnonymousNameIsSessionOnly(t *testing.T) {
	t.Setenv("PLUMTREE_IDENTITY_USER", "anonymous:test")
	t.Setenv("PLUMTREE_IDENTITY_KIND", string(sdk.IdentityAnonymous))
	t.Setenv("PLUMTREE_IDENTITY_AUTHENTICATED", "false")
	t.Setenv("PLUMTREE_IDENTITY_OWNS_APP", "false")

	c := newChat()
	if !c.rememberName("Ada") {
		t.Fatal("valid anonymous name was rejected")
	}
	if c.name != "Ada" || c.naming {
		t.Fatalf("session name was not applied: %+v", c)
	}
	if c.userKey != "" {
		t.Fatalf("anonymous userKey = %q, want empty", c.userKey)
	}
	keys, err := sdk.KVList(profilePrefix, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("anonymous profile was persisted under %v", keys)
	}
}
