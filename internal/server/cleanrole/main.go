// Package cleanrole is the selected root server assembly. It owns the
// encrypted SQLite repository and publishes the clean API only through the
// authenticated SSH control subsystem.
package cleanrole

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/internal/httpapi/v1"
	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	identityservice "github.com/Ceinl/plumtree/internal/server/identity"
	pairingserver "github.com/Ceinl/plumtree/internal/server/pairing"
	"github.com/Ceinl/plumtree/internal/sqlite"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

const defaultProductVersion = "dev"

// Run starts the one-process native server. The process exposes no HTTP,
// gateway, runner, or bearer-token listener; all API traffic is carried over
// the authenticated plumtree-control-v1 SSH subsystem.
func Run(args []string) int {
	if err := run(args); err != nil {
		log.Print(err)
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	return 0
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "bootstrap" {
		return runBootstrap(args[1:], os.Stdout)
	}
	if len(args) > 1 && args[0] == "author" && args[1] == "bootstrap" {
		return runBootstrap(args[2:], os.Stdout)
	}
	fs := flag.NewFlagSet("plumtree", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	database := fs.String("database", "plumtree.db", "path to the Plumtree SQLite database")
	sshAddr := fs.String("ssh-addr", ":2222", "SSH control listener address")
	hostKeyPath := fs.String("host-key", "plumtree_host_key", "persistent SSH host-key path")
	serverID := fs.String("server-id", "", "stable server identity; generated and persisted when omitted")
	productVersion := fs.String("product-version", defaultProductVersion, "exact Plumtree product version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*productVersion) == "" {
		return errors.New("clean server: product version is required")
	}

	return Serve(context.Background(), ServeConfig{Database: *database, SSHAddress: *sshAddr, HostKeyPath: *hostKeyPath, ServerID: *serverID, ProductVersion: *productVersion})
}

func runBootstrap(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("plumtree bootstrap", flag.ContinueOnError)
	database := fs.String("database", "plumtree.db", "path to the Plumtree SQLite database")
	handle := fs.String("handle", "", "author handle bound to this authority")
	device := fs.String("device", "device", "first device name")
	ttl := fs.Duration("ttl", 10*time.Minute, "one-use authority lifetime")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *handle == "" {
		return errors.New("usage: plumtree bootstrap -database PATH -handle HANDLE [-device NAME] [-ttl 10m]")
	}
	result, err := Bootstrap(context.Background(), BootstrapConfig{Database: *database, Handle: *handle, DeviceName: *device, TTL: *ttl})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(map[string]any{"bootstrapID": result.ID, "handle": result.Handle, "deviceName": result.DeviceName, "secret": string(result.Secret), "expiresAt": result.ExpiresAt})
}

type ServeConfig struct {
	Database, SSHAddress, HostKeyPath, ServerID, ProductVersion string
	Ready                                                       func(string)
}

// Serve runs the selected native SSH and SQLite assembly until ctx ends.
func Serve(ctx context.Context, cfg ServeConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.ProductVersion) == "" {
		return errors.New("clean server: product version is required")
	}
	repo, err := sqlite.OpenRepository(cfg.Database, nil)
	if err != nil {
		return fmt.Errorf("clean server: open repository: %w", err)
	}
	defer repo.Close()

	signer, hostKeyFingerprint, err := loadOrCreateHostKey(cfg.HostKeyPath)
	if err != nil {
		return fmt.Errorf("clean server: host key: %w", err)
	}
	identity, err := ensureIdentity(ctx, repo, cfg.ServerID, signer, hostKeyFingerprint)
	if err != nil {
		return fmt.Errorf("clean server: identity: %w", err)
	}
	identities, err := newIdentityService(repo, cfg.SSHAddress)
	if err != nil {
		return fmt.Errorf("clean server: identity service: %w", err)
	}
	api, err := v1.New(v1.Config{Repository: repo, Identity: identities, ProductVersion: cfg.ProductVersion})
	if err != nil {
		return fmt.Errorf("clean server: API: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.SSHAddress)
	if err != nil {
		return fmt.Errorf("clean server: SSH listen: %w", err)
	}
	defer listener.Close()

	sshConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{Extensions: map[string]string{
				"plumtree-fingerprint": ssh.FingerprintSHA256(key),
				"plumtree-public-key":  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
			}}, nil
		},
	}
	sshConfig.AddHostKey(signer)
	log.Printf("plumtree server %s listening on %s", identity.ID, listener.Addr())
	if cfg.Ready != nil {
		cfg.Ready(listener.Addr().String())
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) && ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("clean server: accept SSH connection: %w", err)
		}
		go serveConnection(conn, sshConfig, repo, identities, api, identity, cfg.ProductVersion)
	}
}

