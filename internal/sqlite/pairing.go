package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BootstrapAuthority is local operator authority for one first-author
// registration. Verifier is internal proof material and must not be printed.
type BootstrapAuthority struct {
	ID, Handle, DeviceName string
	Salt, Verifier         []byte
	FailedAttempts         int
	CreatedAt, ExpiresAt   time.Time
	ConsumedAt             *time.Time
}

type BootstrapAuthorityInput struct {
	ID, Handle, DeviceName string
	Salt, Verifier         []byte
	ExpiresAt              time.Time
}

type PairingCredential struct {
	AuthorID, Handle, DeviceName string
	Salt, Verifier               []byte
	Generation                   int
}

// EnrollmentCredential returns active add-device proof material to the
// bounded SSH pairing handler. It is never part of the control API response.
func (r *Repository) EnrollmentCredential(ctx context.Context, tokenID string) (PairingCredential, error) {
	if err := validateID(tokenID); err != nil {
		return PairingCredential{}, err
	}
	var out PairingCredential
	var purpose string
	var failed, expires int64
	var consumed sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT author_id,purpose,intended_device_name,salt,verifier,failed_attempts,expires_at_ns,consumed_at_ns FROM device_enrollment_tokens WHERE id=?`, tokenID).Scan(
		&out.AuthorID, &purpose, &out.DeviceName, &out.Salt, &out.Verifier, &failed, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingCredential{}, ErrNotFound
	}
	if err != nil {
		return PairingCredential{}, storageError(err)
	}
	if purpose != "add_device" || consumed.Valid || expires <= r.now().UnixNano() || failed >= 5 {
		return PairingCredential{}, ErrConflict
	}
	return clonePairingCredential(out), nil
}

// RecoveryCredential resolves an author handle to the current recovery proof
// material for the pairing handler. The material never leaves the server.
func (r *Repository) RecoveryCredential(ctx context.Context, handle string) (PairingCredential, error) {
	if err := validateHandle(handle); err != nil {
		return PairingCredential{}, err
	}
	var out PairingCredential
	err := r.db.QueryRowContext(ctx, `SELECT a.id,a.handle,r.salt,r.verifier,r.generation FROM authors a JOIN author_recovery r ON r.author_id=a.id WHERE a.handle=? AND a.suspended=0`, handle).Scan(
		&out.AuthorID, &out.Handle, &out.Salt, &out.Verifier, &out.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return PairingCredential{}, ErrNotFound
	}
	if err != nil {
		return PairingCredential{}, storageError(err)
	}
	return clonePairingCredential(out), nil
}

func clonePairingCredential(value PairingCredential) PairingCredential {
	value.Salt = append([]byte(nil), value.Salt...)
	value.Verifier = append([]byte(nil), value.Verifier...)
	return value
}

type RecoveryInput struct {
	AuthorID, DeviceID, DeviceName, PublicKey, Fingerprint string
	CurrentVerifier, NextSalt, NextVerifier                []byte
	RevokeOldDevices                                       bool
}

// CompleteRecovery enrolls the replacement key, rotates recovery material,
// invalidates invitations, and optionally revokes old devices in one write.
func (r *Repository) CompleteRecovery(ctx context.Context, input RecoveryInput) (Device, error) {
	for _, value := range []string{input.AuthorID, input.DeviceID, input.DeviceName, input.PublicKey, input.Fingerprint} {
		if err := validateID(value); err != nil {
			return Device{}, err
		}
	}
	if len(input.CurrentVerifier) == 0 || len(input.NextSalt) == 0 || len(input.NextVerifier) == 0 {
		return Device{}, fmt.Errorf("%w: recovery verifier", ErrInvalid)
	}
	now := r.now()
	device := Device{ID: input.DeviceID, AuthorID: input.AuthorID, Name: input.DeviceName, PublicKey: input.PublicKey, Fingerprint: input.Fingerprint, CreatedAt: now}
	err := r.mutate(ctx, "author-recovery", CommitEvent{Operation: "author-recovery", Kind: "author", ID: input.AuthorID}, func(m *MutationTx) error {
		var stored []byte
		var generation int
		row, err := m.QueryRowContext(ctx, `SELECT r.verifier,r.generation FROM author_recovery r JOIN authors a ON a.id=r.author_id WHERE r.author_id=? AND a.suspended=0`, input.AuthorID)
		if err != nil {
			return err
		}
		if err := row.Scan(&stored, &generation); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if !bytesEqual(stored, input.CurrentVerifier) {
			return ErrConflict
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO devices(id,author_id,name,public_key,fingerprint,created_at_ns) VALUES(?,?,?,?,?,?)`, device.ID, device.AuthorID, device.Name, device.PublicKey, device.Fingerprint, now.UnixNano()); err != nil {
			return mapConstraint(err)
		}
		if _, err := m.ExecContext(ctx, `UPDATE author_recovery SET salt=?,verifier=?,generation=?,rotated_at_ns=? WHERE author_id=?`, input.NextSalt, input.NextVerifier, generation+1, now.UnixNano(), input.AuthorID); err != nil {
			return err
		}
		if _, err := m.ExecContext(ctx, `UPDATE device_enrollment_tokens SET consumed_at_ns=? WHERE author_id=? AND consumed_at_ns IS NULL`, now.UnixNano(), input.AuthorID); err != nil {
			return err
		}
		if input.RevokeOldDevices {
			if _, err := m.ExecContext(ctx, `UPDATE devices SET revoked_at_ns=?,revoked_by_kind='recovery',revoked_by_id=? WHERE author_id=? AND id<>? AND revoked_at_ns IS NULL`, now.UnixNano(), input.AuthorID, input.AuthorID, input.DeviceID); err != nil {
				return err
			}
		}
		return m.audit(ctx, AuditInput{ID: "audit_recovery_" + input.DeviceID, ScopeAuthorID: input.AuthorID, ActorKind: "recovery", ActorAuthorID: input.AuthorID, Action: "author.recover", TargetKind: "device", TargetID: input.DeviceID})
	})
	return device, err
}

