package main

import (
	"os"
	"path/filepath"
	"testing"
)

// seedDeployProject writes a minimal app project with saved deploy metadata and
// chdirs into it for the duration of the test.
func seedDeployProject(t *testing.T, meta deployMetadata) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plumtree.json"), []byte(`{"name":"counter","type":"tui"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDeployMetadata(dir, meta); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(oldwd) })
}

// The deploy identity comes from the saved file; the server URL and token come
// from the environment, not the file or any flag.
func TestDeployReadOptionsUsesEnvForServerAndToken(t *testing.T) {
	seedDeployProject(t, deployMetadata{
		ServerURL:  "http://stale-from-file:9999",
		DevToken:   "stale-from-file",
		DeployID:   "dep_000001",
		ClaimToken: "claim-token",
	})
	t.Setenv("PLUMTREE_SERVER_URL", "https://main.example")
	t.Setenv("PLUMTREE_DEV_TOKEN", "env-secret")

	_, deployID, server, token, err := deployReadOptions("")
	if err != nil {
		t.Fatal(err)
	}
	if deployID != "dep_000001" || server != "https://main.example" || token != "env-secret" {
		t.Fatalf("deployID=%q server=%q token=%q", deployID, server, token)
	}
}

// A deploy-id argument overrides the saved identity.
func TestDeployReadOptionsDeployArgOverridesSaved(t *testing.T) {
	seedDeployProject(t, deployMetadata{DeployID: "dep_000001", ClaimToken: "claim-token"})
	t.Setenv("PLUMTREE_DEV_TOKEN", "env-secret")

	_, deployID, _, _, err := deployReadOptions("dep_other")
	if err != nil {
		t.Fatal(err)
	}
	if deployID != "dep_other" {
		t.Fatalf("deployID = %q, want dep_other", deployID)
	}
}

// With no token in the environment, read-only commands fall back to the
// conventional local-dev token and the built-in default server.
func TestDeployReadOptionsDefaultsLocalDevToken(t *testing.T) {
	seedDeployProject(t, deployMetadata{DeployID: "dep_000001", ClaimToken: "claim-token"})
	t.Setenv("PLUMTREE_SERVER_URL", "")
	t.Setenv("PLUMTREE_DEV_TOKEN", "")

	_, _, server, token, err := deployReadOptions("")
	if err != nil {
		t.Fatal(err)
	}
	if token != "local-dev" {
		t.Fatalf("token = %q, want local-dev", token)
	}
	if server != localServerURL {
		t.Fatalf("server = %q, want local default %q", server, localServerURL)
	}
}

func TestClaimTokenFromURL(t *testing.T) {
	raw := "http://localhost:18080/claim/dep_000001/token_abc-123"
	if got := claimTokenFromURL(raw, "dep_000001"); got != "token_abc-123" {
		t.Fatalf("token = %q", got)
	}
	if got := claimTokenFromURL(raw, "dep_other"); got != "" {
		t.Fatalf("token for wrong deploy = %q", got)
	}
}
