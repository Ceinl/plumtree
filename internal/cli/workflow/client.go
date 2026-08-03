package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/protocol/control"
	"github.com/Ceinl/plumtree/internal/runner"
)

var (
	ErrAPI     = errors.New("workflow: API request failed")
	ErrProblem = errors.New("workflow: server rejected request")
	ErrBuild   = errors.New("workflow: local build failed")
)

type ProblemError struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

func (e *ProblemError) Error() string {
	if e == nil {
		return ErrProblem.Error()
	}
	message := e.Code
	if e.Detail != "" {
		message += ": " + runner.SanitizeTerminalText(e.Detail)
	}
	return fmt.Sprintf("%s (%d)", message, e.Status)
}

type API struct {
	HTTP  *http.Client
	Close io.Closer
}

func NewAPI(connection *paired.ControlConnection) (*API, error) {
	if connection == nil || connection.HTTP == nil {
		return nil, fmt.Errorf("%w: control connection is required", ErrAPI)
	}
	return &API{HTTP: connection.HTTP, Close: connection}, nil
}

func (a *API) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	if a == nil || a.HTTP == nil || !strings.HasPrefix(path, control.APIPrefix+"/") {
		return fmt.Errorf("%w: invalid client or path", ErrAPI)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://plumtree"+path, body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAPI, err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := a.HTTP.Do(request)
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrAPI, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem ProblemError
		if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&problem); err == nil && problem.Code != "" {
			problem.Status = response.StatusCode
			return &problem
		}
		return fmt.Errorf("%w: server returned %s", ErrAPI, response.Status)
	}
	if out == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(response.Body, 4<<20))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: invalid response: %v", ErrAPI, err)
	}
	return nil
}

type Version struct {
	Product    string           `json:"product"`
	Version    string           `json:"version"`
	APIVersion int              `json:"apiVersion"`
	ABIVersion int              `json:"abiVersion"`
	Limits     map[string]int64 `json:"limits"`
}

func (a *API) Version(ctx context.Context) (Version, error) {
	var out Version
	err := a.do(ctx, http.MethodGet, control.APIPrefix+"/version", nil, "", &out)
	return out, err
}

type ArtifactRequest struct {
	Name          string
	Type          string
	Access        string
	SourceDigest  string
	BuildMetadata map[string]string
	ABIVersion    uint8
	WASM          []byte
}

type DeployResult struct {
	API      int             `json:"apiVersion"`
	App      json.RawMessage `json:"app"`
	Deploy   json.RawMessage `json:"deploy"`
	Artifact json.RawMessage `json:"artifact"`
}

