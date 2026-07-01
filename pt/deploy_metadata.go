package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type deployMetadata struct {
	ServerURL      string `json:"serverUrl"`
	DevToken       string `json:"devToken,omitempty"`
	DeployID       string `json:"deployId"`
	ClaimToken     string `json:"claimToken"`
	ClaimURL       string `json:"claimUrl"`
	ClaimExpiresAt string `json:"claimExpiresAt,omitempty"`
	AppHandle      string `json:"appHandle,omitempty"`
	UpdatedAt      string `json:"updatedAt"`
}

func deployMetadataPath(proj string) string {
	return filepath.Join(proj, ".plumtree", "deploy.json")
}

func readDeployMetadata(proj string) (*deployMetadata, error) {
	b, err := os.ReadFile(deployMetadataPath(proj))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var meta deployMetadata
	if err := json.Unmarshal(b, &meta); err != nil {
		return nil, fmt.Errorf(".plumtree/deploy.json: %w", err)
	}
	return &meta, nil
}

func writeDeployMetadata(proj string, meta deployMetadata) error {
	path := deployMetadataPath(proj)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func updateCurrentDeployMetadata(meta deployMetadata) error {
	proj, err := findProject()
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return writeDeployMetadata(proj, meta)
}

// defaultServerURL and defaultDevToken are baked into the binary at build time
// so a released pt publishes to the maintainer's control plane with no
// configuration: a user downloads the release and runs `pt deploy`. The release
// build injects them from CI secrets, e.g. in GitHub Actions:
//
//	go build -ldflags "\
//	  -X 'main.defaultServerURL=$PLUMTREE_SERVER_URL' \
//	  -X 'main.defaultDevToken=$PLUMTREE_DEV_TOKEN'"
//
// pt is `package main`, so the linker symbol path is `main`, not the import
// path github.com/Ceinl/plumtree/pt.
//
// Both are empty in an un-baked local build; the matching PLUMTREE_* environment
// variables override the baked values for the maintainer's own development.
var (
	defaultServerURL = ""
	defaultDevToken  = ""
)

// localServerURL is the fallback when neither a baked value nor the environment
// supplies one, so an un-baked dev build still targets the local control plane.
const localServerURL = "http://localhost:18080"

// resolveServerURL returns the deploy target: PLUMTREE_SERVER_URL if set, then
// the value baked at build time, then the local dev server. The address is build
// or environment configuration, not a per-command flag, so authors publish to
// the maintainer's server without knowing or passing it.
func resolveServerURL() string {
	return normalizedServerURL(firstNonEmpty(os.Getenv("PLUMTREE_SERVER_URL"), defaultServerURL, localServerURL))
}

// resolveDevToken returns the deploy token: PLUMTREE_DEV_TOKEN if set, then the
// value baked at build time. Empty when neither is present.
func resolveDevToken() string {
	return firstNonEmpty(os.Getenv("PLUMTREE_DEV_TOKEN"), defaultDevToken)
}

// deployReadOptions resolves the target for the read-only commands (inspect,
// logs, whoami): the deploy identity comes from the per-app .plumtree/deploy.json
// while the server URL and token come from the environment.
func deployReadOptions(deployArg string) (*deployMetadata, string, string, string, error) {
	proj, err := findProject()
	if err != nil {
		return nil, "", "", "", err
	}
	meta, err := readDeployMetadata(proj)
	if err != nil {
		return nil, "", "", "", err
	}
	if meta == nil {
		return nil, "", "", "", errors.New("no deploy claim metadata found; run pt deploy first")
	}
	server := resolveServerURL()
	devToken := firstNonEmpty(resolveDevToken(), "local-dev")
	deployID := deployArg
	if deployID == "" {
		deployID = meta.DeployID
	}
	if deployID == "" || meta.ClaimToken == "" {
		return nil, "", "", "", errors.New("deploy claim metadata is incomplete; run pt deploy again")
	}
	return meta, deployID, server, devToken, nil
}

// usableDeployMetadata reports whether saved metadata can update an existing
// deploy in place: it just needs the deploy identity, since the server URL and
// token now come from the environment rather than the file.
func usableDeployMetadata(meta *deployMetadata) bool {
	return meta != nil && meta.DeployID != "" && meta.ClaimToken != ""
}

func normalizedServerURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func responseClaimURL(res deployResponse) string {
	if res.Deploy.ClaimURL != "" {
		return res.Deploy.ClaimURL
	}
	return res.ClaimURL
}

func deployClaimURL(meta *deployMetadata) string {
	if meta == nil {
		return ""
	}
	if meta.ClaimURL != "" {
		return meta.ClaimURL
	}
	if meta.ServerURL == "" || meta.DeployID == "" || meta.ClaimToken == "" {
		return ""
	}
	return normalizedServerURL(meta.ServerURL) + "/claim/" + url.PathEscape(meta.DeployID) + "/" + url.PathEscape(meta.ClaimToken)
}

func claimTokenFromURL(raw, deployID string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "claim" {
		return ""
	}
	gotDeployID, err := url.PathUnescape(parts[1])
	if err != nil || gotDeployID != deployID {
		return ""
	}
	token, err := url.PathUnescape(parts[2])
	if err != nil {
		return ""
	}
	return token
}
