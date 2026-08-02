package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/internal/control"
)

func TestDevPingRequiresValidToken(t *testing.T) {
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret"})
	for _, token := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodGet, "/api/dev/ping", nil)
		if token != "" {
			req.Header.Set("X-Plumtree-Dev-Token", token)
		}
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status = %d, want 401", token, rec.Code)
		}
	}
}

func TestDevPingReturnsEmptyDeploymentList(t *testing.T) {
	server := NewWithConfig(Config{Store: control.NewStore(), DevToken: "secret"})
	rec := serveDevPing(t, server, "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Apps   []any  `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "ok" || len(body.Apps) != 0 {
		t.Fatalf("response = %+v", body)
	}
}

func TestDevPingListsOnlyActiveDeployedApps(t *testing.T) {
	store := control.NewStore()
	owner, err := store.CreateOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.CreateApp(control.AppInput{OwnerID: owner.ID, Name: "counter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(control.AppInput{OwnerID: owner.ID, Name: "undeployed"}); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateArtifact(control.ArtifactInput{
		Digest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	deploy, err := store.CreateDeploy(control.DeployInput{
		AppID:            active.ID,
		ArtifactID:       artifact.ID,
		SourceDigest:     "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		CreatedByOwnerID: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateDeploy(active.ID, deploy.ID); err != nil {
		t.Fatal(err)
	}

	server := NewWithConfig(Config{Store: store, DevToken: "secret"})
	rec := serveDevPing(t, server, "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Apps []struct {
			Handle         string `json:"handle"`
			ActiveDeployID string `json:"activeDeployId"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Apps) != 1 || body.Apps[0].Handle != "alice/counter" || body.Apps[0].ActiveDeployID != deploy.ID {
		t.Fatalf("apps = %+v", body.Apps)
	}
}

func TestDevPingListsAutoClaimedAppsWithoutOwner(t *testing.T) {
	store := control.NewStore()
	server := NewWithConfig(Config{Store: store, DevToken: "secret", AutoClaim: true})
	created := createDevDeploy(t, server, []byte("local wasm"))

	rec := serveDevPing(t, server, "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Apps []struct {
			Handle         string `json:"handle"`
			ActiveDeployID string `json:"activeDeployId"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Apps) != 1 || body.Apps[0].Handle != "counter" || body.Apps[0].ActiveDeployID != created.Deploy.ID {
		t.Fatalf("apps = %+v", body.Apps)
	}
}

func TestDevPingLogsLifecycleWithoutCredentials(t *testing.T) {
	var logs []string
	server := NewWithConfig(Config{
		Store:    control.NewStore(),
		DevToken: "secret-never-log",
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})

	request := func(method, token string) {
		req := httptest.NewRequest(method, "/api/dev/ping", nil)
		req.RemoteAddr = "100.64.1.9:43123"
		if token != "" {
			req.Header.Set("X-Plumtree-Dev-Token", token)
		}
		server.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}
	request(http.MethodGet, "secret-never-log")
	request(http.MethodGet, "wrong-never-log")
	request(http.MethodPost, "secret-never-log")

	want := []string{
		`ping auth=ok peer="100.64.1.9" apps=0`,
		`ping auth=failed peer="100.64.1.9"`,
		`ping auth=not-checked peer="100.64.1.9" result=method-not-allowed`,
	}
	if len(logs) != len(want) {
		t.Fatalf("logs = %q, want %d events", logs, len(want))
	}
	for i := range want {
		if logs[i] != want[i] {
			t.Errorf("log[%d] = %q, want %q", i, logs[i], want[i])
		}
	}
	joined := strings.Join(logs, "\n")
	for _, credential := range []string{"secret-never-log", "wrong-never-log"} {
		if strings.Contains(joined, credential) {
			t.Fatalf("logs exposed credential %q: %s", credential, joined)
		}
	}
}

func serveDevPing(t *testing.T, server *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/dev/ping", nil)
	req.Header.Set("X-Plumtree-Dev-Token", token)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	return rec
}
