package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"golang.org/x/crypto/ssh"
)

func TestSSHKeysRequireShooAuthentication(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{}, "http://localhost:8080")
	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/me/ssh-keys"},
		{http.MethodPost, "/api/me/ssh-keys"},
		{http.MethodDelete, "/api/me/ssh-keys/key_000001"},
	} {
		rec := serveTestRequest(t, server, request.method, request.path, nil, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want 401", request.method, request.path, rec.Code)
		}
	}
}

func TestSSHKeyRegistrationListDuplicateAndRevocation(t *testing.T) {
	store := control.NewStore()
	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_alice"}}, "http://localhost:8080")
	authorizedKey, fingerprint := testAuthorizedKey(t)
	requestBody := `{"name":"laptop","publicKey":` + mustJSON(t, authorizedKey+" alice@host") + `}`

	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/ssh-keys", strings.NewReader(requestBody), "test-token")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var registered struct {
		SSHKey struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Fingerprint string `json:"fingerprint"`
		} `json:"sshKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.SSHKey.ID == "" || registered.SSHKey.Name != "laptop" || registered.SSHKey.Fingerprint != fingerprint {
		t.Fatalf("registered key = %+v", registered.SSHKey)
	}
	if strings.Contains(rec.Body.String(), authorizedKey) || strings.Contains(rec.Body.String(), "publicKey") {
		t.Fatalf("registration response exposed public key contents: %s", rec.Body.String())
	}

	rec = serveTestRequest(t, server, http.MethodGet, "/api/me/ssh-keys", nil, "test-token")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fingerprint) {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), authorizedKey) || strings.Contains(rec.Body.String(), "publicKey") {
		t.Fatalf("list response exposed public key contents: %s", rec.Body.String())
	}

	rec = serveTestRequest(t, server, http.MethodPost, "/api/me/ssh-keys", strings.NewReader(requestBody), "test-token")
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if _, owner, err := store.ResolveSSHKey(fingerprint); err != nil || owner.ID == "" {
		t.Fatalf("registered fingerprint did not resolve to owner: owner=%+v err=%v", owner, err)
	}
	rec = serveTestRequest(t, server, http.MethodDelete, "/api/me/ssh-keys/"+registered.SSHKey.ID, nil, "test-token")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, _, err := store.ResolveSSHKey(fingerprint); err == nil {
		t.Fatal("revoked fingerprint still resolves")
	}
}

func TestSSHKeyRevocationIsOwnerScoped(t *testing.T) {
	store := control.NewStore()
	alice, _, err := store.EnsureOwnerForIdentity(control.IdentityInput{Provider: control.ProviderShoo, Subject: "ps_alice"})
	if err != nil {
		t.Fatal(err)
	}
	authorizedKey, fingerprint := testAuthorizedKey(t)
	key, err := store.RegisterSSHKey(control.SSHKeyInput{
		OwnerID: alice.ID, Name: "laptop", PublicKey: authorizedKey, Fingerprint: fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	bobServer := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_bob"}}, "http://localhost:8080")
	rec := serveTestRequest(t, bobServer, http.MethodDelete, "/api/me/ssh-keys/"+key.ID, nil, "test-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner revoke status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, _, err := store.ResolveSSHKey(fingerprint); err != nil {
		t.Fatalf("cross-owner revoke removed key: %v", err)
	}
}

func TestSSHKeyRegistrationRejectsInvalidPublicKey(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_alice"}}, "http://localhost:8080")
	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/ssh-keys", strings.NewReader(
		`{"name":"laptop","publicKey":"not a public key"}`,
	), "test-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSSHKeyRegistrationRejectsTrailingJSON(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_alice"}}, "http://localhost:8080")
	authorizedKey, _ := testAuthorizedKey(t)
	body := `{"name":"laptop","publicKey":` + mustJSON(t, authorizedKey) + `}{"name":"second"}`
	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/ssh-keys", strings.NewReader(body), "test-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func testAuthorizedKey(t *testing.T) (string, string) {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sshKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshKey))), ssh.FingerprintSHA256(sshKey)
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
