package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalid   = errors.New("sqlite repository: invalid input")
	ErrNotFound  = errors.New("sqlite repository: not found")
	ErrConflict  = errors.New("sqlite repository: conflict")
	ErrQuota     = errors.New("sqlite repository: quota exceeded")
	ErrSuspended = errors.New("sqlite repository: suspended")

	ErrInjectedStatement = errors.New("sqlite repository: injected statement failure")
	ErrInjectedCommit    = errors.New("sqlite repository: injected commit failure")
)

// Faults allows tests to fail a named mutation before a statement or commit.
// It is intentionally an internal test seam, not a production retry policy.
type Faults struct {
	Statement func(operation string) error
	Commit    func(operation string) error
}

// RepositoryOption configures the unselected SQLite repository.
type RepositoryOption func(*Repository)

func WithRepositoryClock(now func() time.Time) RepositoryOption {
	return func(r *Repository) {
		if now != nil {
			r.now = now
		}
	}
}

func WithRepositoryFaults(faults Faults) RepositoryOption {
	return func(r *Repository) { r.faults = faults }
}

func WithCommitListener(listener func(CommitEvent) error) RepositoryOption {
	return func(r *Repository) {
		if listener != nil {
			r.listeners = append(r.listeners, listener)
		}
	}
}

// CommitEvent is published only after the corresponding transaction commits.
type CommitEvent struct {
	Operation string
	Kind      string
	ID        string
}

// Repository is a concrete local repository. It is deliberately not wired to
// the current control server until the later clean-break cutover.
type Repository struct {
	db        *DB
	now       func() time.Time
	faults    Faults
	listeners []func(CommitEvent) error
	mu        sync.RWMutex
}

// OpenRepository opens the configured engine, initializes schema v1, and
// leaves the current server's JSON/envelope store untouched.
func OpenRepository(path string, key []byte, options ...RepositoryOption) (*Repository, error) {
	db, err := Open(path, key)
	if err != nil {
		return nil, err
	}
	r, err := NewRepository(db, options...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return r, nil
}

// NewRepository initializes schema v1 on an already opened engine.
func NewRepository(db *DB, options ...RepositoryOption) (*Repository, error) {
	r := &Repository{db: db, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(r)
		}
	}
	if err := EnsureSchema(context.Background(), db); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Repository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *Repository) DB() *DB {
	if r == nil {
		return nil
	}
	return r.db
}

// MutationTx exposes only guarded transaction operations. All writes use the
// driver's BEGIN IMMEDIATE mode configured by the engine DSN.
type MutationTx struct {
	tx   *sql.Tx
	repo *Repository
	op   string
}

func (m *MutationTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := m.statement(); err != nil {
		return nil, err
	}
	return m.tx.ExecContext(ctx, query, args...)
}

func (m *MutationTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if err := m.statement(); err != nil {
		return nil, err
	}
	return m.tx.QueryContext(ctx, query, args...)
}

func (m *MutationTx) QueryRowContext(ctx context.Context, query string, args ...any) (*sql.Row, error) {
	if err := m.statement(); err != nil {
		return nil, err
	}
	return m.tx.QueryRowContext(ctx, query, args...), nil
}

func (m *MutationTx) statement() error {
	if m.repo.faults.Statement != nil {
		if err := m.repo.faults.Statement(m.op); err != nil {
			return fmt.Errorf("%w: %v", ErrInjectedStatement, err)
		}
	}
	return nil
}

func (r *Repository) mutate(ctx context.Context, operation string, event CommitEvent, fn func(*MutationTx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	m := &MutationTx{tx: tx, repo: r, op: operation}
	if err := fn(m); err != nil {
		_ = tx.Rollback()
		return mapRepositoryError(err)
	}
	if r.faults.Commit != nil {
		if err := r.faults.Commit(operation); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("%w: %v", ErrInjectedCommit, err)
		}
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return storageError(err)
	}
	if event.Kind != "" {
		return r.notify(event)
	}
	return nil
}

func (r *Repository) notify(event CommitEvent) error {
	r.mu.RLock()
	listeners := append([]func(CommitEvent) error(nil), r.listeners...)
	r.mu.RUnlock()
	for _, listener := range listeners {
		if err := listener(event); err != nil {
			return fmt.Errorf("sqlite repository: post-commit listener: %w", err)
		}
	}
	return nil
}

// ServerIdentity is the persisted stable server identity and host-key pin.
type ServerIdentity struct {
	ID                    string
	SSHHostKeyAlgorithm   string
	SSHHostKeyFingerprint string
	CreatedAt             time.Time
}

