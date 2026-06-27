// Package httpbackend implements gateway.Backend over the control plane's
// operator-internal gateway API, so the SSH gateway can run as its own process
// or container, talking to a remote control plane over HTTP.
package httpbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
	"github.com/Ceinl/plumtree/ssh-gateway/gatewayapi"
)

// Client calls the control plane's gateway API. It satisfies gateway.Backend.
type Client struct {
	base   string // control-plane base URL, no trailing slash
	token  string
	http   *http.Client
	maxLog int64 // response body cap for non-WASM responses
}

// New returns a Client targeting baseURL with the shared gateway token.
func New(baseURL, token string) *Client {
	return &Client{
		base:   strings.TrimRight(baseURL, "/"),
		token:  token,
		http:   &http.Client{Timeout: 30 * time.Second},
		maxLog: 1 << 20,
	}
}

var _ gateway.Backend = (*Client)(nil)

func (c *Client) ResolveRunnable(handle string) (gateway.Runnable, error) {
	var resp gatewayapi.ResolveResponse
	err := c.do(http.MethodPost, gatewayapi.BasePath+"/resolve",
		gatewayapi.ResolveRequest{Handle: handle}, &resp)
	if err != nil {
		return gateway.Runnable{}, err
	}
	return gateway.Runnable{
		AppID:    resp.AppID,
		AppName:  resp.AppName,
		OwnerID:  resp.OwnerID,
		DeployID: resp.DeployID,
		AppType:  resp.AppType,
		WASM:     resp.WASM,
	}, nil
}

func (c *Client) StartSession(appID, deployID string) (string, error) {
	var resp gatewayapi.StartSessionResponse
	err := c.do(http.MethodPost, gatewayapi.BasePath+"/sessions",
		gatewayapi.StartSessionRequest{AppID: appID, DeployID: deployID}, &resp)
	if err != nil {
		return "", err
	}
	return resp.SessionID, nil
}

func (c *Client) RecordSessionLog(sessionID, log string, truncated bool) error {
	return c.do(http.MethodPost,
		gatewayapi.BasePath+"/sessions/"+url.PathEscape(sessionID)+"/log",
		gatewayapi.RecordLogRequest{Log: log, Truncated: truncated}, nil)
}

func (c *Client) EndSession(sessionID string) error {
	return c.do(http.MethodPost,
		gatewayapi.BasePath+"/sessions/"+url.PathEscape(sessionID)+"/end", nil, nil)
}

func (c *Client) SecretsForApp(appID string) map[string]string {
	var resp gatewayapi.SecretsResponse
	if err := c.do(http.MethodGet,
		gatewayapi.BasePath+"/apps/"+url.PathEscape(appID)+"/secrets", nil, &resp); err != nil {
		return nil
	}
	return resp.Secrets
}

func (c *Client) EgressAllowlist(appID string) []string {
	var resp gatewayapi.EgressResponse
	if err := c.do(http.MethodGet,
		gatewayapi.BasePath+"/apps/"+url.PathEscape(appID)+"/egress", nil, &resp); err != nil {
		return nil
	}
	return resp.Allow
}

// do issues a request to path, JSON-encoding body when non-nil and decoding a
// 2xx response into out when non-nil. Non-2xx responses are turned into errors,
// mapping the API's error codes back to the gateway's sentinel errors.
func (c *Client) do(method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set(gatewayapi.TokenHeader, c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.statusError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, c.maxLog)).Decode(out)
}

func (c *Client) statusError(resp *http.Response) error {
	var e gatewayapi.ErrorResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, c.maxLog)).Decode(&e)
	msg := e.Error
	if msg == "" {
		msg = resp.Status
	}
	switch e.Code {
	case gatewayapi.CodeSuspended:
		return fmt.Errorf("%w: %s", gateway.ErrSuspended, msg)
	case gatewayapi.CodeQuota:
		return fmt.Errorf("%w: %s", gateway.ErrQuota, msg)
	default:
		return fmt.Errorf("gateway api %s: %s", resp.Status, msg)
	}
}
