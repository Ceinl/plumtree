package httpapi

import "net/http"

// handleDevPing verifies access to the development API and returns the
// actively deployed apps visible to the server-level development token.
func (s *Server) handleDevPing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeDevDeploy(w, r) {
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
}
