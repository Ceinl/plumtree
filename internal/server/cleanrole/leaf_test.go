package cleanrole

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/sqlite"
	"golang.org/x/crypto/ssh"
)

func TestPublicLeafExecReturnsGuestStatusAndRecordsSession(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	wasm := buildCleanCLI(t)
	appID := seedLeafApp(t, database, wasm, "public")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		_ = Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-leaf", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	client := dialLeaf(t, <-ready, "alice/tool", nil)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.CombinedOutput("not-a-command")
	var exit *ssh.ExitError
	if !errors.As(err, &exit) || exit.ExitStatus() != 2 {
		t.Fatalf("exit=%v output=%s", err, output)
	}
	if !strings.Contains(string(output), "unknown command") {
		t.Fatalf("output=%s", output)
	}

	repo, err := sqlite.OpenRepository(database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	sessions, err := repo.ListSessions(context.Background(), appID, 10)
	if err != nil || len(sessions) != 1 || sessions[0].EndedAt == nil || !strings.Contains(sessions[0].Log, "unknown command") {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
}

func TestLeafAccessMatrixUsesProvedAppRelativeKeys(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	wasm := buildCleanCLI(t)
	owner, access, unknown := testSigner(t), testSigner(t), testSigner(t)
	seedAccessApps(t, database, wasm, owner, access)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		_ = Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-access", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	address := <-ready
	tests := []struct {
		name, handle string
		signer       ssh.Signer
		allowed      bool
	}{
		{"public anonymous", "alice/public-tool", nil, true},
		{"public unknown key", "alice/public-tool", unknown, true},
		{"restricted owner", "alice/private-tool", owner, true},
		{"restricted access key", "alice/private-tool", access, true},
		{"restricted unknown key", "alice/private-tool", unknown, false},
		{"restricted anonymous", "alice/private-tool", nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := dialLeaf(t, address, test.handle, test.signer)
			defer client.Close()
			session, err := client.NewSession()
			if err != nil {
				t.Fatal(err)
			}
			output, runErr := session.CombinedOutput("get_identity")
			if test.allowed && runErr != nil {
				t.Fatalf("allowed session failed: %v output=%s", runErr, output)
			}
			if !test.allowed && runErr == nil {
				t.Fatalf("denied session ran: %s", output)
			}
		})
	}
}

func TestPublicLeafShellRunsTUIAndHandlesDisconnect(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "plumtree.db")
	wasm := buildCounter(t)
	seedLeafAppNamed(t, database, wasm, "public", "counter", "tui")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		_ = Serve(ctx, ServeConfig{Database: database, SSHAddress: "127.0.0.1:0", HostKeyPath: filepath.Join(dir, "host_key"), ServerID: "server-tui", ProductVersion: "dev", Ready: func(address string) { ready <- address }})
	}()
	client := dialLeaf(t, <-ready, "alice/counter", nil)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output safeText
	session.Stdout = &output
	if err := session.RequestPty("xterm", 10, 30, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	if err := session.Shell(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for !strings.Contains(output.String(), "Count: 0") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(output.String(), "Count: 0") {
		t.Fatalf("initial frame=%q", output.String())
	}
	_, _ = stdin.Write([]byte("q"))
	if err := session.Wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[?1006h") || !strings.Contains(output.String(), "\x1b[?1006l") {
		t.Fatalf("terminal lifecycle=%q", output.String())
	}
}

type safeText struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeText) Write(value []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(value)
}

func (s *safeText) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func buildCleanCLI(t *testing.T) []byte {
	t.Helper()
	output := filepath.Join(t.TempDir(), "cleancli.wasm")
	command := exec.Command("go", "build", "-o", output, ".")
	command.Dir = "../../../examples/agentboard/app"
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK="+filepath.Join(root, "go.work"))
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build clean CLI: %v\n%s", err, combined)
	}
	wasm, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

func buildCounter(t *testing.T) []byte {
	t.Helper()
	output := filepath.Join(t.TempDir(), "counter.wasm")
	command := exec.Command("go", "build", "-o", output, ".")
	command.Dir = "../../../sdk/examples/counter"
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOWORK="+filepath.Join(root, "go.work"))
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build counter: %v\n%s", err, combined)
	}
	wasm, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

func seedLeafApp(t *testing.T, database string, wasm []byte, access string) string {
	return seedLeafAppNamed(t, database, wasm, access, "tool", "cli")
}

func seedLeafAppNamed(t *testing.T, database string, wasm []byte, access, name, kind string) string {
	t.Helper()
	repo, err := sqlite.OpenRepository(database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	_, _, err = repo.RegisterAuthor(context.Background(), sqlite.RegistrationInput{AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop", PublicKey: "owner-key", Fingerprint: "owner-fingerprint", RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"), Quota: &sqlite.Quota{AuthorID: "author-1", MaxSessions: 8}})
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(wasm)
	artifact, err := repo.PutArtifact(context.Background(), sqlite.ArtifactInput{ID: "artifact-1", Digest: "sha256:" + hex.EncodeToString(digestBytes[:]), WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	app, err := repo.CreateApp(context.Background(), sqlite.AppInput{ID: "app-1", AuthorID: "author-1", Name: name, Kind: kind, AccessMode: access, CreatedByDeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(context.Background(), sqlite.DeploymentInput{ID: "deployment-1", AppID: app.ID, ArtifactID: artifact.ID, DeployedByDeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ActivateDeployment(context.Background(), app.ID, deployment.ID); err != nil {
		t.Fatal(err)
	}
	return app.ID
}

func seedAccessApps(t *testing.T, database string, wasm []byte, owner, access ssh.Signer) {
	t.Helper()
	repo, err := sqlite.OpenRepository(database, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	_, device, err := repo.RegisterAuthor(context.Background(), sqlite.RegistrationInput{
		AuthorID: "author-1", Handle: "alice", DeviceID: "device-1", DeviceName: "laptop",
		PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(owner.PublicKey()))), Fingerprint: ssh.FingerprintSHA256(owner.PublicKey()),
		RecoverySalt: []byte("salt"), RecoveryVerifier: []byte("verifier"), Quota: &sqlite.Quota{AuthorID: "author-1", MaxSessions: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(wasm)
	artifact, err := repo.PutArtifact(context.Background(), sqlite.ArtifactInput{ID: "artifact-1", Digest: "sha256:" + hex.EncodeToString(digestBytes[:]), WASM: wasm, ABIVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	for index, app := range []struct{ name, access string }{{"public-tool", "public"}, {"private-tool", "restricted"}} {
		created, err := repo.CreateApp(context.Background(), sqlite.AppInput{ID: "app-" + strconv.Itoa(index+1), AuthorID: "author-1", Name: app.name, Kind: "cli", AccessMode: app.access, CreatedByDeviceID: device.ID})
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := repo.CreateDeployment(context.Background(), sqlite.DeploymentInput{ID: "deployment-" + strconv.Itoa(index+1), AppID: created.ID, ArtifactID: artifact.ID, DeployedByDeviceID: device.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.ActivateDeployment(context.Background(), created.ID, deployment.ID); err != nil {
			t.Fatal(err)
		}
		if app.access == "restricted" {
			_, err = repo.AddAccessKey(context.Background(), sqlite.AccessKeyInput{ID: "access-1", AppID: created.ID, Name: "guest", PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(access.PublicKey()))), Fingerprint: ssh.FingerprintSHA256(access.PublicKey()), AddedByDeviceID: device.ID})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func dialLeaf(t *testing.T, address, user string, signer ssh.Signer) *ssh.Client {
	t.Helper()
	config := &ssh.ClientConfig{User: user, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second}
	if signer != nil {
		config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	} else {
		config.Auth = []ssh.AuthMethod{ssh.KeyboardInteractive(func(_, _ string, _ []string, _ []bool) ([]string, error) { return nil, nil })}
	}
	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		host, port, _ := net.SplitHostPort(address)
		t.Fatalf("dial leaf %s:%s (port %s): %v", host, port, strconv.Itoa(mustAtoi(t, port)), err)
	}
	return client
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	result, err := strconv.Atoi(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
