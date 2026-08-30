// Package controlclient implements gateway.Backend over the root server's
// operator-internal gateway API, so the SSH gateway can run as its own process
// or container, talking to a remote root server over HTTP.
package controlclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/backoff"
	"github.com/Ceinl/plumtree/internal/gateway"
	"github.com/Ceinl/plumtree/internal/protocol/gateway"
	"github.com/Ceinl/plumtree/internal/runner"
)

// Client calls the control plane's gateway API. It satisfies gateway.Backend.
type Client struct {
	base                string // root-server base URL, no trailing slash
	token               string
	http                *http.Client
	maxResponseBody     int64 // metadata and error response cap
	maxArtifactResponse int64 // resolve response cap, including base64-encoded WASM
	gatewayID           string
	// Logf, when set, receives operator diagnostics such as end-session retry
	// outcomes and definitive rejections.
	Logf func(format string, args ...any)

	endMu       sync.Mutex
	endQueue    []pendingEndSession // awaiting confirmation, in queue order
	endQueued   map[string]struct{} // session IDs already held for retry
	endDraining bool                // the drain goroutine is running
	endWake     chan struct{}       // nudges the drain loop to an earlier deadline
	endBackoff  time.Duration       // retry backoff base; zero selects endRetryBase (test seam)
}

const (
	maxResponseBody     = 1 << 20   // 1 MiB
	maxArtifactResponse = 256 << 20 // 256 MiB

	// End-session retries release a lost quota slot; they must outlast any
	// transient server outage without spinning.
	maxPendingEndSessions = 1024
	endRetryBase          = 250 * time.Millisecond
	endRetryMax           = 30 * time.Second

	watcherPollBackoffBase = 100 * time.Millisecond
	watcherPollBackoffMax  = 8 * time.Second
	watcherAckBackoffBase  = 100 * time.Millisecond
	watcherAckBackoffMax   = 5 * time.Second
)

// pendingEndSession is one session whose end call is unconfirmed on the
// control plane and is being retried in the background.
type pendingEndSession struct {
	sessionID string
	attempts  int
	nextAt    time.Time
}

