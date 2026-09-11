package gateway

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// VMGuest describes one persistent guest VM backing a vm-type app. The mapping
// file is operator-managed JSON keyed by app ID:
//
//	{"app_abc123": {"addr": "127.0.0.1:2223", "user": "vm",
//	  "keyFile": "/home/dima/plumtree/vms/vm/guest_ed25519",
//	  "hostKey": "ssh-ed25519 AAAA..."}}
//
// The file is re-read on every VM session so operators can add or rotate
// guests without restarting the gateway. A missing file means no VM backend is
// configured; sessions fail closed.
type VMGuest struct {
	Addr    string `json:"addr"`
	User    string `json:"user"`
	KeyFile string `json:"keyFile"`
	HostKey string `json:"hostKey"`
}

const (
	maxVMGuestFileBytes = 1 << 20
	vmGuestDialTimeout  = 10 * time.Second
)

// vmFailureClass keeps the leaf-facing message specific without leaking
// operator paths or key material: not-configured vs unmapped vs unreachable.
var (
	errVMNotConfigured = errors.New("gateway: vm backend is not configured")
	errVMMisconfigured = errors.New("gateway: vm backend is misconfigured")
	errVMUnmapped      = errors.New("gateway: vm backend has no guest for this app")
	errVMUnreachable   = errors.New("gateway: vm guest is unreachable")
)

// loadVMGuests parses the operator's guest mapping. Unknown fields are
// rejected so typos fail loudly instead of silently misrouting sessions.
func loadVMGuests(path string) (map[string]VMGuest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxVMGuestFileBytes {
		return nil, fmt.Errorf("gateway: vm guest file exceeds %d bytes", maxVMGuestFileBytes)
	}
	var guests map[string]VMGuest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&guests); err != nil {
		return nil, fmt.Errorf("gateway: invalid vm guest file: %w", err)
	}
	if guests == nil {
		guests = map[string]VMGuest{}
	}
	return guests, nil
}

func (g VMGuest) validate() error {
	if g.Addr == "" || strings.ContainsAny(g.Addr, " \t\r\n") {
		return fmt.Errorf("gateway: vm guest addr is required")
	}
	if g.User == "" || strings.ContainsAny(g.User, " \t\r\n/") {
		return fmt.Errorf("gateway: vm guest user is required")
	}
	if g.KeyFile == "" {
		return fmt.Errorf("gateway: vm guest keyFile is required")
	}
	if g.HostKey == "" {
		return fmt.Errorf("gateway: vm guest hostKey is required")
	}
	return nil
}

// lookupVMGuest resolves the guest backing one app ID through the configured
// mapping file. Every error fails closed: the caller renders the session
// unavailable rather than dialing a default.
func (s *Server) lookupVMGuest(appID string) (VMGuest, error) {
	if s.vmGuestFile == "" {
		return VMGuest{}, errVMNotConfigured
	}
	guests, err := loadVMGuests(s.vmGuestFile)
	if err != nil {
		return VMGuest{}, fmt.Errorf("%w: %v", errVMMisconfigured, err)
	}
	guest, ok := guests[appID]
	if !ok {
		return VMGuest{}, errVMUnmapped
	}
	if err := guest.validate(); err != nil {
		return VMGuest{}, fmt.Errorf("%w: %v", errVMMisconfigured, err)
	}
	return guest, nil
}

