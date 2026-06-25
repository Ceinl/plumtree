package sshgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
	"golang.org/x/crypto/ssh"
	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/runner"
)

type ptyRequest struct {
	Term              string
	Columns, Rows     uint32
	WidthPx, HeightPx uint32
	Modes             string
}

type windowChange struct {
	Columns, Rows     uint32
	WidthPx, HeightPx uint32
}

func (s *Server) handleSession(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request, app control.App, deploy control.Deploy, wasm []byte, appType string, identity runner.Identity) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu      sync.Mutex
		w, h    = 80, 24
		started bool
	)
	winch := make(chan os.Signal, 1)
	size := func() (int, int) { mu.Lock(); defer mu.Unlock(); return w, h }

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p ptyRequest
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				mu.Lock()
				w, h = int(p.Columns), int(p.Rows)
				mu.Unlock()
			}
			req.Reply(true, nil)
		case "window-change":
			var p windowChange
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				mu.Lock()
				w, h = int(p.Columns), int(p.Rows)
				mu.Unlock()
				select {
				case winch <- syscall.SIGWINCH:
				default:
				}
			}
		case "shell", "exec":
			req.Reply(true, nil)
			if started {
				continue
			}
			started = true
			go s.startSession(ctx, cancel, ch, app, deploy, wasm, appType, identity, size, winch)
		case "env":
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
	cancel()
}

func (s *Server) startSession(ctx context.Context, cancel context.CancelFunc, ch ssh.Channel, app control.App, deploy control.Deploy, wasm []byte, appType string, identity runner.Identity, size func() (int, int), winch chan os.Signal) {
	if !s.acquireSlot() {
		s.logf("reject %q: runner at capacity (%d sessions)", app.Name, s.MaxConcurrentSessions)
		fmt.Fprintf(ch.Stderr(), "the runner is at capacity; try again shortly\r\n")
		ch.Close()
		cancel()
		return
	}
	defer s.releaseSlot()

	session, err := s.Store.StartSession(app.ID, deploy.ID)
	if err != nil {
		s.logf("start session %q: %v", app.Name, err)
		msg := "session unavailable; try again later"
		if errors.Is(err, control.ErrQuota) {
			msg = "this app has reached its daily connection limit; try again later"
		}
		fmt.Fprintf(ch.Stderr(), "%s\r\n", msg)
		ch.Close()
		cancel()
		return
	}
	s.sessions.add(session.ID, sessionEntry{
		ownerID:  app.OwnerID,
		appID:    app.ID,
		deployID: deploy.ID,
		cancel:   cancel,
	})
	defer s.sessions.remove(session.ID)

	caps := s.capsFor(app)
	caps.Auth = runner.StaticAuth{Identity: identity}
	log, truncated := s.runSession(ctx, ch, wasm, appType, caps, size, winch)
	if _, err := s.Store.RecordSessionLog(session.ID, log, truncated); err != nil {
		s.logf("record session log %q: %v", session.ID, err)
	}
	if _, err := s.Store.EndSession(session.ID); err != nil {
		s.logf("end session %q: %v", session.ID, err)
	}
	ch.Close()
	cancel()
}

func (s *Server) runSession(ctx context.Context, ch ssh.Channel, wasm []byte, appType string, caps runner.Capabilities, size func() (int, int), winch chan os.Signal) (string, bool) {
	lim := s.Limits
	if lim.MemoryPages == 0 {
		lim = runner.DefaultLimits
	}
	if appType == "cli" {
		if err := s.Runner.RunCLI(ctx, wasm, lim, caps, nil, ch); err != nil {
			fmt.Fprintf(ch.Stderr(), "app error: %v\r\n", err)
		}
		return "", false
	}

	w, h := size()
	if w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	io.WriteString(ch, terminal.HIDE_CURSOR+terminal.OPEN_ALT+terminal.CLEAR_SCREEN+terminal.MOVE_CURSOR)
	defer io.WriteString(ch, terminal.SHOW_CURSOR+terminal.CLOSE_ALT)

	src := &runner.TTYSource{
		Keys:    keyboard.ListenReader(ctx, ch),
		Winch:   winch,
		Refresh: runner.DefaultRefresh,
		Size:    size,
	}
	sink := runner.NewTTYSinkWriter(w, h, s.MaxFPS, ch)

	logs := newCapWriter(maxSessionLogBytes)
	err := s.Runner.Run(ctx, wasm, lim, caps, src, sink, logs)
	switch {
	case err == nil, errors.Is(err, context.Canceled):
	default:
		s.logf("session error: %v", err)
	}
	return logs.String(), logs.truncated
}
