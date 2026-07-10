// Package httpbackend implements gateway.Backend over the control plane's
// operator-internal gateway API, so the SSH gateway can run as its own process
// or container, talking to a remote control plane over HTTP.
package httpbackend

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
	"github.com/Ceinl/plumtree/ssh-gateway/gatewayapi"
)

// Client calls the control plane's gateway API. It satisfies gateway.Backend.
type Client struct {
	base                string // control-plane base URL, no trailing slash
	token               string
	http                *http.Client
	maxResponseBody     int64 // metadata and error response cap
	maxArtifactResponse int64 // resolve response cap, including base64-encoded WASM
	gatewayID           string
}

const (
	maxResponseBody     = 1 << 20   // 1 MiB
	maxArtifactResponse = 256 << 20 // 256 MiB
)

func (c *Client) ResolveIdentity(fingerprint string) (runner.Identity, error) {
	var resp gatewayapi.IdentityResponse
	err := c.do(http.MethodPost, gatewayapi.BasePath+"/identity",
		gatewayapi.IdentityRequest{Fingerprint: fingerprint}, &resp)
	if err != nil {
		return runner.Identity{}, err
	}
	return runner.Identity{User: resp.User, Authenticated: resp.Authenticated}, nil
}

// New returns a Client targeting baseURL with the shared gateway token.
func New(baseURL, token string) *Client {
	var idBytes [16]byte
	_, _ = rand.Read(idBytes[:])
	return &Client{
		base:                strings.TrimRight(baseURL, "/"),
		token:               token,
		http:                &http.Client{Timeout: 30 * time.Second},
		maxResponseBody:     maxResponseBody,
		maxArtifactResponse: maxArtifactResponse,
		gatewayID:           hex.EncodeToString(idBytes[:]),
	}
}

var _ gateway.Backend = (*Client)(nil)
var _ gateway.SuspensionSource = (*Client)(nil)

func (c *Client) StartSuspensionWatcher(ctx context.Context, handle func(context.Context, gateway.Suspension) error) error {
	register := gatewayapi.RegisterSuspensionsRequest{GatewayID: c.gatewayID}
	if _, err := c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions", register, nil, c.maxResponseBody); err != nil {
		return fmt.Errorf("register suspension watcher: %w", err)
	}
	go func() {
		defer c.do(http.MethodDelete, gatewayapi.BasePath+"/suspensions", register, nil)
		for ctx.Err() == nil {
			var event gatewayapi.SuspensionResponse
			status, err := c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions/next",
				gatewayapi.NextSuspensionRequest{GatewayID: c.gatewayID}, &event, c.maxResponseBody)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				_, _ = c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions", register, nil, c.maxResponseBody)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if status == http.StatusNoContent {
				continue
			}
			var scope gateway.KillScope
			switch event.Scope {
			case "owner":
				scope = gateway.KillOwner
			case "app":
				scope = gateway.KillApp
			case "deploy":
				scope = gateway.KillDeploy
			default:
				continue
			}
			if err := handle(ctx, gateway.Suspension{Scope: scope, ID: event.ID}); err != nil {
				continue
			}
			ack := gatewayapi.AckSuspensionRequest{GatewayID: c.gatewayID, DeliveryID: event.DeliveryID}
			for ctx.Err() == nil {
				if _, err := c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions/ack", ack, nil, c.maxResponseBody); err == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
	return nil
}

func (c *Client) ResolveRunnable(handle string) (gateway.Runnable, error) {
	var resp gatewayapi.ResolveResponse
	err := c.doWithResponseLimit(http.MethodPost, gatewayapi.BasePath+"/resolve",
		gatewayapi.ResolveRequest{Handle: handle}, &resp, c.maxArtifactResponse)
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
	return c.doWithResponseLimit(method, path, body, out, c.maxResponseBody)
}

func (c *Client) doWithResponseLimit(method, path string, body, out any, responseLimit int64) error {
	_, err := c.doContext(context.Background(), method, path, body, out, responseLimit)
	return err
}

func (c *Client) doContext(ctx context.Context, method, path string, body, out any, responseLimit int64) (int, error) {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reqBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set(gatewayapi.TokenHeader, c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, c.statusError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, responseLimit)).Decode(out)
}

func (c *Client) statusError(resp *http.Response) error {
	var e gatewayapi.ErrorResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, c.maxResponseBody)).Decode(&e)
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
