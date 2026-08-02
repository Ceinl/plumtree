// Package keyboard exposes the SDK host-side terminal input parser.
package keyboard

import (
	"context"
	"io"

	internal "github.com/Ceinl/plumtree/sdk/internal/tui/keyboard"
)

type Event = internal.Event
type EventType = internal.EventType

const (
	KeyRune           = internal.KeyRune
	KeyEnter          = internal.KeyEnter
	KeyBackspace      = internal.KeyBackspace
	KeyCtrlC          = internal.KeyCtrlC
	KeyTab            = internal.KeyTab
	KeyEscape         = internal.KeyEscape
	KeyArrowUp        = internal.KeyArrowUp
	KeyArrowDown      = internal.KeyArrowDown
	KeyArrowRight     = internal.KeyArrowRight
	KeyArrowLeft      = internal.KeyArrowLeft
	KeyHome           = internal.KeyHome
	KeyEnd            = internal.KeyEnd
	KeyPageUp         = internal.KeyPageUp
	KeyPageDown       = internal.KeyPageDown
	KeyDelete         = internal.KeyDelete
	KeyMouseWheelUp   = internal.KeyMouseWheelUp
	KeyMouseWheelDown = internal.KeyMouseWheelDown
	KeyMouseLeftDown  = internal.KeyMouseLeftDown
	KeyMouseLeftDrag  = internal.KeyMouseLeftDrag
	KeyMouseLeftUp    = internal.KeyMouseLeftUp
	KeyPaste          = internal.KeyPaste
	KeyUnknown        = internal.KeyUnknown
)

// Listen parses keyboard input from stdin until ctx is cancelled or stdin
// closes.
func Listen(ctx context.Context) <-chan Event { return internal.Listen(ctx) }

// ListenReader parses keyboard input from an arbitrary reader, such as an SSH
// channel, until the reader ends or ctx is cancelled.
func ListenReader(ctx context.Context, reader io.Reader) <-chan Event {
	return internal.ListenReader(ctx, reader)
}
