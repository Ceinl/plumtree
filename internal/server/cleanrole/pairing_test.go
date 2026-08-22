package cleanrole

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	"github.com/Ceinl/plumtree/internal/cli/workflow"
	protocol "github.com/Ceinl/plumtree/internal/protocol/pairing"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

func TestBootstrapCommandPrintsOneUseAuthorityWithoutVerifier(t *testing.T) {
	var out bytes.Buffer
	database := filepath.Join(t.TempDir(), "plumtree.db")
	if err := runBootstrap([]string{"-database", database, "-handle", "alice", "-device", "laptop", "-ttl", "2m"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"bootstrapID"`)) || !bytes.Contains(out.Bytes(), []byte(`"secret"`)) || bytes.Contains(out.Bytes(), []byte("verifier")) {
		t.Fatalf("output=%s", out.Bytes())
	}
}

func TestNativeBootstrapPairAndAuthenticatedStatusJourney(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	bootstrap, err := Bootstrap(context.Background(), BootstrapConfig{Database: database, Handle: "alice", DeviceName: "laptop", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-test", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	address := <-ready
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, "client", "servers.json")
	manager := paired.Manager{StorePath: storePath, Keys: paired.FileKeyStore{Dir: filepath.Join(dir, "client", "keys")}}
	record, err := manager.Pair(context.Background(), paired.PairInput{Host: host, Port: port, Name: "local", DeviceName: "laptop", ConfirmHostKey: true, Purpose: protocol.PurposeNewAuthor, Identifier: bootstrap.ID, Secret: bootstrap.Secret, RecoverySecret: []byte("offline-recovery-secret-012345")}, paired.NewProbe(time.Second), liveExchange)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := paired.DialControl(context.Background(), record, paired.DialConfig{KeyStore: manager.Keys, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	version, err := paired.Preflight(context.Background(), connection.HTTP, "dev", 0)
	if err != nil || version.Version != "dev" {
		t.Fatalf("version=%+v err=%v", version, err)
	}
	cancel()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestPTPairThenStatusAgainstFreshNativeServer(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	bootstrap, err := Bootstrap(context.Background(), BootstrapConfig{Database: database, Handle: "alice", DeviceName: "laptop", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		_ = Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-cli", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	host, portText, err := net.SplitHostPort(<-ready)
	if err != nil {
		t.Fatal(err)
	}
	storePath, keyDir := filepath.Join(dir, "client", "servers.json"), filepath.Join(dir, "client", "keys")
	var out bytes.Buffer
	runner := workflow.Runner{In: strings.NewReader(""), Out: &out, StorePath: storePath, KeyDir: keyDir,
		Open: func(ctx context.Context, record paired.ServerRecord) (*workflow.API, error) {
			connection, err := paired.DialControl(ctx, record, paired.DialConfig{KeyStore: paired.FileKeyStore{Dir: keyDir}, Timeout: time.Second})
			if err != nil {
				return nil, err
			}
			return workflow.NewAPI(connection)
		}}
	if err := runner.Run([]string{"pair", host, "--bootstrap", bootstrap.ID, "--secret", string(bootstrap.Secret), "--yes", "--port", portText, "--name", "local", "--device", "laptop"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runner.Run([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Server: local (dev)") {
		t.Fatalf("status output=%s", out.String())
	}
}

func TestSecondDeviceCanPairListAndBeRevoked(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	bootstrap, err := Bootstrap(context.Background(), BootstrapConfig{Database: database, Handle: "alice", DeviceName: "laptop", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		_ = Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-devices", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	host, portText, err := net.SplitHostPort(<-ready)
	if err != nil {
		t.Fatal(err)
	}
	newRunner := func(name string, output *bytes.Buffer) workflow.Runner {
		keyDir := filepath.Join(dir, name, "keys")
		return workflow.Runner{In: strings.NewReader(""), Out: output, StorePath: filepath.Join(dir, name, "servers.json"), KeyDir: keyDir,
			Open: func(ctx context.Context, record paired.ServerRecord) (*workflow.API, error) {
				connection, err := paired.DialControl(ctx, record, paired.DialConfig{KeyStore: paired.FileKeyStore{Dir: keyDir}, Timeout: time.Second})
				if err != nil {
					return nil, err
				}
				return workflow.NewAPI(connection)
			}}
	}
	var firstOut, secondOut bytes.Buffer
	first := newRunner("first", &firstOut)
	if err := first.Run([]string{"pair", "--bootstrap", bootstrap.ID, "--secret", string(bootstrap.Secret), "--yes", "--port", portText, "--device", "laptop", host}); err != nil {
		t.Fatal(err)
	}
	firstOut.Reset()
	if err := first.Run([]string{"device", "invite", "phone"}); err != nil {
		t.Fatal(err)
	}
	var invite struct {
		Invitation workflow.Invitation `json:"invitation"`
	}
	if err := json.Unmarshal(firstOut.Bytes(), &invite); err != nil {
		t.Fatal(err)
	}
	second := newRunner("second", &secondOut)
	if err := second.Run([]string{"pair", "--token", invite.Invitation.ID, "--secret", invite.Invitation.Secret, "--yes", "--port", portText, "--device", "phone", host}); err != nil {
		t.Fatal(err)
	}
	secondStore, err := paired.Load(filepath.Join(dir, "second", "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	phone, err := secondStore.CurrentRecord()
	if err != nil {
		t.Fatal(err)
	}
	firstOut.Reset()
	if err := first.Run([]string{"device", "list"}); err != nil {
		t.Fatal(err)
	}
	var devices workflow.DevicesResult
	if err := json.Unmarshal(firstOut.Bytes(), &devices); err != nil || len(devices.Devices) != 2 {
		t.Fatalf("devices=%+v output=%s err=%v", devices, firstOut.String(), err)
	}
	if err := first.Run([]string{"device", "revoke", phone.DeviceID, "--yes"}); err != nil {
		t.Fatal(err)
	}
	if err := second.Run([]string{"status"}); err == nil {
		t.Fatal("revoked device retained control access")
	}
}

func TestOfflineRecoveryRejectsWrongPhraseAndRevokesLostDevice(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	bootstrap, err := Bootstrap(context.Background(), BootstrapConfig{Database: database, Handle: "alice", DeviceName: "laptop", TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		_ = Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-recovery", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	host, portText, err := net.SplitHostPort(<-ready)
	if err != nil {
		t.Fatal(err)
	}
	makeRunner := func(name string) workflow.Runner {
		keyDir := filepath.Join(dir, name, "keys")
		return workflow.Runner{In: strings.NewReader(""), Out: &bytes.Buffer{}, StorePath: filepath.Join(dir, name, "servers.json"), KeyDir: keyDir,
			Open: func(ctx context.Context, record paired.ServerRecord) (*workflow.API, error) {
				connection, err := paired.DialControl(ctx, record, paired.DialConfig{KeyStore: paired.FileKeyStore{Dir: keyDir}, Timeout: time.Second})
				if err != nil {
					return nil, err
				}
				return workflow.NewAPI(connection)
			}}
	}
	old := makeRunner("old")
	currentRecovery := "current-recovery-secret-012345"
	if err := old.Run([]string{"pair", "--bootstrap", bootstrap.ID, "--secret", string(bootstrap.Secret), "--next-recovery-secret", currentRecovery, "--yes", "--port", portText, "--device", "laptop", host}); err != nil {
		t.Fatal(err)
	}
	replacement := makeRunner("replacement")
	common := []string{"recover", "--author", "alice", "--next-recovery-secret", "rotated-recovery-secret-012345", "--yes", "--port", portText, "--device", "replacement", host}
	wrong := append([]string(nil), common...)
	wrong = append(wrong[:len(wrong)-1], append([]string{"--secret", "wrong-recovery-secret-012345"}, wrong[len(wrong)-1:]...)...)
	if err := replacement.Run(wrong); err == nil {
		t.Fatal("wrong recovery phrase was accepted")
	}
	correct := append([]string(nil), common...)
	correct = append(correct[:len(correct)-1], append([]string{"--secret", currentRecovery}, correct[len(correct)-1:]...)...)
	if err := replacement.Run(correct); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Run([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if err := old.Run([]string{"status"}); err == nil {
		t.Fatal("recovery did not revoke the lost device")
	}
}

func liveExchange(ctx context.Context, pin transport.HostPin, signer ssh.Signer, input paired.PairInput) (paired.PairResult, error) {
	record := paired.ServerRecord{Name: "pairing", ServerID: pin.StableID, Host: pin.Endpoint.Host, Port: pin.Endpoint.Port, HostKeyAlgorithm: pin.Algorithm, HostKeyFingerprint: pin.Fingerprint, ProductVersion: pin.ProductVersion, KeyRef: "unused"}
	connection, err := paired.DialPairing(ctx, record, paired.DialConfig{Signer: signer, Timeout: time.Second})
	if err != nil {
		return paired.PairResult{}, err
	}
	defer connection.Close()
	transcript, err := paired.NewTranscript(connection.SessionID, pin, signer, input.Purpose, input.Identifier)
	if err != nil {
		return paired.PairResult{}, err
	}
	return paired.ExchangePairing(ctx, connection.Channel, transcript, input.Secret, paired.ExchangeOptions{DeviceName: input.DeviceName, RecoverySecret: input.RecoverySecret})
}
