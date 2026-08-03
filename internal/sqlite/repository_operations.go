package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AppInput describes an app owned by an author.
type AppInput struct {
	ID, AuthorID, Name, Kind, AccessMode string
	CreatedAt                            time.Time
}

// AccessKey is an app-scoped public key. Private key material is never stored.
type AccessKey struct {
	ID, AppID, Name, PublicKey, Fingerprint, AddedByDeviceID string
	CreatedAt                                                time.Time
}

type AccessKeyInput struct {
	ID, AppID, Name, PublicKey, Fingerprint, AddedByDeviceID string
	CreatedAt                                                time.Time
}

type Deployment struct {
	ID, AppID, ArtifactID, DeployedByDeviceID string
	CreatedAt                                 time.Time
}

type DeploymentInput struct {
	ID, AppID, ArtifactID, DeployedByDeviceID string
	CreatedAt                                 time.Time
}

type CapabilityEntry struct {
	Capability, Key string
	Value           []byte
	UpdatedAt       time.Time
}

type SecretMetadata struct {
	AppID, Key string
	Version    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Session struct {
	ID, AppID, DeploymentID, ArtifactDigest, Log, LeafIdentitySummary string
	StartedAt                                                         time.Time
	EndedAt                                                           *time.Time
	LogTruncated                                                      bool
}

type Quota struct {
	AuthorID                                        string
	MaxApps, MaxDeploymentsPerApp, MaxSecretsPerApp int
	MaxSessions                                     int
}

type EnrollmentToken struct {
	ID, AuthorID, Purpose, IssuedByKind, IssuedByDeviceID, IntendedDeviceName string
	Salt, Verifier                                                            []byte
	FailedAttempts                                                            int
	CreatedAt, ExpiresAt                                                      time.Time
	ConsumedAt                                                                *time.Time
}

type EnrollmentTokenInput struct {
	ID, AuthorID, Purpose, IssuedByKind, IssuedByDeviceID, IntendedDeviceName string
	Salt, Verifier                                                            []byte
	ExpiresAt                                                                 time.Time
}

type GCResult struct {
	AuditEvents, Sessions, Artifacts, Blobs int64
}

func (r *Repository) CreateApp(ctx context.Context, input AppInput) (App, error) {
	if err := validateID(input.ID); err != nil {
		return App{}, err
	}
	if err := validateID(input.AuthorID); err != nil {
		return App{}, err
	}
	if err := validateID(input.Name); err != nil {
		return App{}, err
	}
	if input.Kind != "tui" && input.Kind != "cli" {
		return App{}, fmt.Errorf("%w: app kind", ErrInvalid)
	}
	if input.AccessMode != "public" && input.AccessMode != "restricted" {
		return App{}, fmt.Errorf("%w: app access mode", ErrInvalid)
	}
	ns := input.CreatedAt.UnixNano()
	if ns == 0 {
		ns = r.now().UnixNano()
	}
	app := App{ID: input.ID, AuthorID: input.AuthorID, Name: input.Name, Kind: input.Kind, AccessMode: input.AccessMode, CreatedAt: time.Unix(0, ns), UpdatedAt: time.Unix(0, ns)}
	err := r.mutate(ctx, "app-create", CommitEvent{Operation: "app-create", Kind: "app", ID: app.ID}, func(m *MutationTx) error {
		var author string
		row, rowErr := m.QueryRowContext(ctx, `SELECT id FROM authors WHERE id=?`, app.AuthorID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&author); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		var maxApps, appCount int
		quotaRow, quotaErr := m.QueryRowContext(ctx, `SELECT COALESCE((SELECT max_apps FROM author_quotas WHERE author_id=?),0)`, app.AuthorID)
		if quotaErr == nil {
			if err := quotaRow.Scan(&maxApps); err != nil {
				return err
			}
			if maxApps > 0 {
				countRow, countErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE author_id=?`, app.AuthorID)
				if countErr != nil {
					return countErr
				}
				if err := countRow.Scan(&appCount); err != nil {
					return err
				}
				if appCount >= maxApps {
					return ErrQuota
				}
			}
		} else {
			return quotaErr
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO apps(id,author_id,name,kind,access_mode,suspended,created_at_ns,updated_at_ns) VALUES(?,?,?,?,?,0,?,?)`, app.ID, app.AuthorID, app.Name, app.Kind, app.AccessMode, ns, ns); err != nil {
			return mapConstraint(err)
		}
		return m.audit(ctx, AuditInput{ID: "audit_" + app.ID, ScopeAuthorID: app.AuthorID, ActorKind: "device", ActorAuthorID: app.AuthorID, Action: "app.create", TargetKind: "app", TargetID: app.ID, ActorSnapshot: app.AuthorID, TargetSnapshot: app.Name})
	})
	return app, err
}

func (r *Repository) ListApps(ctx context.Context, authorID string) ([]App, error) {
	if err := validateID(authorID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,author_id,name,kind,access_mode,suspended,created_at_ns,updated_at_ns FROM apps WHERE author_id=? ORDER BY name`, authorID)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		var a App
		var suspended int
		var created, updated int64
		if err := rows.Scan(&a.ID, &a.AuthorID, &a.Name, &a.Kind, &a.AccessMode, &suspended, &created, &updated); err != nil {
			return nil, storageError(err)
		}
		a.Suspended = suspended != 0
		a.CreatedAt = time.Unix(0, created)
		a.UpdatedAt = time.Unix(0, updated)
		out = append(out, a)
	}
	return out, storageError(rows.Err())
}

func (r *Repository) AddAccessKey(ctx context.Context, input AccessKeyInput) (AccessKey, error) {
	for _, v := range []string{input.ID, input.AppID, input.Name, input.PublicKey, input.Fingerprint, input.AddedByDeviceID} {
		if err := validateID(v); err != nil {
			return AccessKey{}, err
		}
	}
	ns := input.CreatedAt.UnixNano()
	if ns == 0 {
		ns = r.now().UnixNano()
	}
	key := AccessKey{ID: input.ID, AppID: input.AppID, Name: input.Name, PublicKey: input.PublicKey, Fingerprint: input.Fingerprint, AddedByDeviceID: input.AddedByDeviceID, CreatedAt: time.Unix(0, ns)}
	err := r.mutate(ctx, "access-key-add", CommitEvent{Operation: "access-key-add", Kind: "access-key", ID: key.ID}, func(m *MutationTx) error {
		var ok int
		row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps a JOIN devices d ON d.author_id=a.author_id WHERE a.id=? AND d.id=? AND d.revoked_at_ns IS NULL`, key.AppID, key.AddedByDeviceID)
		if rowErr != nil {
			return rowErr
		}
		err := row.Scan(&ok)
		if err != nil {
			return err
		}
		if ok == 0 {
			return ErrNotFound
		}
		_, err = m.ExecContext(ctx, `INSERT INTO app_access_keys(id,app_id,name,public_key,fingerprint,added_by_device_id,created_at_ns) VALUES(?,?,?,?,?,?,?)`, key.ID, key.AppID, key.Name, key.PublicKey, key.Fingerprint, key.AddedByDeviceID, ns)
		return mapConstraint(err)
	})
	return key, err
}

