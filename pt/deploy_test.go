package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployStartsFreshOnConfiguredServerChangeAndDropsLegacyToken(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "plumtree.json"), []byte(`{"name":"counter","type":"tui"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDeployMetadata(proj, deployMetadata{
		ServerURL:  "https://old.example",
		DevToken:   "legacy-shared-token",
		DeployID:   "dep_000001",
		ClaimToken: "old-claim-token",
	}); err != nil {
		t.Fatal(err)
	}

	var methods []string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method != http.MethodPost || r.URL.Path != "/api/dev/deploy" {
			http.Error(w, "unexpected request", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app": map[string]any{"name": "counter"},
			"deploy": map[string]any{
				"id":       "dep_000001",
				"claimUrl": server.URL + "/claim/dep_000001/new-claim-token",
			},
		})
	}))
	t.Cleanup(server.Close)

	t.Setenv("PLUMTREE_PT_CONFIG", filepath.Join(t.TempDir(), "pt.json"))
	t.Setenv("PLUMTREE_SERVER_URL", server.URL)
	t.Setenv("PLUMTREE_DEV_TOKEN", "new-shared-token")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })

	if err := cmdDeploy(nil); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 1 || methods[0] != http.MethodPost {
		t.Fatalf("deploy methods = %v, want [POST]", methods)
	}

	b, err := os.ReadFile(deployMetadataPath(proj))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"devToken"`) || strings.Contains(string(b), "new-shared-token") || strings.Contains(string(b), "legacy-shared-token") {
		t.Fatalf("deploy metadata retained shared token: %s", b)
	}
	meta, err := readDeployMetadata(proj)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ServerURL != server.URL || meta.DeployID != "dep_000001" || meta.ClaimToken != "new-claim-token" {
		t.Fatalf("deploy metadata = %+v", meta)
	}
}