func serveConnection(raw net.Conn, config *ssh.ServerConfig, repo *sqlite.Repository, identities *identityservice.Service, api *v1.Server, identity sqlite.ServerIdentity, productVersion string) {
	serverConn, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		_ = raw.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	fingerprint := ""
	publicKey := ""
	if serverConn.Permissions != nil && serverConn.Permissions.Extensions != nil {
		fingerprint = serverConn.Permissions.Extensions["plumtree-fingerprint"]
		publicKey = serverConn.Permissions.Extensions["plumtree-public-key"]
	}
	var principal v1.Principal
	if fingerprint != "" {
		device, lookupErr := repo.DeviceByFingerprint(context.Background(), fingerprint)
		if lookupErr != nil && !errors.Is(lookupErr, sqlite.ErrNotFound) {
			_ = serverConn.Close()
			return
		}
		if lookupErr == nil {
			principal = v1.Principal{ServerID: identity.ID, AuthorID: device.AuthorID, DeviceID: device.ID, Fingerprint: device.Fingerprint}
		}
	}
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		go serveSession(channel, channelRequests, identities, api, principal, identity, productVersion, base64.RawStdEncoding.EncodeToString(serverConn.SessionID()), publicKey, fingerprint)
	}
	_ = serverConn.Close()
}

func serveSession(channel ssh.Channel, requests <-chan *ssh.Request, identities *identityservice.Service, api *v1.Server, principal v1.Principal, identity sqlite.ServerIdentity, productVersion, sessionID, publicKey, fingerprint string) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			_ = request.Reply(false, nil)
			continue
		}
		var subsystemRequest struct{ Name string }
		if err := ssh.Unmarshal(request.Payload, &subsystemRequest); err != nil {
			_ = request.Reply(false, nil)
			continue
		}
		subsystem := subsystemRequest.Name
		switch subsystem {
		case transport.ControlSubsystem:
			if principal.DeviceID == "" {
				_ = request.Reply(false, nil)
				return
			}
			_ = request.Reply(true, nil)
			handler := httpHandlerWithPrincipal(api.Handler(), principal)
			_ = transport.ServeHTTPStream(channel, handler, productVersion)
			return
		case transport.PairSubsystem:
			if principal.DeviceID != "" || publicKey == "" || fingerprint == "" {
				_ = request.Reply(false, nil)
				return
			}
			_ = request.Reply(true, nil)
			handler := pairingserver.Handler{Identity: identities, ServerID: identity.ID, HostKeyAlgorithm: identity.SSHHostKeyAlgorithm,
				HostKeyFingerprint: identity.SSHHostKeyFingerprint, ProductVersion: productVersion, SessionID: sessionID,
				CandidatePublicKey: publicKey, CandidateFingerprint: fingerprint}
			_ = handler.Serve(channel)
			return
		default:
			_ = request.Reply(false, nil)
			return
		}
	}
}

func newIdentityService(repo *sqlite.Repository, sshAddress string) (*identityservice.Service, error) {
	cfg := serverconfig.Default()
	cfg.Roles.Control = true
	cfg.Exposure.SSH = serverconfig.ExposureGate{Enabled: true, Address: sshAddress}
	return identityservice.New(repo, cfg)
}

func httpHandlerWithPrincipal(handler http.Handler, principal v1.Principal) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(v1.WithPrincipal(r.Context(), principal)))
	})
}

func ensureIdentity(ctx context.Context, repo *sqlite.Repository, requestedID string, signer ssh.Signer, fingerprint string) (sqlite.ServerIdentity, error) {
	identity, err := repo.ServerIdentity(ctx)
	if err == nil {
		if identity.SSHHostKeyAlgorithm != signer.PublicKey().Type() || identity.SSHHostKeyFingerprint != fingerprint {
			return sqlite.ServerIdentity{}, errors.New("persisted server identity does not match host key")
		}
		return identity, nil
	}
	if !errors.Is(err, sqlite.ErrNotFound) {
		return sqlite.ServerIdentity{}, err
	}
	if requestedID == "" {
		requestedID, err = randomIdentityID()
		if err != nil {
			return sqlite.ServerIdentity{}, err
		}
	}
	identity = sqlite.ServerIdentity{ID: requestedID, SSHHostKeyAlgorithm: signer.PublicKey().Type(), SSHHostKeyFingerprint: fingerprint}
	if err := repo.SetServerIdentity(ctx, identity); err != nil {
		return sqlite.ServerIdentity{}, err
	}
	return identity, nil
}

func randomIdentityID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "server_" + fmt.Sprintf("%x", raw[:]), nil
}

func loadOrCreateHostKey(path string) (ssh.Signer, string, error) {
	if data, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, "", err
		}
		return signer, ssh.FingerprintSHA256(signer.PublicKey()), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "plumtree server host key")
	if err != nil {
		return nil, "", err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, "", err
		}
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		return nil, "", err
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, "", err
	}
	return signer, ssh.FingerprintSHA256(signer.PublicKey()), nil
}