func (r *Repository) CreateDeployment(ctx context.Context, input DeploymentInput) (Deployment, error) {
	for _, v := range []string{input.ID, input.AppID, input.ArtifactID, input.DeployedByDeviceID} {
		if err := validateID(v); err != nil {
			return Deployment{}, err
		}
	}
	ns := input.CreatedAt.UnixNano()
	if ns == 0 {
		ns = r.now().UnixNano()
	}
	d := Deployment{ID: input.ID, AppID: input.AppID, ArtifactID: input.ArtifactID, DeployedByDeviceID: input.DeployedByDeviceID, CreatedAt: time.Unix(0, ns)}
	err := r.mutate(ctx, "deployment-create", CommitEvent{Operation: "deployment-create", Kind: "deployment", ID: d.ID}, func(m *MutationTx) error {
		var n int
		row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps a JOIN devices dv ON dv.author_id=a.author_id JOIN artifacts ar ON ar.id=? WHERE a.id=? AND dv.id=? AND dv.revoked_at_ns IS NULL`, d.ArtifactID, d.AppID, d.DeployedByDeviceID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		var maxDeployments, deploymentCount int
		quotaRow, quotaErr := m.QueryRowContext(ctx, `SELECT COALESCE((SELECT q.max_deployments_per_app FROM author_quotas q JOIN apps a ON a.author_id=q.author_id WHERE a.id=?),0)`, d.AppID)
		if quotaErr == nil {
			if err := quotaRow.Scan(&maxDeployments); err != nil {
				return err
			}
			if maxDeployments > 0 {
				countRow, countErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_deployments WHERE app_id=?`, d.AppID)
				if countErr != nil {
					return countErr
				}
				if err := countRow.Scan(&deploymentCount); err != nil {
					return err
				}
				if deploymentCount >= maxDeployments {
					return ErrQuota
				}
			}
		} else {
			return quotaErr
		}
		_, err := m.ExecContext(ctx, `INSERT INTO app_deployments(id,app_id,artifact_id,deployed_by_device_id,created_at_ns) VALUES(?,?,?,?,?)`, d.ID, d.AppID, d.ArtifactID, d.DeployedByDeviceID, ns)
		return mapConstraint(err)
	})
	return d, err
}

