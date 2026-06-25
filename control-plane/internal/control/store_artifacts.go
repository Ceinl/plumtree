package control

import "fmt"

func (s *Store) CreateArtifact(in ArtifactInput) (Artifact, error) {
	if err := validateDigest("artifact digest", in.Digest); err != nil {
		return Artifact{}, err
	}
	if in.SizeBytes < 0 {
		return Artifact{}, fmt.Errorf("%w: artifact size cannot be negative", ErrInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	artifact := Artifact{
		ID:            s.nextID("art"),
		Digest:        in.Digest,
		SizeBytes:     in.SizeBytes,
		ABIVersion:    in.ABIVersion,
		BuildMetadata: cloneStringMap(in.BuildMetadata),
		CreatedAt:     s.now(),
	}
	s.artifacts[artifact.ID] = artifact
	if err := s.persistLocked(); err != nil {
		return Artifact{}, err
	}
	return cloneArtifact(artifact), nil
}

func (s *Store) GetArtifact(id string) (Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.artifacts[id]
	if !ok {
		return Artifact{}, fmt.Errorf("%w: artifact %q", ErrNotFound, id)
	}
	return cloneArtifact(artifact), nil
}

func (s *Store) PutArtifactBytes(artifactID string, wasm []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.artifacts[artifactID]
	if !ok {
		return fmt.Errorf("%w: artifact %q", ErrNotFound, artifactID)
	}
	if int64(len(wasm)) != artifact.SizeBytes {
		return fmt.Errorf("%w: artifact bytes size does not match metadata", ErrInvalid)
	}
	if digestBytes(wasm) != artifact.Digest {
		return fmt.Errorf("%w: artifact bytes digest does not match metadata", ErrInvalid)
	}
	if err := s.blobs.Put(artifactID, wasm); err != nil {
		return err
	}
	return s.persistLocked()
}
