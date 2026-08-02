package layout

import "github.com/Ceinl/plumtree/sdk/internal/tui/screen"

type Styler interface {
	GetStyle() Style
}

// Main interface for all layout components
type Component interface {
	Styler

	IsDirty() bool
	MakeDirty()
	ClearDirty()

	Layout(x, y, w, h int)
	Render(screen *screen.Screen)

	SetParent(parent Component)
}
