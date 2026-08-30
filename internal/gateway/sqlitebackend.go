package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Ceinl/plumtree/internal/backoff"
	"github.com/Ceinl/plumtree/internal/runner"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

const (
	// maxPendingSuspensions bounds how many undelivered suspension events wait
	// for the kill switch; beyond it the oldest are dropped (suspensions of the
	// same deployment coalesce, so a burst collapses instead of growing).
	maxPendingSuspensions = 64
	suspensionRetryBase   = 500 * time.Millisecond
	suspensionRetryMax    = 30 * time.Second
)

// SQLiteBackend selects the local repository as the gateway control service
// adapter for the native all-in-one role.
type SQLiteBackend struct {
	Repository *sqlite.Repository
	// Logf, when set, receives operator diagnostics about suspension delivery.
	Logf func(format string, args ...any)

	mu            sync.Mutex
	watch         func(context.Context, Suspension) error
	pending       []Suspension  // undelivered events awaiting the kill switch
	retry         *time.Timer   // schedules the next flushPending pass
	retryAttempts int           // consecutive failed passes; drives backoff
	retryBase     time.Duration // zero selects suspensionRetryBase (test seam)
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
	pending := b.pending
	b.pending = nil
	b.retryAttempts = 0
	timer := b.retry
	b.retry = nil
	b.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
	// Deliver every suspension committed before registration before returning:
	// the gateway admits no session until this call returns, so nothing escapes
	// a kill switch that was requested while the watcher was still coming up.
	for _, event := range pending {
		if err := b.deliver(event); err != nil {
			b.requeue(event)
		}
	}
	return nil
}

func (b *SQLiteBackend) committed(event sqlite.CommitEvent) error {
	if event.Operation != "deployment-suspension" {
		return nil
	}
	suspension := Suspension{Scope: KillDeploy, ID: event.ID}
	b.mu.Lock()
	watch := b.watch
	if watch == nil {
		b.enqueueLocked(suspension)
		b.mu.Unlock()
		b.logf("deployment %s suspended before the kill switch registered; holding for delivery", suspension.ID)
		return nil
	}
	b.mu.Unlock()
	if err := b.deliver(suspension); err != nil {
		// The suspension is already committed; a failing kill switch must not
		// silently drop it. Keep it pending and retry in the background.
		b.requeue(suspension)
	}
	return nil
}

// deliver hands one suspension to the kill switch. Its return is the
// acknowledgement.
func (b *SQLiteBackend) deliver(event Suspension) error {
	b.mu.Lock()
	watch := b.watch
	b.mu.Unlock()
	if watch == nil {
		return errors.New("gateway: kill switch is not registered")
	}
	return watch(context.Background(), event)
}

// requeue keeps a failed delivery pending and schedules a bounded-backoff retry
// so a slow or failing kill switch never drops a committed suspension.
func (b *SQLiteBackend) requeue(event Suspension) {
	b.mu.Lock()
	b.enqueueLocked(event)
	b.scheduleRetryLocked()
	b.mu.Unlock()
}

// enqueueLocked records an event for delivery, coalescing duplicates and
// bounding the queue by dropping the oldest entries.
func (b *SQLiteBackend) enqueueLocked(event Suspension) {
	for _, existing := range b.pending {
		if existing == event {
			return
		}
	}
	if len(b.pending) >= maxPendingSuspensions {
		dropped := b.pending[0]
		b.pending = b.pending[1:]
		b.logf("suspension backlog full; dropping oldest delivery %s/%s", dropped.Scope, dropped.ID)
	}
	b.pending = append(b.pending, event)
}

func (b *SQLiteBackend) scheduleRetryLocked() {
	base := b.retryBase
	if base <= 0 {
		base = suspensionRetryBase
	}
	delay := backoff.Delay(b.retryAttempts, base, suspensionRetryMax)
	b.retryAttempts++
	if b.retry == nil {
		b.retry = time.AfterFunc(delay, b.flushPending)
		return
	}
	b.retry.Reset(delay)
}

// flushPending retries every pending delivery; failures stay pending and are
// rescheduled with growing delay until the kill switch acknowledges them.
func (b *SQLiteBackend) flushPending() {
	b.mu.Lock()
	watch := b.watch
	pending := b.pending
	b.pending = nil
	b.retry = nil
	b.mu.Unlock()
	if watch == nil || len(pending) == 0 {
		return
	}
	var failed []Suspension
	for _, event := range pending {
		if err := watch(context.Background(), event); err != nil {
			failed = append(failed, event)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(failed) > 0 {
		// Preserve order: retried deliveries first, then anything that arrived
		// while this pass was running.
		b.pending = append(failed, b.pending...)
		b.scheduleRetryLocked()
		b.logf("retrying %d suspension deliveries after failure", len(failed))
		return
	}
	b.retryAttempts = 0
}

func (b *SQLiteBackend) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
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

func (b *SQLiteBackend) SecretsForApp(appID string) (map[string]string, error) {
	metadata, err := b.Repository.ListSecrets(context.Background(), appID)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(metadata))
	for _, item := range metadata {
		_, value, err := b.Repository.Secret(context.Background(), appID, item.Key)
		if err != nil {
			return nil, err
		}
		result[item.Key] = string(value)
	}
	return result, nil
}

func (b *SQLiteBackend) EgressAllowlist(appID string) ([]string, error) {
	return b.Repository.ListEgressHosts(context.Background(), appID)
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
