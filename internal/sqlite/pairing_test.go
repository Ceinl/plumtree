package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBootstrapAuthorityRegistersExactlyOneAuthor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo, err := OpenRepository(":memory:", nil, WithRepositoryClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	authority, err := repo.CreateBootstrapAuthority(context.Background(), BootstrapAuthorityInput{
		ID: "bootstrap-1", Handle: "alice", DeviceName: "laptop",
		Salt: []byte("bootstrap-salt"), Verifier: []byte("bootstrap-verifier"),
		ExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	registration := RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop",
		PublicKey: "ssh-ed25519 AAAA", Fingerprint: "SHA256:device",
		RecoverySalt: []byte("recovery-salt"), RecoveryVerifier: []byte("recovery-verifier"),
		CreatedAt: now,
	}
	author, device, err := repo.CompleteBootstrapRegistration(context.Background(), authority.ID, authority.Verifier, registration)
	if err != nil || author.Handle != "alice" || device.Name != "laptop" {
		t.Fatalf("author=%+v device=%+v err=%v", author, device, err)
	}
	if _, _, err := repo.CompleteBootstrapRegistration(context.Background(), authority.ID, authority.Verifier, registration); !errors.Is(err, ErrConflict) {
		t.Fatalf("replayed bootstrap err=%v", err)
	}
}

func TestBootstrapAuthorityRejectsWrongProofAndExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo, err := OpenRepository(":memory:", nil, WithRepositoryClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	input := BootstrapAuthorityInput{ID: "bootstrap-1", Handle: "alice", DeviceName: "laptop", Salt: []byte("salt"), Verifier: []byte("good"), ExpiresAt: now.Add(time.Minute)}
	if _, err := repo.CreateBootstrapAuthority(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	registration := RegistrationInput{AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop", PublicKey: "key", Fingerprint: "fingerprint", RecoverySalt: []byte("recovery-salt"), RecoveryVerifier: []byte("recovery-verifier"), CreatedAt: now}
	if _, _, err := repo.CompleteBootstrapRegistration(context.Background(), input.ID, []byte("wrong"), registration); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong proof err=%v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := repo.CompleteBootstrapRegistration(context.Background(), input.ID, input.Verifier, registration); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired proof err=%v", err)
	}
}

func TestRecoveryEnrollsReplacementAndRevokesOldDevicesAtomically(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo, err := OpenRepository(":memory:", nil, WithRepositoryClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	_, _, err = repo.RegisterAuthor(context.Background(), RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-old", DeviceName: "old",
		PublicKey: "old-key", Fingerprint: "old-fingerprint", RecoverySalt: []byte("old-salt"), RecoveryVerifier: []byte("old-verifier"), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	device, err := repo.CompleteRecovery(context.Background(), RecoveryInput{
		AuthorID: "author-1", CurrentVerifier: []byte("old-verifier"), DeviceID: "device-new", DeviceName: "new",
		PublicKey: "new-key", Fingerprint: "new-fingerprint", NextSalt: []byte("next-salt"), NextVerifier: []byte("next-verifier"), RevokeOldDevices: true,
	})
	if err != nil || device.ID != "device-new" {
		t.Fatalf("device=%+v err=%v", device, err)
	}
	if _, err := repo.DeviceByFingerprint(context.Background(), "old-fingerprint"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old device remains active: %v", err)
	}
	credential, err := repo.RecoveryCredential(context.Background(), "alice")
	if err != nil || string(credential.Verifier) != "next-verifier" || credential.Generation != 2 {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}
