package terminal

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// TmuxKeys manages tmux's extended-keys option for the lifetime of the app.
// plums asks the terminal to distinguish modified keys like Shift+Enter (CSI-u
// / modifyOtherKeys), but tmux only forwards those to the application when
// extended-keys is enabled; with the common default of "off" Shift+Enter
// arrives as a bare Enter, which inserts a newline instead of submitting in
// split layout. EnableTmuxExtendedKeys turns it on at startup and Restore puts
// the prior value back on exit.
//
// We scope the option to the current tmux session (`set` without `-s`) rather
// than to the server (`set -s`). The server-wide option is a single shared
// global: if two app instances run in the same tmux server, the second to
// start records the first's value as "previous" and on exit clobbers it,
// breaking key reporting for the still-running instance. Session scope keeps
// extended-keys working for this app while isolating each instance's save and
// restore. The tradeoff is purely the scope of the toggle; session scope is
// sufficient because the panes this app runs in belong to the current session.
type TmuxKeys struct {
	active   bool
	previous string
}

// EnableTmuxExtendedKeys enables tmux extended-keys when running inside tmux and
// the option is not already on. It returns a handle whose Restore method undoes
// the change; both are no-ops outside tmux or when tmux is unavailable.
func EnableTmuxExtendedKeys() *TmuxKeys {
	keys := &TmuxKeys{}
	if os.Getenv("TMUX") == "" {
		return keys
	}
	showCtx, showCancel := context.WithTimeout(context.Background(), 2*time.Second)
	current := tmuxShowExtendedKeys(showCtx)
	showCancel()
	keys.previous = current
	// "on" forwards extended keys to apps that request them; "always" forwards
	// unconditionally. Either already satisfies us, so only act on "off"/unset.
	if current == "on" || current == "always" {
		return keys
	}
	// Give the set command its own fresh deadline so a slow show above doesn't
	// leave it with a near-expired context.
	setCtx, setCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer setCancel()
	if err := exec.CommandContext(setCtx, "tmux", "set", "extended-keys", "on").Run(); err != nil {
		return keys
	}
	keys.active = true
	return keys
}

// Restore reverts the extended-keys option to its prior value if this handle
// changed it.
func (k *TmuxKeys) Restore() {
	if k == nil || !k.active {
		return
	}
	// Idempotent: only restore once even if called from both a signal handler
	// and a deferred cleanup.
	k.active = false
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if k.previous == "" {
		// The option was at its default; "off" is tmux's documented default.
		_ = exec.CommandContext(ctx, "tmux", "set", "extended-keys", "off").Run()
		return
	}
	_ = exec.CommandContext(ctx, "tmux", "set", "extended-keys", k.previous).Run()
}

func tmuxShowExtendedKeys(ctx context.Context) string {
	// Read the session-scoped value (with inheritance from the server option)
	// so the saved "previous" matches the scope we set/restore at below.
	out, err := exec.CommandContext(ctx, "tmux", "show", "-v", "extended-keys").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
