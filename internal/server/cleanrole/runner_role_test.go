package cleanrole

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/internal/runner"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
)

func TestRunnerRoleServesIsolatedWorkerOverAuthenticatedUnixSocket(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "runner-worker")
	command := exec.Command("go", "build", "-o", worker, "./cmd/runner-worker")
	command.Dir = filepath.Join("..", "..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build runner worker: %v\n%s", err, output)
	}
	tokenPath := filepath.Join(dir, "runner.token")
	if err := os.WriteFile(tokenPath, []byte("isolated-runner-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	socketDir, err := os.MkdirTemp("/tmp", "pt-r-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	endpoint := "unix://" + filepath.Join(socketDir, "runner.sock")
	cfg := serverconfig.Default()
	cfg.Roles = serverconfig.Roles{Runner: true}
	cfg.Runtime.RunnerEndpoint = endpoint
	cfg.Runtime.RunnerWorker = worker
	cfg.Runtime.RunnerScratchRoot = filepath.Join(dir, "scratch")
	cfg.Secrets.RunnerTokenFile = tokenPath
	projection, err := serverconfig.MaterializeRole(cfg, serverconfig.RoleRunner)
	if err != nil {
		t.Fatal(err)
	}
	var brokerLog bytes.Buffer
	component := &runnerComponent{projection: projection, out: &brokerLog}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := component.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := component.Ready(ctx); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	process := runner.NewRemoteProcessRunner(endpoint, "isolated-runner-token")
	err = process.RunCLI(context.Background(), buildCleanCLI(t), runner.DefaultLimits,
		runner.Capabilities{Auth: runner.StaticAuth{Identity: runner.Identity{User: "owner", Kind: runner.IdentitySSHKey, Authenticated: true}}},
		[]string{"get_identity"}, &output)
	if err != nil || !strings.Contains(output.String(), "owner authenticated=true") {
		t.Fatalf("isolated output=%q err=%v broker=%q", output.String(), err, brokerLog.String())
	}
	cancel()
	if err := component.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A tls:// endpoint serves the broker over server-authenticated TLS loaded
// from the PLUMTREE_RUNNER_TLS_CERT/KEY environment entries, and production
// refuses the cleartext tcp:// transport by name.
func TestRunnerRoleServesBrokerOverTLSAndRefusesPlainTCPInProduction(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "runner-worker")
	command := exec.Command("go", "build", "-o", worker, "./cmd/runner-worker")
	command.Dir = filepath.Join("..", "..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build runner worker: %v\n%s", err, output)
	}
	tokenPath := filepath.Join(dir, "runner.token")
	if err := os.WriteFile(tokenPath, []byte("tls-runner-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(dir, "broker.crt")
	keyFile := filepath.Join(dir, "broker.key")
	certPEM, keyPEM := testRunnerCertificate(t)
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := serverconfig.Default()
	cfg.Roles = serverconfig.Roles{Runner: true}
	endpoint := "tls://127.0.0.1:0"
	cfg.Runtime.RunnerEndpoint = endpoint
	cfg.Runtime.RunnerWorker = worker
	cfg.Runtime.RunnerScratchRoot = filepath.Join(dir, "scratch")
	cfg.Secrets.RunnerTokenFile = tokenPath
	projection, err := serverconfig.MaterializeRole(cfg, serverconfig.RoleRunner)
	if err != nil {
		t.Fatal(err)
	}
	var brokerLog bytes.Buffer
	component := &runnerComponent{
		projection: projection,
		out:        &brokerLog,
		environ:    []string{"PLUMTREE_RUNNER_TLS_CERT=" + certFile, "PLUMTREE_RUNNER_TLS_KEY=" + keyFile},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := component.Start(ctx); err != nil {
		t.Fatal(err)
	}

	listenerAddr := component.listener.Addr().String()
	var output bytes.Buffer
	process := runner.NewRemoteProcessRunner("tls://"+listenerAddr, "tls-runner-token")
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("add runner certificate to test roots")
	}
	process.TLSClientConfig = &tls.Config{RootCAs: roots}
	err = process.RunCLI(context.Background(), buildCleanCLI(t), runner.DefaultLimits,
		runner.Capabilities{Auth: runner.StaticAuth{Identity: runner.Identity{User: "owner", Kind: runner.IdentitySSHKey, Authenticated: true}}},
		[]string{"get_identity"}, &output)
	if err != nil || !strings.Contains(output.String(), "owner authenticated=true") {
		t.Fatalf("TLS output=%q err=%v broker=%q", output.String(), err, brokerLog.String())
	}
	cancel()
	if err := component.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The same configuration over plain tcp:// must be refused in production.
	cfg.Runtime.Production = true
	cfg.Runtime.RunnerEndpoint = "tcp://127.0.0.1:7947"
	productionProjection, err := serverconfig.MaterializeRole(cfg, serverconfig.RoleRunner)
	if err != nil {
		t.Fatal(err)
	}
	plain := &runnerComponent{projection: productionProjection, out: io.Discard}
	if err := plain.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "tcp://") {
		t.Fatalf("production plain tcp:// start error = %v, want refusal naming tcp://", err)
	}
}

func testRunnerCertificate(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}