func (r *Repository) ActivateDeployment(ctx context.Context, appID, deploymentID string) error {
	if err := validateID(appID); err != nil {
		return err
	}
	if err := validateID(deploymentID); err != nil {
		return err
	}
	return r.mutate(ctx, "deployment-activate", CommitEvent{Operation: "deployment-activate", Kind: "deployment", ID: deploymentID}, func(m *MutationTx) error {
		var n int
		row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_deployments WHERE id=? AND app_id=?`, deploymentID, appID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err := m.ExecContext(ctx, `INSERT INTO app_active_deployments(app_id,deployment_id) VALUES(?,?) ON CONFLICT(app_id) DO UPDATE SET deployment_id=excluded.deployment_id`, appID, deploymentID)
		return mapConstraint(err)
	})
}

func (r *Repository) SetDeploymentSuspended(ctx context.Context, deploymentID string, suspended bool) error {
	if err := validateID(deploymentID); err != nil {
		return err
	}
	return r.mutate(ctx, "deployment-suspension", CommitEvent{Operation: "deployment-suspension", Kind: "deployment", ID: deploymentID}, func(m *MutationTx) error {
		var n int
		row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_deployments WHERE id=?`, deploymentID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if suspended {
			_, err := m.ExecContext(ctx, `INSERT OR IGNORE INTO suspended_deployments(deployment_id) VALUES(?)`, deploymentID)
			return err
		}
		_, err := m.ExecContext(ctx, `DELETE FROM suspended_deployments WHERE deployment_id=?`, deploymentID)
		return err
	})
}

func (r *Repository) SetCapabilityConfig(ctx context.Context, appID, capability, key, value string) error {
	return r.setCapabilityConfig(ctx, appID, capability, key, value)
}
func (r *Repository) setCapabilityConfig(ctx context.Context, appID, capability, key, value string) error {
	for _, v := range []string{appID, capability, key} {
		if err := validateID(v); err != nil {
			return err
		}
	}
	if len(value) > 8192 {
		return fmt.Errorf("%w: capability config", ErrInvalid)
	}
	return r.mutate(ctx, "capability-config", CommitEvent{Operation: "capability-config", Kind: "app", ID: appID}, func(m *MutationTx) error {
		if err := appExists(ctx, m, appID); err != nil {
			return err
		}
		_, err := m.ExecContext(ctx, `INSERT INTO app_capability_config(app_id,capability,key,value) VALUES(?,?,?,?) ON CONFLICT(app_id,capability,key) DO UPDATE SET value=excluded.value`, appID, capability, key, value)
		return err
	})
}

func (r *Repository) SetCapabilityValue(ctx context.Context, appID, capability, key string, value []byte) error {
	for _, v := range []string{appID, capability, key} {
		if err := validateID(v); err != nil {
			return err
		}
	}
	if len(value) > 1<<20 {
		return fmt.Errorf("%w: capability value", ErrInvalid)
	}
	now := r.now().UnixNano()
	return r.mutate(ctx, "capability-value", CommitEvent{Operation: "capability-value", Kind: "app", ID: appID}, func(m *MutationTx) error {
		if err := appExists(ctx, m, appID); err != nil {
			return err
		}
		_, err := m.ExecContext(ctx, `INSERT INTO app_capability_values(app_id,capability,key,value,updated_at_ns) VALUES(?,?,?,?,?) ON CONFLICT(app_id,capability,key) DO UPDATE SET value=excluded.value,updated_at_ns=excluded.updated_at_ns`, appID, capability, key, value, now)
		return err
	})
}

