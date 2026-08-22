package cleanrole

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	protocol "github.com/Ceinl/plumtree/internal/protocol/pairing"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

type BootstrapConfig struct {
	Database, Handle, DeviceName string
	TTL                          time.Duration
}

type BootstrapResult struct {
	ID, Handle, DeviceName string
	Secret                 []byte
	ExpiresAt              time.Time
}

// Bootstrap creates one local, time-bounded first-author authority. The
// returned secret is shown once and only its salted verifier is stored.
func Bootstrap(ctx context.Context, cfg BootstrapConfig) (BootstrapResult, error) {
	if cfg.Database == "" || cfg.Handle == "" || cfg.DeviceName == "" {
		return BootstrapResult{}, errors.New("clean server: database, handle, and device name are required")
	}
	if cfg.TTL == 0 {
		cfg.TTL = 10 * time.Minute
	}
	if cfg.TTL < time.Minute || cfg.TTL > time.Hour {
		return BootstrapResult{}, errors.New("clean server: bootstrap TTL must be between 1m and 1h")
	}
	repo, err := sqlite.OpenRepository(cfg.Database, nil)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer repo.Close()
	identities, err := newIdentityService(repo, "local-bootstrap")
	if err != nil {
		return BootstrapResult{}, err
	}
	secretRaw := make([]byte, 32)
	salt := make([]byte, 16)
	if _, err := rand.Read(secretRaw); err != nil {
		return BootstrapResult{}, err
	}
	if _, err := rand.Read(salt); err != nil {
		return BootstrapResult{}, err
	}
	secret := []byte(base64.RawURLEncoding.EncodeToString(secretRaw))
	verifier, err := protocol.DeriveVerifier(salt, secret)
	if err != nil {
		return BootstrapResult{}, err
	}
	idRaw := make([]byte, 16)
	if _, err := rand.Read(idRaw); err != nil {
		return BootstrapResult{}, err
	}
	id := "bootstrap_" + base64.RawURLEncoding.EncodeToString(idRaw)
	expires := time.Now().Add(cfg.TTL)
	if _, err := identities.CreateBootstrapAuthorityLocal(ctx, sqlite.BootstrapAuthorityInput{ID: id, Handle: cfg.Handle, DeviceName: cfg.DeviceName, Salt: salt, Verifier: verifier, ExpiresAt: expires}); err != nil {
		return BootstrapResult{}, fmt.Errorf("clean server: create bootstrap authority: %w", err)
	}
	return BootstrapResult{ID: id, Handle: cfg.Handle, DeviceName: cfg.DeviceName, Secret: secret, ExpiresAt: expires}, nil
}