func (r *Repository) SetServerIdentity(ctx context.Context, identity ServerIdentity) error {
	if err := validateID(identity.ID); err != nil {
		return err
	}
	if identity.SSHHostKeyAlgorithm == "" || identity.SSHHostKeyFingerprint == "" {
		return fmt.Errorf("%w: server host identity", ErrInvalid)
	}
	ns := identity.CreatedAt.UnixNano()
	if ns == 0 {
		ns = r.now().UnixNano()
	}
	return r.mutate(ctx, "server-identity", CommitEvent{Operation: "server-identity", Kind: "server", ID: identity.ID}, func(m *MutationTx) error {
		_, err := m.ExecContext(ctx, `INSERT INTO server_identity(singleton,id,ssh_host_key_algorithm,ssh_host_key_fingerprint,created_at_ns)
VALUES(1,?,?,?,?)
ON CONFLICT(singleton) DO UPDATE SET id=excluded.id, ssh_host_key_algorithm=excluded.ssh_host_key_algorithm,
ssh_host_key_fingerprint=excluded.ssh_host_key_fingerprint`, identity.ID, identity.SSHHostKeyAlgorithm, identity.SSHHostKeyFingerprint, ns)
		return err
	})
}

func (r *Repository) ServerIdentity(ctx context.Context) (ServerIdentity, error) {
	var identity ServerIdentity
	var ns int64
	err := r.db.QueryRowContext(ctx, `SELECT id,ssh_host_key_algorithm,ssh_host_key_fingerprint,created_at_ns
FROM server_identity WHERE singleton=1`).Scan(&identity.ID, &identity.SSHHostKeyAlgorithm, &identity.SSHHostKeyFingerprint, &ns)
	if errors.Is(err, sql.ErrNoRows) {
		return ServerIdentity{}, ErrNotFound
	}
	if err != nil {
		return ServerIdentity{}, storageError(err)
	}
	identity.CreatedAt = time.Unix(0, ns)
	return identity, nil
}

// RecoverySalt returns only the non-secret salt needed to verify caller-held
// recovery material. The stored verifier is never returned.
func (r *Repository) RecoverySalt(ctx context.Context, authorID string) ([]byte, error) {
	if err := validateID(authorID); err != nil {
		return nil, err
	}
	var salt []byte
	err := r.db.QueryRowContext(ctx, `SELECT salt FROM author_recovery WHERE author_id=?`, authorID).Scan(&salt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, storageError(err)
	}
	return append([]byte(nil), salt...), nil
}

// EnrollmentSalt returns only the non-secret salt for an unconsumed token.
func (r *Repository) EnrollmentSalt(ctx context.Context, tokenID string) ([]byte, error) {
	if err := validateID(tokenID); err != nil {
		return nil, err
	}
	var salt []byte
	err := r.db.QueryRowContext(ctx, `SELECT salt FROM device_enrollment_tokens WHERE id=?`, tokenID).Scan(&salt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, storageError(err)
	}
	return append([]byte(nil), salt...), nil
}

