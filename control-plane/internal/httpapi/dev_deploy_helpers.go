package httpapi

import (
	"net/http"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func (s *Server) writeDeployLogs(w http.ResponseWriter, app control.App) {
	if app.ID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "deploy is not claimed yet"})
		return
	}
	sessions, err := s.store.ListSessionsForApp(app.ID)
	if err != nil {
		writeControlError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(sessions))
	for _, session := range sessions {
		item := map[string]any{
			"id":        session.ID,
			"appId":     session.AppID,
			"deployId":  session.DeployID,
			"startedAt": session.StartedAt,
		}
		if session.EndedAt != nil {
			item["endedAt"] = session.EndedAt
		}
		if session.Log != "" {
			item["log"] = session.Log
		}
		if session.LogTruncated {
			item["logTruncated"] = true
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": items})
}

func (s *Server) authorizeDevDeploy(w http.ResponseWriter, r *http.Request) bool {
	if s.devToken == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "dev deploy API is disabled"})
		return false
	}
	if r.Header.Get("X-Plumtree-Dev-Token") != s.devToken {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid dev token"})
		return false
	}
	return true
}
