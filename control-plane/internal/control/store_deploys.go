package control

import (
	"fmt"
	"sort"
)

func (s *Store) CreateDeploy(in DeployInput) (Deploy, error) {
	if err := validateDigest("source digest", in.SourceDigest); err != nil {
		return Deploy{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.apps[in.AppID]
	if !ok {
		return Deploy{}, fmt.Errorf("%w: app %q", ErrNotFound, in.AppID)
	}
	if _, ok := s.artifacts[in.ArtifactID]; !ok {
		return Deploy{}, fmt.Errorf("%w: artifact %q", ErrNotFound, in.ArtifactID)
	}
	if _, ok := s.owners[in.CreatedByOwnerID]; !ok {
		return Deploy{}, fmt.Errorf("%w: owner %q", ErrNotFound, in.CreatedByOwnerID)
	}
	if app.OwnerID != in.CreatedByOwnerID {
		return Deploy{}, fmt.Errorf("%w: deploy creator does not own app", ErrInvalid)
	}
	if err := s.checkDeployQuotaLocked(app.ID); err != nil {
		return Deploy{}, err
	}
	deploy := Deploy{
		ID:               s.nextID("dep"),
		AppID:            in.AppID,
		AppName:          app.Name,
		ArtifactID:       in.ArtifactID,
		SourceDigest:     in.SourceDigest,
		CreatedByOwnerID: in.CreatedByOwnerID,
		CreatedAt:        s.now(),
	}
	s.deploys[deploy.ID] = deploy
	if err := s.persistLocked(); err != nil {
		return Deploy{}, err
	}
	return cloneDeploy(deploy), nil
}

func (s *Store) ResolveRunnable(handle string) (App, Deploy, Artifact, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var (
		app      App
		deploy   Deploy
		artifact Artifact
		err      error
	)
	if id, ok := previewDeployID(handle); ok && s.anonymousPreview {
		app, deploy, artifact, err = s.resolvePreviewLocked(id)
	} else {
		app, deploy, artifact, err = s.resolveActiveLocked(handle)
	}
	if err != nil {
		return App{}, Deploy{}, Artifact{}, nil, err
	}
	wasm, ok := s.blobs.Get(artifact.ID)
	if !ok || len(wasm) == 0 {
		return App{}, Deploy{}, Artifact{}, nil, fmt.Errorf("%w: artifact bytes for %q", ErrNotFound, artifact.ID)
	}
	return app, cloneDeploy(deploy), cloneArtifact(artifact), wasm, nil
}

func (s *Store) GetDeploy(id string) (Deploy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	deploy, ok := s.deploys[id]
	if !ok {
		return Deploy{}, fmt.Errorf("%w: deploy %q", ErrNotFound, id)
	}
	return cloneDeploy(deploy), nil
}

// ActivateDeploy moves an app's active deploy pointer after proving the deploy
// belongs to the same app. Deploy records themselves remain immutable.
func (s *Store) ActivateDeploy(appID, deployID string) (App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.apps[appID]
	if !ok {
		return App{}, fmt.Errorf("%w: app %q", ErrNotFound, appID)
	}
	deploy, ok := s.deploys[deployID]
	if !ok {
		return App{}, fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.AppID != app.ID {
		return App{}, fmt.Errorf("%w: deploy %q does not belong to app %q", ErrInvalid, deployID, appID)
	}
	app.ActiveDeployID = deploy.ID
	s.apps[app.ID] = app
	if err := s.persistLocked(); err != nil {
		return App{}, err
	}
	return app, nil
}

func (s *Store) InspectDeployClaim(deployID, claimTokenHash string) (Deploy, App, Owner, Artifact, error) {
	if err := validateDigest("claim token hash", claimTokenHash); err != nil {
		return Deploy{}, App{}, Owner{}, Artifact{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	deploy, ok := s.deploys[deployID]
	if !ok {
		return Deploy{}, App{}, Owner{}, Artifact{}, fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.ClaimTokenHash == "" || deploy.ClaimTokenHash != claimTokenHash {
		return Deploy{}, App{}, Owner{}, Artifact{}, fmt.Errorf("%w: invalid deploy claim token", ErrInvalid)
	}
	artifact, ok := s.artifacts[deploy.ArtifactID]
	if !ok {
		return Deploy{}, App{}, Owner{}, Artifact{}, fmt.Errorf("%w: artifact %q", ErrNotFound, deploy.ArtifactID)
	}
	var app App
	var owner Owner
	if deploy.AppID != "" {
		var ok bool
		app, ok = s.apps[deploy.AppID]
		if !ok {
			return Deploy{}, App{}, Owner{}, Artifact{}, fmt.Errorf("%w: app %q", ErrNotFound, deploy.AppID)
		}
		owner, ok = s.owners[app.OwnerID]
		if !ok {
			return Deploy{}, App{}, Owner{}, Artifact{}, fmt.Errorf("%w: owner %q", ErrNotFound, app.OwnerID)
		}
	}
	return cloneDeploy(deploy), app, owner, cloneArtifact(artifact), nil
}

func (s *Store) ListSessionsForApp(appID string) ([]Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.apps[appID]; !ok {
		return nil, fmt.Errorf("%w: app %q", ErrNotFound, appID)
	}
	var out []Session
	for _, session := range s.sessions {
		if session.AppID == appID {
			out = append(out, cloneSession(session))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

func (s *Store) ResolveActiveDeploy(ownerHandle, appName string) (App, Deploy, Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, deploy, artifact, err := s.resolveActiveLocked(ownerHandle + "/" + appName)
	if err != nil {
		return App{}, Deploy{}, Artifact{}, err
	}
	return app, cloneDeploy(deploy), cloneArtifact(artifact), nil
}

// AnonymousPreviewEnabled reports whether anonymous preview run is on, so the
// API can advertise the preview handle to the deploy client.
func (s *Store) AnonymousPreviewEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.anonymousPreview
}

// previewDeployID extracts the deploy id from a "preview-<deployID>" handle.
func previewDeployID(handle string) (string, bool) {
	const prefix = "preview-"
	if len(handle) > len(prefix) && handle[:len(prefix)] == prefix {
		return handle[len(prefix):], true
	}
	return "", false
}

// resolvePreviewLocked resolves any deploy by id for an anonymous preview run.
// It returns a synthetic, ownerless App (ID "preview-<deployID>") so the session
// runs in the tightest sandbox — KV scoped to the preview, but no secrets and no
// egress (both are owner-gated). Suspended deploys and owners are still blocked.
func (s *Store) resolvePreviewLocked(deployID string) (App, Deploy, Artifact, error) {
	deploy, ok := s.deploys[deployID]
	if !ok {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if _, suspended := s.suspendedDeploys[deployID]; suspended {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: deploy %q", ErrSuspended, deployID)
	}
	if deploy.AppID != "" {
		if app, ok := s.apps[deploy.AppID]; ok {
			if app.Suspended || s.owners[app.OwnerID].Suspended {
				return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: deploy %q", ErrSuspended, deployID)
			}
		}
	}
	artifact, ok := s.artifacts[deploy.ArtifactID]
	if !ok {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: artifact %q", ErrNotFound, deploy.ArtifactID)
	}
	app := App{ID: "preview-" + deployID, Name: deploy.AppName} // OwnerID empty => tightest sandbox
	return app, deploy, artifact, nil
}

func (s *Store) resolveActiveLocked(handle string) (App, Deploy, Artifact, error) {
	var app App
	if ownerHandle, appName, ok := splitHandle(handle); ok {
		ownerID, ok := s.ownerByHandle[ownerHandle]
		if !ok {
			return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: owner %q", ErrNotFound, ownerHandle)
		}
		appID, ok := s.appByOwnerName[appKey{ownerID: ownerID, name: appName}]
		if !ok {
			return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: app %q", ErrNotFound, handle)
		}
		app = s.apps[appID]
	} else {
		var found bool
		for _, candidate := range s.apps {
			if candidate.Name != handle {
				continue
			}
			if s.owners[candidate.OwnerID].Handle == "" {
				continue
			}
			if found {
				return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: app name %q is ambiguous; use owner/app", ErrConflict, handle)
			}
			app = candidate
			found = true
		}
		if !found {
			return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: app %q", ErrNotFound, handle)
		}
	}
	if app.ActiveDeployID == "" {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: app %q has no active deploy", ErrNotFound, handle)
	}
	if s.owners[app.OwnerID].Suspended {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: owner of app %q", ErrSuspended, handle)
	}
	if app.Suspended {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: app %q", ErrSuspended, handle)
	}
	if _, ok := s.suspendedDeploys[app.ActiveDeployID]; ok {
		return App{}, Deploy{}, Artifact{}, fmt.Errorf("%w: active deploy of app %q", ErrSuspended, handle)
	}
	deploy := s.deploys[app.ActiveDeployID]
	artifact := s.artifacts[deploy.ArtifactID]
	return app, deploy, artifact, nil
}

// SetOwnerSuspended toggles the owner-level kill switch. While suspended, none
// of the owner's apps resolve to a runnable session.
func (s *Store) SetOwnerSuspended(ownerID string, suspended bool) (Owner, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.owners[ownerID]
	if !ok {
		return Owner{}, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	owner.Suspended = suspended
	s.owners[owner.ID] = owner
	if err := s.persistLocked(); err != nil {
		return Owner{}, err
	}
	return owner, nil
}

// SetAppSuspended toggles the app-level kill switch.
func (s *Store) SetAppSuspended(appID string, suspended bool) (App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.apps[appID]
	if !ok {
		return App{}, fmt.Errorf("%w: app %q", ErrNotFound, appID)
	}
	app.Suspended = suspended
	s.apps[app.ID] = app
	if err := s.persistLocked(); err != nil {
		return App{}, err
	}
	return app, nil
}

// SetDeploySuspended toggles the deploy-level kill switch. The deploy record is
// unchanged; suspension is tracked separately so deploy records stay immutable.
func (s *Store) SetDeploySuspended(deployID string, suspended bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deploys[deployID]; !ok {
		return fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if suspended {
		s.suspendedDeploys[deployID] = struct{}{}
	} else {
		delete(s.suspendedDeploys, deployID)
	}
	return s.persistLocked()
}