// Author, Device, App, ArtifactMetadata, and Runnable are intentionally
// metadata-oriented DTOs. Runnable is the explicit exception that includes
// the validated artifact bytes.
type Author struct {
	ID, Handle string
	Suspended  bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Device struct {
	ID, AuthorID, Name, PublicKey, Fingerprint string
	CreatedAt                                  time.Time
	RevokedAt                                  *time.Time
}

type App struct {
	ID, AuthorID, Name, Kind, AccessMode string
	Suspended                            bool
	CreatedAt, UpdatedAt                 time.Time
}

type ArtifactMetadata struct {
	ID, Digest string
	SizeBytes  int64
	ABIVersion int
	CreatedAt  time.Time
}

type Runnable struct {
	App          App
	DeploymentID string
	Artifact     ArtifactMetadata
	WASM         []byte
}

type RegistrationInput struct {
	AuthorID, Handle                             string
	DeviceID, DeviceName, PublicKey, Fingerprint string
	RecoverySalt, RecoveryVerifier               []byte
	CreatedAt                                    time.Time
	Quota                                        *Quota
}

// RegisterAuthor atomically creates author, recovery, first device, and audit
// state. Recovery material is accepted only as verifier bytes and is never
// copied into the audit record.
func (r *Repository) RegisterAuthor(ctx context.Context, input RegistrationInput) (Author, Device, error) {
	if err := validateID(input.AuthorID); err != nil {
		return Author{}, Device{}, err
	}
	if err := validateHandle(input.Handle); err != nil {
		return Author{}, Device{}, err
	}
	for _, value := range [][]byte{input.RecoverySalt, input.RecoveryVerifier} {
		if len(value) == 0 || len(value) > 1024 {
			return Author{}, Device{}, fmt.Errorf("%w: recovery verifier", ErrInvalid)
		}
	}
	for _, value := range []string{input.DeviceID, input.DeviceName, input.PublicKey, input.Fingerprint} {
		if err := validateID(value); err != nil {
			return Author{}, Device{}, err
		}
	}
	ns := input.CreatedAt.UnixNano()
	if ns == 0 {
		ns = r.now().UnixNano()
	}
	author := Author{ID: input.AuthorID, Handle: input.Handle, CreatedAt: time.Unix(0, ns), UpdatedAt: time.Unix(0, ns)}
	device := Device{ID: input.DeviceID, AuthorID: input.AuthorID, Name: input.DeviceName, PublicKey: input.PublicKey, Fingerprint: input.Fingerprint, CreatedAt: time.Unix(0, ns)}
	err := r.mutate(ctx, "public-registration", CommitEvent{Operation: "public-registration", Kind: "author", ID: author.ID}, func(m *MutationTx) error {
		if err := ensureHandleAvailable(ctx, m, input.Handle, ns); err != nil {
			return err
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO authors(id,handle,suspended,created_at_ns,updated_at_ns) VALUES(?,?,0,?,?)`, author.ID, author.Handle, ns, ns); err != nil {
			return mapConstraint(err)
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO author_recovery(author_id,salt,verifier,generation,created_at_ns,rotated_at_ns) VALUES(?,?,?,1,?,?)`, author.ID, input.RecoverySalt, input.RecoveryVerifier, ns, ns); err != nil {
			return err
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO devices(id,author_id,name,public_key,fingerprint,created_at_ns) VALUES(?,?,?,?,?,?)`, device.ID, device.AuthorID, device.Name, device.PublicKey, device.Fingerprint, ns); err != nil {
			return mapConstraint(err)
		}
		if input.Quota != nil {
			q := input.Quota
			if q.AuthorID != "" && q.AuthorID != author.ID {
				return fmt.Errorf("%w: quota author", ErrInvalid)
			}
			if q.MaxApps < 0 || q.MaxDeploymentsPerApp < 0 || q.MaxSecretsPerApp < 0 || q.MaxSessions < 0 {
				return fmt.Errorf("%w: quota", ErrInvalid)
			}
			if _, err := m.ExecContext(ctx, `INSERT INTO author_quotas(author_id,max_apps,max_deployments_per_app,max_secrets_per_app,max_sessions) VALUES(?,?,?,?,?)`, author.ID, q.MaxApps, q.MaxDeploymentsPerApp, q.MaxSecretsPerApp, q.MaxSessions); err != nil {
				return mapConstraint(err)
			}
		}
		return m.audit(ctx, AuditInput{ID: "audit_" + input.AuthorID, ScopeAuthorID: author.ID, ActorKind: "public_registration", ActorAuthorID: author.ID, Action: "author.register", TargetKind: "author", TargetID: author.ID, ActorSnapshot: author.ID, TargetSnapshot: author.Handle})
	})
	return author, device, err
}

func ensureHandleAvailable(ctx context.Context, m *MutationTx, handle string, nowNS int64) error {
	var id string
	row, err := m.QueryRowContext(ctx, `SELECT id FROM authors WHERE handle=?`, handle)
	if err != nil {
		return err
	}
	if err := row.Scan(&id); err == nil {
		return ErrConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	row, err = m.QueryRowContext(ctx, `SELECT available_after_ns FROM author_handle_tombstones WHERE handle=?`, handle)
	if err != nil {
		return err
	}
	var available int64
	if err := row.Scan(&available); err == nil && available > nowNS {
		return ErrConflict
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if available <= nowNS {
		if _, err := m.ExecContext(ctx, `DELETE FROM author_handle_tombstones WHERE handle=?`, handle); err != nil {
			return err
		}
	}
	return nil
}

func (m *MutationTx) audit(ctx context.Context, input AuditInput) error {
	if err := validateID(input.ID); err != nil {
		return err
	}
	if input.ActorKind == "" || input.Action == "" || input.TargetKind == "" || input.TargetID == "" {
		return fmt.Errorf("%w: audit identity", ErrInvalid)
	}
	if len(input.ActorSnapshot) > 2048 || len(input.TargetSnapshot) > 2048 || len(input.DetailsJSON) > 8192 {
		return fmt.Errorf("%w: audit snapshot too large", ErrInvalid)
	}
	if input.DetailsJSON == "" {
		input.DetailsJSON = "{}"
	}
	if !json.Valid([]byte(input.DetailsJSON)) {
		return fmt.Errorf("%w: invalid audit details", ErrInvalid)
	}
	_, err := m.ExecContext(ctx, `INSERT INTO audit_events(id,scope_author_id,occurred_at_ns,actor_kind,actor_author_id,actor_device_id,action,target_kind,target_id,actor_snapshot,target_snapshot,details_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, input.ID, input.ScopeAuthorID, m.repo.now().UnixNano(), input.ActorKind, input.ActorAuthorID, input.ActorDeviceID, input.Action, input.TargetKind, input.TargetID, input.ActorSnapshot, input.TargetSnapshot, input.DetailsJSON)
	return mapConstraint(err)
}

type AuditInput struct {
	ID, ScopeAuthorID, ActorKind, ActorAuthorID, ActorDeviceID               string
	Action, TargetKind, TargetID, ActorSnapshot, TargetSnapshot, DetailsJSON string
}

type AuditEvent struct {
	ID, ScopeAuthorID, ActorKind, ActorAuthorID, ActorDeviceID               string
	Action, TargetKind, TargetID, ActorSnapshot, TargetSnapshot, DetailsJSON string
	OccurredAt                                                               time.Time
}

type AuditFilter struct {
	ScopeAuthorID string
	Action        string
	TargetKind    string
	Before        time.Time
	Limit         int
}

func (r *Repository) ListAudit(ctx context.Context, scopeAuthorID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query := `SELECT id,scope_author_id,actor_kind,actor_author_id,actor_device_id,action,target_kind,target_id,actor_snapshot,target_snapshot,details_json,occurred_at_ns
FROM audit_events WHERE scope_author_id=? ORDER BY occurred_at_ns DESC LIMIT ?`
	rows, err := r.db.QueryContext(ctx, query, scopeAuthorID, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var result []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var ns int64
		if err := rows.Scan(&event.ID, &event.ScopeAuthorID, &event.ActorKind, &event.ActorAuthorID, &event.ActorDeviceID, &event.Action, &event.TargetKind, &event.TargetID, &event.ActorSnapshot, &event.TargetSnapshot, &event.DetailsJSON, &ns); err != nil {
			return nil, storageError(err)
		}
		event.OccurredAt = time.Unix(0, ns)
		result = append(result, event)
	}
	return result, storageError(rows.Err())
}

// ListAuditFiltered keeps filtering in SQL so a caller cannot bypass the
// scope or accidentally truncate a large author's history in memory.
func (r *Repository) ListAuditFiltered(ctx context.Context, filter AuditFilter) ([]AuditEvent, error) {
	if filter.ScopeAuthorID != "" {
		if err := validateID(filter.ScopeAuthorID); err != nil {
			return nil, err
		}
	}
	if filter.Action != "" {
		if err := validateID(filter.Action); err != nil {
			return nil, err
		}
	}
	if filter.TargetKind != "" {
		if err := validateID(filter.TargetKind); err != nil {
			return nil, err
		}
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	query := `SELECT id,scope_author_id,actor_kind,actor_author_id,actor_device_id,action,target_kind,target_id,actor_snapshot,target_snapshot,details_json,occurred_at_ns FROM audit_events WHERE 1=1`
	args := make([]any, 0, 4)
	if filter.ScopeAuthorID != "" {
		query += ` AND scope_author_id=?`
		args = append(args, filter.ScopeAuthorID)
	}
	if filter.Action != "" {
		query += ` AND action=?`
		args = append(args, filter.Action)
	}
	if filter.TargetKind != "" {
		query += ` AND target_kind=?`
		args = append(args, filter.TargetKind)
	}
	if !filter.Before.IsZero() {
		query += ` AND occurred_at_ns<?`
		args = append(args, filter.Before.UnixNano())
	}
	query += ` ORDER BY occurred_at_ns DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var result []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var ns int64
		if err := rows.Scan(&event.ID, &event.ScopeAuthorID, &event.ActorKind, &event.ActorAuthorID, &event.ActorDeviceID, &event.Action, &event.TargetKind, &event.TargetID, &event.ActorSnapshot, &event.TargetSnapshot, &event.DetailsJSON, &ns); err != nil {
			return nil, storageError(err)
		}
		event.OccurredAt = time.Unix(0, ns)
		result = append(result, event)
	}
	return result, storageError(rows.Err())
}

// PruneAudit removes only audit rows. It deliberately does not invoke the
// broader repository garbage collector, so audit retention cannot remove
// sessions or unreferenced artifacts as a side effect.
func (r *Repository) PruneAudit(ctx context.Context, before time.Time) (int64, error) {
	var removed int64
	err := r.mutate(ctx, "audit-prune", CommitEvent{Operation: "audit-prune", Kind: "audit", ID: "repository"}, func(m *MutationTx) error {
		result, err := m.ExecContext(ctx, `DELETE FROM audit_events WHERE occurred_at_ns<?`, before.UnixNano())
		if err != nil {
			return err
		}
		removed, _ = result.RowsAffected()
		return nil
	})
	return removed, err
}

func validateID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("%w: identifier", ErrInvalid)
	}
	return nil
}

func validateHandle(value string) error {
	if err := validateID(value); err != nil {
		return err
	}
	if strings.ToLower(value) != value || strings.ContainsAny(value, " /\\") {
		return fmt.Errorf("%w: handle", ErrInvalid)
	}
	return nil
}

func mapConstraint(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unique constraint") {
		return ErrConflict
	}
	return storageError(err)
}

func storageError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrQuota) || errors.Is(err, ErrSuspended) || errors.Is(err, ErrInjectedStatement) || errors.Is(err, ErrInjectedCommit) {
		return err
	}
	return fmt.Errorf("sqlite repository: storage failure: %w", err)
}

func mapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrQuota) || errors.Is(err, ErrSuspended) || errors.Is(err, ErrInjectedStatement) || errors.Is(err, ErrInjectedCommit) {
		return err
	}
	return mapConstraint(err)
}

func (r *Repository) PutArtifact(ctx context.Context, input ArtifactInput) (ArtifactMetadata, error) {
	metadata, err := validateArtifact(input)
	if err != nil {
		return ArtifactMetadata{}, err
	}
	err = r.mutate(ctx, "artifact-put", CommitEvent{Operation: "artifact-put", Kind: "artifact", ID: metadata.ID}, func(m *MutationTx) error {
		var storedSize int64
		var storedWASM []byte
		row, err := m.QueryRowContext(ctx, `SELECT size_bytes,wasm FROM artifact_blobs WHERE digest=?`, metadata.Digest)
		if err != nil {
			return err
		}
		scanErr := row.Scan(&storedSize)
		if scanErr == nil {
			if storedSize != metadata.SizeBytes || !bytes.Equal(storedWASM, input.WASM) {
				return ErrConflict
			}
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		} else if _, err := m.ExecContext(ctx, `INSERT INTO artifact_blobs(digest,size_bytes,wasm) VALUES(?,?,?)`, metadata.Digest, metadata.SizeBytes, input.WASM); err != nil {
			return mapConstraint(err)
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO artifacts(id,digest,size_bytes,abi_version,created_at_ns) VALUES(?,?,?,?,?)`, metadata.ID, metadata.Digest, metadata.SizeBytes, metadata.ABIVersion, metadata.CreatedAt.UnixNano()); err != nil {
			return mapConstraint(err)
		}
		for key, value := range input.BuildMetadata {
			if err := validateID(key); err != nil || len(value) > 2048 {
				return fmt.Errorf("%w: build metadata", ErrInvalid)
			}
			if _, err := m.ExecContext(ctx, `INSERT INTO artifact_build_metadata(artifact_id,key,value) VALUES(?,?,?)`, metadata.ID, key, value); err != nil {
				return err
			}
		}
		return m.audit(ctx, AuditInput{ID: "audit_" + metadata.ID, ActorKind: "system", Action: "artifact.put", TargetKind: "artifact", TargetID: metadata.ID, ActorSnapshot: "system", TargetSnapshot: metadata.Digest})
	})
	return metadata, err
}

type ArtifactInput struct {
	ID, Digest    string
	WASM          []byte
	ABIVersion    int
	CreatedAt     time.Time
	BuildMetadata map[string]string
}

func validateArtifact(input ArtifactInput) (ArtifactMetadata, error) {
	if err := validateID(input.ID); err != nil || len(input.WASM) > 64<<20 {
		return ArtifactMetadata{}, fmt.Errorf("%w: artifact", ErrInvalid)
	}
	digest := sha256.Sum256(input.WASM)
	expected := "sha256:" + hex.EncodeToString(digest[:])
	if input.Digest != expected || input.ABIVersion < 0 || input.ABIVersion > 255 {
		return ArtifactMetadata{}, fmt.Errorf("%w: artifact digest or ABI", ErrInvalid)
	}
	created := input.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	return ArtifactMetadata{ID: input.ID, Digest: input.Digest, SizeBytes: int64(len(input.WASM)), ABIVersion: input.ABIVersion, CreatedAt: created}, nil
}

func (r *Repository) ListArtifactMetadata(ctx context.Context, limit int) ([]ArtifactMetadata, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,digest,size_bytes,abi_version,created_at_ns FROM artifacts ORDER BY created_at_ns DESC LIMIT ?`, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var result []ArtifactMetadata
	for rows.Next() {
		var artifact ArtifactMetadata
		var ns int64
		if err := rows.Scan(&artifact.ID, &artifact.Digest, &artifact.SizeBytes, &artifact.ABIVersion, &ns); err != nil {
			return nil, storageError(err)
		}
		artifact.CreatedAt = time.Unix(0, ns)
		result = append(result, artifact)
	}
	return result, storageError(rows.Err())
}

func (r *Repository) ResolveRunnable(ctx context.Context, authorID, appName string) (Runnable, error) {
	if err := validateID(authorID); err != nil {
		return Runnable{}, err
	}
	var result Runnable
	var appSuspended, deploymentSuspended int
	var createdNS, updatedNS, artifactCreatedNS int64
	err := r.db.QueryRowContext(ctx, `SELECT a.id,a.author_id,a.name,a.kind,a.access_mode,a.suspended,a.created_at_ns,a.updated_at_ns,
 d.id,ar.id,ar.digest,ar.size_bytes,ar.abi_version,ar.created_at_ns,
 CASE WHEN sd.deployment_id IS NULL THEN 0 ELSE 1 END
FROM apps a JOIN app_active_deployments ad ON ad.app_id=a.id
JOIN app_deployments d ON d.id=ad.deployment_id AND d.app_id=a.id
JOIN artifacts ar ON ar.id=d.artifact_id
LEFT JOIN suspended_deployments sd ON sd.deployment_id=d.id
WHERE a.author_id=? AND a.name=?`, authorID, appName).Scan(
		&result.App.ID, &result.App.AuthorID, &result.App.Name, &result.App.Kind, &result.App.AccessMode, &appSuspended, &createdNS, &updatedNS,
		&result.DeploymentID, &result.Artifact.ID, &result.Artifact.Digest, &result.Artifact.SizeBytes, &result.Artifact.ABIVersion, &artifactCreatedNS, &deploymentSuspended)
	if errors.Is(err, sql.ErrNoRows) {
		return Runnable{}, ErrNotFound
	}
	if err != nil {
		return Runnable{}, storageError(err)
	}
	result.App.Suspended = appSuspended != 0
	result.App.CreatedAt = time.Unix(0, createdNS)
	result.App.UpdatedAt = time.Unix(0, updatedNS)
	result.Artifact.CreatedAt = time.Unix(0, artifactCreatedNS)
	if result.App.Suspended || deploymentSuspended != 0 {
		return Runnable{}, ErrSuspended
	}
	if err := r.db.QueryRowContext(ctx, `SELECT wasm FROM artifact_blobs WHERE digest=?`, result.Artifact.Digest).Scan(&result.WASM); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Runnable{}, ErrNotFound
		}
		return Runnable{}, storageError(err)
	}
	if int64(len(result.WASM)) != result.Artifact.SizeBytes || !VerifyDigestBytes(result.Artifact.Digest, result.WASM) {
		return Runnable{}, fmt.Errorf("sqlite repository: artifact integrity failure")
	}
	return result, nil
}

// VerifyDigestBytes is used by deployment callers before entering a write
// transaction and by tests to prove that deduplicated bytes cannot change.
func VerifyDigestBytes(digest string, wasm []byte) bool {
	hash := sha256.Sum256(wasm)
	return subtle.ConstantTimeCompare([]byte(digest), []byte("sha256:"+hex.EncodeToString(hash[:]))) == 1
}

func jsonDetails(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil || len(b) > 8192 {
		return "", fmt.Errorf("%w: details", ErrInvalid)
	}
	return string(b), nil
}

func equalBytes(a, b []byte) bool { return bytes.Equal(a, b) }
