package buildworker

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Service is the HTTP front for a Builder. It runs as its own process,
// separate from the control plane, so untrusted compilation never shares an
// address space with app metadata, auth state, or secrets.
type Service struct {
	builder *Builder
	// token, when set, is required in the X-Plumtree-Build-Token header. The
	// control plane is the only intended caller.
	token string
	// maxBody bounds the request body independently of the source-size check,
	// guarding the JSON decode itself.
	maxBody int64
}

// NewService wraps a Builder. An empty token disables auth (local/dev use).
func NewService(builder *Builder, token string) *Service {
	return &Service{builder: builder, token: token, maxBody: builder.cfg.MaxSourceBytes*2 + (1 << 20)}
}

// Handler returns the service's HTTP routes.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/build", s.handleBuild)
	return mux
}

func (s *Service) handleBuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Plumtree-Build-Token")), []byte(s.token)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid build token"})
		return
	}
	var req Request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.maxBody)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := s.builder.Build(r.Context(), req)
	if err != nil {
		// Worker-internal error: the build could not be attempted.
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Client calls a remote build Service. The control plane uses it to compile
// uploaded source without hosting the toolchain in its own process.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a Client with a default timeout sized for cold builds.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// Build sends a build request and returns the structured Result. A non-nil
// error indicates a transport or worker-internal failure; an author-caused
// build failure arrives as Result.Failure with a nil error.
func (c *Client) Build(ctx context.Context, req Request) (Result, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/build", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		httpReq.Header.Set("X-Plumtree-Build-Token", c.Token)
	}
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("build worker returned %s: %s", resp.Status, bytes.TrimSpace(data))
	}
	var res Result
	if err := json.Unmarshal(data, &res); err != nil {
		return Result{}, fmt.Errorf("decode build result: %w", err)
	}
	return res, nil
}
