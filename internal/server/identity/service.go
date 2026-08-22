// Package identity provides paired-author and audit services for the server.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

var (
	ErrGateDisabled = errors.New("identity: configuration gate disabled")
	ErrUnauthorized = errors.New("identity: unauthorized")
	ErrSecret       = errors.New("identity: invalid secret")
)

type Gate string

const (
	GateHTTP    Gate = "http"
	GateSSH     Gate = "ssh"
	GateGateway Gate = "gateway"
)

type IDFactory func(prefix string) string

type Service struct {
	repo *sqlite.Repository
	cfg  serverconfig.Config
	now  func() time.Time
	id   IDFactory
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

func WithIDFactory(factory IDFactory) Option {
	return func(s *Service) {
		if factory != nil {
			s.id = factory
		}
	}
}

func New(repo *sqlite.Repository, cfg serverconfig.Config, options ...Option) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("%w: repository is required", sqlite.ErrInvalid)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if _, err := serverconfig.NewControlRole(cfg); err != nil {
		return nil, err
	}
	s := &Service{repo: repo, cfg: cfg, now: time.Now, id: randomID}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s, nil
}

type RegisterInput struct {
	Handle, DeviceName, PublicKey, Fingerprint string
	RecoverySecret                             []byte
}

type Registration struct {
	Author sqlite.Author
	Device sqlite.Device
}

type EnrollmentChallenge struct {
	ID, AuthorID, DeviceName string
	Secret                   []byte
	ExpiresAt                time.Time
}

func (s *Service) RegisterAuthor(ctx context.Context, input RegisterInput) (Registration, error) {
	if err := s.requireGate(GateHTTP); err != nil {
		return Registration{}, err
	}
	return s.registerAuthor(ctx, input)
}

// RegisterAuthorLocal is the trusted local bootstrap path. It does not
// require an exposure gate because it is intended for an operator on the host.
func (s *Service) RegisterAuthorLocal(ctx context.Context, input RegisterInput) (Registration, error) {
	return s.registerAuthor(ctx, input)
}

func (s *Service) registerAuthor(ctx context.Context, input RegisterInput) (Registration, error) {
	if err := validateSecret(input.RecoverySecret); err != nil {
		return Registration{}, err
	}
	authorID, deviceID := s.id("author"), s.id("device")
	salt, err := randomBytes(16)
	if err != nil {
		return Registration{}, err
	}
	quota := &sqlite.Quota{AuthorID: authorID, MaxApps: s.cfg.Limits.MaxApps, MaxDeploymentsPerApp: s.cfg.Limits.MaxDeployments, MaxSessions: s.cfg.Limits.MaxSessions}
	author, device, err := s.repo.RegisterAuthor(ctx, sqlite.RegistrationInput{
		AuthorID: authorID, Handle: input.Handle, DeviceID: deviceID, DeviceName: input.DeviceName,
		PublicKey: input.PublicKey, Fingerprint: input.Fingerprint, RecoverySalt: salt,
		RecoveryVerifier: deriveVerifier(salt, input.RecoverySecret), Quota: quota, CreatedAt: s.now(),
	})
	if err != nil {
		return Registration{}, err
	}
	return Registration{Author: author, Device: device}, nil
}

func (s *Service) BeginDeviceAddition(ctx context.Context, authorID, issuingDeviceID, deviceName string) (EnrollmentChallenge, error) {
	if err := s.requireGate(GateSSH); err != nil {
		return EnrollmentChallenge{}, err
	}
	return s.beginDeviceAddition(ctx, authorID, issuingDeviceID, deviceName)
}

