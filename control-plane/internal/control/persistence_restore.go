package control

import (
	"fmt"
	"strconv"
	"strings"
)

func (s *Store) restoreSnapshot(snap storeSnapshot) (bool, error) {
	if snap.Version != storeSnapshotVersion {
		return false, fmt.Errorf("%w: unsupported snapshot version %d", ErrInvalid, snap.Version)
	}

	for k, v := range snap.Seq {
		if k == "" || v < 0 {
			return false, fmt.Errorf("%w: invalid sequence %q=%d", ErrInvalid, k, v)
		}
		s.seq[k] = v
	}

	for _, owner := range snap.Owners {
		if owner.ID == "" {
			return false, fmt.Errorf("%w: owner ID is required", ErrInvalid)
		}
		if owner.Handle != "" {
			if err := ValidateName(owner.Handle); err != nil {
				return false, err
			}
		}
		if _, ok := s.owners[owner.ID]; ok {
			return false, fmt.Errorf("%w: duplicate owner ID %q", ErrConflict, owner.ID)
		}
		if owner.Handle != "" {
			if _, ok := s.ownerByHandle[owner.Handle]; ok {
				return false, fmt.Errorf("%w: duplicate owner handle %q", ErrConflict, owner.Handle)
			}
		}
		s.owners[owner.ID] = owner
		if owner.Handle != "" {
			s.ownerByHandle[owner.Handle] = owner.ID
		}
		bumpSeq(s.seq, "own", owner.ID)
	}

	for _, identity := range snap.Identities {
		if !validProvider(identity.Provider) {
			return false, fmt.Errorf("%w: unknown identity provider %q", ErrInvalid, identity.Provider)
		}
		if err := validateNonEmpty("identity subject", identity.Subject); err != nil {
			return false, err
		}
		if _, ok := s.owners[identity.OwnerID]; !ok {
			return false, fmt.Errorf("%w: owner %q", ErrNotFound, identity.OwnerID)
		}
		key := identityKey{provider: identity.Provider, subject: identity.Subject}
		if _, ok := s.identities[key]; ok {
			return false, fmt.Errorf("%w: duplicate identity %q/%q", ErrConflict, identity.Provider, identity.Subject)
		}
		s.identities[key] = identity
	}
	migrated := s.migrateGeneratedIdentityHandlesLocked()

	for _, app := range snap.Apps {
		if app.ID == "" {
			return false, fmt.Errorf("%w: app ID is required", ErrInvalid)
		}
		if _, ok := s.owners[app.OwnerID]; !ok {
			return false, fmt.Errorf("%w: owner %q", ErrNotFound, app.OwnerID)
		}
		if err := ValidateName(app.Name); err != nil {
			return false, err
		}
		key := appKey{ownerID: app.OwnerID, name: app.Name}
		if _, ok := s.apps[app.ID]; ok {
			return false, fmt.Errorf("%w: duplicate app ID %q", ErrConflict, app.ID)
		}
		if _, ok := s.appByOwnerName[key]; ok {
			return false, fmt.Errorf("%w: duplicate app %q", ErrConflict, app.Name)
		}
		s.apps[app.ID] = app
		s.appByOwnerName[key] = app.ID
		bumpSeq(s.seq, "app", app.ID)
	}

	for _, artifact := range snap.Artifacts {
		if artifact.ID == "" {
			return false, fmt.Errorf("%w: artifact ID is required", ErrInvalid)
		}
		if err := validateDigest("artifact digest", artifact.Digest); err != nil {
			return false, err
		}
		if artifact.SizeBytes < 0 {
			return false, fmt.Errorf("%w: artifact size cannot be negative", ErrInvalid)
		}
		if _, ok := s.artifacts[artifact.ID]; ok {
			return false, fmt.Errorf("%w: duplicate artifact ID %q", ErrConflict, artifact.ID)
		}
		s.artifacts[artifact.ID] = cloneArtifact(artifact)
		bumpSeq(s.seq, "art", artifact.ID)
	}

	for artifactID, blob := range snap.Blobs {
		artifact, ok := s.artifacts[artifactID]
		if !ok {
			return false, fmt.Errorf("%w: artifact %q", ErrNotFound, artifactID)
		}
		if int64(len(blob)) != artifact.SizeBytes {
			return false, fmt.Errorf("%w: artifact bytes size does not match metadata", ErrInvalid)
		}
		if digestBytes(blob) != artifact.Digest {
			return false, fmt.Errorf("%w: artifact bytes digest does not match metadata", ErrInvalid)
		}
		if err := s.blobs.Put(artifactID, blob); err != nil {
			return false, err
		}
	}

	for _, deploy := range snap.Deploys {
		if deploy.ID == "" {
			return false, fmt.Errorf("%w: deploy ID is required", ErrInvalid)
		}
		if err := validateDigest("source digest", deploy.SourceDigest); err != nil {
			return false, err
		}
		if _, ok := s.artifacts[deploy.ArtifactID]; !ok {
			return false, fmt.Errorf("%w: artifact %q", ErrNotFound, deploy.ArtifactID)
		}
		if deploy.AppID == "" && deploy.CreatedByOwnerID == "" {
			if err := ValidateName(deploy.AppName); err != nil {
				return false, err
			}
			if err := validateDigest("claim token hash", deploy.ClaimTokenHash); err != nil {
				return false, err
			}
			if deploy.ClaimExpiresAt == nil {
				expiresAt := deploy.CreatedAt.Add(s.deployClaimTTL)
				deploy.ClaimExpiresAt = &expiresAt
				migrated = true
			}
		} else {
			if deploy.AppID == "" {
				return false, fmt.Errorf("%w: deploy app ID is required", ErrInvalid)
			}
			if deploy.CreatedByOwnerID == "" {
				return false, fmt.Errorf("%w: deploy creator owner ID is required", ErrInvalid)
			}
			app, ok := s.apps[deploy.AppID]
			if !ok {
				return false, fmt.Errorf("%w: app %q", ErrNotFound, deploy.AppID)
			}
			if _, ok := s.owners[deploy.CreatedByOwnerID]; !ok {
				return false, fmt.Errorf("%w: owner %q", ErrNotFound, deploy.CreatedByOwnerID)
			}
			if app.OwnerID != deploy.CreatedByOwnerID {
				return false, fmt.Errorf("%w: deploy creator does not own app", ErrInvalid)
			}
			if deploy.AppName != "" {
				if err := ValidateName(deploy.AppName); err != nil {
					return false, err
				}
			}
			if deploy.ClaimTokenHash != "" {
				if err := validateDigest("claim token hash", deploy.ClaimTokenHash); err != nil {
					return false, err
				}
			}
		}
		if _, ok := s.deploys[deploy.ID]; ok {
			return false, fmt.Errorf("%w: duplicate deploy ID %q", ErrConflict, deploy.ID)
		}
		s.deploys[deploy.ID] = cloneDeploy(deploy)
		bumpSeq(s.seq, "dep", deploy.ID)
	}
	if s.deleteExpiredDeployClaimsLocked(s.now()) > 0 {
		migrated = true
	}

	for _, app := range s.apps {
		if app.ActiveDeployID == "" {
			continue
		}
		deploy, ok := s.deploys[app.ActiveDeployID]
		if !ok {
			return false, fmt.Errorf("%w: active deploy %q", ErrNotFound, app.ActiveDeployID)
		}
		if deploy.AppID != app.ID {
			return false, fmt.Errorf("%w: active deploy %q does not belong to app %q", ErrInvalid, deploy.ID, app.ID)
		}
	}

	for _, key := range snap.SSHKeys {
		if key.ID == "" {
			return false, fmt.Errorf("%w: SSH key ID is required", ErrInvalid)
		}
		if _, ok := s.owners[key.OwnerID]; !ok {
			return false, fmt.Errorf("%w: owner %q", ErrNotFound, key.OwnerID)
		}
		if err := ValidateName(key.Name); err != nil {
			return false, err
		}
		if err := validateNonEmpty("public key", key.PublicKey); err != nil {
			return false, err
		}
		if err := validateNonEmpty("fingerprint", key.Fingerprint); err != nil {
			return false, err
		}
		if _, ok := s.sshKeys[key.ID]; ok {
			return false, fmt.Errorf("%w: duplicate SSH key ID %q", ErrConflict, key.ID)
		}
		if _, ok := s.sshKeyByFingerprint[key.Fingerprint]; ok {
			return false, fmt.Errorf("%w: duplicate SSH key fingerprint", ErrConflict)
		}
		s.sshKeys[key.ID] = key
		s.sshKeyByFingerprint[key.Fingerprint] = key.ID
		bumpSeq(s.seq, "key", key.ID)
	}

	for _, token := range snap.CITokens {
		if token.ID == "" {
			return false, fmt.Errorf("%w: token ID is required", ErrInvalid)
		}
		if _, ok := s.owners[token.OwnerID]; !ok {
			return false, fmt.Errorf("%w: owner %q", ErrNotFound, token.OwnerID)
		}
		if err := ValidateName(token.Name); err != nil {
			return false, err
		}
		if err := validateNonEmpty("token hash", token.TokenHash); err != nil {
			return false, err
		}
		if len(token.Scopes) == 0 {
			return false, fmt.Errorf("%w: at least one token scope is required", ErrInvalid)
		}
		for _, scope := range token.Scopes {
			if !validScope(scope) {
				return false, fmt.Errorf("%w: unknown token scope %q", ErrInvalid, scope)
			}
		}
		if _, ok := s.ciTokens[token.ID]; ok {
			return false, fmt.Errorf("%w: duplicate token ID %q", ErrConflict, token.ID)
		}
		s.ciTokens[token.ID] = cloneToken(token)
		bumpSeq(s.seq, "tok", token.ID)
	}

	for _, secret := range snap.Secrets {
		if _, ok := s.apps[secret.AppID]; !ok {
			return false, fmt.Errorf("%w: app %q", ErrNotFound, secret.AppID)
		}
		if err := validateSecretKey(secret.Key); err != nil {
			return false, err
		}
		key := secretKey{appID: secret.AppID, key: secret.Key}
		if _, ok := s.secrets[key]; ok {
			return false, fmt.Errorf("%w: duplicate secret %q", ErrConflict, secret.Key)
		}
		s.secrets[key] = secret
	}

	for _, sv := range snap.SecretValues {
		if _, ok := s.apps[sv.AppID]; !ok {
			return false, fmt.Errorf("%w: app %q", ErrNotFound, sv.AppID)
		}
		s.secretValues[secretKey{appID: sv.AppID, key: sv.Key}] = append([]byte(nil), sv.Value...)
	}

	for appID, hosts := range snap.EgressAllow {
		if _, ok := s.apps[appID]; !ok {
			return false, fmt.Errorf("%w: app %q", ErrNotFound, appID)
		}
		s.egressAllow[appID] = append([]string(nil), hosts...)
	}

	for _, session := range snap.Sessions {
		if session.ID == "" {
			return false, fmt.Errorf("%w: session ID is required", ErrInvalid)
		}
		deploy, ok := s.deploys[session.DeployID]
		if !ok {
			return false, fmt.Errorf("%w: deploy %q", ErrNotFound, session.DeployID)
		}
		if app, ok := s.apps[session.AppID]; ok {
			if deploy.AppID != app.ID {
				return false, fmt.Errorf("%w: deploy %q does not belong to app %q", ErrInvalid, deploy.ID, app.ID)
			}
		} else if previewID, preview := previewDeployID(session.AppID); !preview || previewID != deploy.ID {
			return false, fmt.Errorf("%w: app %q", ErrNotFound, session.AppID)
		}
		if _, ok := s.sessions[session.ID]; ok {
			return false, fmt.Errorf("%w: duplicate session ID %q", ErrConflict, session.ID)
		}
		if session.EndedAt == nil {
			now := s.now()
			session.EndedAt = &now
		}
		s.sessions[session.ID] = cloneSession(session)
		bumpSeq(s.seq, "ses", session.ID)
	}

	for ownerID, quotas := range snap.Quotas {
		if _, ok := s.owners[ownerID]; !ok {
			return false, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
		}
		s.quotas[ownerID] = quotas
	}

	for _, deployID := range snap.SuspendedDeploys {
		if _, ok := s.deploys[deployID]; !ok {
			return false, fmt.Errorf("%w: suspended deploy %q", ErrNotFound, deployID)
		}
		s.suspendedDeploys[deployID] = struct{}{}
	}
	return migrated, nil
}

func bumpSeq(seq map[string]int, prefix, id string) {
	n, ok := parseSeqID(prefix, id)
	if ok && n > seq[prefix] {
		seq[prefix] = n
	}
}

func parseSeqID(prefix, id string) (int, bool) {
	head := prefix + "_"
	if !strings.HasPrefix(id, head) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, head))
	if err != nil {
		return 0, false
	}
	return n, true
}
