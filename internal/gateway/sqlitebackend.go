package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/Ceinl/plumtree/internal/runner"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

// SQLiteBackend selects the local repository as the gateway control service
// adapter for the native all-in-one role.
type SQLiteBackend struct {
	Repository *sqlite.Repository
	mu         sync.RWMutex
	watch      func(context.Context, Suspension) error
}

// NewSQLiteBackend connects post-commit suspension events to the embedded
// gateway kill switch.
func NewSQLiteBackend(repository *sqlite.Repository) *SQLiteBackend {
	backend := &SQLiteBackend{Repository: repository}
	repository.AddCommitListener(backend.committed)
	return backend
}

func (b *SQLiteBackend) StartSuspensionWatcher(_ context.Context, handle func(context.Context, Suspension) error) error {
	b.mu.Lock()
	b.watch = handle
	b.mu.Unlock()
	return nil
}

func (b *SQLiteBackend) committed(event sqlite.CommitEvent) error {
	if event.Operation != "deployment-suspension" {
		return nil
	}
	b.mu.RLock()
	watch := b.watch
	b.mu.RUnlock()
	if watch == nil {
		return nil
	}
	return watch(context.Background(), Suspension{Scope: KillDeploy, ID: event.ID})
}

func (b *SQLiteBackend) ResolveIdentity(fingerprint string) (runner.Identity, error) {
	device, err := b.Repository.DeviceByFingerprint(context.Background(), fingerprint)
	if errors.Is(err, sqlite.ErrNotFound) {
		return runner.Identity{User: fingerprint, Kind: runner.IdentitySSHKey}, nil
	}
	if err != nil {
		return runner.Identity{}, err
	}
	return runner.Identity{User: fingerprint, Kind: runner.IdentitySSHKey, Authenticated: true, OwnerID: device.AuthorID}, nil
}

func (b *SQLiteBackend) ResolveRunnable(handle string) (Runnable, error) {
	return b.ResolveRunnableFor(handle, runner.Identity{})
}

func (b *SQLiteBackend) ResolveRunnableFor(handle string, identity runner.Identity) (Runnable, error) {
	author, app, ok := strings.Cut(handle, "/")
	if !ok || author == "" || app == "" || strings.Contains(app, "/") {
		return Runnable{}, sqlite.ErrNotFound
	}
	fingerprint := ""
	if identity.Kind == runner.IdentitySSHKey {
		fingerprint = identity.User
	}
	resolved, err := b.Repository.ResolveLeafRunnable(context.Background(), author, app, fingerprint, identity.OwnerID)
	if errors.Is(err, sqlite.ErrSuspended) {
		return Runnable{}, ErrSuspended
	}
	if err != nil {
		return Runnable{}, err
	}
	return Runnable{AppID: resolved.App.ID, AppName: resolved.App.Name, OwnerID: resolved.App.AuthorID,
		DeployID: resolved.DeploymentID, ArtifactDigest: resolved.Artifact.Digest, AppType: resolved.App.Kind, WASM: resolved.WASM}, nil
}

func (b *SQLiteBackend) StartSession(appID, deployID string) (string, error) {
	deployment, _, err := b.Repository.Deployment(context.Background(), deployID)
	if err != nil || deployment.AppID != appID {
		return "", err
	}
	_, artifact, err := b.Repository.CurrentDeployment(context.Background(), appID)
	if err != nil {
		return "", err
	}
	return b.StartAccountedSession(appID, deployID, artifact.Digest, "")
}

func (b *SQLiteBackend) StartAccountedSession(appID, deployID, artifactDigest, identitySummary string) (string, error) {
	id := randomSessionID()
	err := b.Repository.StartSession(context.Background(), sqlite.Session{ID: id, AppID: appID, DeploymentID: deployID, ArtifactDigest: artifactDigest, LeafIdentitySummary: identitySummary})
	if errors.Is(err, sqlite.ErrSuspended) {
		return "", ErrSuspended
	}
	if errors.Is(err, sqlite.ErrQuota) {
		return "", ErrQuota
	}
	return id, err
}

func (b *SQLiteBackend) RecordSessionLog(sessionID, log string, truncated bool) error {
	return b.Repository.RecordSessionLog(context.Background(), sessionID, log, truncated)
}

func (b *SQLiteBackend) EndSession(sessionID string) error {
	_, err := b.Repository.EndSession(context.Background(), sessionID)
	return err
}

func (b *SQLiteBackend) SecretsForApp(appID string) map[string]string {
	metadata, err := b.Repository.ListSecrets(context.Background(), appID)
	if err != nil {
		return nil
	}
	result := make(map[string]string, len(metadata))
	for _, item := range metadata {
		_, value, err := b.Repository.Secret(context.Background(), appID, item.Key)
		if err != nil {
			return nil
		}
		result[item.Key] = string(value)
	}
	return result
}

func (b *SQLiteBackend) EgressAllowlist(appID string) []string {
	result, _ := b.Repository.ListEgressHosts(context.Background(), appID)
	return result
}

func randomSessionID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return "session_" + hex.EncodeToString(raw[:])
}

var _ Backend = (*SQLiteBackend)(nil)
var _ IdentityAwareBackend = (*SQLiteBackend)(nil)
var _ AccountedBackend = (*SQLiteBackend)(nil)
var _ SuspensionSource = (*SQLiteBackend)(nil)
