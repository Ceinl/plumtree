package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ApplicationDeploymentInput is the authenticated, server-validated input to
// an atomic application replacement. Artifact identity is derived from WASM
// bytes by the repository; callers cannot choose its digest or size.
type ApplicationDeploymentInput struct {
	AuthorID, DeviceID, AppName, Kind, AccessMode, SourceDigest string
	PreviousDeploymentID                                        string
	Artifact                                                    ArtifactInput
}

type ApplicationDeployment struct {
	App        App
	Deployment Deployment
	Artifact   ArtifactMetadata
}

// Author returns an author without recovery material.
func (r *Repository) Author(ctx context.Context, authorID string) (Author, error) {
	if err := validateID(authorID); err != nil {
		return Author{}, err
	}
	var a Author
	var created, updated int64
	var suspended int
	err := r.db.QueryRowContext(ctx, `SELECT id,handle,suspended,created_at_ns,updated_at_ns FROM authors WHERE id=?`, authorID).Scan(&a.ID, &a.Handle, &suspended, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Author{}, ErrNotFound
	}
	if err != nil {
		return Author{}, storageError(err)
	}
	a.Suspended = suspended != 0
	a.CreatedAt, a.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
	return a, nil
}

// App returns an app metadata record without deployment bytes.
func (r *Repository) App(ctx context.Context, appID string) (App, error) {
	if err := validateID(appID); err != nil {
		return App{}, err
	}
	var a App
	var created, updated int64
	var suspended int
	err := r.db.QueryRowContext(ctx, `SELECT id,author_id,name,kind,access_mode,suspended,created_at_ns,updated_at_ns FROM apps WHERE id=?`, appID).Scan(&a.ID, &a.AuthorID, &a.Name, &a.Kind, &a.AccessMode, &suspended, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, storageError(err)
	}
	a.Suspended = suspended != 0
	a.CreatedAt, a.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
	return a, nil
}

func (r *Repository) Deployment(ctx context.Context, deploymentID string) (Deployment, App, error) {
	if err := validateID(deploymentID); err != nil {
		return Deployment{}, App{}, err
	}
	var d Deployment
	var a App
	var deploymentNS, createdNS, updatedNS int64
	var suspended int
	err := r.db.QueryRowContext(ctx, `SELECT d.id,d.app_id,d.artifact_id,d.deployed_by_device_id,d.created_at_ns,
a.id,a.author_id,a.name,a.kind,a.access_mode,a.suspended,a.created_at_ns,a.updated_at_ns
FROM app_deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=?`, deploymentID).Scan(
		&d.ID, &d.AppID, &d.ArtifactID, &d.DeployedByDeviceID, &deploymentNS,
		&a.ID, &a.AuthorID, &a.Name, &a.Kind, &a.AccessMode, &suspended, &createdNS, &updatedNS)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, App{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, App{}, storageError(err)
	}
	d.CreatedAt = time.Unix(0, deploymentNS)
	a.Suspended = suspended != 0
	a.CreatedAt, a.UpdatedAt = time.Unix(0, createdNS), time.Unix(0, updatedNS)
	return d, a, nil
}

func (r *Repository) CurrentDeployment(ctx context.Context, appID string) (Deployment, ArtifactMetadata, error) {
	if err := validateID(appID); err != nil {
		return Deployment{}, ArtifactMetadata{}, err
	}
	var d Deployment
	var a ArtifactMetadata
	var deploymentNS, artifactNS int64
	err := r.db.QueryRowContext(ctx, `SELECT d.id,d.app_id,d.artifact_id,d.deployed_by_device_id,d.created_at_ns,
a.id,a.digest,a.size_bytes,a.abi_version,a.created_at_ns
FROM app_active_deployments active JOIN app_deployments d ON d.id=active.deployment_id
JOIN artifacts a ON a.id=d.artifact_id WHERE active.app_id=?`, appID).Scan(
		&d.ID, &d.AppID, &d.ArtifactID, &d.DeployedByDeviceID, &deploymentNS,
		&a.ID, &a.Digest, &a.SizeBytes, &a.ABIVersion, &artifactNS)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ArtifactMetadata{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, ArtifactMetadata{}, storageError(err)
	}
	d.CreatedAt, a.CreatedAt = time.Unix(0, deploymentNS), time.Unix(0, artifactNS)
	return d, a, nil
}