func (r *Repository) CapabilityConfig(ctx context.Context, appID string) ([]CapabilityEntry, error) {
	return r.capabilityRows(ctx, `SELECT capability,key,value,'' FROM app_capability_config WHERE app_id=? ORDER BY capability,key`, appID)
}
func (r *Repository) CapabilityValues(ctx context.Context, appID string) ([]CapabilityEntry, error) {
	return r.capabilityRows(ctx, `SELECT capability,key,value,updated_at_ns FROM app_capability_values WHERE app_id=? ORDER BY capability,key`, appID)
}
func (r *Repository) capabilityRows(ctx context.Context, q string, appID string) ([]CapabilityEntry, error) {
	if err := validateID(appID); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, q, appID)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var out []CapabilityEntry
	for rows.Next() {
		var e CapabilityEntry
		var stamp string
		if err := rows.Scan(&e.Capability, &e.Key, &e.Value, &stamp); err != nil {
			return nil, storageError(err)
		}
		if stamp != "" {
			var ns int64
			_, _ = fmt.Sscan(stamp, &ns)
			e.UpdatedAt = time.Unix(0, ns)
		}
		out = append(out, e)
	}
	return out, storageError(rows.Err())
}

func (r *Repository) SetSecret(ctx context.Context, appID, key string, value []byte) (SecretMetadata, error) {
	if err := validateID(appID); err != nil {
		return SecretMetadata{}, err
	}
	if err := validateID(key); err != nil {
		return SecretMetadata{}, err
	}
	if len(value) > 1<<20 {
		return SecretMetadata{}, fmt.Errorf("%w: secret", ErrInvalid)
	}
	now := r.now().UnixNano()
	var out SecretMetadata
	err := r.mutate(ctx, "secret-set", CommitEvent{Operation: "secret-set", Kind: "secret", ID: appID + ":" + key}, func(m *MutationTx) error {
		if err := appExists(ctx, m, appID); err != nil {
			return err
		}
		var maxSecrets, secretCount int
		quotaRow, quotaErr := m.QueryRowContext(ctx, `SELECT COALESCE((SELECT q.max_secrets_per_app FROM author_quotas q JOIN apps a ON a.author_id=q.author_id WHERE a.id=?),0)`, appID)
		if quotaErr == nil {
			if err := quotaRow.Scan(&maxSecrets); err != nil {
				return err
			}
			if maxSecrets > 0 {
				countRow, countErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM secrets WHERE app_id=?`, appID)
				if countErr != nil {
					return countErr
				}
				if err := countRow.Scan(&secretCount); err != nil {
					return err
				}
				if secretCount >= maxSecrets {
					var existing int
					existingRow, existingErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM secrets WHERE app_id=? AND key=?`, appID, key)
					if existingErr != nil {
						return existingErr
					}
					if err := existingRow.Scan(&existing); err != nil {
						return err
					}
					if existing == 0 {
						return ErrQuota
					}
				}
			}
		} else {
			return quotaErr
		}
		var version int
		var createdAt int64
		row, rowErr := m.QueryRowContext(ctx, `SELECT version FROM secrets WHERE app_id=? AND key=?`, appID, key)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&version); errors.Is(err, sql.ErrNoRows) {
			version = 1
			createdAt = now
		} else if err != nil {
			return err
		} else {
			version++
			createdRow, createdErr := m.QueryRowContext(ctx, `SELECT created_at_ns FROM secrets WHERE app_id=? AND key=?`, appID, key)
			if createdErr != nil {
				return createdErr
			}
			if err := createdRow.Scan(&createdAt); err != nil {
				return err
			}
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO secrets(app_id,key,version,created_at_ns,updated_at_ns) VALUES(?,?,?,?,?) ON CONFLICT(app_id,key) DO UPDATE SET version=excluded.version,updated_at_ns=excluded.updated_at_ns`, appID, key, version, now, now); err != nil {
			return err
		}
		_, err := m.ExecContext(ctx, `INSERT INTO secret_values(app_id,key,value) VALUES(?,?,?) ON CONFLICT(app_id,key) DO UPDATE SET value=excluded.value`, appID, key, value)
		out = SecretMetadata{AppID: appID, Key: key, Version: version, CreatedAt: time.Unix(0, createdAt), UpdatedAt: time.Unix(0, now)}
		return err
	})
	return out, err
}

func (r *Repository) Secret(ctx context.Context, appID, key string) (SecretMetadata, []byte, error) {
	var s SecretMetadata
	var c, u int64
	var value []byte
	err := r.db.QueryRowContext(ctx, `SELECT s.version,s.created_at_ns,s.updated_at_ns,v.value FROM secrets s JOIN secret_values v ON v.app_id=s.app_id AND v.key=s.key WHERE s.app_id=? AND s.key=?`, appID, key).Scan(&s.Version, &c, &u, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return SecretMetadata{}, nil, ErrNotFound
	}
	if err != nil {
		return SecretMetadata{}, nil, storageError(err)
	}
	s.AppID = appID
	s.Key = key
	s.CreatedAt = time.Unix(0, c)
	s.UpdatedAt = time.Unix(0, u)
	return s, value, nil
}

func (r *Repository) SetEgressHost(ctx context.Context, appID, host string, allowed bool) error {
	if err := validateID(appID); err != nil {
		return err
	}
	if strings.TrimSpace(host) == "" || len(host) > 255 {
		return fmt.Errorf("%w: egress host", ErrInvalid)
	}
	return r.mutate(ctx, "egress-set", CommitEvent{Operation: "egress-set", Kind: "app", ID: appID}, func(m *MutationTx) error {
		if err := appExists(ctx, m, appID); err != nil {
			return err
		}
		if allowed {
			_, err := m.ExecContext(ctx, `INSERT OR IGNORE INTO app_egress_allow(app_id,host) VALUES(?,?)`, appID, host)
			return err
		}
		_, err := m.ExecContext(ctx, `DELETE FROM app_egress_allow WHERE app_id=? AND host=?`, appID, host)
		return err
	})
}

func (r *Repository) SetQuota(ctx context.Context, q Quota) error {
	if err := validateID(q.AuthorID); err != nil {
		return err
	}
	if q.MaxApps < 0 || q.MaxDeploymentsPerApp < 0 || q.MaxSecretsPerApp < 0 || q.MaxSessions < 0 {
		return fmt.Errorf("%w: quota", ErrInvalid)
	}
	return r.mutate(ctx, "quota-set", CommitEvent{Operation: "quota-set", Kind: "author", ID: q.AuthorID}, func(m *MutationTx) error {
		var n int
		row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM authors WHERE id=?`, q.AuthorID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err := m.ExecContext(ctx, `INSERT INTO author_quotas(author_id,max_apps,max_deployments_per_app,max_secrets_per_app,max_sessions) VALUES(?,?,?,?,?) ON CONFLICT(author_id) DO UPDATE SET max_apps=excluded.max_apps,max_deployments_per_app=excluded.max_deployments_per_app,max_secrets_per_app=excluded.max_secrets_per_app,max_sessions=excluded.max_sessions`, q.AuthorID, q.MaxApps, q.MaxDeploymentsPerApp, q.MaxSecretsPerApp, q.MaxSessions)
		return err
	})
}