func (s *Service) beginDeviceAddition(ctx context.Context, authorID, issuingDeviceID, deviceName string) (EnrollmentChallenge, error) {
	secret, err := randomBytes(32)
	if err != nil {
		return EnrollmentChallenge{}, err
	}
	salt, err := randomBytes(16)
	if err != nil {
		return EnrollmentChallenge{}, err
	}
	expires := s.now().Add(15 * time.Minute)
	token, err := s.repo.CreateEnrollmentToken(ctx, sqlite.EnrollmentTokenInput{
		ID: s.id("enroll"), AuthorID: authorID, Purpose: "add_device", IssuedByKind: "device",
		IssuedByDeviceID: issuingDeviceID, IntendedDeviceName: deviceName, Salt: salt,
		Verifier: deriveVerifier(salt, secret), ExpiresAt: expires,
	})
	if err != nil {
		return EnrollmentChallenge{}, err
	}
	return EnrollmentChallenge{ID: token.ID, AuthorID: token.AuthorID, DeviceName: token.IntendedDeviceName, Secret: append([]byte(nil), secret...), ExpiresAt: token.ExpiresAt}, nil
}

func (s *Service) CompleteDeviceAddition(ctx context.Context, tokenID string, secret []byte, publicKey, fingerprint string) (sqlite.Device, error) {
	if err := s.requireGate(GateSSH); err != nil {
		return sqlite.Device{}, err
	}
	if err := validateSecret(secret); err != nil {
		return sqlite.Device{}, err
	}
	// The repository compares this verifier inside the same transaction that
	// inserts the device and consumes the token.
	salt, err := s.repo.EnrollmentSalt(ctx, tokenID)
	if err != nil {
		return sqlite.Device{}, err
	}
	return s.repo.CompleteDeviceEnrollment(ctx, sqlite.DeviceEnrollmentInput{TokenID: tokenID, Verifier: deriveVerifier(salt, secret), DeviceID: s.id("device"), PublicKey: publicKey, Fingerprint: fingerprint})
}

func (s *Service) RotateRecovery(ctx context.Context, authorID string, currentSecret, nextSecret []byte) error {
	if err := validateSecret(currentSecret); err != nil {
		return err
	}
	if err := validateSecret(nextSecret); err != nil {
		return err
	}
	currentSalt, err := s.repo.RecoverySalt(ctx, authorID)
	if err != nil {
		return err
	}
	salt, err := randomBytes(16)
	if err != nil {
		return err
	}
	// Recovery material is high-entropy local material; only its salted digest
	// enters SQLite, never either caller-provided secret.
	return s.repo.RotateRecovery(ctx, authorID, deriveVerifier(currentSalt, currentSecret), salt, deriveVerifier(salt, nextSecret))
}

func (s *Service) RenameAuthor(ctx context.Context, authorID, newHandle string, reserveUntil time.Time) error {
	return s.repo.RenameAuthor(ctx, authorID, newHandle, reserveUntil)
}

func (s *Service) RetireAuthor(ctx context.Context, authorID string, recoverySecret []byte, reserveUntil time.Time) error {
	if err := validateSecret(recoverySecret); err != nil {
		return err
	}
	// The repository's recovery verifier is salted; local recovery uses the
	// current registration salt passed through the explicit recovery operation.
	salt, err := s.repo.RecoverySalt(ctx, authorID)
	if err != nil {
		return err
	}
	return s.repo.RetireAuthor(ctx, authorID, deriveVerifier(salt, recoverySecret), reserveUntil)
}

func (s *Service) RetireAuthorLocal(ctx context.Context, authorID string, reserveUntil time.Time) error {
	return s.repo.RetireAuthor(ctx, authorID, nil, reserveUntil)
}

func (s *Service) RevokeDevice(ctx context.Context, authorID, actorDeviceID, targetDeviceID string) error {
	if err := s.requireGate(GateSSH); err != nil {
		return err
	}
	return s.repo.RevokeDeviceAuthorized(ctx, authorID, actorDeviceID, targetDeviceID)
}

