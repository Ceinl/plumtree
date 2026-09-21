package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testKeypair(t *testing.T) (ed25519.PrivateKey, ssh.Signer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = pub
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return priv, signer
}

func writePrivateKeyFile(t *testing.T, dir string, priv ed25519.PrivateKey) string {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(block)
	if _, err := ssh.ParsePrivateKey(encoded); err != nil {
		t.Fatalf("written key does not parse: %v", err)
	}
	path := filepath.Join(dir, "guest_ed25519")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func sshKeyBase64(t *testing.T, signer ssh.Signer) string {
	t.Helper()
	fields := strings.Fields(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))))
	if len(fields) < 2 {
		t.Fatal("bad authorized key")
	}
	return fields[1]
}

func TestLoadVMGuests(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vmguests.json")
	doc := `{"app-1": {"addr": "127.0.0.1:2223", "user": "vm", "keyFile": "/keys/guest", "hostKey": "ssh-ed25519 AAAA"}}`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	guests, err := loadVMGuests(path)
	if err != nil {
		t.Fatalf("loadVMGuests: %v", err)
	}
	got, ok := guests["app-1"]
	if !ok || got.Addr != "127.0.0.1:2223" || got.User != "vm" {
		t.Fatalf("guests = %v", guests)
	}
	if err := got.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"app-1": {"addr": "x", "bogus": 1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadVMGuests(bad); err == nil {
		t.Fatal("unknown field should fail")
	}
	if _, err := loadVMGuests(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file should fail")
	}
	if _, err := loadVMGuests(""); err == nil {
		t.Fatal("empty path should fail")
	}
}

func TestDecodeTerminalModes(t *testing.T) {
	// ECHO(53)=1, TTY_OP_END(0).
	raw := string([]byte{53, 0, 0, 0, 1, 0})
	modes := decodeTerminalModes(raw)
	if modes[53] != 1 {
		t.Fatalf("modes = %v", modes)
	}
	if len(decodeTerminalModes("xy")) != 0 {
		t.Fatal("short blob should decode empty")
	}
	if len(decodeTerminalModes("")) != 0 {
		t.Fatal("empty blob should decode empty")
	}
}

// stubGuestServer runs a minimal SSH server behaving like a guest VM: exec
// prints canned output, shell reads one line and echoes it back.
func stubGuestServer(t *testing.T, hostSigner, userSigner ssh.Signer) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wantKey := string(userSigner.PublicKey().Marshal())
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				cfg := &ssh.ServerConfig{
					PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
						if string(key.Marshal()) != wantKey {
							return nil, fmt.Errorf("unknown key")
						}
						return nil, nil
					},
				}
				cfg.AddHostKey(hostSigner)
				sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sconn.Close()
				go ssh.DiscardRequests(reqs)
				for newCh := range chans {
					if newCh.ChannelType() != "session" {
						_ = newCh.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, reqs, err := newCh.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer ch.Close()
						for req := range reqs {
							switch req.Type {
							case "pty-req", "env", "window-change":
								_ = req.Reply(true, nil)
							case "exec":
								_ = req.Reply(true, nil)
								_, _ = io.WriteString(ch, "guest-output\n")
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							case "shell":
								_ = req.Reply(true, nil)
								line, err := readLine(ch)
								if err != nil {
									return
								}
								_, _ = fmt.Fprintf(ch, "shell saw: %s\n", line)
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							default:
								_ = req.Reply(false, nil)
							}
						}
					}()
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func readLine(r io.Reader) (string, error) {
	var out strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimRight(out.String(), "\r"), nil
			}
			out.WriteByte(buf[0])
		}
		if err != nil {
			return out.String(), err
		}
	}
}

