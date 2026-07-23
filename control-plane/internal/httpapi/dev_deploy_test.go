package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func TestDevDeployRequiresToken(t *testing.T) {
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret"})
	req := httptest.NewRequest(http.MethodPost, "/api/dev/deploy", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestDevDeployCreatesClaimRequiredDeploy(t *testing.T) {
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
		Store:     store,
		Verifier:  fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}},
		AppOrigin: "http://plumtree.test",
		DevToken:  "secret",
	})

	created := createDevDeploy(t, server, []byte("local wasm"))
	if created.Deploy.ID == "" || created.Deploy.ClaimURL == "" || created.Deploy.ClaimExpiresAt == "" {
		t.Fatalf("created response = %+v", created)
	}
	if !strings.HasPrefix(created.Deploy.ClaimURL, "http://plumtree.test/claim/") {
		t.Fatalf("claim URL = %q", created.Deploy.ClaimURL)
	}
	if created.App.Handle != "" {
		t.Fatalf("app handle before claim = %q, want empty", created.App.Handle)
	}

	rec := serveTestRequest(t, server, http.MethodGet, "/api/apps", nil, "shoo-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("apps status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var apps struct {
		Apps []struct {
			Handle string `json:"handle"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}
	if len(apps.Apps) != 0 {
		t.Fatalf("apps before claim = %+v", apps.Apps)
	}
}

func TestDevDeployAutoClaimsWithoutShoo(t *testing.T) {
	store := control.NewStore()
	server := NewWithConfig(Config{
		Store:          store,
		DevToken:       "secret",
		AutoClaimOwner: "local",
	})

	created := createDevDeploy(t, server, []byte("local wasm"))
	if !created.Deploy.Claimed {
		t.Fatalf("deploy claimed = false, response = %+v", created)
	}
	if created.App.Handle != "local/counter" {
		t.Fatalf("app handle = %q, want local/counter", created.App.Handle)
	}
	if created.Deploy.ClaimToken == "" {
		t.Fatal("auto-claimed response is missing claim token")
	}
	if created.Deploy.ClaimURL != "" || created.ClaimURL != "" {
		t.Fatalf("auto-claimed response exposed Shoo claim URL: %+v", created)
	}

	rec := serveDevRequest(t, server, http.MethodGet, "/api/dev/deploy/"+url.PathEscape(created.Deploy.ID), nil, created.Deploy.ClaimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"handle":"local/counter"`) {
		t.Fatalf("inspect body = %s", rec.Body.String())
	}
}

func TestClaimDeployAttachesToAuthenticatedOwner(t *testing.T) {
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
	server := NewWithConfig(Config{
		Store:     store,
		Verifier:  fakeVerifier{claims: shoo.Claims{PairwiseSub: "ps_test"}},
		AppOrigin: "http://plumtree.test",
		DevToken:  "secret",
	})

	created := createDevDeploy(t, server, []byte("local wasm"))
	claimToken := claimTokenFromTestURL(t, created.Deploy.ClaimURL, created.Deploy.ID)
	rec := claimDeploy(t, server, created.Deploy.ID, claimToken, "shoo-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = serveTestRequest(t, server, http.MethodGet, "/api/apps", nil, "shoo-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("apps status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var apps struct {
		Apps []struct {
			Handle         string `json:"handle"`
			ActiveDeployID string `json:"activeDeployId"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}
	if len(apps.Apps) != 1 || apps.Apps[0].Handle != owner.Handle+"/counter" || apps.Apps[0].ActiveDeployID != created.Deploy.ID {
		t.Fatalf("apps = %+v", apps.Apps)
	}
}

func TestDevDeployUpdateUsesClaimToken(t *testing.T) {
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
	rec := claimDeploy(t, server, created.Deploy.ID, claimToken, "shoo-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", rec.Code, rec.Body.String())
	}

	nextWASM := []byte("next wasm")
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(devDeployBody(nextWASM)); err != nil {
		t.Fatal(err)
	}
	rec = serveDevRequest(t, server, http.MethodPut, "/api/dev/deploy/"+url.PathEscape(created.Deploy.ID), &buf, claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var updated devDeployHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.App.Handle != "alice/counter" || !updated.Deploy.Claimed {
		t.Fatalf("updated response = %+v", updated)
	}

	_, gotDeploy, _, gotWASM, err := store.ResolveRunnable("alice/counter")
	if err != nil {
		t.Fatal(err)
	}
	if gotDeploy.ID != created.Deploy.ID || string(gotWASM) != string(nextWASM) {
		t.Fatalf("deploy=%+v wasm=%q", gotDeploy, gotWASM)
	}
}

func TestDevDeployInspectAndLogsUseClaimToken(t *testing.T) {
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
	rec := claimDeploy(t, server, created.Deploy.ID, claimToken, "shoo-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body = %s", rec.Code, rec.Body.String())
	}

	rec = serveDevRequest(t, server, http.MethodGet, "/api/dev/deploy/"+url.PathEscape(created.Deploy.ID), nil, claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"handle":"alice/counter"`) {
		t.Fatalf("inspect body = %s", rec.Body.String())
	}

	rec = serveDevRequest(t, server, http.MethodGet, "/api/dev/deploy/"+url.PathEscape(created.Deploy.ID)+"/logs", nil, claimToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("logs status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"sessions":[]`) {
		t.Fatalf("logs body = %s", rec.Body.String())
	}
}

func TestDeployClaimExpiresAfterThirtySeconds(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	store := control.NewStore(control.WithClock(func() time.Time { return now }))
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
	now = now.Add(control.DeployClaimTTL)
	rec := claimDeploy(t, server, created.Deploy.ID, claimToken, "shoo-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("claim status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := store.GetDeploy(created.Deploy.ID); !errors.Is(err, control.ErrNotFound) {
		t.Fatalf("expired deploy error = %v, want ErrNotFound", err)
	}
}

type devDeployHTTPResponse struct {
	App struct {
		Handle string `json:"handle"`
	} `json:"app"`
	Deploy struct {
		ID             string `json:"id"`
		ClaimURL       string `json:"claimUrl"`
		ClaimToken     string `json:"claimToken"`
		Claimed        bool   `json:"claimed"`
		ClaimExpiresAt string `json:"claimExpiresAt"`
	} `json:"deploy"`
	ClaimURL string `json:"claimUrl"`
}

func createDevDeploy(t *testing.T, server *Server, wasm []byte) devDeployHTTPResponse {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(devDeployBody(wasm)); err != nil {
		t.Fatal(err)
	}
	rec := serveDevRequest(t, server, http.MethodPost, "/api/dev/deploy", &buf, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var created devDeployHTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Deploy.ClaimURL == "" {
		created.Deploy.ClaimURL = created.ClaimURL
	}
	return created
}

func claimDeploy(t *testing.T, server *Server, deployID, claimToken, authToken string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/claims/" + url.PathEscape(deployID) + "/" + url.PathEscape(claimToken)
	return serveTestRequest(t, server, http.MethodPost, path, nil, authToken)
}

func serveDevRequest(t *testing.T, server *Server, method, path string, body io.Reader, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("X-Plumtree-Dev-Token", "secret")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}

func devDeployBody(wasm []byte) map[string]any {
	return map[string]any{
		"appName":           "counter",
		"appType":           "tui",
		"artifactDigest":    testDigest(wasm),
		"artifactSizeBytes": len(wasm),
		"abiVersion":        0,
		"sourceDigest":      "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"buildMetadata":     map[string]string{"go": "1.26.2"},
		"wasm":              wasm,
	}
}

func claimTokenFromTestURL(t *testing.T, raw, deployID string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "claim" || parts[1] != deployID {
		t.Fatalf("bad claim URL %q", raw)
	}
	return parts[2]
}

func testDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