func (r *Repository) CreateBootstrapAuthority(ctx context.Context, input BootstrapAuthorityInput) (BootstrapAuthority, error) {
	if err := validateID(input.ID); err != nil {
		return BootstrapAuthority{}, err
	}
	if err := validateHandle(input.Handle); err != nil {
		return BootstrapAuthority{}, err
	}
	if err := validateID(input.DeviceName); err != nil {
		return BootstrapAuthority{}, err
	}
	if len(input.Salt) == 0 || len(input.Verifier) == 0 || len(input.Salt) > 1024 || len(input.Verifier) > 1024 {
		return BootstrapAuthority{}, fmt.Errorf("%w: bootstrap verifier", ErrInvalid)
	}
	now := r.now()
	if !input.ExpiresAt.After(now) {
		return BootstrapAuthority{}, fmt.Errorf("%w: bootstrap expiry", ErrInvalid)
	}
	authority := BootstrapAuthority{ID: input.ID, Handle: input.Handle, DeviceName: input.DeviceName,
		Salt: append([]byte(nil), input.Salt...), Verifier: append([]byte(nil), input.Verifier...), CreatedAt: now, ExpiresAt: input.ExpiresAt}
	err := r.mutate(ctx, "bootstrap-authority-create", CommitEvent{}, func(m *MutationTx) error {
		_, err := m.ExecContext(ctx, `INSERT INTO bootstrap_pairing_authorities(id,handle,intended_device_name,salt,verifier,failed_attempts,created_at_ns,expires_at_ns) VALUES(?,?,?,?,?,0,?,?)`,
			authority.ID, authority.Handle, authority.DeviceName, authority.Salt, authority.Verifier, now.UnixNano(), authority.ExpiresAt.UnixNano())
		return mapConstraint(err)
	})
	return authority, err
}

