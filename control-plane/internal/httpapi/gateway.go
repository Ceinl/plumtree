package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/ssh-gateway/gatewayapi"
)

// The gateway API lets a standalone SSH gateway use the control plane as its
// Backend: resolving app handles to runnable WASM, opening/closing sessions, and
// reading claimed-only capability config. It is guarded by a shared operator
// token (not user auth) and is disabled unless GatewayToken is configured. The
// embedded all-in-one gateway bypasses this and talks to the store directly.

func (s *Server) authorizeGateway(w http.ResponseWriter, r *http.Request) bool {
	if s.gatewayToken == "" {
		writeGatewayError(w, http.StatusNotFound, "", "gateway API is disabled")
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(gatewayapi.TokenHeader)), []byte(s.gatewayToken)) != 1 {
		writeGatewayError(w, http.StatusUnauthorized, "", "invalid gateway token")
		return false
	}
	return true
}

func (s *Server) handleGatewayResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeGateway(w, r) {
		return
	}
	var req gatewayapi.ResolveRequest
	if err := readGatewayJSON(w, r, &req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	app, deploy, artifact, wasm, err := s.store.ResolveRunnable(req.Handle)
	if err != nil {
		if errors.Is(err, control.ErrSuspended) {
			writeGatewayError(w, http.StatusForbidden, gatewayapi.CodeSuspended, err.Error())
			return
		}
		writeGatewayError(w, http.StatusNotFound, "", err.Error())
		return
	}
	appType := artifact.BuildMetadata["app_type"]
	if appType == "" {
		appType = "tui"
	}
	writeJSON(w, http.StatusOK, gatewayapi.ResolveResponse{
		AppID:    app.ID,
		AppName:  app.Name,
		OwnerID:  app.OwnerID,
		DeployID: deploy.ID,
		AppType:  appType,
		WASM:     wasm,
	})
}

func (s *Server) handleGatewayIdentity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeGateway(w, r) {
		return
	}
	var req gatewayapi.IdentityRequest
	if err := readGatewayJSON(w, r, &req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	req.Fingerprint = strings.TrimSpace(req.Fingerprint)
	if req.Fingerprint == "" {
		writeGatewayError(w, http.StatusBadRequest, "", "fingerprint is required")
		return
	}
	_, owner, err := s.store.ResolveSSHKey(req.Fingerprint)
	if err != nil && !errors.Is(err, control.ErrNotFound) {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gatewayapi.IdentityResponse{
		User:          req.Fingerprint,
		Authenticated: err == nil,
		OwnerID:       owner.ID,
	})
}

func (s *Server) handleGatewayStartSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeGateway(w, r) {
		return
	}
	var req gatewayapi.StartSessionRequest
	if err := readGatewayJSON(w, r, &req); err != nil {
		writeGatewayError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	session, err := s.store.StartSession(req.AppID, req.DeployID)
	if err != nil {
		if errors.Is(err, control.ErrSuspended) {
			writeGatewayError(w, http.StatusForbidden, gatewayapi.CodeSuspended, err.Error())
			return
		}
		if errors.Is(err, control.ErrQuota) {
			writeGatewayError(w, http.StatusTooManyRequests, gatewayapi.CodeQuota, err.Error())
			return
		}
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, gatewayapi.StartSessionResponse{SessionID: session.ID})
}

// handleGatewaySessionByID routes /internal/gateway/sessions/{id}/{log|end}.
func (s *Server) handleGatewaySessionByID(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGateway(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, gatewayapi.BasePath+"/sessions/")
	id, sub, ok := splitIDAction(rest)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch sub {
	case "log":
		var req gatewayapi.RecordLogRequest
		if err := readGatewayJSON(w, r, &req); err != nil {
			writeGatewayError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		if _, err := s.store.RecordSessionLog(id, req.Log, req.Truncated); err != nil {
			writeControlError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case "end":
		if _, err := s.store.EndSession(id); err != nil {
			writeControlError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// handleGatewayApp routes /internal/gateway/apps/{appID}/{secrets|egress}.
func (s *Server) handleGatewayApp(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeGateway(w, r) {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, gatewayapi.BasePath+"/apps/")
	appID, sub, ok := splitIDAction(rest)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch sub {
	case "secrets":
		writeJSON(w, http.StatusOK, gatewayapi.SecretsResponse{Secrets: s.store.SecretsForApp(appID)})
	case "egress":
		writeJSON(w, http.StatusOK, gatewayapi.EgressResponse{Allow: s.store.EgressAllowlist(appID)})
	default:
		http.NotFound(w, r)
	}
}

// splitIDAction parses "{id}/{action}" out of a path remainder, URL-decoding the
// id segment.
func splitIDAction(rest string) (id, action string, ok bool) {
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	decoded, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", false
	}
	return decoded, parts[1], true
}

func readGatewayJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(dst)
}

func writeGatewayError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, gatewayapi.ErrorResponse{Error: msg, Code: code})
}
