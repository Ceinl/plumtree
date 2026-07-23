package control

import (
	"fmt"
	"sort"
)

// ResolveSSHKey resolves a proved SSH public-key fingerprint to its registered
// key and owner. Callers must only use this after the SSH transport has verified
// a signature made by the corresponding private key.
func (s *Store) ResolveSSHKey(fingerprint string) (SSHKey, Owner, error) {
	if err := validateNonEmpty("fingerprint", fingerprint); err != nil {
		return SSHKey{}, Owner{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	keyID, ok := s.sshKeyByFingerprint[fingerprint]
	if !ok {
		return SSHKey{}, Owner{}, fmt.Errorf("%w: SSH key fingerprint", ErrNotFound)
	}
	key, ok := s.sshKeys[keyID]
	if !ok {
		return SSHKey{}, Owner{}, fmt.Errorf("%w: SSH key %q", ErrNotFound, keyID)
	}
	owner, ok := s.owners[key.OwnerID]
	if !ok {
		return SSHKey{}, Owner{}, fmt.Errorf("%w: owner %q", ErrNotFound, key.OwnerID)
	}
	return key, owner, nil
}

func (s *Store) RegisterSSHKey(in SSHKeyInput) (SSHKey, error) {
	if err := ValidateName(in.Name); err != nil {
		return SSHKey{}, err
	}
	if err := validateNonEmpty("public key", in.PublicKey); err != nil {
		return SSHKey{}, err
	}
	if err := validateNonEmpty("fingerprint", in.Fingerprint); err != nil {
		return SSHKey{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[in.OwnerID]; !ok {
		return SSHKey{}, fmt.Errorf("%w: owner %q", ErrNotFound, in.OwnerID)
	}
	if _, ok := s.sshKeyByFingerprint[in.Fingerprint]; ok {
		return SSHKey{}, fmt.Errorf("%w: SSH key fingerprint already registered", ErrConflict)
	}
	key := SSHKey{
		ID:          s.nextID("key"),
		OwnerID:     in.OwnerID,
		Name:        in.Name,
		PublicKey:   in.PublicKey,
		Fingerprint: in.Fingerprint,
		CreatedAt:   s.now(),
	}
	s.sshKeys[key.ID] = key
	s.sshKeyByFingerprint[key.Fingerprint] = key.ID
	if err := s.persistLocked(); err != nil {
		return SSHKey{}, err
	}
	return key, nil
}

// ListSSHKeys returns an owner's registered SSH keys ordered by ID.
func (s *Store) ListSSHKeys(ownerID string) ([]SSHKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.owners[ownerID]; !ok {
		return nil, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	var keys []SSHKey
	for _, key := range s.sshKeys {
		if key.OwnerID == ownerID {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	return keys, nil
}

// RevokeSSHKey removes one of an owner's registered SSH keys. A key owned by
// someone else is reported as not found so ownership cannot be probed.
func (s *Store) RevokeSSHKey(ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.sshKeys[id]
	if !ok || key.OwnerID != ownerID {
		return fmt.Errorf("%w: SSH key %q", ErrNotFound, id)
	}
	delete(s.sshKeys, id)
	delete(s.sshKeyByFingerprint, key.Fingerprint)
	return s.persistLocked()
}

func (s *Store) CreateCIToken(in CITokenInput) (CIToken, error) {
	if err := ValidateName(in.Name); err != nil {
		return CIToken{}, err
	}
	if err := validateNonEmpty("token hash", in.TokenHash); err != nil {
		return CIToken{}, err
	}
	if len(in.Scopes) == 0 {
		return CIToken{}, fmt.Errorf("%w: at least one token scope is required", ErrInvalid)
	}
	for _, scope := range in.Scopes {
		if !validScope(scope) {
			return CIToken{}, fmt.Errorf("%w: unknown token scope %q", ErrInvalid, scope)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[in.OwnerID]; !ok {
		return CIToken{}, fmt.Errorf("%w: owner %q", ErrNotFound, in.OwnerID)
	}
	token := CIToken{
		ID:        s.nextID("tok"),
		OwnerID:   in.OwnerID,
		Name:      in.Name,
		TokenHash: in.TokenHash,
		Scopes:    cloneScopes(in.Scopes),
		CreatedAt: s.now(),
	}
	s.ciTokens[token.ID] = token
	if err := s.persistLocked(); err != nil {
		return CIToken{}, err
	}
	return cloneToken(token), nil
}

func (s *Store) GetCIToken(id string) (CIToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.ciTokens[id]
	if !ok {
		return CIToken{}, fmt.Errorf("%w: token %q", ErrNotFound, id)
	}
	return cloneToken(token), nil
}

// ListCITokens returns an owner's CI tokens (active and revoked) ordered by ID.
func (s *Store) ListCITokens(ownerID string) ([]CIToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.owners[ownerID]; !ok {
		return nil, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	var tokens []CIToken
	for _, token := range s.ciTokens {
		if token.OwnerID == ownerID {
			tokens = append(tokens, cloneToken(token))
		}
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].ID < tokens[j].ID })
	return tokens, nil
}

// RevokeCIToken marks an owner's CI token revoked. It is idempotent and fails
// with ErrNotFound if the token is unknown or owned by someone else.
func (s *Store) RevokeCIToken(ownerID, id string) (CIToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.ciTokens[id]
	if !ok || token.OwnerID != ownerID {
		return CIToken{}, fmt.Errorf("%w: token %q", ErrNotFound, id)
	}
	if token.RevokedAt == nil {
		now := s.now()
		token.RevokedAt = &now
		s.ciTokens[id] = token
		if err := s.persistLocked(); err != nil {
			return CIToken{}, err
		}
	}
	return cloneToken(token), nil
}

// AuthenticateCIToken resolves an active (non-revoked) CI token by its hash and
// returns the token with its owner. Revoked and unknown hashes both report
// ErrNotFound so callers cannot distinguish them.
func (s *Store) AuthenticateCIToken(tokenHash string) (CIToken, Owner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, token := range s.ciTokens {
		if token.TokenHash != tokenHash || token.RevokedAt != nil {
			continue
		}
		owner, ok := s.owners[token.OwnerID]
		if !ok {
			return CIToken{}, Owner{}, fmt.Errorf("%w: owner %q", ErrNotFound, token.OwnerID)
		}
		return cloneToken(token), owner, nil
	}
	return CIToken{}, Owner{}, fmt.Errorf("%w: CI token", ErrNotFound)
}