func (r *Repository) StartSession(ctx context.Context, s Session) error {
	if err := validateID(s.ID); err != nil {
		return err
	}
	for _, v := range []string{s.AppID, s.DeploymentID, s.ArtifactDigest} {
		if err := validateID(v); err != nil {
			return err
		}
	}
	if len(s.LeafIdentitySummary) > 2048 {
		return fmt.Errorf("%w: session identity", ErrInvalid)
	}
	ns := s.StartedAt.UnixNano()
	if ns == 0 {
		ns = r.now().UnixNano()
	}
	return r.mutate(ctx, "session-start", CommitEvent{Operation: "session-start", Kind: "session", ID: s.ID}, func(m *MutationTx) error {
		var author string
		var suspended, deploymentSuspended int
		var deploymentDigest string
		row, rowErr := m.QueryRowContext(ctx, `SELECT a.author_id,a.suspended,CASE WHEN sd.deployment_id IS NULL THEN 0 ELSE 1 END,ar.digest FROM apps a JOIN app_deployments d ON d.app_id=a.id JOIN artifacts ar ON ar.id=d.artifact_id LEFT JOIN suspended_deployments sd ON sd.deployment_id=d.id WHERE a.id=? AND d.id=? AND EXISTS(SELECT 1 FROM app_active_deployments ad WHERE ad.app_id=a.id AND ad.deployment_id=d.id)`, s.AppID, s.DeploymentID)
		if rowErr != nil {
			return rowErr
		}
		err := row.Scan(&author, &suspended, &deploymentSuspended, &deploymentDigest)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if suspended != 0 || deploymentSuspended != 0 {
			return ErrSuspended
		}
		if deploymentDigest != s.ArtifactDigest {
			return ErrNotFound
		}
		var quota, open int
		row, rowErr = m.QueryRowContext(ctx, `SELECT COALESCE(max_sessions,0) FROM author_quotas WHERE author_id=?`, author)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&quota); errors.Is(err, sql.ErrNoRows) {
			quota = 0
		} else if err != nil {
			return err
		}
		if quota > 0 {
			row, rowErr = m.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE app_id=? AND ended_at_ns IS NULL`, s.AppID)
			if rowErr != nil {
				return rowErr
			}
			if err := row.Scan(&open); err != nil {
				return err
			}
			if open >= quota {
				return ErrQuota
			}
		}
		_, err = m.ExecContext(ctx, `INSERT INTO sessions(id,app_id,deployment_id,artifact_digest,started_at_ns,log,log_truncated,leaf_identity_summary) VALUES(?,?,?,?,?,?,?,?)`, s.ID, s.AppID, s.DeploymentID, s.ArtifactDigest, ns, s.Log, s.LogTruncated, s.LeafIdentitySummary)
		return mapConstraint(err)
	})
}

func (r *Repository) RecordSessionLog(ctx context.Context, sessionID, log string, truncated bool) error {
	if err := validateID(sessionID); err != nil {
		return err
	}
	if len(log) > 4<<20 {
		return fmt.Errorf("%w: session log", ErrInvalid)
	}
	return r.mutate(ctx, "session-log", CommitEvent{Operation: "session-log", Kind: "session", ID: sessionID}, func(m *MutationTx) error {
		res, err := m.ExecContext(ctx, `UPDATE sessions SET log=?,log_truncated=? WHERE id=? AND ended_at_ns IS NULL`, log, truncated, sessionID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (r *Repository) EndSession(ctx context.Context, sessionID string) (*time.Time, error) {
	if err := validateID(sessionID); err != nil {
		return nil, err
	}
	ended := r.now()
	err := r.mutate(ctx, "session-end", CommitEvent{Operation: "session-end", Kind: "session", ID: sessionID}, func(m *MutationTx) error {
		res, err := m.ExecContext(ctx, `UPDATE sessions SET ended_at_ns=? WHERE id=? AND ended_at_ns IS NULL`, ended.UnixNano(), sessionID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ended, nil
}

func (r *Repository) ListSessions(ctx context.Context, appID string, limit int) ([]Session, error) {
	if err := validateID(appID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,app_id,deployment_id,artifact_digest,started_at_ns,ended_at_ns,log,log_truncated,leaf_identity_summary FROM sessions WHERE app_id=? ORDER BY started_at_ns DESC LIMIT ?`, appID, limit)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var start int64
		var end sql.NullInt64
		var tr int
		if err := rows.Scan(&s.ID, &s.AppID, &s.DeploymentID, &s.ArtifactDigest, &start, &end, &s.Log, &tr, &s.LeafIdentitySummary); err != nil {
			return nil, storageError(err)
		}
		s.StartedAt = time.Unix(0, start)
		s.LogTruncated = tr != 0
		if end.Valid {
			v := time.Unix(0, end.Int64)
			s.EndedAt = &v
		}
		out = append(out, s)
	}
	return out, storageError(rows.Err())
}

