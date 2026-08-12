package ui

// InputKind identifies input presented to the semantic UI router.
type InputKind uint8

const (
	NoInput InputKind = iota
	KeyInputKind
	MouseInputKind
	PasteInputKind
)

// Key is a named or printable key. Named values are negative.
type Key rune

const (
	KeyEnter Key = -(iota + 1)
	KeyEscape
	KeyTab
	KeySpace
)

// Input is an alias-friendly value used by app.Runtime to avoid an app/ui
// import cycle. KeyInput, MouseInput, and PasteInput are aliases of Input.
type Input struct {
	Kind   InputKind
	Key    Key
	Shift  bool
	Ctrl   bool
	Alt    bool
	X, Y   int
	Button uint8
	Action uint8
	Text   string
}

type KeyInput = Input
type MouseInput = Input
type PasteInput = Input

// Focus tracks the semantic key currently focused.
type Focus struct{ key string }

// NewFocus creates empty focus state.
func NewFocus() *Focus { return &Focus{} }

// Key returns the focused node key.
func (focus *Focus) Key() string {
	if focus == nil {
		return ""
	}
	return focus.key
}

// SetKey sets focus explicitly.
func (focus *Focus) SetKey(key string) {
	if focus != nil {
		focus.key = key
	}
}

// ReconcileFocus preserves focus by key, otherwise selects the first keyed
// button in rendered order.
func ReconcileFocus(root Node, focus *Focus) {
	if focus == nil {
		return
	}
	buttons := focusable(root)
	if focus.key != "" {
		for _, button := range buttons {
			if button.nodeData().key == focus.key {
				return
			}
		}
	}
	if len(buttons) > 0 {
		focus.key = buttons[0].nodeData().key
	} else {
		focus.key = ""
	}
}

// Handle gives semantic controls first refusal of key and mouse input.
func Handle(root Node, input Input, focus *Focus) (any, bool) {
	if input.Kind == MouseInputKind {
		return nil, false
	}
	return handleHits(root, rootHits(root), input, focus)
}

// HandleFrame routes input using the rendered hit regions, preserving exact
// layout coordinates for nested buttons.
func HandleFrame(frame Frame, input Input, focus *Focus) (any, bool) {
	hits := make([]hit, len(frame.hits))
	for index, region := range frame.hits {
		hits[index] = hit{x: region.x, y: region.y, width: region.width, height: region.height, node: region.node}
	}
	return handleHits(frame.root, hits, input, focus)
}

func handleHits(root Node, hits []hit, input Input, focus *Focus) (any, bool) {
	if root == nil {
		return nil, false
	}
	switch input.Kind {
	case KeyInputKind:
		buttons := focusable(root)
		if input.Key == KeyTab {
			if len(buttons) == 0 {
				return nil, false
			}
			index := -1
			for i, button := range buttons {
				if focus != nil && button.nodeData().key == focus.key {
					index = i
					break
				}
			}
			if focus != nil {
				focus.key = buttons[(index+1)%len(buttons)].nodeData().key
			}
			return nil, true
		}
		for _, button := range buttons {
			if focus == nil || button.nodeData().key != focus.key {
				continue
			}
			if input.Key == KeyEnter || input.Key == KeySpace || (input.Key == ' ' && input.Key >= 0) {
				return button.nodeData().event, true
			}
		}
	case MouseInputKind:
		for index := len(hits) - 1; index >= 0; index-- {
			hit := hits[index]
			if input.X < hit.x || input.Y < hit.y || input.X >= hit.x+hit.width || input.Y >= hit.y+hit.height {
				continue
			}
			if input.Button == 1 && (input.Action == 1 || input.Action == 2) {
				if focus != nil {
					focus.key = hit.node.nodeData().key
				}
				return hit.node.nodeData().event, true
			}
		}
	}
	return nil, false
}

type hit struct {
	x, y, width, height int
	node                Node
}

func rootHits(root Node) []hit {
	// Hit regions are attached during Render; direct Handle calls use a simple
	// structural fallback, while Runtime supplies the rendered frame regions.
	var result []hit
	collectHits(root, 0, 0, &result)
	return result
}

func collectHits(node Node, x, y int, result *[]hit) {
	if node == nil {
		return
	}
	base := node.nodeData()
	if base.kind == kindButton {
		*result = append(*result, hit{x: x, y: y, width: max(1, len([]rune(base.text))+4), height: 1, node: node})
	}
	for _, child := range base.children {
		collectHits(child, x, y, result)
	}
}

func focusable(root Node) []Node {
	var result []Node
	var walk func(Node)
	walk = func(node Node) {
		if node == nil {
			return
		}
		if node.nodeData().kind == kindButton && node.nodeData().key != "" {
			result = append(result, node)
		}
		for _, child := range node.nodeData().children {
			walk(child)
		}
	}
	walk(root)
	return result
}

// ButtonEvent returns the semantic event for the first button with label.
// It is intended for deterministic harnesses and accessibility tooling; real
// input should use HandleFrame so hit-testing and focus rules are exercised.
func ButtonEvent(root Node, label string) (any, bool) {
	var found any
	ok := false
	var walk func(Node)
	walk = func(node Node) {
		if node == nil || ok {
			return
		}
		base := node.nodeData()
		if base.kind == kindButton && base.text == label {
			found, ok = base.event, true
			return
		}
		for _, child := range base.children {
			walk(child)
		}
	}
	walk(root)
	return found, ok
}