func (c *Client) ResolveIdentity(fingerprint string) (runner.Identity, error) {
	var resp gatewayapi.IdentityResponse
	err := c.do(http.MethodPost, gatewayapi.BasePath+"/identity",
		gatewayapi.IdentityRequest{Fingerprint: fingerprint}, &resp)
	if err != nil {
		return runner.Identity{}, err
	}
	identity := runner.Identity{User: resp.User, Authenticated: resp.Authenticated, Kind: runner.IdentitySSHKey}
	if resp.Authenticated {
		// Never trust owner metadata on an unauthenticated identity: a hostile
		// or buggy control plane must not be able to grant owner authority.
		identity.OwnerID = resp.OwnerID
	}
	return identity, nil
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
		endQueued:           make(map[string]struct{}),
		endWake:             make(chan struct{}, 1),
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
		pollFailures := 0
		for ctx.Err() == nil {
			var event gatewayapi.SuspensionResponse
			status, err := c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions/next",
				gatewayapi.NextSuspensionRequest{GatewayID: c.gatewayID}, &event, c.maxResponseBody)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				// Re-register and wait with growing, jittered delay so a slow
				// control plane does not turn into a tight retry loop.
				_, _ = c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions", register, nil, c.maxResponseBody)
				time.Sleep(backoff.Delay(pollFailures, watcherPollBackoffBase, watcherPollBackoffMax))
				pollFailures++
				continue
			}
			pollFailures = 0
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
			ackFailures := 0
			for ctx.Err() == nil {
				if _, err := c.doContext(ctx, http.MethodPost, gatewayapi.BasePath+"/suspensions/ack", ack, nil, c.maxResponseBody); err == nil {
					break
				}
				time.Sleep(backoff.Delay(ackFailures, watcherAckBackoffBase, watcherAckBackoffMax))
				ackFailures++
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

// EndSession marks a session finished on the control plane. The request carries
// an idempotency key so a lost response can be replayed safely instead of
// permanently leaking the session's quota slot: transient failures are queued
// for bounded-backoff background retries until the control plane confirms.
func (c *Client) EndSession(sessionID string) error {
	err := c.do(http.MethodPost,
		gatewayapi.BasePath+"/sessions/"+url.PathEscape(sessionID)+"/end", nil, nil,
		withHeader("Idempotency-Key", "end-"+sessionID))
	if err == nil {
		c.removeQueuedEnd(sessionID)
		return nil
	}
	if !isTransient(err) {
		c.logf("end-session %s definitively rejected (%v); not retrying", sessionID, err)
		return err
	}
	if c.queuePendingEnd(sessionID) {
		return err
	}
	c.logf("end-session retry queue is full; %s teardown proceeds unconfirmed (quota slot may leak)", sessionID)
	return err
}

// queuePendingEnd holds a session for background end retries, starting the
// drain goroutine if needed. It reports false when the queue is at capacity,
// degrading without blocking the caller's session teardown.
func (c *Client) queuePendingEnd(sessionID string) bool {
	base := c.endBackoff
	if base <= 0 {
		base = endRetryBase
	}
	c.endMu.Lock()
	defer c.endMu.Unlock()
	if _, ok := c.endQueued[sessionID]; !ok {
		if len(c.endQueue) >= maxPendingEndSessions {
			return false
		}
		c.endQueued[sessionID] = struct{}{}
		c.endQueue = append(c.endQueue, pendingEndSession{sessionID: sessionID, nextAt: time.Now().Add(base)})
	}
	if !c.endDraining {
		c.endDraining = true
		go c.drainPendingEndSessions()
	} else {
		select {
		case c.endWake <- struct{}{}:
		default:
		}
	}
	return true
}

// drainPendingEndSessions retries unconfirmed end-session calls with bounded
// exponential backoff plus jitter until each is confirmed, rejected
// definitively, or dropped because its caller gave up on the client.
func (c *Client) drainPendingEndSessions() {
	for {
		now := time.Now()
		var due []pendingEndSession
		var kept []pendingEndSession
		delay := time.Duration(0)
		c.endMu.Lock()
		for _, item := range c.endQueue {
			if item.nextAt.After(now) {
				kept = append(kept, item)
				if d := item.nextAt.Sub(now); delay == 0 || d < delay {
					delay = d
				}
			} else {
				due = append(due, item)
			}
		}
		c.endQueue = kept
		c.endMu.Unlock()

		if len(due) == 0 {
			if len(kept) == 0 {
				c.endMu.Lock()
				empty := len(c.endQueue) == 0
				if empty {
					c.endDraining = false
				}
				c.endMu.Unlock()
				if empty {
					return
				}
				continue
			}
			timer := time.NewTimer(delay)
			select {
			case <-c.endWake:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}

		for _, item := range due {
			err := c.do(http.MethodPost,
				gatewayapi.BasePath+"/sessions/"+url.PathEscape(item.sessionID)+"/end", nil, nil,
				withHeader("Idempotency-Key", "end-"+item.sessionID))
			switch {
			case err == nil:
				c.removeQueuedEnd(item.sessionID)
				if item.attempts > 0 {
					c.logf("end-session %s confirmed after %d retries", item.sessionID, item.attempts)
				}
			case !isTransient(err):
				c.removeQueuedEnd(item.sessionID)
				c.logf("end-session %s definitively rejected by control plane (%v); stopping retries", item.sessionID, err)
			default:
				base := c.endBackoff
				if base <= 0 {
					base = endRetryBase
				}
				item.attempts++
				item.nextAt = time.Now().Add(backoff.Delay(item.attempts, base, endRetryMax))
				c.requeuePendingEnd(item)
			}
		}
	}
}

// requeuePendingEnd puts a failed retry back into the queue unless its caller
// already confirmed or dropped it meanwhile.
func (c *Client) requeuePendingEnd(item pendingEndSession) {
	c.endMu.Lock()
	defer c.endMu.Unlock()
	if _, ok := c.endQueued[item.sessionID]; !ok {
		return
	}
	c.endQueue = append(c.endQueue, item)
}

// removeQueuedEnd drops a session from the retry set after confirmation or a
// definitive rejection.
func (c *Client) removeQueuedEnd(sessionID string) {
	c.endMu.Lock()
	defer c.endMu.Unlock()
	delete(c.endQueued, sessionID)
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Client) SecretsForApp(appID string) (map[string]string, error) {
	var resp gatewayapi.SecretsResponse
	if err := c.do(http.MethodGet,
		gatewayapi.BasePath+"/apps/"+url.PathEscape(appID)+"/secrets", nil, &resp); err != nil {
		return nil, fmt.Errorf("%w: secrets: %v", gateway.ErrCapsUnavailable, err)
	}
	return resp.Secrets, nil
}

func (c *Client) EgressAllowlist(appID string) ([]string, error) {
	var resp gatewayapi.EgressResponse
	if err := c.do(http.MethodGet,
		gatewayapi.BasePath+"/apps/"+url.PathEscape(appID)+"/egress", nil, &resp); err != nil {
		return nil, fmt.Errorf("%w: egress: %v", gateway.ErrCapsUnavailable, err)
	}
	return resp.Allow, nil
}

// requestOption customizes a single outgoing API request.
type requestOption func(*http.Request)

// withHeader sets one header on the request, e.g. an idempotency key.
func withHeader(key, value string) requestOption {
	return func(r *http.Request) { r.Header.Set(key, value) }
}

// do issues a request to path, JSON-encoding body when non-nil and decoding a
// 2xx response into out when non-nil. Non-2xx responses are turned into errors,
// mapping the API's error codes back to the gateway's sentinel errors.
func (c *Client) do(method, path string, body, out any, options ...requestOption) error {
	return c.doWithResponseLimit(method, path, body, out, c.maxResponseBody, options...)
}

func (c *Client) doWithResponseLimit(method, path string, body, out any, responseLimit int64, options ...requestOption) error {
	_, err := c.doContext(context.Background(), method, path, body, out, responseLimit, options...)
	return err
}

func (c *Client) doContext(ctx context.Context, method, path string, body, out any, responseLimit int64, options ...requestOption) (int, error) {
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
	for _, option := range options {
		option(req)
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

// StatusError reports a non-2xx server response. Callers classify it by
// code — definitive versus transient — instead of matching error strings.
type StatusError struct {
	Code    int    // HTTP status code
	Status  string // wire status line, e.g. "503 Service Unavailable"
	Message string // server-supplied detail, when present
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("gateway api %s: %s", e.Status, e.Message)
}

// Transient reports whether the failed call may be retried: server-side 5xx
// plus the explicitly retryable 408 and 429. Remaining 4xx codes are
// definitive rejections that no retry can fix.
func (e *StatusError) Transient() bool {
	return e.Code >= 500 || e.Code == http.StatusRequestTimeout || e.Code == http.StatusTooManyRequests
}

// isTransient reports whether a failed API call may be retried. Transport-level
// errors carry no status and are always considered transient; only a parsed
// StatusError can be definitive.
func isTransient(err error) bool {
	var status *StatusError
	if errors.As(err, &status) {
		return status.Transient()
	}
	return true
}

func (c *Client) statusError(resp *http.Response) error {
	var e gatewayapi.ErrorResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, c.maxResponseBody)).Decode(&e)
	msg := e.Error
	if msg == "" {
		msg = resp.Status
	}
	status := &StatusError{Code: resp.StatusCode, Status: resp.Status, Message: msg}
	switch e.Code {
	case gatewayapi.CodeSuspended:
		return fmt.Errorf("%w: %w", gateway.ErrSuspended, status)
	case gatewayapi.CodeQuota:
		return fmt.Errorf("%w: %w", gateway.ErrQuota, status)
	default:
		return status
	}
}