func appExists(ctx context.Context, m *MutationTx, appID string) error {
	var n int
	row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE id=?`, appID)
	if rowErr != nil {
		return rowErr
	}
	if err := row.Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RenameAuthor reserves the former handle before changing the active handle.
// Both writes are in one immediate transaction, so handle reuse cannot race.
func (r *Repository) RenameAuthor(ctx context.Context, authorID, newHandle string, reserveUntil time.Time) error {
	if err := validateID(authorID); err != nil {
		return err
	}
	if err := validateHandle(newHandle); err != nil {
		return err
	}
	return r.mutate(ctx, "author-rename", CommitEvent{Operation: "author-rename", Kind: "author", ID: authorID}, func(m *MutationTx) error {
		var oldHandle string
		row, rowErr := m.QueryRowContext(ctx, `SELECT handle FROM authors WHERE id=?`, authorID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&oldHandle); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if oldHandle == newHandle {
			return nil
		}
		now := r.now().UnixNano()
		if err := ensureHandleAvailable(ctx, m, newHandle, now); err != nil {
			return err
		}
		available := reserveUntil.UnixNano()
		if available <= now {
			available = now + int64(24*time.Hour)
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO author_handle_tombstones(handle,former_author_id,reason,reserved_at_ns,available_after_ns) VALUES(?,?,?, ?,?)`, oldHandle, authorID, "renamed", now, available); err != nil {
			return mapConstraint(err)
		}
		_, err := m.ExecContext(ctx, `UPDATE authors SET handle=?,updated_at_ns=? WHERE id=?`, newHandle, now, authorID)
		return mapConstraint(err)
	})
}

