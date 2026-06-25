package layout

import "github.com/Ceinl/plumtree/tui-runtime/screen"

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