// BootstrapAuthorityCredential returns the bounded proof record used by the
// pairing subsystem. The completion transaction still rechecks all fields.
func (r *Repository) BootstrapAuthorityCredential(ctx context.Context, id string) (BootstrapAuthority, error) {
	if err := validateID(id); err != nil {
		return BootstrapAuthority{}, err
	}
	var out BootstrapAuthority
	var created, expires int64
	var consumed sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT id,handle,intended_device_name,salt,verifier,failed_attempts,created_at_ns,expires_at_ns,consumed_at_ns FROM bootstrap_pairing_authorities WHERE id=?`, id).Scan(
		&out.ID, &out.Handle, &out.DeviceName, &out.Salt, &out.Verifier, &out.FailedAttempts, &created, &expires, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return BootstrapAuthority{}, ErrNotFound
	}
	if err != nil {
		return BootstrapAuthority{}, storageError(err)
	}
	out.CreatedAt, out.ExpiresAt = time.Unix(0, created), time.Unix(0, expires)
	if consumed.Valid {
		value := time.Unix(0, consumed.Int64)
		out.ConsumedAt = &value
	}
	if out.ConsumedAt != nil || !out.ExpiresAt.After(r.now()) || out.FailedAttempts >= 5 {
		return BootstrapAuthority{}, ErrConflict
	}
	out.Salt = append([]byte(nil), out.Salt...)
	out.Verifier = append([]byte(nil), out.Verifier...)
	return out, nil
}

// CompleteBootstrapRegistration consumes the local authority in the same
// immediate transaction that creates the author, recovery verifier, device,
// quota, and audit event.
func (r *Repository) CompleteBootstrapRegistration(ctx context.Context, authorityID string, verifier []byte, input RegistrationInput) (Author, Device, error) {
	if err := validateID(authorityID); err != nil {
		return Author{}, Device{}, err
	}
	if err := validateID(input.AuthorID); err != nil {
		return Author{}, Device{}, err
	}
	if err := validateHandle(input.Handle); err != nil {
		return Author{}, Device{}, err
	}
	for _, value := range []string{input.DeviceID, input.DeviceName, input.PublicKey, input.Fingerprint} {
		if err := validateID(value); err != nil {
			return Author{}, Device{}, err
		}
	}
	if len(verifier) == 0 || len(input.RecoverySalt) == 0 || len(input.RecoveryVerifier) == 0 {
		return Author{}, Device{}, fmt.Errorf("%w: bootstrap or recovery verifier", ErrInvalid)
	}
	now := r.now()
	author := Author{ID: input.AuthorID, Handle: input.Handle, CreatedAt: now, UpdatedAt: now}
	device := Device{ID: input.DeviceID, AuthorID: input.AuthorID, Name: input.DeviceName, PublicKey: input.PublicKey, Fingerprint: input.Fingerprint, CreatedAt: now}
	verificationFailed := false
	err := r.mutate(ctx, "bootstrap-registration", CommitEvent{Operation: "bootstrap-registration", Kind: "author", ID: input.AuthorID}, func(m *MutationTx) error {
		var handle, deviceName string
		var stored []byte
		var failed, expires int64
		var consumed sql.NullInt64
		row, err := m.QueryRowContext(ctx, `SELECT handle,intended_device_name,verifier,failed_attempts,expires_at_ns,consumed_at_ns FROM bootstrap_pairing_authorities WHERE id=?`, authorityID)
		if err != nil {
			return err
		}
		if err := row.Scan(&handle, &deviceName, &stored, &failed, &expires, &consumed); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if consumed.Valid || expires <= now.UnixNano() || failed >= 5 || handle != input.Handle || deviceName != input.DeviceName {
			return ErrConflict
		}
		if !bytesEqual(stored, verifier) {
			failed++
			if _, err := m.ExecContext(ctx, `UPDATE bootstrap_pairing_authorities SET failed_attempts=? WHERE id=?`, failed, authorityID); err != nil {
				return err
			}
			verificationFailed = true
			return nil
		}
		if err := ensureHandleAvailable(ctx, m, input.Handle, now.UnixNano()); err != nil {
			return err
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO authors(id,handle,suspended,created_at_ns,updated_at_ns) VALUES(?,?,0,?,?)`, author.ID, author.Handle, now.UnixNano(), now.UnixNano()); err != nil {
			return mapConstraint(err)
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO author_recovery(author_id,salt,verifier,generation,created_at_ns,rotated_at_ns) VALUES(?,?,?,1,?,?)`, author.ID, input.RecoverySalt, input.RecoveryVerifier, now.UnixNano(), now.UnixNano()); err != nil {
			return err
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO devices(id,author_id,name,public_key,fingerprint,created_at_ns) VALUES(?,?,?,?,?,?)`, device.ID, device.AuthorID, device.Name, device.PublicKey, device.Fingerprint, now.UnixNano()); err != nil {
			return mapConstraint(err)
		}
		if input.Quota != nil {
			q := input.Quota
			if q.AuthorID != "" && q.AuthorID != author.ID {
				return fmt.Errorf("%w: quota author", ErrInvalid)
			}
			if _, err := m.ExecContext(ctx, `INSERT INTO author_quotas(author_id,max_apps,max_deployments_per_app,max_secrets_per_app,max_sessions) VALUES(?,?,?,?,?)`, author.ID, q.MaxApps, q.MaxDeploymentsPerApp, q.MaxSecretsPerApp, q.MaxSessions); err != nil {
				return mapConstraint(err)
			}
		}
		if _, err := m.ExecContext(ctx, `UPDATE bootstrap_pairing_authorities SET consumed_at_ns=? WHERE id=?`, now.UnixNano(), authorityID); err != nil {
			return err
		}
		return m.audit(ctx, AuditInput{ID: "audit_" + input.AuthorID, ScopeAuthorID: author.ID, ActorKind: "operator", ActorAuthorID: author.ID, Action: "author.register", TargetKind: "author", TargetID: author.ID})
	})
	if err != nil {
		return Author{}, Device{}, err
	}
	if verificationFailed {
		return Author{}, Device{}, ErrConflict
	}
	return author, device, nil
}
