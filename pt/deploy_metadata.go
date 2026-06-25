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

func deployReadOptions(serverFlag, devTokenFlag, deployArg string) (*deployMetadata, string, string, string, error) {
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
	server := normalizedServerURL(firstNonEmpty(serverFlag, meta.ServerURL, "http://localhost:18080"))
	devToken := firstNonEmpty(devTokenFlag, meta.DevToken, "local-dev")
	if devToken == "" {
		return nil, "", "", "", errors.New("missing --dev-token; run pt deploy with --dev-token once, or pass --dev-token/PLUMTREE_DEV_TOKEN")
	}
	deployID := deployArg
	if deployID == "" {
		deployID = meta.DeployID
	}
	if deployID == "" || meta.ClaimToken == "" {
		return nil, "", "", "", errors.New("deploy claim metadata is incomplete; run pt deploy again")
	}
	return meta, deployID, server, devToken, nil
}

func usableDeployMetadata(meta *deployMetadata, serverURL string) bool {
	if meta == nil {
		return false
	}
	return normalizedServerURL(meta.ServerURL) == normalizedServerURL(serverURL) && meta.DeployID != "" && meta.ClaimToken != ""
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