// dialVMGuest opens an authenticated SSH client to the guest, verifying the
// pinned host key. The private key never leaves the server process.
func dialVMGuest(guest VMGuest) (*ssh.Client, error) {
	keyPEM, err := os.ReadFile(guest.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("gateway: read vm guest key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("gateway: parse vm guest key: %w", err)
	}
	fields := strings.Fields(guest.HostKey)
	if len(fields) < 2 {
		return nil, fmt.Errorf("gateway: malformed vm guest hostKey")
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(guest.HostKey))
	if err != nil {
		return nil, fmt.Errorf("gateway: parse vm guest hostKey: %w", err)
	}
	config := &ssh.ClientConfig{
		User:            guest.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
		// Restrict negotiation to the pinned key's algorithm: the guest
		// offers several host keys and the handshake would otherwise settle
		// on the first mutually supported one, failing the pin.
		HostKeyAlgorithms: []string{hostKey.Type()},
		Timeout:           vmGuestDialTimeout,
	}
	client, err := ssh.Dial("tcp", guest.Addr, config)
	if err != nil {
		return nil, fmt.Errorf("gateway: dial vm guest: %w", err)
	}
	return client, nil
}

// decodeTerminalModes converts a raw SSH pty-req modes blob into terminal
// modes for the guest pty. Malformed tails are dropped; an empty result
// requests the guest default modes.
func decodeTerminalModes(raw string) ssh.TerminalModes {
	modes := ssh.TerminalModes{}
	data := []byte(raw)
	for len(data) >= 5 {
		op := data[0]
		if op == 0 { // TTY_OP_END
			break
		}
		value := binary.BigEndian.Uint32(data[1:5])
		modes[op] = value
		data = data[5:]
	}
	return modes
}

// runVMSession bridges one leaf SSH channel to the persistent guest VM backing
// the app: an interactive shell when the leaf asked for "shell", otherwise the
// leaf's exec command run through the guest shell. It reports the bounded
// session transcript and the guest exit status, mirroring the WASM path so
// accounting and logging stay uniform.
func (s *Server) runVMSession(ctx context.Context, ch ssh.Channel, run Runnable, leaf leafChannel, size func() (int, int), winch chan os.Signal) (string, bool, uint32) {
	guest, err := s.lookupVMGuest(run.AppID)
	if err != nil {
		s.logf("vm session app=%q: %v", run.AppName, err)
		fmt.Fprintf(ch.Stderr(), "vm backend is not configured for app %q\r\n", run.AppName)
		return "", false, 1
	}
	client, err := dialVMGuest(guest)
	if err != nil {
		s.logf("vm session app=%q: %v", run.AppName, err)
		fmt.Fprintf(ch.Stderr(), "%v\r\n", errVMUnreachable)
		return "", false, 1
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		s.logf("vm session app=%q: new guest session: %v", run.AppName, err)
		fmt.Fprintf(ch.Stderr(), "%v\r\n", errVMUnreachable)
		return "", false, 1
	}
	defer sess.Close()

	for name, value := range leaf.env {
		_ = sess.Setenv(name, value)
	}

	logs := newCapWriter(maxSessionLogBytes)
	sess.Stdout = io.MultiWriter(ch, logs)
	sess.Stderr = io.MultiWriter(ch.Stderr(), logs)
	stdin, err := sess.StdinPipe()
	if err != nil {
		s.logf("vm session app=%q: guest stdin: %v", run.AppName, err)
		fmt.Fprintf(ch.Stderr(), "%v\r\n", errVMUnreachable)
		return "", false, 1
	}
	go func() {
		_, _ = io.Copy(stdin, ch)
		_ = stdin.Close()
	}()

	if leaf.pty {
		w, h := 80, 24
		if size != nil {
			if sw, sh := size(); sw > 0 && sh > 0 {
				w, h = sw, sh
			}
		}
		term := leaf.term
		if term == "" {
			term = "xterm-256color"
		}
		if err := sess.RequestPty(term, h, w, decodeTerminalModes(leaf.modes)); err != nil {
			s.logf("vm session app=%q: guest pty: %v", run.AppName, err)
			fmt.Fprintf(ch.Stderr(), "%v\r\n", errVMUnreachable)
			return logs.String(), logs.truncated, 1
		}
		if winch != nil {
			watchDone := make(chan struct{})
			defer close(watchDone)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-watchDone:
						return
					case _, ok := <-winch:
						if !ok {
							return
						}
						if nw, nh := size(); nw > 0 && nh > 0 {
							_ = sess.WindowChange(nh, nw)
						}
					}
				}
			}()
		}
	}

	if leaf.exec {
		err = sess.Start(leaf.cmd)
	} else {
		err = sess.Shell()
	}
	if err != nil {
		s.logf("vm session app=%q: guest start: %v", run.AppName, err)
		fmt.Fprintf(ch.Stderr(), "%v\r\n", errVMUnreachable)
		return logs.String(), logs.truncated, 1
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- sess.Wait() }()
	var waitErr error
	select {
	case <-ctx.Done():
		_ = sess.Close()
		waitErr = <-waitDone
	case waitErr = <-waitDone:
	}
	status := uint32(0)
	if waitErr != nil {
		status = 1
		var exitErr *ssh.ExitError
		if errors.As(waitErr, &exitErr) {
			status = uint32(exitErr.ExitStatus())
		} else {
			s.logf("vm session app=%q: guest wait: %v", run.AppName, waitErr)
		}
	}
	return logs.String(), logs.truncated, status
}