// DeployApplication performs app creation/reconciliation, deduplicated blob
// storage, deployment insertion, active-pointer replacement, and redacted
// audit in one immediate transaction. Replaced deployments remain when an
// open session references them and are collected after that session ends.
func (r *Repository) DeployApplication(ctx context.Context, input ApplicationDeploymentInput) (ApplicationDeployment, error) {
	if err := validateID(input.AuthorID); err != nil {
		return ApplicationDeployment{}, err
	}
	if err := validateID(input.DeviceID); err != nil {
		return ApplicationDeployment{}, err
	}
	if err := validateID(input.AppName); err != nil || input.AppName == "" {
		return ApplicationDeployment{}, fmt.Errorf("%w: app name", ErrInvalid)
	}
	if input.Kind != "tui" && input.Kind != "cli" {
		return ApplicationDeployment{}, fmt.Errorf("%w: app kind", ErrInvalid)
	}
	if input.AccessMode != "public" && input.AccessMode != "restricted" {
		return ApplicationDeployment{}, fmt.Errorf("%w: app access mode", ErrInvalid)
	}
	if input.SourceDigest == "" {
		return ApplicationDeployment{}, fmt.Errorf("%w: source digest", ErrInvalid)
	}
	metadata, err := validateArtifact(input.Artifact)
	if err != nil {
		return ApplicationDeployment{}, err
	}
	result := ApplicationDeployment{Artifact: metadata}
	err = r.mutate(ctx, "application-deploy", CommitEvent{Operation: "application-deploy", Kind: "deployment"}, func(m *MutationTx) error {
		var active int
		row, err := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM devices WHERE id=? AND author_id=? AND revoked_at_ns IS NULL`, input.DeviceID, input.AuthorID)
		if err != nil {
			return err
		}
		if err := row.Scan(&active); err != nil {
			return err
		}
		if active == 0 {
			return ErrNotFound
		}

		var app App
		var created, updated int64
		var suspended int
		var appErr error
		if input.PreviousDeploymentID != "" {
			appRow, rowErr := m.QueryRowContext(ctx, `SELECT a.id,a.author_id,a.name,a.kind,a.access_mode,a.suspended,a.created_at_ns,a.updated_at_ns
FROM app_deployments d JOIN apps a ON a.id=d.app_id WHERE d.id=?`, input.PreviousDeploymentID)
			if rowErr != nil {
				return rowErr
			}
			appErr = appRow.Scan(&app.ID, &app.AuthorID, &app.Name, &app.Kind, &app.AccessMode, &suspended, &created, &updated)
			if errors.Is(appErr, sql.ErrNoRows) {
				return ErrNotFound
			}
			if appErr != nil {
				return appErr
			}
			if app.AuthorID != input.AuthorID || app.Name != input.AppName {
				return ErrNotFound
			}
		} else {
			appRow, rowErr := m.QueryRowContext(ctx, `SELECT id,author_id,name,kind,access_mode,suspended,created_at_ns,updated_at_ns FROM apps WHERE author_id=? AND name=?`, input.AuthorID, input.AppName)
			if rowErr != nil {
				return rowErr
			}
			appErr = appRow.Scan(&app.ID, &app.AuthorID, &app.Name, &app.Kind, &app.AccessMode, &suspended, &created, &updated)
			if errors.Is(appErr, sql.ErrNoRows) {
				if err := reserveAppName(ctx, m, input.AppName); err != nil {
					return err
				}
				now := r.now().UnixNano()
				app.ID = "app_" + randomRepositoryID()
				app.AuthorID, app.Name, app.Kind, app.AccessMode = input.AuthorID, input.AppName, input.Kind, input.AccessMode
				app.CreatedAt, app.UpdatedAt = time.Unix(0, now), time.Unix(0, now)
				if _, err := m.ExecContext(ctx, `INSERT INTO apps(id,author_id,name,kind,access_mode,suspended,created_at_ns,updated_at_ns) VALUES(?,?,?,?,?,0,?,?)`, app.ID, app.AuthorID, app.Name, app.Kind, app.AccessMode, now, now); err != nil {
					return mapConstraint(err)
				}
			} else if appErr != nil {
				return appErr
			}
		}
		app.Suspended = suspended != 0
		if app.CreatedAt.IsZero() {
			app.CreatedAt, app.UpdatedAt = time.Unix(0, created), time.Unix(0, updated)
		}
		if app.ID != "" && (app.Kind != input.Kind || app.AccessMode != input.AccessMode) {
			now := r.now().UnixNano()
			if _, err := m.ExecContext(ctx, `UPDATE apps SET kind=?,access_mode=?,updated_at_ns=? WHERE id=?`, input.Kind, input.AccessMode, now, app.ID); err != nil {
				return err
			}
			app.Kind, app.AccessMode, app.UpdatedAt = input.Kind, input.AccessMode, time.Unix(0, now)
		}
		result.App = app
		var oldDeploymentID string
		activeRow, activeErr := m.QueryRowContext(ctx, `SELECT deployment_id FROM app_active_deployments WHERE app_id=?`, app.ID)
		if activeErr != nil {
			return activeErr
		}
		activeScanErr := activeRow.Scan(&oldDeploymentID)
		if activeScanErr != nil && !errors.Is(activeScanErr, sql.ErrNoRows) {
			return activeScanErr
		}

		row, err = m.QueryRowContext(ctx, `SELECT size_bytes,wasm FROM artifact_blobs WHERE digest=?`, metadata.Digest)
		if err != nil {
			return err
		}
		var storedSize int64
		var storedWASM []byte
		scanErr := row.Scan(&storedSize, &storedWASM)
		if scanErr == nil {
			if storedSize != metadata.SizeBytes || !equalBytes(storedWASM, input.Artifact.WASM) {
				return ErrConflict
			}
		} else if !errors.Is(scanErr, sql.ErrNoRows) {
			return scanErr
		} else if _, err := m.ExecContext(ctx, `INSERT INTO artifact_blobs(digest,size_bytes,wasm) VALUES(?,?,?)`, metadata.Digest, metadata.SizeBytes, input.Artifact.WASM); err != nil {
			return mapConstraint(err)
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO artifacts(id,digest,size_bytes,abi_version,created_at_ns) VALUES(?,?,?,?,?)`, metadata.ID, metadata.Digest, metadata.SizeBytes, metadata.ABIVersion, metadata.CreatedAt.UnixNano()); err != nil {
			return mapConstraint(err)
		}
		for key, value := range input.Artifact.BuildMetadata {
			if err := validateID(key); err != nil || len(value) > 2048 {
				return fmt.Errorf("%w: build metadata", ErrInvalid)
			}
			if _, err := m.ExecContext(ctx, `INSERT INTO artifact_build_metadata(artifact_id,key,value) VALUES(?,?,?)`, metadata.ID, key, value); err != nil {
				return err
			}
		}
		deployment := Deployment{ID: "dep_" + randomRepositoryID(), AppID: app.ID, ArtifactID: metadata.ID, DeployedByDeviceID: input.DeviceID, CreatedAt: r.now()}
		if _, err := m.ExecContext(ctx, `INSERT INTO app_deployments(id,app_id,artifact_id,deployed_by_device_id,created_at_ns) VALUES(?,?,?,?,?)`, deployment.ID, deployment.AppID, deployment.ArtifactID, deployment.DeployedByDeviceID, deployment.CreatedAt.UnixNano()); err != nil {
			return mapConstraint(err)
		}
		if _, err := m.ExecContext(ctx, `INSERT INTO app_active_deployments(app_id,deployment_id) VALUES(?,?) ON CONFLICT(app_id) DO UPDATE SET deployment_id=excluded.deployment_id`, app.ID, deployment.ID); err != nil {
			return mapConstraint(err)
		}
		if oldDeploymentID != "" && oldDeploymentID != deployment.ID {
			var open int
			openRow, rowErr := m.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE deployment_id=? AND ended_at_ns IS NULL`, oldDeploymentID)
			if rowErr != nil {
				return rowErr
			}
			if err := openRow.Scan(&open); err != nil {
				return err
			}
			if open == 0 {
				var oldArtifactID string
				var oldDigest string
				oldArtifactRow, rowErr := m.QueryRowContext(ctx, `SELECT artifact_id FROM app_deployments WHERE id=?`, oldDeploymentID)
				if rowErr != nil {
					return rowErr
				}
				if err := oldArtifactRow.Scan(&oldArtifactID); err == nil {
					digestRow, rowErr := m.QueryRowContext(ctx, `SELECT digest FROM artifacts WHERE id=?`, oldArtifactID)
					if rowErr != nil {
						return rowErr
					}
					if err := digestRow.Scan(&oldDigest); err != nil {
						return err
					}
					if _, err := m.ExecContext(ctx, `DELETE FROM app_deployments WHERE id=?`, oldDeploymentID); err != nil {
						return err
					}
					if _, err := m.ExecContext(ctx, `DELETE FROM artifacts WHERE id=? AND NOT EXISTS(SELECT 1 FROM app_deployments WHERE artifact_id=?)`, oldArtifactID, oldArtifactID); err != nil {
						return err
					}
					if _, err := m.ExecContext(ctx, `DELETE FROM artifact_blobs WHERE digest=? AND NOT EXISTS(SELECT 1 FROM artifacts WHERE digest=?)`, oldDigest, oldDigest); err != nil {
						return err
					}
				}
			}
		}
		result.Deployment = deployment
		if err := m.audit(ctx, AuditInput{ID: "audit_" + deployment.ID, ScopeAuthorID: input.AuthorID, ActorKind: "device", ActorAuthorID: input.AuthorID, ActorDeviceID: input.DeviceID, Action: "deployment.replace", TargetKind: "deployment", TargetID: deployment.ID, ActorSnapshot: input.DeviceID, TargetSnapshot: app.Name}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ApplicationDeployment{}, err
	}
	return result, nil
}

func randomRepositoryID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
