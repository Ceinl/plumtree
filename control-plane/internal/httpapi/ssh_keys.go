package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"golang.org/x/crypto/ssh"
)

const maxSSHKeyRequestBytes = 1 << 20

type registerSSHKeyRequest struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

func (s *Server) handleSSHKeys(w http.ResponseWriter, r *http.Request) {
	owner, _, err := s.authenticate(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys, err := s.store.ListSSHKeys(owner.ID)
		if err != nil {
			writeControlError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			items = append(items, sshKeyResponse(key))
		}
		writeJSON(w, http.StatusOK, map[string]any{"sshKeys": items})
	case http.MethodPost:
		var req registerSSHKeyRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSSHKeyRequestBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(req.PublicKey)))
		if err != nil || len(strings.TrimSpace(string(rest))) != 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid OpenSSH public key"})
			return
		}
		key, err := s.store.RegisterSSHKey(control.SSHKeyInput{
			OwnerID:     owner.ID,
			Name:        strings.TrimSpace(req.Name),
			PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))),
			Fingerprint: ssh.FingerprintSHA256(publicKey),
		})
		if err != nil {
			writeControlError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"sshKey": sshKeyResponse(key)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSSHKeyByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	owner, _, err := s.authenticate(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	id, ok := pathTail("/api/me/ssh-keys/", r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.store.RevokeSSHKey(owner.ID, id); err != nil {
		writeControlError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sshKeyResponse(key control.SSHKey) map[string]any {
	return map[string]any{
		"id":          key.ID,
		"name":        key.Name,
		"fingerprint": key.Fingerprint,
		"createdAt":   key.CreatedAt,
	}
}
