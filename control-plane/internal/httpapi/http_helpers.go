package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/control-plane/internal/auth/shoo"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

func firstNonEmpty[T ~string](values ...T) string {
	for _, value := range values {
		if value != "" {
			return string(value)
		}
	}
	return ""
}

func (s *Server) claimURL(r *http.Request, deployID, claimToken string) string {
	origin := strings.TrimRight(s.appOrigin, "/")
	if origin == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		origin = scheme + "://" + r.Host
	}
	return origin + "/claim/" + url.PathEscape(deployID) + "/" + url.PathEscape(claimToken)
}

func newClaimToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func claimPath(prefix, path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || rest == "" {
		return "", "", false
	}
	deployID, token, ok := strings.Cut(rest, "/")
	if !ok || deployID == "" || token == "" || strings.Contains(token, "/") {
		return "", "", false
	}
	var err error
	deployID, err = url.PathUnescape(deployID)
	if err != nil {
		return "", "", false
	}
	token, err = url.PathUnescape(token)
	if err != nil {
		return "", "", false
	}
	return deployID, token, true
}

func pathTail(prefix, path string) (string, bool) {
	tail := strings.TrimPrefix(path, prefix)
	if tail == path || tail == "" || strings.Contains(tail, "/") {
		return "", false
	}
	value, err := url.PathUnescape(tail)
	if err != nil || value == "" {
		return "", false
	}
	return value, true
}

func (s *Server) authenticate(r *http.Request) (control.Owner, shoo.Claims, error) {
	if s.verifier == nil {
		return control.Owner{}, shoo.Claims{}, errAuthUnavailable
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		return control.Owner{}, shoo.Claims{}, errMissingBearer
	}
	claims, err := s.verifier.Verify(r.Context(), token)
	if err != nil {
		return control.Owner{}, shoo.Claims{}, err
	}
	owner, _, err := s.store.EnsureOwnerForIdentity(control.IdentityInput{
		Provider: control.ProviderShoo,
		Subject:  claims.PairwiseSub,
	})
	return owner, claims, err
}

func cloneMetadata(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func bearerToken(header string) string {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

var (
	errMissingBearer   = errors.New("missing bearer token")
	errAuthUnavailable = errors.New("auth verifier unavailable")
)

func writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	if errors.Is(err, errAuthUnavailable) {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeControlError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, control.ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, control.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, control.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, control.ErrQuota):
		status = http.StatusTooManyRequests
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeDeployClaimError(w http.ResponseWriter, err error) {
	if errors.Is(err, control.ErrInvalid) && strings.Contains(err.Error(), "claim token") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeControlError(w, err)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://shoo.dev 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self' https://shoo.dev; img-src 'self' https: data:; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func afterDeployClaimTTL(ttl time.Duration, fn func()) {
	if ttl <= 0 {
		ttl = control.DeployClaimTTL
	}
	time.AfterFunc(ttl, fn)
}
