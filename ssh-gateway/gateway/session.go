package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/Ceinl/plumtree/runner"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/ssh-gateway/gatewayapi"
	"github.com/Ceinl/plumtree/tui-runtime/keyboard"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
	"github.com/Ceinl/plumtree/tui-runtime/terminal"
	"golang.org/x/crypto/ssh"
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

type execRequest struct{ Command string }

func (s *Server) handleSession(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request, handle string, identity runner.Identity) {
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
			p, err := parsePTYRequest(req.Payload)
			if err != nil {
				req.Reply(false, nil)
				continue
			}
			mu.Lock()
			w, h = int(p.Columns), int(p.Rows)
			mu.Unlock()
			req.Reply(true, nil)
		case "window-change":
			p, err := parseWindowChange(req.Payload)
			if err != nil {
				req.Reply(false, nil)
				continue
			}
			mu.Lock()
			w, h = int(p.Columns), int(p.Rows)
			mu.Unlock()
			select {
			case winch <- syscall.SIGWINCH:
			default:
			}
			req.Reply(true, nil)
		case "shell", "exec":
			var args []string
			if req.Type == "exec" {
				var payload execRequest
				if len(req.Payload) > 4+abi.ActionMaxCommand || ssh.Unmarshal(req.Payload, &payload) != nil {
					req.Reply(false, nil)
					continue
				}
				var err error
				args, err = gatewayapi.ParseExecCommand(payload.Command)
				if err != nil {
					req.Reply(true, nil)
					_ = json.NewEncoder(ch).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "invalid_request", "message": err.Error()}})
					ch.Close()
					cancel()
					return
				}
			}
			req.Reply(true, nil)
			if started {
				continue
			}
			started = true
			go s.startSessionArgs(ctx, cancel, ch, handle, identity, size, winch, args)
		case "env":
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
	cancel()
}

func parsePTYRequest(payload []byte) (ptyRequest, error) {
	var p ptyRequest
	if err := ssh.Unmarshal(payload, &p); err != nil {
		return p, fmt.Errorf("invalid pty request: %w", err)
	}
	if err := validateRequestDimensions(p.Columns, p.Rows); err != nil {
		return p, err
	}
	return p, nil
}

func parseWindowChange(payload []byte) (windowChange, error) {
	var p windowChange
	if err := ssh.Unmarshal(payload, &p); err != nil {
		return p, fmt.Errorf("invalid window-change request: %w", err)
	}
	if err := validateRequestDimensions(p.Columns, p.Rows); err != nil {
		return p, err
	}
	return p, nil
}

func validateRequestDimensions(columns, rows uint32) error {
	// Compare as uint32 before converting to int so this remains safe on 32-bit
	// builds as well as the current 64-bit gateway targets.
	if columns < screen.MinWidth || columns > screen.MaxWidth ||
		rows < screen.MinHeight || rows > screen.MaxHeight ||
		uint64(columns)*uint64(rows) > screen.MaxCells {
		return fmt.Errorf("terminal dimensions %dx%d outside allowed range", columns, rows)
	}
	return nil
}

func (s *Server) startSession(ctx context.Context, cancel context.CancelFunc, ch ssh.Channel, handle string, identity runner.Identity, size func() (int, int), winch chan os.Signal) {
	s.startSessionArgs(ctx, cancel, ch, handle, identity, size, winch, nil)
}

