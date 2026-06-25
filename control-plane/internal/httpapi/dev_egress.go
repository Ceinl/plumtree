package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleDevEgress serves /api/dev/deploy/{deployID}/egress[/{host}], the gated
// egress allowlist API used by `pt egress`. Like secrets it is authorized by the
// dev token plus the deploy claim token and requires a claimed app: egress is
// unlocked by claiming, then narrowed to allowlisted hosts.
//
//	GET             list allowed hosts
//	POST   {host}   add a host
//	DELETE /{host}  remove a host
func (s *Server) handleDevEgress(w http.ResponseWriter, r *http.Request, deployID, tail string) {
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
		writeJSON(w, http.StatusConflict, map[string]string{"error": "claim the app before configuring egress"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"hosts": s.store.EgressAllowlist(app.ID)})
	case http.MethodPost, http.MethodPut:
		var body struct {
			Host string `json:"host"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil || strings.TrimSpace(body.Host) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing host"})
			return
		}
		hosts, err := s.store.AddEgressHost(app.ID, strings.ToLower(strings.TrimSpace(body.Host)))
		if err != nil {
			writeControlError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
	case http.MethodDelete:
		host := strings.ToLower(strings.Trim(tail, "/"))
		if host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing host"})
			return
		}
		hosts, err := s.store.RemoveEgressHost(app.ID, host)
		if err != nil {
			writeControlError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
