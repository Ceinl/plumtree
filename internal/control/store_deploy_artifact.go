package control

import "fmt"

// CreateDeployClaimWithArtifact commits artifact metadata, bytes, and the new
// deploy claim as one store operation. reservation must have been obtained
// before building with ReserveDeployClaimQuota.
func (s *Store) CreateDeployClaimWithArtifact(reservation uint64, artifactIn ArtifactInput, wasm []byte, deployIn DeployClaimInput) (Artifact, Deploy, error) {
	if err := validateArtifactMaterial(artifactIn, wasm); err != nil {
		return Artifact{}, Deploy{}, err
	}
	if err := validateDeployClaimInputForUpdate(deployIn); err != nil {
		return Artifact{}, Deploy{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.deployClaimReservations[reservation]; !ok {
		return Artifact{}, Deploy{}, fmt.Errorf("%w: deploy quota reservation is not active", ErrInvalid)
	}

	now := s.now()
	oldArtSeq, oldDepSeq := s.seq["art"], s.seq["dep"]
	artifact := Artifact{
		ID: s.nextID("art"), Digest: artifactIn.Digest, SizeBytes: artifactIn.SizeBytes,
		ABIVersion: artifactIn.ABIVersion, BuildMetadata: cloneStringMap(artifactIn.BuildMetadata), CreatedAt: now,
	}
	deployIn.ArtifactID = artifact.ID
	deploy := Deploy{
		ID: s.nextID("dep"), AppName: deployIn.AppName, AppType: deployIn.AppType,
		ArtifactID: artifact.ID, SourceDigest: deployIn.SourceDigest,
		ClaimTokenHash: deployIn.ClaimTokenHash, CreatedAt: now,
	}
	expiresAt := now.Add(s.deployClaimTTL)
	deploy.ClaimExpiresAt = &expiresAt

	if len(wasm) > 0 {
		if err := s.blobs.Put(artifact.ID, wasm); err != nil {
			s.seq["art"], s.seq["dep"] = oldArtSeq, oldDepSeq
			return Artifact{}, Deploy{}, err
		}
	}
	s.artifacts[artifact.ID] = artifact
	s.deploys[deploy.ID] = deploy
	if err := s.persistLocked(); err != nil {
		delete(s.artifacts, artifact.ID)
		delete(s.deploys, deploy.ID)
		s.blobs.Delete(artifact.ID)
		s.seq["art"], s.seq["dep"] = oldArtSeq, oldDepSeq
		return Artifact{}, Deploy{}, err
	}
	delete(s.deployClaimReservations, reservation)
	s.recentDeployClaims = append(s.recentDeployClaims, now)
	return cloneArtifact(artifact), cloneDeploy(deploy), nil
}

// UpdateDeployClaimWithArtifact authenticates again under the commit lock and
// switches the deploy to a newly-created artifact atomically.
func (s *Store) UpdateDeployClaimWithArtifact(deployID string, artifactIn ArtifactInput, wasm []byte, deployIn DeployClaimInput) (Artifact, App, Deploy, bool, error) {
	if err := validateArtifactMaterial(artifactIn, wasm); err != nil {
		return Artifact{}, App{}, Deploy{}, false, err
	}
	if err := validateDeployClaimInputForUpdate(deployIn); err != nil {
		return Artifact{}, App{}, Deploy{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteExpiredDeployClaimsLocked(s.now())
	deploy, ok := s.deploys[deployID]
	if !ok {
		return Artifact{}, App{}, Deploy{}, false, fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.ClaimTokenHash == "" || deploy.ClaimTokenHash != deployIn.ClaimTokenHash {
		return Artifact{}, App{}, Deploy{}, false, fmt.Errorf("%w: invalid deploy claim token", ErrInvalid)
	}
	if deploy.AppName != "" && deploy.AppName != deployIn.AppName {
		return Artifact{}, App{}, Deploy{}, false, fmt.Errorf("%w: app name cannot change for an existing deploy", ErrInvalid)
	}

	oldDeploy := deploy
	var app App
	claimed := deploy.CreatedByOwnerID != ""
	if claimed {
		var exists bool
		app, exists = s.apps[deploy.AppID]
		if !exists {
			return Artifact{}, App{}, Deploy{}, false, fmt.Errorf("%w: app %q", ErrNotFound, deploy.AppID)
		}
	}
	oldArtSeq := s.seq["art"]
	artifact := Artifact{
		ID: s.nextID("art"), Digest: artifactIn.Digest, SizeBytes: artifactIn.SizeBytes,
		ABIVersion: artifactIn.ABIVersion, BuildMetadata: cloneStringMap(artifactIn.BuildMetadata), CreatedAt: s.now(),
	}
	if len(wasm) > 0 {
		if err := s.blobs.Put(artifact.ID, wasm); err != nil {
			s.seq["art"] = oldArtSeq
			return Artifact{}, App{}, Deploy{}, false, err
		}
	}
	s.artifacts[artifact.ID] = artifact
	deploy.AppName, deploy.AppType = deployIn.AppName, deployIn.AppType
	deploy.ArtifactID, deploy.SourceDigest = artifact.ID, deployIn.SourceDigest
	if deploy.CreatedByOwnerID == "" {
		expiresAt := s.now().Add(s.deployClaimTTL)
		deploy.ClaimExpiresAt = &expiresAt
	}
	s.deploys[deploy.ID] = deploy
	if err := s.persistLocked(); err != nil {
		delete(s.artifacts, artifact.ID)
		s.blobs.Delete(artifact.ID)
		s.deploys[deploy.ID] = oldDeploy
		s.seq["art"] = oldArtSeq
		return Artifact{}, App{}, Deploy{}, false, err
	}

	return cloneArtifact(artifact), app, cloneDeploy(deploy), claimed, nil
}

func validateArtifactMaterial(in ArtifactInput, wasm []byte) error {
	if err := validateDigest("artifact digest", in.Digest); err != nil {
		return err
	}
	if in.SizeBytes < 0 {
		return fmt.Errorf("%w: artifact size cannot be negative", ErrInvalid)
	}
	if len(wasm) > 0 {
		if int64(len(wasm)) != in.SizeBytes || digestBytes(wasm) != in.Digest {
			return fmt.Errorf("%w: artifact bytes do not match size/digest metadata", ErrInvalid)
		}
	}
	return nil
}

func validateDeployClaimInputForUpdate(in DeployClaimInput) error {
	if err := ValidateName(in.AppName); err != nil {
		return err
	}
	if err := validateDigest("source digest", in.SourceDigest); err != nil {
		return err
	}
	return validateDigest("claim token hash", in.ClaimTokenHash)
}
