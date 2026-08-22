package sqlite

import (
	"context"
	"errors"
	"fmt"
)

const schemaVersion = 1

// schemaStatements is the clean-break v1 state schema. It is deliberately
// kept as ordered statements so initialization can roll back as one unit and
// tests can inspect the exact table/index/trigger inventory.
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS server_identity (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  id TEXT NOT NULL UNIQUE,
  ssh_host_key_algorithm TEXT NOT NULL,
  ssh_host_key_fingerprint TEXT NOT NULL,
  created_at_ns INTEGER NOT NULL
) STRICT`,
	`CREATE TABLE IF NOT EXISTS authors (
  id TEXT PRIMARY KEY,
  handle TEXT NOT NULL UNIQUE,
  suspended INTEGER NOT NULL CHECK (suspended IN (0,1)),
  created_at_ns INTEGER NOT NULL,
  updated_at_ns INTEGER NOT NULL
) STRICT`,
	`CREATE TABLE IF NOT EXISTS author_handle_tombstones (
  handle TEXT PRIMARY KEY,
  former_author_id TEXT NOT NULL,
  reason TEXT NOT NULL CHECK (reason IN ('renamed','removed')),
  reserved_at_ns INTEGER NOT NULL,
  available_after_ns INTEGER NOT NULL CHECK (available_after_ns >= reserved_at_ns)
) STRICT`,
	`CREATE INDEX IF NOT EXISTS author_handle_tombstones_expiry_idx
 ON author_handle_tombstones(available_after_ns)`,
	`CREATE TABLE IF NOT EXISTS author_recovery (
  author_id TEXT PRIMARY KEY REFERENCES authors(id) ON DELETE CASCADE,
  salt BLOB NOT NULL,
  verifier BLOB NOT NULL,
  generation INTEGER NOT NULL CHECK (generation >= 1),
  created_at_ns INTEGER NOT NULL,
  rotated_at_ns INTEGER NOT NULL
) STRICT`,
	`CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY,
  author_id TEXT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  public_key TEXT NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  created_at_ns INTEGER NOT NULL,
  last_used_at_ns INTEGER,
  revoked_at_ns INTEGER,
  revoked_by_kind TEXT CHECK (revoked_by_kind IN ('device','recovery','operator')),
  revoked_by_id TEXT,
  UNIQUE (author_id, name),
  CHECK ((revoked_at_ns IS NULL AND revoked_by_kind IS NULL AND revoked_by_id IS NULL)
      OR (revoked_at_ns IS NOT NULL AND revoked_by_kind IS NOT NULL))
) STRICT`,
	`CREATE INDEX IF NOT EXISTS devices_author_idx ON devices(author_id, id)`,
	`CREATE INDEX IF NOT EXISTS devices_active_fingerprint_idx
 ON devices(fingerprint) WHERE revoked_at_ns IS NULL`,
	`CREATE TABLE IF NOT EXISTS device_enrollment_tokens (
  id TEXT PRIMARY KEY,
  author_id TEXT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
  purpose TEXT NOT NULL CHECK (purpose IN ('add_device','operator_recovery')),
  issued_by_kind TEXT NOT NULL CHECK (issued_by_kind IN ('device','operator')),
  issued_by_device_id TEXT,
  intended_device_name TEXT NOT NULL,
  salt BLOB NOT NULL,
  verifier BLOB NOT NULL,
  failed_attempts INTEGER NOT NULL CHECK (failed_attempts BETWEEN 0 AND 5),
  created_at_ns INTEGER NOT NULL,
  expires_at_ns INTEGER NOT NULL CHECK (expires_at_ns > created_at_ns),
  consumed_at_ns INTEGER,
  CHECK ((issued_by_kind='device' AND issued_by_device_id IS NOT NULL)
      OR (issued_by_kind='operator' AND issued_by_device_id IS NULL))
) STRICT`,
	`CREATE INDEX IF NOT EXISTS device_enrollment_tokens_active_idx
 ON device_enrollment_tokens(author_id, expires_at_ns)
 WHERE consumed_at_ns IS NULL`,
	`CREATE TABLE IF NOT EXISTS bootstrap_pairing_authorities (
  id TEXT PRIMARY KEY,
  handle TEXT NOT NULL,
  intended_device_name TEXT NOT NULL,
  salt BLOB NOT NULL,
  verifier BLOB NOT NULL,
  failed_attempts INTEGER NOT NULL CHECK (failed_attempts BETWEEN 0 AND 5),
  created_at_ns INTEGER NOT NULL,
  expires_at_ns INTEGER NOT NULL CHECK (expires_at_ns > created_at_ns),
  consumed_at_ns INTEGER
) STRICT`,
	`CREATE INDEX IF NOT EXISTS bootstrap_pairing_authorities_active_idx
 ON bootstrap_pairing_authorities(expires_at_ns)
 WHERE consumed_at_ns IS NULL`,
	`CREATE TABLE IF NOT EXISTS apps (
  id TEXT PRIMARY KEY,
  author_id TEXT NOT NULL REFERENCES authors(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('tui','cli')),
  access_mode TEXT NOT NULL CHECK (access_mode IN ('public','restricted')),
  suspended INTEGER NOT NULL CHECK (suspended IN (0,1)),
  created_at_ns INTEGER NOT NULL,
  updated_at_ns INTEGER NOT NULL,
  UNIQUE (author_id, name),
  UNIQUE (id, author_id)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS artifact_blobs (
  digest TEXT PRIMARY KEY,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  wasm BLOB NOT NULL CHECK (length(wasm) = size_bytes),
  CHECK (length(digest)=71 AND substr(digest,1,7)='sha256:'
     AND substr(digest,8) NOT GLOB '*[^0-9a-f]*')
) STRICT`,
	`CREATE TABLE IF NOT EXISTS artifacts (
  id TEXT PRIMARY KEY,
  digest TEXT NOT NULL,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  abi_version INTEGER NOT NULL CHECK (abi_version BETWEEN 0 AND 255),
  created_at_ns INTEGER NOT NULL,
  CHECK (length(digest)=71 AND substr(digest,1,7)='sha256:'
     AND substr(digest,8) NOT GLOB '*[^0-9a-f]*')
) STRICT`,
	`CREATE INDEX IF NOT EXISTS artifacts_digest_idx ON artifacts(digest)`,
	`CREATE TRIGGER IF NOT EXISTS artifact_blob_size_insert
 BEFORE INSERT ON artifact_blobs
 WHEN EXISTS (SELECT 1 FROM artifacts WHERE digest=NEW.digest AND size_bytes<>NEW.size_bytes)
 BEGIN SELECT RAISE(ABORT, 'artifact blob size mismatch'); END`,
	`CREATE TRIGGER IF NOT EXISTS artifact_size_insert
 BEFORE INSERT ON artifacts
 WHEN EXISTS (SELECT 1 FROM artifact_blobs WHERE digest=NEW.digest AND size_bytes<>NEW.size_bytes)
 BEGIN SELECT RAISE(ABORT, 'artifact blob size mismatch'); END`,
	`CREATE TRIGGER IF NOT EXISTS artifact_size_update
 BEFORE UPDATE OF digest,size_bytes ON artifacts
 WHEN EXISTS (SELECT 1 FROM artifact_blobs WHERE digest=NEW.digest AND size_bytes<>NEW.size_bytes)
 BEGIN SELECT RAISE(ABORT, 'artifact blob size mismatch'); END`,
	`CREATE TABLE IF NOT EXISTS artifact_build_metadata (
  artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (artifact_id, key)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS app_access_keys (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  public_key TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  added_by_device_id TEXT NOT NULL REFERENCES devices(id),
  created_at_ns INTEGER NOT NULL,
  UNIQUE (app_id, name),
  UNIQUE (app_id, fingerprint)
) STRICT`,
	`CREATE INDEX IF NOT EXISTS app_access_keys_fingerprint_idx
 ON app_access_keys(app_id, fingerprint)`,
	`CREATE TABLE IF NOT EXISTS app_deployments (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  artifact_id TEXT NOT NULL REFERENCES artifacts(id),
  deployed_by_device_id TEXT NOT NULL REFERENCES devices(id),
  created_at_ns INTEGER NOT NULL,
  UNIQUE (id, app_id)
) STRICT`,
	`CREATE INDEX IF NOT EXISTS app_deployments_app_created_idx
 ON app_deployments(app_id, created_at_ns DESC)`,
	`CREATE INDEX IF NOT EXISTS app_deployments_artifact_idx
 ON app_deployments(artifact_id)`,
	`CREATE TABLE IF NOT EXISTS app_active_deployments (
  app_id TEXT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
  deployment_id TEXT NOT NULL UNIQUE,
  FOREIGN KEY (deployment_id, app_id) REFERENCES app_deployments(id, app_id)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS app_capability_config (
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  capability TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (app_id, capability, key)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS app_capability_values (
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  capability TEXT NOT NULL,
  key TEXT NOT NULL,
  value BLOB NOT NULL,
  updated_at_ns INTEGER NOT NULL,
  PRIMARY KEY (app_id, capability, key)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS secrets (
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  version INTEGER NOT NULL CHECK (version >= 1),
  created_at_ns INTEGER NOT NULL,
  updated_at_ns INTEGER NOT NULL,
  PRIMARY KEY (app_id, key)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS secret_values (
  app_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value BLOB NOT NULL,
  PRIMARY KEY (app_id, key),
  FOREIGN KEY (app_id, key) REFERENCES secrets(app_id, key) ON DELETE CASCADE
) STRICT`,
	`CREATE TABLE IF NOT EXISTS app_egress_allow (
  app_id TEXT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  host TEXT NOT NULL,
  PRIMARY KEY (app_id, host)
) STRICT`,
	`CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  app_id TEXT NOT NULL,
  deployment_id TEXT NOT NULL,
  artifact_digest TEXT NOT NULL,
  started_at_ns INTEGER NOT NULL,
  ended_at_ns INTEGER,
  log TEXT NOT NULL,
  log_truncated INTEGER NOT NULL CHECK (log_truncated IN (0,1)),
  leaf_identity_summary TEXT NOT NULL
) STRICT`,
	`CREATE INDEX IF NOT EXISTS sessions_app_started_idx
 ON sessions(app_id, started_at_ns DESC)`,
	`CREATE INDEX IF NOT EXISTS sessions_open_app_idx
 ON sessions(app_id) WHERE ended_at_ns IS NULL`,
	`CREATE TABLE IF NOT EXISTS author_quotas (
  author_id TEXT PRIMARY KEY REFERENCES authors(id) ON DELETE CASCADE,
  max_apps INTEGER NOT NULL,
  max_deployments_per_app INTEGER NOT NULL,
  max_secrets_per_app INTEGER NOT NULL,
  max_sessions INTEGER NOT NULL
) STRICT`,
	`CREATE TABLE IF NOT EXISTS suspended_deployments (
  deployment_id TEXT PRIMARY KEY REFERENCES app_deployments(id) ON DELETE CASCADE
) STRICT`,
	`CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  scope_author_id TEXT,
  occurred_at_ns INTEGER NOT NULL,
  actor_kind TEXT NOT NULL CHECK (actor_kind IN
    ('public_registration','device','recovery','operator','system')),
  actor_author_id TEXT,
  actor_device_id TEXT,
  action TEXT NOT NULL,
  target_kind TEXT NOT NULL,
  target_id TEXT NOT NULL,
  actor_snapshot TEXT NOT NULL,
  target_snapshot TEXT NOT NULL,
  details_json TEXT NOT NULL
) STRICT`,
	`CREATE INDEX IF NOT EXISTS audit_events_author_time_idx
 ON audit_events(scope_author_id, occurred_at_ns DESC)`,
	`CREATE INDEX IF NOT EXISTS audit_events_expiry_idx
 ON audit_events(occurred_at_ns)`,
	`PRAGMA user_version = 1`,
}

// EnsureSchema creates the selected repository schema.
func EnsureSchema(ctx context.Context, db *DB) error {
	if db == nil || db.DB == nil {
		return errors.New("sqlite: nil database")
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("sqlite: enable foreign keys: %w", err)
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("sqlite: read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("sqlite: unsupported schema version %d", version)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin schema transaction: %w", err)
	}
	for _, statement := range schemaStatements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlite: initialize schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: commit schema: %w", err)
	}
	return nil
}

// SchemaVersion is the current clean-break repository schema version.
func SchemaVersion() int { return schemaVersion }

// SchemaStatements returns a copy for inspection and qualification tooling.
func SchemaStatements() []string { return append([]string(nil), schemaStatements...) }
