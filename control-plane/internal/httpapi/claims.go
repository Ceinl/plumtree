package httpapi

import "net/http"

func (s *Server) handleClaimAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	deployID, claimToken, ok := claimPath("/api/claims/", r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	owner, _, err := s.authenticate(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if owner.Handle == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "choose an owner handle first"})
		return
	}
	app, deploy, status, err := s.store.ClaimDeploy(deployID, hashClaimToken(claimToken), owner.ID)
	if err != nil {
		writeDeployClaimError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status,
		"app": map[string]any{
			"id":             app.ID,
			"name":           app.Name,
			"handle":         owner.Handle + "/" + app.Name,
			"activeDeployId": app.ActiveDeployID,
		},
		"deploy": map[string]any{
			"id":      deploy.ID,
			"claimed": deploy.CreatedByOwnerID != "",
		},
	})
}

func (s *Server) handleClaimPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	deployID, claimToken, ok := claimPath("/claim/", r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = claimTmpl.Execute(w, struct {
		DeployID   string
		ClaimToken string
		CSPNonce   string
	}{DeployID: deployID, ClaimToken: claimToken, CSPNonce: cspNonce(r.Context())})
}

func (s *Server) scheduleDeployClaimCleanup() {
	afterDeployClaimTTL(s.store.DeployClaimTTL(), func() {
		_, _ = s.store.DeleteExpiredDeployClaims()
	})
}