func (a *API) Deploy(ctx context.Context, input ArtifactRequest, previous string) (DeployResult, error) {
	if len(input.WASM) == 0 || input.Name == "" || (input.Type != "tui" && input.Type != "cli") || (input.Access != "public" && input.Access != "restricted") {
		return DeployResult{}, fmt.Errorf("%w: invalid artifact request", ErrBuild)
	}
	digest := input.SourceDigest
	if digest == "" {
		digest = digestBytes(input.WASM)
	}
	metadata, err := json.Marshal(struct {
		AppName       string            `json:"appName"`
		AppType       string            `json:"appType"`
		AccessMode    string            `json:"accessMode"`
		ABIVersion    uint8             `json:"abiVersion"`
		SourceDigest  string            `json:"sourceDigest"`
		BuildMetadata map[string]string `json:"buildMetadata"`
	}{input.Name, input.Type, input.Access, input.ABIVersion, digest, input.BuildMetadata})
	if err != nil {
		return DeployResult{}, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	metaHeader := make(textproto.MIMEHeader)
	metaHeader.Set("Content-Disposition", `form-data; name="metadata"`)
	metaHeader.Set("Content-Type", "application/json")
	part, err := mw.CreatePart(metaHeader)
	if err != nil {
		return DeployResult{}, err
	}
	if _, err := part.Write(metadata); err != nil {
		return DeployResult{}, err
	}
	artifactHeader := make(textproto.MIMEHeader)
	artifactHeader.Set("Content-Disposition", `form-data; name="artifact"`)
	artifactHeader.Set("Content-Type", "application/wasm")
	part, err = mw.CreatePart(artifactHeader)
	if err != nil {
		return DeployResult{}, err
	}
	if _, err := part.Write(input.WASM); err != nil {
		return DeployResult{}, err
	}
	if err := mw.Close(); err != nil {
		return DeployResult{}, err
	}
	path := control.APIPrefix + "/deployments"
	method := http.MethodPost
	if previous != "" {
		path += "/" + url.PathEscape(previous)
		method = http.MethodPut
	}
	var result DeployResult
	if err := a.do(ctx, method, path, &body, mw.FormDataContentType(), &result); err != nil {
		return DeployResult{}, err
	}
	return result, nil
}

type App struct{ ID, AuthorID, Name, Kind, AccessMode, ActiveDeployID string }
type AppsResult struct {
	Apps []App `json:"apps"`
}
type AppResult struct {
	App              App             `json:"app"`
	ActiveDeployment json.RawMessage `json:"activeDeployment"`
}
type SessionsResult struct {
	Sessions []json.RawMessage `json:"sessions"`
}
type SecretsResult struct {
	Secrets []json.RawMessage `json:"secrets"`
}
type EgressResult struct {
	Hosts []string `json:"hosts"`
}
type AccessResult struct {
	Keys []json.RawMessage `json:"keys"`
}
type AuditResult struct {
	Events []json.RawMessage `json:"events"`
}

func (a *API) Apps(ctx context.Context) (AppsResult, error) {
	var out AppsResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/apps", nil, "", &out)
}
func (a *API) App(ctx context.Context, id string) (AppResult, error) {
	var out AppResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/apps/"+url.PathEscape(id), nil, "", &out)
}
func (a *API) Sessions(ctx context.Context, id string) (SessionsResult, error) {
	var out SessionsResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/apps/"+url.PathEscape(id)+"/sessions", nil, "", &out)
}
func (a *API) Secrets(ctx context.Context, id string) (SecretsResult, error) {
	var out SecretsResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/apps/"+url.PathEscape(id)+"/secrets", nil, "", &out)
}
func (a *API) Egress(ctx context.Context, id string) (EgressResult, error) {
	var out EgressResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/apps/"+url.PathEscape(id)+"/egress", nil, "", &out)
}
func (a *API) Access(ctx context.Context, id string) (AccessResult, error) {
	var out AccessResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/apps/"+url.PathEscape(id)+"/access", nil, "", &out)
}

func (a *API) AddAccess(ctx context.Context, appID, name, publicKey, fingerprint string) error {
	return a.writeJSON(ctx, http.MethodPost, "/apps/"+url.PathEscape(appID)+"/access", map[string]string{"name": name, "publicKey": publicKey, "fingerprint": fingerprint}, nil)
}

func (a *API) RemoveAccess(ctx context.Context, appID, keyID string) error {
	return a.do(ctx, http.MethodDelete, control.APIPrefix+"/apps/"+url.PathEscape(appID)+"/access/"+url.PathEscape(keyID), nil, "", nil)
}
func (a *API) Audit(ctx context.Context) (AuditResult, error) {
	var out AuditResult
	return out, a.do(ctx, http.MethodGet, control.APIPrefix+"/audit", nil, "", &out)
}

func (a *API) SetSecret(ctx context.Context, appID, key, value string) error {
	return a.writeJSON(ctx, http.MethodPut, "/apps/"+url.PathEscape(appID)+"/secrets/"+url.PathEscape(key), map[string]string{"value": value}, nil)
}
func (a *API) DeleteSecret(ctx context.Context, appID, key string) error {
	return a.do(ctx, http.MethodDelete, control.APIPrefix+"/apps/"+url.PathEscape(appID)+"/secrets/"+url.PathEscape(key), nil, "", nil)
}
func (a *API) SetEgress(ctx context.Context, appID, host string, allowed bool) error {
	method, path := http.MethodPut, "/apps/"+url.PathEscape(appID)+"/egress"
	if !allowed {
		method, path = http.MethodDelete, path+"/"+url.PathEscape(host)
		return a.do(ctx, method, control.APIPrefix+path, nil, "", nil)
	}
	return a.writeJSON(ctx, method, path, map[string]string{"host": host}, nil)
}

func (a *API) writeJSON(ctx context.Context, method, path string, value, out any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return a.do(ctx, method, control.APIPrefix+path, bytes.NewReader(data), "application/json", out)
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SSHInstruction returns a direct leaf command. It intentionally does not
// route through pt or expose a control URL/token.
func SSHInstruction(record paired.ServerRecord, handle string) (string, error) {
	if handle == "" || record.Host == "" || record.Port < 1 || record.Port > 65535 || strings.ContainsAny(handle+record.Host, "\r\n\x1b") {
		return "", fmt.Errorf("%w: incomplete SSH target", ErrTarget)
	}
	return fmt.Sprintf("ssh -p %d %s@%s", record.Port, handle, record.Host), nil
}

func (a *API) FollowSessions(ctx context.Context, appID string, interval time.Duration, emit func(SessionsResult) error) error {
	if interval <= 0 {
		interval = time.Second
	}
	for {
		result, err := a.Sessions(ctx, appID)
		if err != nil {
			return err
		}
		if emit != nil {
			if err := emit(result); err != nil {
				return err
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