func (s *Service) RevokeDeviceByRecovery(ctx context.Context, authorID string, recoverySecret []byte, targetDeviceID string) error {
	if err := validateSecret(recoverySecret); err != nil {
		return err
	}
	salt, err := s.repo.RecoverySalt(ctx, authorID)
	if err != nil {
		return err
	}
	return s.repo.RevokeDeviceWithRecovery(ctx, authorID, deriveVerifier(salt, recoverySecret), targetDeviceID)
}

func (s *Service) CreateApp(ctx context.Context, authorID, actorDeviceID, appID, name, kind, accessMode string) (sqlite.App, error) {
	if err := s.requireGate(GateGateway); err != nil {
		return sqlite.App{}, err
	}
	return s.repo.CreateApp(ctx, sqlite.AppInput{ID: appID, AuthorID: authorID, CreatedByDeviceID: actorDeviceID, Name: name, Kind: kind, AccessMode: accessMode, CreatedAt: s.now()})
}

func (s *Service) AddAccessKey(ctx context.Context, input sqlite.AccessKeyInput) (sqlite.AccessKey, error) {
	if err := s.requireGate(GateGateway); err != nil {
		return sqlite.AccessKey{}, err
	}
	return s.repo.AddAccessKey(ctx, input)
}

func (s *Service) ListAccessKeys(ctx context.Context, authorID, appID string) ([]sqlite.AccessKey, error) {
	if err := s.requireGate(GateGateway); err != nil {
		return nil, err
	}
	return s.repo.ListAccessKeys(ctx, authorID, appID)
}

func (s *Service) RemoveAccessKey(ctx context.Context, authorID, appID, keyID, actorDeviceID string) error {
	if err := s.requireGate(GateGateway); err != nil {
		return err
	}
	return s.repo.RemoveAccessKey(ctx, authorID, appID, keyID, actorDeviceID)
}

func (s *Service) Deploy(ctx context.Context, input sqlite.DeploymentInput) (sqlite.Deployment, error) {
	if err := s.requireGate(GateGateway); err != nil {
		return sqlite.Deployment{}, err
	}
	deployment, err := s.repo.CreateDeployment(ctx, input)
	if err != nil {
		return sqlite.Deployment{}, err
	}
	if err := s.repo.ActivateDeployment(ctx, input.AppID, deployment.ID); err != nil {
		return sqlite.Deployment{}, err
	}
	return deployment, nil
}

func (s *Service) ListAudit(ctx context.Context, filter sqlite.AuditFilter) ([]sqlite.AuditEvent, error) {
	return s.repo.ListAuditFiltered(ctx, filter)
}

func (s *Service) PruneAudit(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.PruneAudit(ctx, before)
}

func (s *Service) Devices(ctx context.Context, authorID string) ([]sqlite.Device, error) {
	return s.repo.ListDevices(ctx, authorID)
}

func (s *Service) requireGate(gate Gate) error {
	var enabled bool
	switch gate {
	case GateHTTP:
		enabled = s.cfg.Exposure.HTTP.Enabled
	case GateSSH:
		enabled = s.cfg.Exposure.SSH.Enabled
	case GateGateway:
		enabled = s.cfg.Exposure.Gateway.Enabled
	default:
		return fmt.Errorf("%w: unknown gate", serverconfig.ErrInvalid)
	}
	if !enabled {
		return fmt.Errorf("%w: %s", ErrGateDisabled, gate)
	}
	return nil
}

func validateSecret(secret []byte) error {
	if len(secret) < 16 || len(secret) > 4096 {
		return fmt.Errorf("%w: secret length", ErrSecret)
	}
	return nil
}

func deriveVerifier(salt, secret []byte) []byte {
	h := sha256.New()
	_, _ = h.Write(salt)
	_, _ = h.Write(secret)
	return h.Sum(nil)
}

func randomBytes(size int) ([]byte, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("identity: random material: %w", err)
	}
	return b, nil
}

func randomID(prefix string) string {
	b, err := randomBytes(16)
	if err != nil {
		return prefix + "_" + fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b)
}
