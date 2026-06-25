package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func TestDevEgressLifecycle(t *testing.T) {
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
		t.Fatalf("claim status = %d", rec.Code)
	}
	base := "/api/dev/deploy/" + url.PathEscape(created.Deploy.ID) + "/egress"

	// Add a host.
	body, _ := json.Marshal(map[string]string{"host": "api.example.com"})
	rec := serveDevRequest(t, server, http.MethodPost, base, bytes.NewReader(body), claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", rec.Code, rec.Body.String())
	}

	app, _, _, err := store.ResolveActiveDeploy("alice", "counter")
	if err != nil {
		t.Fatal(err)
	}
	if got := store.EgressAllowlist(app.ID); len(got) != 1 || got[0] != "api.example.com" {
		t.Fatalf("allowlist = %v", got)
	}

	// Remove it.
	rec = serveDevRequest(t, server, http.MethodDelete, base+"/api.example.com", nil, claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if got := store.EgressAllowlist(app.ID); len(got) != 0 {
		t.Fatalf("allowlist after delete = %v", got)
	}
}