func (s *Server) startSessionArgs(ctx context.Context, cancel context.CancelFunc, ch ssh.Channel, handle string, identity runner.Identity, size func() (int, int), winch chan os.Signal, args []string) {
	if !s.acquireSlot() {
		s.logf("reject %q: runner at capacity (%d sessions)", handle, s.MaxConcurrentSessions)
		fmt.Fprintf(ch.Stderr(), "the runner is at capacity; try again shortly\r\n")
		ch.Close()
		cancel()
		return
	}
	defer s.releaseSlot()

	// Artifact resolution can materialize a large WASM blob. Do it only after
	// runner capacity is reserved, and keep the result scoped to this session
	// rather than to the longer-lived SSH connection.
	run, err := s.Backend.ResolveRunnable(handle)
	if err != nil {
		s.logf("resolve %q failed: %v", handle, err)
		msg := fmt.Sprintf("app %q is not available", handle)
		if errors.Is(err, ErrSuspended) {
			msg = fmt.Sprintf("app %q is temporarily unavailable (suspended)", handle)
		}
		fmt.Fprintf(ch.Stderr(), "%s\r\n", msg)
		ch.Close()
		cancel()
		return
	}
	identity = appRelativeIdentity(identity, run.OwnerID)

	sessionID, err := s.Backend.StartSession(run.AppID, run.DeployID)
	if err != nil {
		s.logf("start session %q: %v", run.AppName, err)
		msg := "session unavailable; try again later"
		if errors.Is(err, ErrSuspended) {
			msg = fmt.Sprintf("app %q is temporarily unavailable (suspended)", handle)
		} else if errors.Is(err, ErrQuota) {
			msg = "this app has reached its daily connection limit; try again later"
		}
		fmt.Fprintf(ch.Stderr(), "%s\r\n", msg)
		ch.Close()
		cancel()
		return
	}
	s.sessions.add(sessionID, sessionEntry{
		ownerID:  run.OwnerID,
		appID:    run.AppID,
		deployID: run.DeployID,
		cancel:   cancel,
	})
	defer s.sessions.remove(sessionID)

	caps := s.capsFor(run.AppID, run.OwnerID)
	caps.Auth = runner.StaticAuth{Identity: identity}
	log, truncated := s.runSessionArgs(ctx, ch, run.WASM, run.AppType, caps, size, winch, args)
	if err := s.Backend.RecordSessionLog(sessionID, log, truncated); err != nil {
		s.logf("record session log %q: %v", sessionID, err)
	}
	if err := s.Backend.EndSession(sessionID); err != nil {
		s.logf("end session %q: %v", sessionID, err)
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
	ch.Close()
	cancel()
}

func appRelativeIdentity(identity runner.Identity, appOwnerID string) runner.Identity {
	identity.OwnsApp = identity.OwnerID != "" && identity.OwnerID == appOwnerID
	identity.OwnerID = ""
	return identity
}

func (s *Server) runSession(ctx context.Context, ch ssh.Channel, wasm []byte, appType string, caps runner.Capabilities, size func() (int, int), winch chan os.Signal) (string, bool) {
	return s.runSessionArgs(ctx, ch, wasm, appType, caps, size, winch, nil)
}

func (s *Server) runSessionArgs(ctx context.Context, ch ssh.Channel, wasm []byte, appType string, caps runner.Capabilities, size func() (int, int), winch chan os.Signal, args []string) (string, bool) {
	lim := s.Limits
	if lim.MemoryPages == 0 {
		lim = runner.DefaultLimits
	}
	if appType == "cli" || len(args) > 0 {
		var err error
		if isolated := s.isolatedRunner(); isolated != nil {
			err = isolated.RunCLI(ctx, wasm, lim, caps, args, ch)
		} else {
			err = s.Runner.RunCLI(ctx, wasm, lim, caps, args, ch)
		}
		if err != nil {
			fmt.Fprintf(ch.Stderr(), "app error: %s\r\n", runner.SanitizeTerminalText(err.Error()))
		}
		return "", false
	}

	w, h := size()
	if w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	io.WriteString(ch, terminal.HIDE_CURSOR+terminal.OPEN_ALT+terminal.ENABLE_MOUSE+terminal.CLEAR_SCREEN+terminal.MOVE_CURSOR)
	defer io.WriteString(ch, terminal.DISABLE_MOUSE+terminal.SHOW_CURSOR+terminal.CLOSE_ALT)

	src := &runner.TTYSource{
		Keys:    keyboard.ListenReader(ctx, ch),
		Winch:   winch,
		Refresh: runner.DefaultRefresh,
		Size:    size,
	}
	sink := runner.NewTTYSinkWriter(w, h, s.MaxFPS, ch)

	logs := newCapWriter(maxSessionLogBytes)
	// When a worker binary is configured, isolate the WASM sandbox in a separate
	// process; otherwise run it in-process with the shared compilation cache.
	var err error
	if isolated := s.isolatedRunner(); isolated != nil {
		err = isolated.Run(ctx, wasm, lim, caps, src, sink, logs)
	} else {
		err = s.Runner.Run(ctx, wasm, lim, caps, src, sink, logs)
	}
	switch {
	case err == nil, errors.Is(err, context.Canceled):
	default:
		s.logf("session error: %v", err)
	}
	return logs.String(), logs.truncated
}

func (s *Server) isolatedRunner() *runner.ProcessRunner {
	if s.RunnerEndpoint != "" {
		return runner.NewRemoteProcessRunner(s.RunnerEndpoint, s.RunnerToken)
	}
	if s.RunnerWorker != "" {
		return runner.NewProcessRunner(s.RunnerWorker)
	}
	return nil
}
