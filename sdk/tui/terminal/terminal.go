// Package terminal exposes the SDK host-side terminal lifecycle helpers.
package terminal

import internal "github.com/Ceinl/plumtree/sdk/internal/tui/terminal"

var ErrNotTerminal = internal.ErrNotTerminal

type Terminal = internal.Terminal
type TmuxKeys = internal.TmuxKeys

const (
	DefaultWidth              = internal.DefaultWidth
	DefaultHeight             = internal.DefaultHeight
	OPEN_ALT                  = internal.OPEN_ALT
	CLOSE_ALT                 = internal.CLOSE_ALT
	HIDE_CURSOR               = internal.HIDE_CURSOR
	SHOW_CURSOR               = internal.SHOW_CURSOR
	ENABLE_MOUSE              = internal.ENABLE_MOUSE
	DISABLE_MOUSE             = internal.DISABLE_MOUSE
	ENABLE_BRACKETED_PASTE    = internal.ENABLE_BRACKETED_PASTE
	DISABLE_BRACKETED_PASTE   = internal.DISABLE_BRACKETED_PASTE
	ENABLE_KITTY_KEYBOARD     = internal.ENABLE_KITTY_KEYBOARD
	DISABLE_KITTY_KEYBOARD    = internal.DISABLE_KITTY_KEYBOARD
	ENABLE_MODIFY_OTHER_KEYS  = internal.ENABLE_MODIFY_OTHER_KEYS
	DISABLE_MODIFY_OTHER_KEYS = internal.DISABLE_MODIFY_OTHER_KEYS
	CLEAR_SCREEN              = internal.CLEAR_SCREEN
	MOVE_CURSOR               = internal.MOVE_CURSOR
)

func New(fd int) *Terminal { return internal.New(fd) }

func EnableTmuxExtendedKeys() *TmuxKeys { return internal.EnableTmuxExtendedKeys() }
