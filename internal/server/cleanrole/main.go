// Package cleanrole is the selected root server assembly. It owns the
// encrypted SQLite repository and publishes the clean API only through the
// authenticated SSH control subsystem.
package cleanrole

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ceinl/plumtree/internal/httpapi/v1"
	"github.com/Ceinl/plumtree/internal/sqlite"
	"github.com/Ceinl/plumtree/internal/transport"
	"golang.org/x/crypto/ssh"
)

const defaultProductVersion = "dev"

// Run starts the one-process native server. The process exposes no HTTP,
// gateway, runner, or bearer-token listener; all API traffic is carried over
// the authenticated plumtree-control-v1 SSH subsystem.
func Run(args []string) {
	if err := run(args); err != nil {
		log.Print(err)
		if !errors.Is(err, flag.ErrHelp) {
			return
		}
	}
}

func run(args []string) error {
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

	ctx := context.Background()
	repo, err := sqlite.OpenRepository(*database, nil)
	if err != nil {
		return fmt.Errorf("clean server: open repository: %w", err)
	}
	defer repo.Close()

	signer, hostKeyFingerprint, err := loadOrCreateHostKey(*hostKeyPath)
	if err != nil {
		return fmt.Errorf("clean server: host key: %w", err)
	}
	identity, err := ensureIdentity(ctx, repo, *serverID, signer, hostKeyFingerprint)
	if err != nil {
		return fmt.Errorf("clean server: identity: %w", err)
	}
	api, err := v1.New(v1.Config{Repository: repo, ProductVersion: *productVersion})
	if err != nil {
		return fmt.Errorf("clean server: API: %w", err)
	}
	listener, err := net.Listen("tcp", *sshAddr)
	if err != nil {
		return fmt.Errorf("clean server: SSH listen: %w", err)
	}
	defer listener.Close()

	sshConfig := &ssh.ServerConfig{
		NoClientAuth: true,
		PublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey) (*ssh.Permissions, error) {
			return &ssh.Permissions{}, nil
		},
		VerifiedPublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey, permissions *ssh.Permissions, _ string) (*ssh.Permissions, error) {
			if permissions == nil {
				return nil, errors.New("clean server: missing SSH permissions")
			}
			if permissions.Extensions == nil {
				permissions.Extensions = make(map[string]string)
			}
			permissions.Extensions["plumtree-fingerprint"] = ssh.FingerprintSHA256(key)
			return permissions, nil
		},
	}
	sshConfig.AddHostKey(signer)
	log.Printf("plumtree server %s listening on %s", identity.ID, listener.Addr())
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("clean server: accept SSH connection: %w", err)
		}
		go serveConnection(conn, sshConfig, repo, api, identity, *productVersion)
	}
}

func serveConnection(raw net.Conn, config *ssh.ServerConfig, repo *sqlite.Repository, api *v1.Server, identity sqlite.ServerIdentity, productVersion string) {
	serverConn, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		_ = raw.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	fingerprint := ""
	if serverConn.Permissions != nil && serverConn.Permissions.Extensions != nil {
		fingerprint = serverConn.Permissions.Extensions["plumtree-fingerprint"]
	}
	var principal v1.Principal
	if fingerprint != "" {
		device, lookupErr := repo.DeviceByFingerprint(context.Background(), fingerprint)
		if lookupErr != nil {
			_ = serverConn.Close()
			return
		}
		principal = v1.Principal{ServerID: identity.ID, AuthorID: device.AuthorID, DeviceID: device.ID, Fingerprint: device.Fingerprint}
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
		go serveSession(channel, channelRequests, api, principal, productVersion)
	}
	_ = serverConn.Close()
}

func serveSession(channel ssh.Channel, requests <-chan *ssh.Request, api *v1.Server, principal v1.Principal, productVersion string) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "subsystem" {
			_ = request.Reply(false, nil)
			continue
		}
		var subsystem string
		if err := ssh.Unmarshal(request.Payload, &subsystem); err != nil || subsystem != transport.ControlSubsystem {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		if principal.DeviceID == "" {
			return
		}
		handler := httpHandlerWithPrincipal(api.Handler(), principal)
		_ = transport.ServeHTTPStream(channel, handler, productVersion)
		return
	}
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
