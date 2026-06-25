package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

// handleDevSecrets serves /api/dev/deploy/{deployID}/secrets[/{key}]. It is the
// claimed-app secret API used by `pt secret`: authorized by the dev token plus
// the deploy claim token (the same proof of ownership `pt deploy` uses), it
// stores values server-side and never returns them. Secrets require a claimed
// app — an owned deploy — so unclaimed previews cannot hold secrets.
//
//	POST   {key,value}  set a secret
//	GET                 list secret metadata (names + versions, never values)
//	DELETE /{key}       remove a secret
func (s *Server) handleDevSecrets(w http.ResponseWriter, r *http.Request, deployID, tail string) {
	if !s.authorizeDevDeploy(w, r) {
		return
	}
	claimToken := bearerToken(r.Header.Get("Authorization"))
	if claimToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing deploy claim token"})
		return
	}
	_, app, _, _, err := s.store.InspectDeployClaim(deployID, hashClaimToken(claimToken))
	if err != nil {
		writeDeployClaimError(w, err)
		return
	}
	if app.ID == "" || app.OwnerID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "claim the app before setting secrets"})
		return
	}

	switch r.Method {
	case http.MethodPost, http.MethodPut:
		s.setDevSecret(w, r, app.ID)
	case http.MethodGet:
		s.listDevSecrets(w, app.ID)
	case http.MethodDelete:
		s.deleteDevSecret(w, app.ID, strings.Trim(tail, "/"))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) setDevSecret(w http.ResponseWriter, r *http.Request, appID string) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	meta, err := s.store.UpsertSecret(control.SecretInput{
		AppID: appID, Key: body.Key, Value: []byte(body.Value),
	})
	if err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":     meta.Key,
		"version": meta.Version,
	})
}

func (s *Server) listDevSecrets(w http.ResponseWriter, appID string) {
	metas := s.store.ListSecrets(appID)
	out := make([]map[string]any, 0, len(metas))
	for _, m := range metas {
		out = append(out, map[string]any{
			"key":       m.Key,
			"version":   m.Version,
			"updatedAt": m.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"secrets": out})
}

func (s *Server) deleteDevSecret(w http.ResponseWriter, appID, key string) {
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing secret key"})
		return
	}
	if err := s.store.DeleteSecret(appID, key); err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": key})
}
