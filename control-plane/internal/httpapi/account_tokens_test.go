package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func TestAppsRequiresBearer(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{}, "http://localhost:8080")
	rec := serveTestRequest(t, server, http.MethodGet, "/api/apps", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMeDoesNotExposeShooPII(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{claims: shoo.Claims{
		PairwiseSub: "ps_test",
		Email:       "person@example.com",
		Name:        "Person Example",
		Picture:     "https://example.com/avatar.png",
	}}, "http://localhost:8080")
	rec := serveTestRequest(t, server, http.MethodGet, "/api/me", nil, "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"person@example.com", "Person Example", "avatar.png", `"email"`, `"picture"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("PII leaked in /api/me response: %s", body)
		}
	}
}

func TestAppsRequireClaimedHandle(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")
	rec := serveTestRequest(t, server, http.MethodGet, "/api/apps", nil, "test-token")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestClaimHandleForAuthenticatedOwner(t *testing.T) {
	store := control.NewStore()
	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")

	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/handle", strings.NewReader(`{"handle":"alice"}`), "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Owner struct {
			Handle      string `json:"handle"`
			NeedsHandle bool   `json:"needsHandle"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Owner.Handle != "alice" || body.Owner.NeedsHandle {
		t.Fatalf("owner = %+v", body.Owner)
	}

	rec = serveTestRequest(t, server, http.MethodGet, "/api/me", nil, "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"handle":"alice"`) || strings.Contains(rec.Body.String(), `"needsHandle":true`) {
		t.Fatalf("me body = %s", rec.Body.String())
	}
}

func TestClaimHandleRejectsConflict(t *testing.T) {
	store := control.NewStore()
	if _, err := store.CreateOwner("alice"); err != nil {
		t.Fatal(err)
	}
	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")

	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/handle", strings.NewReader(`{"handle":"alice"}`), "test-token")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAppsListsAuthenticatedOwnersApps(t *testing.T) {
	store := control.NewStore()
	owner, _, err := store.EnsureOwnerForIdentity(control.IdentityInput{
		Provider: control.ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err = store.ClaimOwnerHandle(owner.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(control.AppInput{OwnerID: owner.ID, Name: "counter", Visibility: control.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")

	rec := serveTestRequest(t, server, http.MethodGet, "/api/apps", nil, "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Apps []struct {
			Handle     string `json:"handle"`
			Visibility string `json:"visibility"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Apps) != 1 || body.Apps[0].Handle != owner.Handle+"/counter" || body.Apps[0].Visibility != "public" {
		t.Fatalf("apps = %+v", body.Apps)
	}
}

func TestCITokenAPILifecycle(t *testing.T) {
	store := control.NewStore()
	owner, _, err := store.EnsureOwnerForIdentity(control.IdentityInput{
		Provider: control.ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOwnerHandle(owner.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")

	// Create returns the plaintext token exactly once.
	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/tokens", strings.NewReader(`{"name":"ci","scopes":["deploy","inspect"]}`), "test-token")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string   `json:"id"`
		Token  string   `json:"token"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || !strings.HasPrefix(created.Token, "ptci_") {
		t.Fatalf("created token = %+v", created)
	}
	if len(created.Scopes) != 2 {
		t.Fatalf("scopes = %v", created.Scopes)
	}

	// The plaintext authenticates against the store; the hash is never returned.
	if _, _, err := store.AuthenticateCIToken(hashClaimToken(created.Token)); err != nil {
		t.Fatalf("AuthenticateCIToken = %v", err)
	}

	// List exposes metadata only, never the plaintext or hash.
	rec = serveTestRequest(t, server, http.MethodGet, "/api/me/tokens", nil, "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "ptci_") || strings.Contains(body, "tokenHash") || strings.Contains(body, "sha256:") {
		t.Fatalf("list leaked a secret: %s", body)
	}

	// Revoke marks the token revoked and stops it authenticating.
	rec = serveTestRequest(t, server, http.MethodDelete, "/api/me/tokens/"+created.ID, nil, "test-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"revoked":true`) {
		t.Fatalf("revoke body = %s", rec.Body.String())
	}
	if _, _, err := store.AuthenticateCIToken(hashClaimToken(created.Token)); err == nil {
		t.Fatal("revoked token still authenticates")
	}
}

func TestCITokenAPIRejectsBadScope(t *testing.T) {
	store := control.NewStore()
	owner, _, err := store.EnsureOwnerForIdentity(control.IdentityInput{
		Provider: control.ProviderShoo,
		Subject:  "ps_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimOwnerHandle(owner.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	server := New(store, fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")

	rec := serveTestRequest(t, server, http.MethodPost, "/api/me/tokens", strings.NewReader(`{"name":"ci","scopes":["root"]}`), "test-token")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCITokenAPIRequiresHandle(t *testing.T) {
	server := New(control.NewStore(), fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}}, "http://localhost:8080")
	rec := serveTestRequest(t, server, http.MethodGet, "/api/me/tokens", nil, "test-token")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
