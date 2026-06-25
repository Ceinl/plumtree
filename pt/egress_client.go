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

type egressResponse struct {
	Hosts []string `json:"hosts"`
}

func egressURL(serverURL, deployID string) string {
	return strings.TrimRight(serverURL, "/") + "/api/dev/deploy/" + url.PathEscape(deployID) + "/egress"
}

func addEgressHost(ctx context.Context, serverURL, devToken, deployID, claimToken, host string) ([]string, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(map[string]string{"host": host}); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, egressURL(serverURL, deployID), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	setSecretAuth(req, devToken, claimToken)
	return doEgressRequest(req)
}

func removeEgressHost(ctx context.Context, serverURL, devToken, deployID, claimToken, host string) ([]string, error) {
	target := egressURL(serverURL, deployID) + "/" + url.PathEscape(host)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return nil, err
	}
	setSecretAuth(req, devToken, claimToken)
	return doEgressRequest(req)
}

func listEgressHosts(ctx context.Context, serverURL, devToken, deployID, claimToken string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, egressURL(serverURL, deployID), nil)
	if err != nil {
		return nil, err
	}
	setSecretAuth(req, devToken, claimToken)
	return doEgressRequest(req)
}

func doEgressRequest(req *http.Request) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out egressResponse
	if err := decodeControlResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Hosts, nil
}