// leafChannelPair returns the client side of a live session channel (the side
// runVMSession bridges) and the server side for feeding stdin. It uses
// loopback TCP: a synchronous in-memory pipe would deadlock the SSH version
// exchange because both sides write before reading.
func leafChannelPair(t *testing.T) (clientCh, serverCh ssh.Channel) {
	t.Helper()
	_, hostSigner := testKeypair(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan ssh.Channel, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		cfg := &ssh.ServerConfig{NoClientAuth: true}
		cfg.AddHostKey(hostSigner)
		sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			return
		}
		defer sconn.Close()
		go ssh.DiscardRequests(reqs)
		for newCh := range chans {
			ch, reqs, err := newCh.Accept()
			if err != nil {
				return
			}
			accepted <- ch
			go ssh.DiscardRequests(reqs)
		}
	}()
	ccfg := &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.FixedHostKey(hostSigner.PublicKey()), Timeout: 5 * time.Second}
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client, _, _, err := ssh.NewClientConn(conn, "test", ccfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ch, _, err := client.OpenChannel("session", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case sch := <-accepted:
		return ch, sch
	case <-time.After(5 * time.Second):
		t.Fatal("leaf channel not accepted")
		return nil, nil
	}
}

func writeGuestFile(t *testing.T, dir, appID, addr, user, keyFile, hostKey string) string {
	t.Helper()
	doc := fmt.Sprintf(`{%q: {"addr": %q, "user": %q, "keyFile": %q, "hostKey": %q}}`, appID, addr, user, keyFile, hostKey)
	path := filepath.Join(dir, "vmguests.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testVMServer(t *testing.T, appID string) (*Server, func()) {
	t.Helper()
	_, hostSigner := testKeypair(t)
	userPriv, userSigner := testKeypair(t)
	addr, stop := stubGuestServer(t, hostSigner, userSigner)
	dir := t.TempDir()
	keyFile := writePrivateKeyFile(t, dir, userPriv)
	hostKeyLine := "ssh-ed25519 " + sshKeyBase64(t, hostSigner)
	guestFile := writeGuestFile(t, dir, appID, addr, "vm", keyFile, hostKeyLine)
	return &Server{vmGuestFile: guestFile}, stop
}

func TestRunVMSessionExec(t *testing.T) {
	s, stop := testVMServer(t, "app-1")
	defer stop()

	leafCh, _ := leafChannelPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	log, truncated, status := s.runVMSession(ctx, leafCh, Runnable{AppID: "app-1", AppName: "vm"},
		leafChannel{exec: true, cmd: "run-tests"}, nil, nil)
	if status != 0 || truncated {
		t.Fatalf("exec = log %q truncated=%v status=%d", log, truncated, status)
	}
	if !strings.Contains(log, "guest-output") {
		t.Fatalf("log = %q", log)
	}
}

func TestRunVMSessionShell(t *testing.T) {
	s, stop := testVMServer(t, "app-1")
	defer stop()

	leafCh, serverCh := leafChannelPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan struct{})
	var log string
	var status uint32
	go func() {
		defer close(done)
		var truncated bool
		log, truncated, status = s.runVMSession(ctx, leafCh, Runnable{AppID: "app-1", AppName: "vm"},
			leafChannel{pty: true, term: "xterm"}, func() (int, int) { return 80, 24 }, nil)
		_ = truncated
	}()
	// Feed one shell line through the leaf channel; the stub guest echoes it.
	if _, err := io.WriteString(serverCh, "hello-shell\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("shell session did not finish")
	}
	if status != 0 {
		t.Fatalf("shell status = %d log = %q", status, log)
	}
	if !strings.Contains(log, "shell saw: hello-shell") {
		t.Fatalf("log = %q", log)
	}
}

func TestRunVMSessionUnmapped(t *testing.T) {
	s := &Server{vmGuestFile: filepath.Join(t.TempDir(), "missing.json")}
	leafCh, _ := leafChannelPair(t)
	log, _, status := s.runVMSession(context.Background(), leafCh,
		Runnable{AppID: "nope", AppName: "vm"}, leafChannel{exec: true, cmd: "x"}, nil, nil)
	if status == 0 || log != "" {
		t.Fatalf("unmapped = log %q status %d", log, status)
	}
}
