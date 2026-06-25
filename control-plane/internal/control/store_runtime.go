package control

import (
	"fmt"
	"sort"
)

func (s *Store) UpsertSecret(in SecretInput) (SecretMetadata, error) {
	if err := validateSecretKey(in.Key); err != nil {
		return SecretMetadata{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[in.AppID]; !ok {
		return SecretMetadata{}, fmt.Errorf("%w: app %q", ErrNotFound, in.AppID)
	}
	key := secretKey{appID: in.AppID, key: in.Key}
	now := s.now()
	meta, ok := s.secrets[key]
	if !ok {
		if err := s.checkSecretQuotaLocked(in.AppID); err != nil {
			return SecretMetadata{}, err
		}
		meta = SecretMetadata{
			AppID:     in.AppID,
			Key:       in.Key,
			Version:   1,
			CreatedAt: now,
			UpdatedAt: now,
		}
	} else {
		meta.Version++
		meta.UpdatedAt = now
	}
	s.secrets[key] = meta
	s.secretValues[key] = append([]byte(nil), in.Value...)
	if err := s.persistLocked(); err != nil {
		return SecretMetadata{}, err
	}
	return meta, nil
}

// DeleteSecret removes a secret (metadata and value). Deleting a missing secret
// reports ErrNotFound.
func (s *Store) DeleteSecret(appID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := secretKey{appID: appID, key: key}
	if _, ok := s.secrets[k]; !ok {
		return fmt.Errorf("%w: secret %q", ErrNotFound, key)
	}
	delete(s.secrets, k)
	delete(s.secretValues, k)
	return s.persistLocked()
}

// ListSecrets returns the value-free metadata for an app's secrets, sorted by
// key. Values are never returned.
func (s *Store) ListSecrets(appID string) []SecretMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SecretMetadata
	for k, meta := range s.secrets {
		if k.appID == appID {
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// SecretsForApp returns the app's secret keys and values, for injection into a
// claimed app's runtime as the Env capability. This is the only method that
// exposes values, and it stays inside the platform (never reaches an HTTP
// response).
func (s *Store) SecretsForApp(appID string) map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string)
	for k, v := range s.secretValues {
		if k.appID == appID {
			out[k.key] = string(v)
		}
	}
	return out
}

// EgressAllowlist returns the app's outbound-HTTP host allowlist (a copy).
func (s *Store) EgressAllowlist(appID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.egressAllow[appID]...)
}

// AddEgressHost adds host to the app's allowlist if absent. It returns the
// updated allowlist.
func (s *Store) AddEgressHost(appID, host string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[appID]; !ok {
		return nil, fmt.Errorf("%w: app %q", ErrNotFound, appID)
	}
	for _, h := range s.egressAllow[appID] {
		if h == host {
			return append([]string(nil), s.egressAllow[appID]...), nil
		}
	}
	s.egressAllow[appID] = append(s.egressAllow[appID], host)
	sort.Strings(s.egressAllow[appID])
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return append([]string(nil), s.egressAllow[appID]...), nil
}

// RemoveEgressHost drops host from the app's allowlist. Removing an absent host
// is not an error.
func (s *Store) RemoveEgressHost(appID, host string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.egressAllow[appID]
	out := make([]string, 0, len(cur))
	for _, h := range cur {
		if h != host {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		delete(s.egressAllow, appID)
	} else {
		s.egressAllow[appID] = out
	}
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	return append([]string(nil), s.egressAllow[appID]...), nil
}

func (s *Store) StartSession(appID, deployID string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	app, ok := s.apps[appID]
	if !ok {
		return Session{}, fmt.Errorf("%w: app %q", ErrNotFound, appID)
	}
	deploy, ok := s.deploys[deployID]
	if !ok {
		return Session{}, fmt.Errorf("%w: deploy %q", ErrNotFound, deployID)
	}
	if deploy.AppID != app.ID {
		return Session{}, fmt.Errorf("%w: deploy %q does not belong to app %q", ErrInvalid, deployID, appID)
	}
	if app.ActiveDeployID != deploy.ID {
		return Session{}, fmt.Errorf("%w: deploy %q is not active for app %q", ErrInvalid, deployID, appID)
	}
	if err := s.checkSessionQuotaLocked(app.OwnerID); err != nil {
		return Session{}, err
	}
	if err := s.checkAppDailySessionQuotaLocked(app.ID); err != nil {
		return Session{}, err
	}
	session := Session{
		ID:        s.nextID("ses"),
		AppID:     app.ID,
		DeployID:  deploy.ID,
		StartedAt: s.now(),
	}
	s.sessions[session.ID] = session
	if err := s.persistLocked(); err != nil {
		return Session{}, err
	}
	return cloneSession(session), nil
}

// RecordSessionLog stores the guest's captured output for a session. truncated
// reports that the runner dropped output past its size cap. It is safe to call
// after EndSession.
func (s *Store) RecordSessionLog(id, log string, truncated bool) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("%w: session %q", ErrNotFound, id)
	}
	session.Log = log
	session.LogTruncated = truncated
	s.sessions[session.ID] = session
	if err := s.persistLocked(); err != nil {
		return Session{}, err
	}
	return cloneSession(session), nil
}

func (s *Store) EndSession(id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("%w: session %q", ErrNotFound, id)
	}
	if session.EndedAt == nil {
		now := s.now()
		session.EndedAt = &now
		s.sessions[session.ID] = session
		if err := s.persistLocked(); err != nil {
			return Session{}, err
		}
	}
	return cloneSession(session), nil
}
