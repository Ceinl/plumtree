package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type deployRequest struct {
	AppName           string            `json:"appName"`
	AppType           string            `json:"appType"`
	ArtifactDigest    string            `json:"artifactDigest"`
	ArtifactSizeBytes int64             `json:"artifactSizeBytes"`
	ABIVersion        uint8             `json:"abiVersion"`
	SourceDigest      string            `json:"sourceDigest"`
	BuildMetadata     map[string]string `json:"buildMetadata"`
	WASM              []byte            `json:"wasm,omitempty"`
	Source            []byte            `json:"source,omitempty"`
}

type deployResponse struct {
	App struct {
		Name           string `json:"name"`
		Handle         string `json:"handle"`
		ActiveDeployID string `json:"activeDeployId"`
	} `json:"app"`
	Deploy struct {
		ID             string `json:"id"`
		ClaimURL       string `json:"claimUrl"`
		ClaimToken     string `json:"claimToken"`
		Claimed        bool   `json:"claimed"`
		ClaimExpiresAt string `json:"claimExpiresAt"`
	} `json:"deploy"`
	ClaimURL      string `json:"claimUrl"`
	PreviewHandle string `json:"previewHandle"`
}

type inspectResponse struct {
	App struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Handle         string `json:"handle"`
		ActiveDeployID string `json:"activeDeployId"`
		Claimed        bool   `json:"claimed"`
	} `json:"app"`
	Deploy struct {
		ID           string `json:"id"`
		AppType      string `json:"appType"`
		SourceDigest string `json:"sourceDigest"`
		CreatedAt    string `json:"createdAt"`
		ClaimedAt    string `json:"claimedAt"`
	} `json:"deploy"`
	Artifact struct {
		ID            string            `json:"id"`
		Digest        string            `json:"digest"`
		SizeBytes     int64             `json:"sizeBytes"`
		ABIVersion    uint8             `json:"abiVersion"`
		BuildMetadata map[string]string `json:"buildMetadata"`
		CreatedAt     string            `json:"createdAt"`
	} `json:"artifact"`
}

type logsResponse struct {
	Sessions []struct {
		ID           string `json:"id"`
		AppID        string `json:"appId"`
		DeployID     string `json:"deployId"`
		StartedAt    string `json:"startedAt"`
		EndedAt      string `json:"endedAt"`
		Log          string `json:"log"`
		LogTruncated bool   `json:"logTruncated"`
	} `json:"sessions"`
}

type pingResponse struct {
	Status string `json:"status"`
	Apps   []struct {
		Handle         string `json:"handle"`
		ActiveDeployID string `json:"activeDeployId"`
	} `json:"apps"`
}

func getPing(ctx context.Context, serverURL, devToken string) (pingResponse, error) {
	target := strings.TrimRight(serverURL, "/") + "/api/dev/ping"
	var out pingResponse
	if err := doControlGET(ctx, target, devToken, "", &out); err != nil {
		return out, err
	}
	if out.Status != "ok" {
		return out, errors.New("unexpected ping response from control plane")
	}
	return out, nil
}

func postDeploy(ctx context.Context, serverURL, devToken string, payload deployRequest) (deployResponse, error) {
	target := strings.TrimRight(serverURL, "/") + "/api/dev/deploy"
	return doDeployRequest(ctx, http.MethodPost, target, devToken, "", payload)
}

func putDeploy(ctx context.Context, serverURL, devToken, deployID, claimToken string, payload deployRequest) (deployResponse, error) {
	target := strings.TrimRight(serverURL, "/") + "/api/dev/deploy/" + url.PathEscape(deployID)
	return doDeployRequest(ctx, http.MethodPut, target, devToken, claimToken, payload)
}

func getDeployInspect(ctx context.Context, serverURL, devToken, deployID, claimToken string) (inspectResponse, error) {
	target := strings.TrimRight(serverURL, "/") + "/api/dev/deploy/" + url.PathEscape(deployID)
	var out inspectResponse
	return out, doControlGET(ctx, target, devToken, claimToken, &out)
}

func getDeployLogs(ctx context.Context, serverURL, devToken, deployID, claimToken string) (logsResponse, error) {
	target := strings.TrimRight(serverURL, "/") + "/api/dev/deploy/" + url.PathEscape(deployID) + "/logs"
	var out logsResponse
	return out, doControlGET(ctx, target, devToken, claimToken, &out)
}

func doControlGET(ctx context.Context, target, devToken, claimToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plumtree-Dev-Token", devToken)
	if claimToken != "" {
		req.Header.Set("Authorization", "Bearer "+claimToken)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decodeControlResponse(resp, out)
}

func doDeployRequest(ctx context.Context, method, target, devToken, claimToken string, payload deployRequest) (deployResponse, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(payload); err != nil {
		return deployResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, &buf)
	if err != nil {
		return deployResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Plumtree-Dev-Token", devToken)
	if claimToken != "" {
		req.Header.Set("Authorization", "Bearer "+claimToken)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return deployResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return deployResponse{}, parseBuildFailure(body)
	}
	var out deployResponse
	if err := decodeControlResponse(resp, &out); err != nil {
		return deployResponse{}, err
	}
	return out, nil
}

func decodeControlResponse(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type buildFailedError struct {
	Stage   string
	Message string
	Log     string
}

func (e *buildFailedError) Error() string {
	msg := "build failed"
	if e.Stage != "" {
		msg += " (" + e.Stage + ")"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if strings.TrimSpace(e.Log) != "" {
		msg += "\n\n" + strings.TrimRight(e.Log, "\n")
	}
	return msg
}

func parseBuildFailure(body []byte) error {
	var payload struct {
		Stage   string `json:"stage"`
		Message string `json:"message"`
		Log     string `json:"log"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Message == "" {
		return &httpStatusError{StatusCode: http.StatusUnprocessableEntity, Status: "422 Unprocessable Entity", Body: strings.TrimSpace(string(body))}
	}
	return &buildFailedError{Stage: payload.Stage, Message: payload.Message, Log: payload.Log}
}

type httpStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	if e.Body == "" {
		return "control-plane deploy failed: " + e.Status
	}
	return "control-plane deploy failed: " + e.Status + ": " + e.Body
}

func isHTTPStatus(err error, status int) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == status
}
