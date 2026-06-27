package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	owner, claims, err := s.authenticate(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner": map[string]any{
			"id":          owner.ID,
			"handle":      owner.Handle,
			"needsHandle": owner.Handle == "",
		},
		"auth": map[string]any{
			"provider": "shoo",
			"subject":  claims.PairwiseSub,
		},
	})
}

type claimHandleRequest struct {
	Handle string `json:"handle"`
}

func (s *Server) handleMeHandle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	owner, _, err := s.authenticate(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req claimHandleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	owner, err = s.store.ClaimOwnerHandle(owner.ID, strings.TrimSpace(req.Handle))
	if err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner": map[string]any{
			"id":          owner.ID,
			"handle":      owner.Handle,
			"needsHandle": false,
		},
	})
}

// requireOwnerWithHandle authenticates the Shoo bearer and requires that the
// owner has already claimed a handle. It writes the error response itself.
func (s *Server) requireOwnerWithHandle(w http.ResponseWriter, r *http.Request) (control.Owner, bool) {
	owner, _, err := s.authenticate(r)
	if err != nil {
		writeAuthError(w, err)
		return control.Owner{}, false
	}
	if owner.Handle == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "choose an owner handle first"})
		return control.Owner{}, false
	}
	return owner, true
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	owner, ok := s.requireOwnerWithHandle(w, r)
	if !ok {
		return
	}
	apps, err := s.store.ListApps(owner.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]map[string]any, 0, len(apps))
	for _, app := range apps {
		items = append(items, map[string]any{
			"id":             app.ID,
			"name":           app.Name,
			"handle":         owner.Handle + "/" + app.Name,
			"activeDeployId": app.ActiveDeployID,
			"createdAt":      app.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": items})
}
