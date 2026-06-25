package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
)

type createTokenRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.requireOwnerWithHandle(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		tokens, err := s.store.ListCITokens(owner.ID)
		if err != nil {
			writeControlError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(tokens))
		for _, token := range tokens {
			items = append(items, tokenJSON(token))
		}
		writeJSON(w, http.StatusOK, map[string]any{"tokens": items})
	case http.MethodPost:
		s.createToken(w, r, owner)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, owner control.Owner) {
	var req createTokenRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scopes, err := parseScopes(req.Scopes)
	if err != nil {
		writeControlError(w, err)
		return
	}
	plaintext, err := newCIToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	token, err := s.store.CreateCIToken(control.CITokenInput{
		OwnerID:   owner.ID,
		Name:      strings.TrimSpace(req.Name),
		TokenHash: hashClaimToken(plaintext),
		Scopes:    scopes,
	})
	if err != nil {
		writeControlError(w, err)
		return
	}
	// The plaintext token is returned exactly once, at creation.
	out := tokenJSON(token)
	out["token"] = plaintext
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleTokenByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	owner, ok := s.requireOwnerWithHandle(w, r)
	if !ok {
		return
	}
	id, ok := pathTail("/api/me/tokens/", r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	token, err := s.store.RevokeCIToken(owner.ID, id)
	if err != nil {
		writeControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenJSON(token))
}

func parseScopes(raw []string) ([]control.TokenScope, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: at least one token scope is required", control.ErrInvalid)
	}
	seen := make(map[control.TokenScope]bool, len(raw))
	scopes := make([]control.TokenScope, 0, len(raw))
	for _, value := range raw {
		scope, err := control.ParseScope(value)
		if err != nil {
			return nil, err
		}
		if seen[scope] {
			continue
		}
		seen[scope] = true
		scopes = append(scopes, scope)
	}
	return scopes, nil
}

func tokenJSON(token control.CIToken) map[string]any {
	out := map[string]any{
		"id":        token.ID,
		"name":      token.Name,
		"scopes":    token.Scopes,
		"createdAt": token.CreatedAt,
		"revoked":   token.RevokedAt != nil,
	}
	if token.RevokedAt != nil {
		out["revokedAt"] = token.RevokedAt
	}
	return out
}

func newCIToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ptci_" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}
