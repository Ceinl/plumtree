package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// streamKeepalive is how often the apps stream sends an SSE comment to keep
// idle connections (and proxies) alive between real updates.
const streamKeepalive = 25 * time.Second

// handleAppsStream pushes the owner's app list over Server-Sent Events: an
// initial snapshot, then a fresh snapshot every time runtime state changes (a
// session starts or ends), so per-app connection counts update without polling.
func (s *Server) handleAppsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	owner, ok := s.requireOwnerWithHandle(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Disable proxy buffering (nginx) so events flush immediately.
	w.Header().Set("X-Accel-Buffering", "no")

	changes, unsubscribe := s.store.Subscribe()
	defer unsubscribe()

	send := func() bool {
		items, err := s.appItems(owner)
		if err != nil {
			return false
		}
		payload, err := json.Marshal(map[string]any{"apps": items})
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !send() {
		return
	}

	keepalive := time.NewTicker(streamKeepalive)
	defer keepalive.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			if !send() {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
