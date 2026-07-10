package control

import (
	"fmt"
	"time"
)

// ReserveDeployClaimQuota reserves capacity in the rolling deploy-claim rate
// limit before expensive build work starts. The returned release function is
// idempotent; a successful CreateDeployClaimWithArtifact consumes the ticket.
func (s *Store) ReserveDeployClaimQuota() (uint64, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.deleteExpiredDeployClaimsLocked(now)
	if err := s.checkDeployClaimRateLocked(now); err != nil {
		return 0, nil, err
	}
	s.nextDeployReservation++
	id := s.nextDeployReservation
	s.deployClaimReservations[id] = struct{}{}
	return id, func() {
		s.mu.Lock()
		delete(s.deployClaimReservations, id)
		s.mu.Unlock()
	}, nil
}

// AuthorizeDeployClaimUpdate validates an update and its claim proof without
// requiring an artifact, so callers can reject it before invoking a builder.
func (s *Store) AuthorizeDeployClaimUpdate(deployID, appName, sourceDigest, claimTokenHash string) error {
	if err := ValidateName(appName); err != nil {
		return err
	}
	if err := validateDigest("source digest", sourceDigest); err != nil {
		return err
	}
	if err := validateDigest("claim token hash", claimTokenHash); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteExpiredDeployClaimsLocked(s.now())
	deploy, ok := s.deploys[deployID]
	if !ok {
		return fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.ClaimTokenHash == "" || deploy.ClaimTokenHash != claimTokenHash {
		return fmt.Errorf("%w: invalid deploy claim token", ErrInvalid)
	}
	if deploy.AppName != "" && deploy.AppName != appName {
		return fmt.Errorf("%w: app name cannot change for an existing deploy", ErrInvalid)
	}
	return nil
}

func (s *Store) CreateDeployClaim(in DeployClaimInput) (Deploy, error) {
	if err := validateDeployClaimInput(in); err != nil {
		return Deploy{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.deleteExpiredDeployClaimsLocked(now)
	if err := s.checkDeployClaimRateLocked(now); err != nil {
		return Deploy{}, err
	}
	if _, ok := s.artifacts[in.ArtifactID]; !ok {
		return Deploy{}, fmt.Errorf("%w: artifact %q", ErrNotFound, in.ArtifactID)
	}
	expiresAt := now.Add(s.deployClaimTTL)
	deploy := Deploy{
		ID:             s.nextID("dep"),
		AppName:        in.AppName,
		AppType:        in.AppType,
		ArtifactID:     in.ArtifactID,
		SourceDigest:   in.SourceDigest,
		ClaimTokenHash: in.ClaimTokenHash,
		CreatedAt:      now,
		ClaimExpiresAt: &expiresAt,
	}
	s.deploys[deploy.ID] = deploy
	if err := s.persistLocked(); err != nil {
		return Deploy{}, err
	}
	s.recentDeployClaims = append(s.recentDeployClaims, now)
	return cloneDeploy(deploy), nil
}

// checkDeployClaimRateLocked enforces the rolling-hour deploy-claim cap, pruning
// timestamps older than the window. It must be called with the lock held.
func (s *Store) checkDeployClaimRateLocked(now time.Time) error {
	if s.maxDeployClaimsPerHour <= 0 {
		return nil
	}
	cutoff := now.Add(-time.Hour)
	kept := s.recentDeployClaims[:0]
	for _, t := range s.recentDeployClaims {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	s.recentDeployClaims = kept
	if len(kept)+len(s.deployClaimReservations) >= s.maxDeployClaimsPerHour {
		return fmt.Errorf("%w: deploy rate limit (%d/hour) exceeded", ErrQuota, s.maxDeployClaimsPerHour)
	}
	return nil
}

func (s *Store) ClaimDeploy(deployID, claimTokenHash, ownerID string) (App, Deploy, string, error) {
	if err := validateDigest("claim token hash", claimTokenHash); err != nil {
		return App{}, Deploy{}, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	expired := s.deleteExpiredDeployClaimsLocked(now)
	owner, ok := s.owners[ownerID]
	if !ok {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, "", fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	if owner.Handle == "" {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, "", fmt.Errorf("%w: owner handle is required", ErrInvalid)
	}
	deploy, ok := s.deploys[deployID]
	if !ok {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, "", fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.ClaimTokenHash == "" || deploy.ClaimTokenHash != claimTokenHash {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, "", fmt.Errorf("%w: invalid deploy claim token", ErrInvalid)
	}
	if deploy.CreatedByOwnerID != "" {
		if deploy.CreatedByOwnerID != owner.ID {
			if expired > 0 {
				_ = s.persistLocked()
			}
			return App{}, Deploy{}, "", fmt.Errorf("%w: deploy %q is claimed by another owner", ErrConflict, deployID)
		}
		app, ok := s.apps[deploy.AppID]
		if !ok {
			if expired > 0 {
				_ = s.persistLocked()
			}
			return App{}, Deploy{}, "", fmt.Errorf("%w: app %q", ErrNotFound, deploy.AppID)
		}
		if expired > 0 {
			if err := s.persistLocked(); err != nil {
				return App{}, Deploy{}, "", err
			}
		}
		return app, cloneDeploy(deploy), "already_claimed", nil
	}

	app, err := s.ensureDeployAppLocked(owner.ID, deploy.AppName)
	if err != nil {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, "", err
	}
	deploy.AppID = app.ID
	deploy.CreatedByOwnerID = owner.ID
	deploy.ClaimedAt = &now
	s.deploys[deploy.ID] = deploy
	app.ActiveDeployID = deploy.ID
	s.apps[app.ID] = app
	if err := s.persistLocked(); err != nil {
		return App{}, Deploy{}, "", err
	}
	return app, cloneDeploy(deploy), "claimed", nil
}

func (s *Store) UpdateDeployClaim(deployID string, in DeployClaimInput) (App, Deploy, bool, error) {
	if err := validateDeployClaimInput(in); err != nil {
		return App{}, Deploy{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	expired := s.deleteExpiredDeployClaimsLocked(now)
	if _, ok := s.artifacts[in.ArtifactID]; !ok {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, false, fmt.Errorf("%w: artifact %q", ErrNotFound, in.ArtifactID)
	}
	deploy, ok := s.deploys[deployID]
	if !ok {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, false, fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.ClaimTokenHash == "" || deploy.ClaimTokenHash != in.ClaimTokenHash {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, false, fmt.Errorf("%w: invalid deploy claim token", ErrInvalid)
	}
	if deploy.AppName != "" && deploy.AppName != in.AppName {
		if expired > 0 {
			_ = s.persistLocked()
		}
		return App{}, Deploy{}, false, fmt.Errorf("%w: app name cannot change for an existing deploy", ErrInvalid)
	}
	deploy.AppName = in.AppName
	deploy.AppType = in.AppType
	deploy.ArtifactID = in.ArtifactID
	deploy.SourceDigest = in.SourceDigest
	if deploy.CreatedByOwnerID == "" {
		expiresAt := now.Add(s.deployClaimTTL)
		deploy.ClaimExpiresAt = &expiresAt
	}
	s.deploys[deploy.ID] = deploy

	var app App
	claimed := deploy.CreatedByOwnerID != ""
	if claimed {
		var ok bool
		app, ok = s.apps[deploy.AppID]
		if !ok {
			return App{}, Deploy{}, false, fmt.Errorf("%w: app %q", ErrNotFound, deploy.AppID)
		}
		if app.ActiveDeployID == "" {
			app.ActiveDeployID = deploy.ID
		}
		s.apps[app.ID] = app
	}
	if err := s.persistLocked(); err != nil {
		return App{}, Deploy{}, false, err
	}
	return app, cloneDeploy(deploy), claimed, nil
}

func (s *Store) DeleteExpiredDeployClaims() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := s.deleteExpiredDeployClaimsLocked(s.now())
	if deleted == 0 {
		return 0, nil
	}
	return deleted, s.persistLocked()
}

func validateDeployClaimInput(in DeployClaimInput) error {
	if err := ValidateName(in.AppName); err != nil {
		return err
	}
	if err := validateNonEmpty("artifact ID", in.ArtifactID); err != nil {
		return err
	}
	if err := validateDigest("source digest", in.SourceDigest); err != nil {
		return err
	}
	if err := validateDigest("claim token hash", in.ClaimTokenHash); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureDeployAppLocked(ownerID, appName string) (App, error) {
	if _, ok := s.owners[ownerID]; !ok {
		return App{}, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	key := appKey{ownerID: ownerID, name: appName}
	if id, ok := s.appByOwnerName[key]; ok {
		return s.apps[id], nil
	}
	if err := s.checkAppQuotaLocked(ownerID); err != nil {
		return App{}, err
	}
	app := App{
		ID:        s.nextID("app"),
		OwnerID:   ownerID,
		Name:      appName,
		CreatedAt: s.now(),
	}
	s.apps[app.ID] = app
	s.appByOwnerName[key] = app.ID
	return app, nil
}

func (s *Store) deleteExpiredDeployClaimsLocked(now time.Time) int {
	deleted := 0
	for id, deploy := range s.deploys {
		if deploy.CreatedByOwnerID != "" || deploy.ClaimTokenHash == "" || deploy.ClaimExpiresAt == nil {
			continue
		}
		if now.Before(*deploy.ClaimExpiresAt) {
			continue
		}
		delete(s.deploys, id)
		s.deleteArtifactIfUnusedLocked(deploy.ArtifactID)
		deleted++
	}
	return deleted
}

func (s *Store) deleteArtifactIfUnusedLocked(artifactID string) {
	for _, deploy := range s.deploys {
		if deploy.ArtifactID == artifactID {
			return
		}
	}
	delete(s.artifacts, artifactID)
	s.blobs.Delete(artifactID)
}
