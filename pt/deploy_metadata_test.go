package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeployReadOptionsUsesSavedDevToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plumtree.json"), []byte(`{"name":"counter","type":"tui"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDeployMetadata(dir, deployMetadata{
		ServerURL:  "http://localhost:18080",
		DevToken:   "saved-secret",
		DeployID:   "dep_000001",
		ClaimToken: "claim-token",
	}); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	_, deployID, server, token, err := deployReadOptions("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if deployID != "dep_000001" || server != "http://localhost:18080" || token != "saved-secret" {
		t.Fatalf("deployID=%q server=%q token=%q", deployID, server, token)
	}
}

func TestDeployReadOptionsFlagOverridesSavedDevToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plumtree.json"), []byte(`{"name":"counter","type":"tui"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDeployMetadata(dir, deployMetadata{
		ServerURL:  "http://localhost:18080",
		DevToken:   "saved-secret",
		DeployID:   "dep_000001",
		ClaimToken: "claim-token",
	}); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	_, deployID, server, token, err := deployReadOptions("http://other.test", "flag-secret", "dep_other")
	if err != nil {
		t.Fatal(err)
	}
	if deployID != "dep_other" || server != "http://other.test" || token != "flag-secret" {
		t.Fatalf("deployID=%q server=%q token=%q", deployID, server, token)
	}
}

func TestDeployReadOptionsDefaultsLocalDevToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plumtree.json"), []byte(`{"name":"counter","type":"tui"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDeployMetadata(dir, deployMetadata{
		ServerURL:  "http://localhost:18080",
		DeployID:   "dep_000001",
		ClaimToken: "claim-token",
	}); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)

	_, _, _, token, err := deployReadOptions("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if token != "local-dev" {
		t.Fatalf("token = %q, want local-dev", token)
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
