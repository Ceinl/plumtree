package runner

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"
)

const (
	brokerMagic       = "PTRUNNER1"
	maxBrokerToken    = 4096
	brokerAuthTimeout = 5 * time.Second
)

// Broker accepts authenticated gateway connections and gives each one a fresh
// runner-worker subprocess. It deliberately does not understand procproto: it
// only bridges the connection to the disposable worker's stdin/stdout.
// Production runs the broker in a separate, networkless container.
type Broker struct {
	WorkerPath  string
	Token       string
	MaxSessions int
	// WorkerUIDBase assigns each concurrent worker its own UID/GID at
	// WorkerUIDBase+slot. Zero keeps the broker's identity (local/test mode).
	// Production sets this and runs the minimal broker with SETUID/SETGID only.
	WorkerUIDBase uint32
	ScratchRoot   string
	Logf          func(format string, args ...any)
	slots         chan int
}

// Serve accepts sessions until ctx is canceled or the listener fails.
func (b *Broker) Serve(ctx context.Context, ln net.Listener) error {
	if b.WorkerPath == "" {
		return errors.New("runner broker: worker path is required")
	}
	if b.Token == "" {
		return errors.New("runner broker: token is required")
	}
	if b.WorkerUIDBase > 0 && b.MaxSessions <= 0 {
		return errors.New("runner broker: per-session worker UIDs require a positive max-sessions")
	}
	if b.WorkerUIDBase > 0 && uint64(b.WorkerUIDBase)+uint64(b.MaxSessions-1) > uint64(^uint32(0)) {
		return errors.New("runner broker: per-session worker UID range overflows")
	}
	if b.MaxSessions > 0 {
		b.slots = make(chan int, b.MaxSessions)
		for slot := range b.MaxSessions {
			b.slots <- slot
		}
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		slot, ok := b.acquire()
		if !ok {
			_ = conn.Close()
			continue
		}
		go func() {
			defer b.release(slot)
			b.serveConn(ctx, conn, slot)
		}()
	}
}

func (b *Broker) serveConn(ctx context.Context, conn net.Conn, slot int) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(brokerAuthTimeout))
	if err := readBrokerAuth(conn, b.Token); err != nil {
		b.logf("reject broker connection: %v", err)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	scratchRoot := b.ScratchRoot
	if scratchRoot == "" {
		scratchRoot = os.TempDir()
	}
	workDir, err := os.MkdirTemp(scratchRoot, "session-*")
	if err != nil {
		b.logf("create worker scratch: %v", err)
		return
	}
	defer os.RemoveAll(workDir)
	uid, gid := uint32(0), uint32(0)
	if b.WorkerUIDBase > 0 {
		uid = b.WorkerUIDBase + uint32(slot)
		gid = uid
		if err := os.Chown(workDir, int(uid), int(gid)); err != nil {
			b.logf("chown worker scratch: %v", err)
			return
		}
	}
	if err := os.Chmod(workDir, 0o700); err != nil {
		b.logf("chmod worker scratch: %v", err)
		return
	}

	cmd := exec.CommandContext(ctx, b.WorkerPath)
	cmd.Dir = workDir
	cmd.Env = []string{
		"HOME=" + workDir,
		"TMPDIR=" + workDir,
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
	if err := configureBrokerWorker(cmd, uid, gid); err != nil {
		b.logf("configure worker identity: %v", err)
		return
	}
	cmd.Stdin = conn
	cmd.Stdout = conn
	stderr := &boundedBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil && ctx.Err() == nil {
		b.logf("worker exited: %v: %s", err, stderr.String())
	}
}

func (b *Broker) acquire() (int, bool) {
	if b.slots == nil {
		return -1, true
	}
	select {
	case slot := <-b.slots:
		return slot, true
	default:
		return 0, false
	}
}

func (b *Broker) release(slot int) {
	if b.slots != nil {
		b.slots <- slot
	}
}

func (b *Broker) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
}

func writeBrokerAuth(w io.Writer, token string) error {
	if len(token) == 0 || len(token) > maxBrokerToken {
		return errors.New("invalid broker token length")
	}
	b := make([]byte, 0, len(brokerMagic)+2+len(token))
	b = append(b, brokerMagic...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(token)))
	b = append(b, token...)
	_, err := w.Write(b)
	return err
}

func readBrokerAuth(r io.Reader, expected string) error {
	var header [len(brokerMagic) + 2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	if string(header[:len(brokerMagic)]) != brokerMagic {
		return errors.New("invalid broker handshake")
	}
	n := int(binary.BigEndian.Uint16(header[len(brokerMagic):]))
	if n == 0 || n > maxBrokerToken {
		return errors.New("invalid broker token length")
	}
	token := make([]byte, n)
	if _, err := io.ReadFull(r, token); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(token, []byte(expected)) != 1 {
		return fmt.Errorf("invalid broker token")
	}
	return nil
}
