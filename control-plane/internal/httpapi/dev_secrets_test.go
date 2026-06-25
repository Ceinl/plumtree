package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func TestDevSecretsLifecycle(t *testing.T) {
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
	server := NewWithConfig(Config{
		Store:    store,
		Verifier: fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}},
		DevToken: "secret",
	})

	created := createDevDeploy(t, server, []byte("local wasm"))
	claimToken := claimTokenFromTestURL(t, created.Deploy.ClaimURL, created.Deploy.ID)
	if rec := claimDeploy(t, server, created.Deploy.ID, claimToken, "shoo-token"); rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", rec.Code, rec.Body.String())
	}
	base := "/api/dev/deploy/" + url.PathEscape(created.Deploy.ID) + "/secrets"

	// Set a secret.
	body, _ := json.Marshal(map[string]string{"key": "API_KEY", "value": "s3cr3t"})
	rec := serveDevRequest(t, server, http.MethodPost, base, bytes.NewReader(body), claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("set status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// The value must be stored server-side for runtime injection...
	app, _, _, err := store.ResolveActiveDeploy("alice", "counter")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.SecretsForApp(app.ID)["API_KEY"]; got != "s3cr3t" {
		t.Fatalf("stored value = %q, want s3cr3t", got)
	}

	// ...but the list endpoint must never return values.
	rec = serveDevRequest(t, server, http.MethodGet, base, nil, claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("list leaked secret value: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "API_KEY") {
		t.Fatalf("list missing key: %s", rec.Body.String())
	}

	// Delete it.
	rec = serveDevRequest(t, server, http.MethodDelete, base+"/API_KEY", nil, claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.SecretsForApp(app.ID)["API_KEY"]; ok {
		t.Fatal("secret survived delete")
	}
}

func TestDevSecretsRequireClaimToken(t *testing.T) {
	store := control.NewStore()
	server := NewWithConfig(Config{Store: store, DevToken: "secret"})
	created := createDevDeploy(t, server, []byte("local wasm"))
	base := "/api/dev/deploy/" + url.PathEscape(created.Deploy.ID) + "/secrets"

	// No claim token: unauthorized.
	rec := serveDevRequest(t, server, http.MethodGet, base, nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