func (r *Repository) RevokeDevice(ctx context.Context, deviceID, byKind, byID string) error {
	if err := validateID(deviceID); err != nil {
		return err
	}
	if byKind != "device" && byKind != "recovery" && byKind != "operator" {
		return fmt.Errorf("%w: revoke actor", ErrInvalid)
	}
	if byKind != "operator" {
		if err := validateID(byID); err != nil {
			return err
		}
	}
	now := r.now().UnixNano()
	return r.mutate(ctx, "device-revoke", CommitEvent{Operation: "device-revoke", Kind: "device", ID: deviceID}, func(m *MutationTx) error {
		res, err := m.ExecContext(ctx, `UPDATE devices SET revoked_at_ns=?,revoked_by_kind=?,revoked_by_id=? WHERE id=? AND revoked_at_ns IS NULL`, now, byKind, nullableID(byID), deviceID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (r *Repository) CreateEnrollmentToken(ctx context.Context, input EnrollmentTokenInput) (EnrollmentToken, error) {
	for _, v := range []string{input.ID, input.AuthorID, input.IntendedDeviceName} {
		if err := validateID(v); err != nil {
			return EnrollmentToken{}, err
		}
	}
	if input.Purpose != "add_device" && input.Purpose != "operator_recovery" {
		return EnrollmentToken{}, fmt.Errorf("%w: token purpose", ErrInvalid)
	}
	if input.IssuedByKind != "device" && input.IssuedByKind != "operator" {
		return EnrollmentToken{}, fmt.Errorf("%w: token issuer", ErrInvalid)
	}
	if input.IssuedByKind == "device" {
		if err := validateID(input.IssuedByDeviceID); err != nil {
			return EnrollmentToken{}, err
		}
	} else if input.IssuedByDeviceID != "" {
		return EnrollmentToken{}, fmt.Errorf("%w: operator token device", ErrInvalid)
	}
	if len(input.Salt) == 0 || len(input.Salt) > 1024 || len(input.Verifier) == 0 || len(input.Verifier) > 1024 {
		return EnrollmentToken{}, fmt.Errorf("%w: token verifier", ErrInvalid)
	}
	now := r.now()
	if input.ExpiresAt.IsZero() || !input.ExpiresAt.After(now) {
		return EnrollmentToken{}, fmt.Errorf("%w: token expiry", ErrInvalid)
	}
	t := EnrollmentToken{ID: input.ID, AuthorID: input.AuthorID, Purpose: input.Purpose, IssuedByKind: input.IssuedByKind, IssuedByDeviceID: input.IssuedByDeviceID, IntendedDeviceName: input.IntendedDeviceName, Salt: append([]byte(nil), input.Salt...), Verifier: append([]byte(nil), input.Verifier...), CreatedAt: now, ExpiresAt: input.ExpiresAt}
	err := r.mutate(ctx, "enrollment-token-create", CommitEvent{Operation: "enrollment-token-create", Kind: "token", ID: t.ID}, func(m *MutationTx) error {
		var n int
		row, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM authors WHERE id=?`, t.AuthorID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if t.IssuedByKind == "device" {
			row, rowErr = m.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id=? AND author_id=? AND revoked_at_ns IS NULL`, t.IssuedByDeviceID, t.AuthorID)
			if rowErr != nil {
				return rowErr
			}
			if err := row.Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
		}
		_, err := m.ExecContext(ctx, `INSERT INTO device_enrollment_tokens(id,author_id,purpose,issued_by_kind,issued_by_device_id,intended_device_name,salt,verifier,failed_attempts,created_at_ns,expires_at_ns) VALUES(?,?,?,?,?,?,?,?,0,?,?)`, t.ID, t.AuthorID, t.Purpose, t.IssuedByKind, nullableID(t.IssuedByDeviceID), t.IntendedDeviceName, t.Salt, t.Verifier, now.UnixNano(), t.ExpiresAt.UnixNano())
		return mapConstraint(err)
	})
	return t, err
}

// VerifyEnrollmentToken atomically consumes only a correct, unexpired token.
// Wrong proofs increment the bounded failure counter without revealing token
// existence to an unauthorised caller.
func (r *Repository) VerifyEnrollmentToken(ctx context.Context, tokenID string, verifier []byte) (EnrollmentToken, error) {
	if err := validateID(tokenID); err != nil {
		return EnrollmentToken{}, err
	}
	var out EnrollmentToken
	var verificationFailed bool
	now := r.now()
	err := r.mutate(ctx, "enrollment-token-verify", CommitEvent{}, func(m *MutationTx) error {
		var failed int
		var expires, created int64
		var consumed sql.NullInt64
		var stored []byte
		var salt []byte
		row, rowErr := m.QueryRowContext(ctx, `SELECT author_id,purpose,issued_by_kind,COALESCE(issued_by_device_id,''),intended_device_name,salt,verifier,failed_attempts,created_at_ns,expires_at_ns,consumed_at_ns FROM device_enrollment_tokens WHERE id=?`, tokenID)
		if rowErr != nil {
			return rowErr
		}
		if err := row.Scan(&out.AuthorID, &out.Purpose, &out.IssuedByKind, &out.IssuedByDeviceID, &out.IntendedDeviceName, &salt, &stored, &failed, &created, &expires, &consumed); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if consumed.Valid || expires <= now.UnixNano() || failed >= 5 {
			return ErrConflict
		}
		if !bytesEqual(stored, verifier) {
			failed++
			if _, err := m.ExecContext(ctx, `UPDATE device_enrollment_tokens SET failed_attempts=? WHERE id=?`, failed, tokenID); err != nil {
				return err
			}
			verificationFailed = true
			return nil
		}
		_, err := m.ExecContext(ctx, `UPDATE device_enrollment_tokens SET consumed_at_ns=? WHERE id=?`, now.UnixNano(), tokenID)
		if err != nil {
			return err
		}
		out.ID = tokenID
		out.Salt = append([]byte(nil), salt...)
		out.Verifier = append([]byte(nil), stored...)
		out.FailedAttempts = failed
		out.CreatedAt = time.Unix(0, created)
		out.ExpiresAt = time.Unix(0, expires)
		out.ConsumedAt = &now
		return nil
	})
	if err != nil {
		return EnrollmentToken{}, err
	}
	if verificationFailed {
		return EnrollmentToken{}, ErrConflict
	}
	return out, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var x byte
	for i := range a {
		x |= a[i] ^ b[i]
	}
	return x == 0
}

// GarbageCollect removes only unreferenced blobs/artifacts and expired
// operational history. The complete cleanup is one immediate transaction.
func (r *Repository) GarbageCollect(ctx context.Context, before time.Time) (GCResult, error) {
	var out GCResult
	cutoff := before.UnixNano()
	err := r.mutate(ctx, "garbage-collect", CommitEvent{Operation: "garbage-collect", Kind: "gc", ID: "repository"}, func(m *MutationTx) error {
		for query, dest := range map[string]*int64{`DELETE FROM audit_events WHERE occurred_at_ns < ?`: &out.AuditEvents, `DELETE FROM sessions WHERE ended_at_ns IS NOT NULL AND ended_at_ns < ?`: &out.Sessions, `DELETE FROM artifacts WHERE created_at_ns < ? AND NOT EXISTS(SELECT 1 FROM app_deployments d WHERE d.artifact_id=artifacts.id)`: &out.Artifacts, `DELETE FROM artifact_blobs WHERE NOT EXISTS(SELECT 1 FROM artifacts a WHERE a.digest=artifact_blobs.digest)`: &out.Blobs} {
			res, err := m.ExecContext(ctx, query, cutoff)
			if err != nil {
				return err
			}
			*dest, _ = res.RowsAffected()
		}
		_, err := m.ExecContext(ctx, `DELETE FROM author_handle_tombstones WHERE available_after_ns <= ?`, r.now().UnixNano())
		return err
	})
	return out, err
}
