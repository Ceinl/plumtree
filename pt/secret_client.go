package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type secretSetResponse struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

type secretListResponse struct {
	Secrets []struct {
		Key       string `json:"key"`
		Version   int    `json:"version"`
		UpdatedAt string `json:"updatedAt"`
	} `json:"secrets"`
}

func secretsURL(serverURL, deployID string) string {
	return strings.TrimRight(serverURL, "/") + "/api/dev/deploy/" + url.PathEscape(deployID) + "/secrets"
}

func setSecret(ctx context.Context, serverURL, devToken, deployID, claimToken, key, value string) (secretSetResponse, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]string{"key": key, "value": value}); err != nil {
		return secretSetResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, secretsURL(serverURL, deployID), &buf)
	if err != nil {
		return secretSetResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	setSecretAuth(req, devToken, claimToken)
	var out secretSetResponse
	if err := doControlRequest(req, &out); err != nil {
		return secretSetResponse{}, err
	}
	return out, nil
}

func listSecrets(ctx context.Context, serverURL, devToken, deployID, claimToken string) (secretListResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretsURL(serverURL, deployID), nil)
	if err != nil {
		return secretListResponse{}, err
	}
	setSecretAuth(req, devToken, claimToken)
	var out secretListResponse
	if err := doControlRequest(req, &out); err != nil {
		return secretListResponse{}, err
	}
	return out, nil
}

func deleteSecret(ctx context.Context, serverURL, devToken, deployID, claimToken, key string) error {
	target := secretsURL(serverURL, deployID) + "/" + url.PathEscape(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	setSecretAuth(req, devToken, claimToken)
	return doControlRequest(req, nil)
}

func setSecretAuth(req *http.Request, devToken, claimToken string) {
	req.Header.Set("X-Plumtree-Dev-Token", devToken)
	if claimToken != "" {
		req.Header.Set("Authorization", "Bearer "+claimToken)
	}
}

// doControlRequest sends req and decodes a JSON response into out (nil to
// ignore the body), reusing the shared control-plane error decoding.
func doControlRequest(req *http.Request, out any) error {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return decodeControlResponse(resp, &struct{}{})
	}
	return decodeControlResponse(resp, out)
}
