package components

import (
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

// Align controls horizontal placement of each visual line within the Text box.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// Text renders a string into its box, wrapping on width and breaking on '\n'.
// If no style is set explicitly it inherits its parent's style, so labels sit
// on their container's background by default.
type Text struct {
	isDirty  bool
	content  string
	parent   layout.Component
	x, y     int
	w, h     int
	style    layout.Style
	hasStyle bool
	align    Align
}

// NewText returns a Text displaying content.
func NewText(content string) *Text {
	return &Text{content: content, isDirty: true}
}

func (t *Text) IsDirty() bool { return t.isDirty }
func (t *Text) MakeDirty()    { t.isDirty = true }
func (t *Text) ClearDirty()   { t.isDirty = false }

func (t *Text) Layout(x, y, w, h int) {
	t.x, t.y, t.w, t.h = x, y, w, h
}

// GetStyle reports the style used for this text (its own if set, else inherited).
func (t *Text) GetStyle() layout.Style { return t.effectiveStyle() }

// SetStyle sets an explicit style; the text no longer inherits its parent's.
func (t *Text) SetStyle(s layout.Style) {
	t.style = s
	t.hasStyle = true
	t.isDirty = true
}

func (t *Text) SetParent(p layout.Component) { t.parent = p }

// SetContent replaces the displayed string, marking the text dirty if changed.
func (t *Text) SetContent(c string) {
	if t.content != c {
		t.content = c
		t.isDirty = true
	}
}

// Content returns the current string.
func (t *Text) Content() string { return t.content }

// SetAlign sets horizontal alignment of each wrapped line.
func (t *Text) SetAlign(a Align) {
	if t.align != a {
		t.align = a
		t.isDirty = true
	}
}

func (t *Text) effectiveStyle() layout.Style {
	if t.hasStyle || t.parent == nil {
		return t.style
	}
	return t.parent.GetStyle()
}

func (t *Text) Render(s *screen.Screen) {
	if t.w <= 0 || t.h <= 0 {
		return
	}
	st := t.effectiveStyle()
	bg, fg, decor := st.GetBackground(), st.GetForeground(), st.GetDecor()

	for i, line := range wrapLines(t.content, t.w) {
		if i >= t.h {
			break
		}
		runes := []rune(line)
		start := t.x
		switch t.align {
		case AlignCenter:
			start = t.x + (t.w-len(runes))/2
		case AlignRight:
			start = t.x + (t.w - len(runes))
		}
		if start < t.x {
			start = t.x
		}
		for j, r := range runes {
			s.Set(start+j, t.y+i, r, fg, bg, decor)
		}
	}
}

// wrapLines splits content into visual lines no wider than w, breaking on
// explicit newlines and hard-wrapping runs that exceed the width.
func wrapLines(content string, w int) []string {
	if w <= 0 {
		return nil
	}
	var lines []string
	for _, para := range splitNewlines(content) {
		runes := []rune(para)
		if len(runes) == 0 {
			lines = append(lines, "")
			continue
		}
		for len(runes) > w {
			lines = append(lines, string(runes[:w]))
			runes = runes[w:]
		}
		lines = append(lines, string(runes))
	}
	return lines
}

func splitNewlines(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
