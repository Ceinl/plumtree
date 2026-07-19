package components

import (
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

type Div struct {
	isDirty bool

	W, H layout.Unit

	justifyContent layout.JustifyContent
	alignItems     layout.AlignItems
	direction      layout.Direction

	children []layout.Component

	parent layout.Component

	cx, cy int
	cw, ch int

	padding layout.Padding

	style layout.Style
}

type childSize struct {
	child  layout.Component
	cw, ch int
	grow   bool
}

func (d *Div) GetStyle() layout.Style {
	return d.style
}

func (d *Div) SetStyle(style layout.Style) {
	d.style = style
}

func NewDiv() *Div {
	// Explicitly pin the layout defaults to the zero-value rendering behaviour
	// the runtime relied on before the enum const blocks were split. Previously
	// these fields fell through their switch statements as zero values and laid
	// out as a top/left-aligned column; now that the enums are zero-based,
	// JLeft/ATop/Column are no longer the zero values, so we set them here to
	// keep a freshly constructed Div rendering identically.
	return &Div{
		direction:      layout.Column,
		justifyContent: layout.JLeft,
		alignItems:     layout.ATop,
	}
}

func (d *Div) IsDirty() bool {
	return d.isDirty
}

func (d *Div) MakeDirty() {
	d.isDirty = true
}

func (d *Div) ClearDirty() {
	d.isDirty = false
}

func (d *Div) Layout(x, y, w, h int) {
	d.cx = x
	d.cy = y
	d.cw = w
	d.ch = h

	pl := d.padding.Left.Resolve(w)
	pr := d.padding.Right.Resolve(w)
	pt := d.padding.Top.Resolve(h)
	pb := d.padding.Bottom.Resolve(h)

	innerW := w - pl - pr
	innerH := h - pt - pb
	if innerW < 0 {
		innerW = 0
	}
	if innerH < 0 {
		innerH = 0
	}

	row := d.direction == layout.Row
	sizes, growCount, fixedMain := d.measureChildren(row, innerW, innerH)
	allocateGrow(sizes, row, mainSize(row, innerW, innerH), fixedMain, growCount)
	d.layoutChildren(sizes, row, x+pl, y+pt, innerW, innerH)
}

func (d *Div) measureChildren(row bool, innerW, innerH int) ([]childSize, int, int) {
	sizes := make([]childSize, 0, len(d.children))
	growCount, fixedMain := 0, 0
	for _, child := range d.children {
		cw, ch, grow := measureChild(child, row, innerW, innerH)
		if grow {
			growCount++
		} else {
			fixedMain += mainSize(row, cw, ch)
		}
		sizes = append(sizes, childSize{child: child, cw: cw, ch: ch, grow: grow})
	}
	return sizes, growCount, fixedMain
}

func measureChild(child layout.Component, row bool, innerW, innerH int) (int, int, bool) {
	cd, ok := child.(*Div)
	if !ok {
		if row {
			return 0, innerH, true
		}
		return innerW, 0, true
	}
	if row {
		cw, grow := resolveUnit(cd.W, innerW, false)
		ch, _ := resolveUnit(cd.H, innerH, true)
		return cw, ch, grow
	}
	cw, _ := resolveUnit(cd.W, innerW, true)
	ch, grow := resolveUnit(cd.H, innerH, false)
	return cw, ch, grow
}

func resolveUnit(unit layout.Unit, total int, growFills bool) (int, bool) {
	if unit.Type == layout.UnitGrow {
		if growFills {
			return total, false
		}
		return 0, true
	}
	return clamp(unit.Resolve(total), 0, total), false
}

func allocateGrow(sizes []childSize, row bool, totalMain, fixedMain, growCount int) {
	if growCount == 0 {
		return
	}
	growSize := 0
	if rem := totalMain - fixedMain; rem > 0 {
		growSize = rem / growCount
	}
	for i := range sizes {
		if sizes[i].grow {
			setMainSize(&sizes[i], row, growSize)
		}
	}
	rem := totalMain - fixedMain - growSize*growCount
	for i := range sizes {
		if rem <= 0 {
			break
		}
		if sizes[i].grow {
			addMainSize(&sizes[i], row, 1)
			rem--
		}
	}
}

func (d *Div) layoutChildren(sizes []childSize, row bool, x, y, innerW, innerH int) {
	totalUsed := 0
	for _, sc := range sizes {
		totalUsed += mainSize(row, sc.cw, sc.ch)
	}
	mainTotal, crossTotal := mainSize(row, innerW, innerH), crossSize(row, innerW, innerH)
	cursor := mainOffset(d.justifyContent, mainTotal, totalUsed)
	for _, sc := range sizes {
		cross := crossOffset(d.alignItems, row, crossTotal, crossSize(row, sc.cw, sc.ch))
		if row {
			sc.child.Layout(x+cursor, y+cross, sc.cw, sc.ch)
			cursor += sc.cw
		} else {
			sc.child.Layout(x+cross, y+cursor, sc.cw, sc.ch)
			cursor += sc.ch
		}
	}
}

func mainOffset(jc layout.JustifyContent, total, used int) int {
	switch jc {
	case layout.JCenter:
		return clamp((total-used)/2, 0, total)
	case layout.JRight:
		return clamp(total-used, 0, total)
	default:
		return 0
	}
}

func crossOffset(ai layout.AlignItems, row bool, total, used int) int {
	switch ai {
	case layout.ACenter:
		return (total - used) / 2
	case layout.ABottom:
		if row {
			return total - used
		}
	case layout.ARight:
		if !row {
			return total - used
		}
	}
	return 0
}

func mainSize(row bool, w, h int) int {
	if row {
		return w
	}
	return h
}

func crossSize(row bool, w, h int) int {
	if row {
		return h
	}
	return w
}

func setMainSize(size *childSize, row bool, value int) {
	if row {
		size.cw = value
	} else {
		size.ch = value
	}
}

func addMainSize(size *childSize, row bool, value int) {
	if row {
		size.cw += value
	} else {
		size.ch += value
	}
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func (d *Div) SetSize(w, h layout.Unit) {
	d.W = w
	d.H = h
}

func (d *Div) SetPadding(p layout.Padding) {
	d.padding = p
}

func (d *Div) Render(s *screen.Screen) {
	bg := d.style.GetBackground()
	fg := d.style.GetForeground()
	decor := d.style.GetDecor()

	for y := d.cy; y < d.cy+d.ch; y++ {
		for x := d.cx; x < d.cx+d.cw; x++ {
			s.Set(x, y, ' ', fg, bg, decor)
		}
	}
	for _, child := range d.children {
		child.Render(s)
	}
}

func (d *Div) JustifyContent(jc layout.JustifyContent) {
	d.justifyContent = jc
}

func (d *Div) AlignItems(ai layout.AlignItems) {
	d.alignItems = ai
}

func (d *Div) SetDirection(dir layout.Direction) {
	d.direction = dir
}

func (d *Div) SetParent(parent layout.Component) {
	d.parent = parent
}

func (d *Div) AppendChild(children layout.Component) {
	children.SetParent(d)
	d.children = append(d.children, children)
}

// HandleMouse routes to children in reverse render order, so overlapping
// components receive events topmost-first.
func (d *Div) HandleMouse(ev layout.MouseEvent) bool {
	for i := len(d.children) - 1; i >= 0; i-- {
		if handler, ok := d.children[i].(layout.MouseHandler); ok && handler.HandleMouse(ev) {
			return true
		}
	}
	return false
}
