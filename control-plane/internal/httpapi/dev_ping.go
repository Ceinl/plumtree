package httpapi

import "net/http"

// handleDevPing verifies access to the development API and returns the
// actively deployed apps visible to the server-level development token.
func (s *Server) handleDevPing(w http.ResponseWriter, r *http.Request) {
	peer := clientIP(r)
	if r.Method != http.MethodGet {
		s.logEvent("ping auth=not-checked peer=%q result=method-not-allowed", peer)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeDevDeploy(w, r) {
		auth := "failed"
		if s.devToken == "" {
			auth = "disabled"
		}
		s.logEvent("ping auth=%s peer=%q", auth, peer)
		return
	}
	deployed := s.store.ListDeployedApps()
	apps := make([]map[string]string, 0, len(deployed))
	for _, item := range deployed {
		apps = append(apps, map[string]string{
			"handle":         item.Owner.Handle + "/" + item.App.Name,
			"activeDeployId": item.App.ActiveDeployID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"apps":   apps,
	})
	s.logEvent("ping auth=ok peer=%q apps=%d", peer, len(apps))
}
