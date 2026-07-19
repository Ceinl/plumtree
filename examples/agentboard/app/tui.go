package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

var workflow = []string{"pending", "todo", "in-progress", "in-review", "done"}

type boardModel struct {
	initialized bool
	identity    sdk.Identity
	boards      []Board
	selected    int
	tasks       []Task
	taskIndex   int
	err         string
}

type statusTheme struct {
	label          string
	marker         string
	background     [3]uint8
	foreground     [3]uint8
	softBackground [3]uint8
}

var statusThemes = map[string]statusTheme{
	"pending": {
		label: "PENDING", marker: "○",
		background: [3]uint8{88, 71, 122}, foreground: [3]uint8{240, 232, 255},
		softBackground: [3]uint8{38, 32, 49},
	},
	"todo": {
		label: "TO DO", marker: "◇",
		background: [3]uint8{54, 82, 126}, foreground: [3]uint8{224, 238, 255},
		softBackground: [3]uint8{28, 38, 55},
	},
	"in-progress": {
		label: "IN PROGRESS", marker: "◐",
		background: [3]uint8{111, 80, 35}, foreground: [3]uint8{255, 239, 204},
		softBackground: [3]uint8{48, 38, 25},
	},
	"in-review": {
		label: "IN REVIEW", marker: "◎",
		background: [3]uint8{104, 61, 91}, foreground: [3]uint8{255, 226, 246},
		softBackground: [3]uint8{47, 30, 43},
	},
	"done": {
		label: "DONE", marker: "✓",
		background: [3]uint8{43, 94, 77}, foreground: [3]uint8{220, 255, 240},
		softBackground: [3]uint8{25, 45, 39},
	},
}

var (
	appBackground    = styled([3]uint8{16, 17, 24}, [3]uint8{221, 222, 232})
	headerBackground = styled([3]uint8{25, 26, 37}, [3]uint8{244, 244, 250})
	tabsBackground   = styled([3]uint8{20, 21, 30}, [3]uint8{183, 185, 201})
	panelBackground  = styled([3]uint8{24, 25, 35}, [3]uint8{221, 222, 232})
	mutedStyle       = styled([3]uint8{24, 25, 35}, [3]uint8{129, 132, 151})
	footerBackground = styled([3]uint8{28, 29, 41}, [3]uint8{176, 179, 197})
	errorBackground  = styled([3]uint8{79, 34, 45}, [3]uint8{255, 221, 226}, tui.Bold)
	selectedCard     = styled([3]uint8{53, 54, 75}, [3]uint8{255, 255, 255}, tui.Bold)
	pressedCard      = styled([3]uint8{72, 73, 99}, [3]uint8{255, 255, 255}, tui.Bold)
)

func (m *boardModel) initialize() {
	if m.initialized {
		return
	}
	m.initialized = true
	id, err := caller()
	if err != nil {
		m.err = err.Error()
		return
	}
	m.identity = id
	m.boards, err = listVisibleBoards(id)
	if err != nil {
		m.err = err.Error()
		return
	}
	m.reload()
}

func (m *boardModel) reload() {
	if len(m.boards) == 0 {
		return
	}
	if m.selected < 0 {
		m.selected = len(m.boards) - 1
	}
	if m.selected >= len(m.boards) {
		m.selected = 0
	}
	var err error
	m.tasks, err = listBoardTasks(m.boards[m.selected])
	if err != nil {
		m.err = err.Error()
	} else {
		m.err = ""
	}
	_ = sdk.Subscribe(boardTopic(m.boards[m.selected].ID))
	if m.taskIndex >= len(m.tasks) {
		m.taskIndex = max(0, len(m.tasks)-1)
	}
}

func (m *boardModel) selectBoard(index int) {
	m.selected, m.taskIndex = index, 0
	m.reload()
}

func (m *boardModel) selectedBoardType() string {
	if len(m.boards) == 0 || m.selected < 0 || m.selected >= len(m.boards) {
		return ""
	}
	return m.boards[m.selected].Type
}

func (m *boardModel) transitionActor() transitionActor {
	if m.selectedBoardType() == "user" {
		return actorPersonal
	}
	if m.identity.OwnsApp {
		return actorOwner
	}
	return actorAgent
}

func (m *boardModel) canAdvance(status string) bool {
	_, err := nextStatus(m.transitionActor(), status)
	return err == nil
}

func (m *boardModel) advance(index int) {
	if index < 0 || index >= len(m.tasks) || !m.canAdvance(m.tasks[index].Status) {
		return
	}
	board, task := m.boards[m.selected], m.tasks[index]
	if _, err := advanceTask(board, m.identity, task.ID, task.Status, m.transitionActor()); err != nil {
		m.err = err.Error()
		return
	}
	m.reload()
}

func (m *boardModel) activateTask(index int) {
	if index < 0 || index >= len(m.tasks) {
		return
	}
	m.taskIndex = index
	if m.canAdvance(m.tasks[index].Status) {
		m.advance(index)
		return
	}
	if m.selectedBoardType() == "user" {
		m.err = "this task is complete"
		return
	}
	switch m.tasks[index].Status {
	case "pending":
		m.err = "pending tasks are awaiting app-owner review"
	case "in-review":
		m.err = "reviewed tasks are awaiting app-owner approval"
	case "done":
		m.err = "this task is complete"
	default:
		m.err = "this transition belongs to a member agent"
	}
}

func (m *boardModel) Update(ev sdk.Event) {
	m.initialize()
	switch e := ev.(type) {
	case sdk.MessageMsg:
		if len(m.boards) > 0 && e.Topic == boardTopic(m.boards[m.selected].ID) {
			m.reload()
		}
	case sdk.KeyMsg:
		switch e.Key {
		case 'q', sdk.KeyCtrlC:
			sdk.Quit()
		case sdk.KeyLeft:
			if len(m.boards) > 0 {
				m.selectBoard((m.selected - 1 + len(m.boards)) % len(m.boards))
			}
		case sdk.KeyRight:
			if len(m.boards) > 0 {
				m.selectBoard((m.selected + 1) % len(m.boards))
			}
		case sdk.KeyUp:
			if m.taskIndex > 0 {
				m.taskIndex--
			}
		case sdk.KeyDown:
			if m.taskIndex+1 < len(m.tasks) {
				m.taskIndex++
			}
		case sdk.KeyEnter:
			m.advance(m.taskIndex)
		}
	}
}

func (m *boardModel) View() tui.Component {
	m.initialize()

	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.SetSize(tui.Grow, tui.Grow)
	root.SetStyle(appBackground)
	root.AppendChild(m.header())

	if m.err != "" {
		root.AppendChild(banner("!  "+m.err, errorBackground))
	}
	if len(m.boards) == 0 {
		root.AppendChild(emptyWorkspace())
		root.AppendChild(m.footer())
		return root
	}

	root.AppendChild(m.boardPicker())
	root.AppendChild(m.boardSummary())
	root.AppendChild(m.boardColumns())
	root.AppendChild(m.footer())
	return root
}

func (m *boardModel) header() tui.Component {
	header := components.NewDiv()
	header.SetDirection(tui.Row)
	header.SetSize(tui.Grow, tui.Px(2))
	header.SetPadding(tui.Padding{Left: tui.Px(2), Right: tui.Px(2)})
	header.SetStyle(headerBackground)

	brand := fixedBox(tui.Px(25), tui.Grow, headerBackground)
	brand.AppendChild(text("◆  AGENTBOARD\nworkflow capability demo", headerBackground, components.AlignLeft))
	header.AppendChild(brand)
	header.AppendChild(spacer(tui.Px(1), tui.Grow, headerBackground))

	modeLabel, modeDetail := "USER MODE", "SSH identity"
	if m.selectedBoardType() == "user" {
		modeLabel, modeDetail = "PERSONAL MODE", "move your private tasks"
	} else if m.selectedBoardType() == "project" && m.identity.OwnsApp {
		modeLabel, modeDetail = "OWNER MODE", "review gates enabled"
	} else if m.selectedBoardType() == "project" {
		modeLabel, modeDetail = "MEMBER MODE", "work on shared tasks"
	}
	modeStyle := styled([3]uint8{25, 26, 37}, [3]uint8{151, 154, 177}, tui.Bold)
	mode := fixedBox(tui.Grow, tui.Grow, modeStyle)
	mode.AppendChild(text(modeLabel+"\n"+modeDetail, modeStyle, components.AlignRight))
	header.AppendChild(mode)
	return header
}

func (m *boardModel) boardPicker() tui.Component {
	picker := components.NewDiv()
	picker.SetDirection(tui.Row)
	picker.SetSize(tui.Grow, tui.Px(2))
	picker.SetPadding(tui.Padding{Left: tui.Px(2), Right: tui.Px(2)})
	picker.SetStyle(tabsBackground)

	for index, board := range m.boards {
		index := index
		label := board.Name
		if board.Type == "project" {
			label = "# " + label
		} else {
			label = "⌂ " + label
		}
		button := components.NewButton(label)
		button.SetStyles(
			styled([3]uint8{30, 31, 43}, [3]uint8{166, 169, 189}),
			styled([3]uint8{74, 65, 113}, [3]uint8{255, 255, 255}, tui.Bold),
			styled([3]uint8{91, 79, 139}, [3]uint8{255, 255, 255}, tui.Bold),
		)
		button.SetFocused(index == m.selected)
		button.OnClick = func() { m.selectBoard(index) }

		width := utf8.RuneCountInString(label) + 4
		wrapper := fixedBox(tui.Px(width), tui.Grow, tabsBackground)
		wrapper.AppendChild(button)
		picker.AppendChild(wrapper)
		picker.AppendChild(spacer(tui.Px(1), tui.Grow, tabsBackground))
	}
	picker.AppendChild(spacer(tui.Grow, tui.Grow, tabsBackground))
	return picker
}

func (m *boardModel) boardSummary() tui.Component {
	board := m.boards[m.selected]
	summary := components.NewDiv()
	summary.SetSize(tui.Grow, tui.Px(2))
	summary.SetPadding(tui.Padding{Left: tui.Px(2), Right: tui.Px(2)})
	summary.SetStyle(tabsBackground)

	kind, detail := "PERSONAL BOARD", "private to your SSH identity"
	if board.Type == "project" {
		kind = "PROJECT  /  " + board.Project
		detail = fmt.Sprintf("shared with %d member%s", len(board.Members), plural(len(board.Members)))
	}
	count := fmt.Sprintf("%d task%s", len(m.tasks), plural(len(m.tasks)))
	content := fmt.Sprintf("%s  ·  %s\n%s  ·  live updates", kind, count, detail)
	summary.AppendChild(text(content, styled([3]uint8{20, 21, 30}, [3]uint8{173, 176, 196}), components.AlignLeft))
	return summary
}

func (m *boardModel) boardColumns() tui.Component {
	columns := components.NewDiv()
	columns.SetDirection(tui.Row)
	columns.SetSize(tui.Grow, tui.Grow)
	columns.SetPadding(tui.Padding{Top: tui.Px(1), Bottom: tui.Px(1), Left: tui.Px(2), Right: tui.Px(2)})
	columns.SetStyle(appBackground)

	for statusIndex, status := range workflow {
		if statusIndex > 0 {
			columns.AppendChild(spacer(tui.Px(1), tui.Grow, appBackground))
		}
		columns.AppendChild(m.taskColumn(status))
	}
	return columns
}

func (m *boardModel) taskColumn(status string) tui.Component {
	theme := statusThemes[status]
	tasks := make([]int, 0)
	for index := range m.tasks {
		if m.tasks[index].Status == status {
			tasks = append(tasks, index)
		}
	}

	column := components.NewDiv()
	column.SetDirection(tui.Column)
	column.SetSize(tui.Grow, tui.Grow)
	column.SetStyle(panelBackground)

	headerStyle := styled(theme.background, theme.foreground, tui.Bold)
	header := fixedBox(tui.Grow, tui.Px(1), headerStyle)
	header.SetPadding(tui.Padding{Left: tui.Px(1), Right: tui.Px(1)})
	header.AppendChild(text(fmt.Sprintf("%s  %s  %d", theme.marker, theme.label, len(tasks)), headerStyle, components.AlignLeft))
	column.AppendChild(header)

	if len(tasks) == 0 {
		empty := fixedBox(tui.Grow, tui.Grow, mutedStyle)
		empty.AppendChild(text("— empty", mutedStyle, components.AlignCenter))
		column.AppendChild(empty)
		return column
	}

	for position, index := range tasks {
		if position > 0 {
			column.AppendChild(spacer(tui.Grow, tui.Px(1), panelBackground))
		}
		column.AppendChild(m.taskCard(index, theme))
	}
	column.AppendChild(spacer(tui.Grow, tui.Grow, panelBackground))
	return column
}

func (m *boardModel) taskCard(index int, theme statusTheme) tui.Component {
	task := m.tasks[index]
	selected := index == m.taskIndex
	normal := styled(theme.softBackground, [3]uint8{214, 216, 227})

	card := fixedBox(tui.Grow, tui.Px(3), normal)
	if selected {
		card.SetStyle(selectedCard)
	}
	card.SetPadding(tui.Padding{Top: tui.Px(1), Left: tui.Px(1), Right: tui.Px(1)})

	marker := "·"
	if m.canAdvance(task.Status) {
		marker = "→"
	}
	buttonLabel := fmt.Sprintf("%s  %s  %s", shortTaskID(task.ID), marker, task.Title)
	if selected {
		buttonLabel = "◆ " + buttonLabel
	}
	button := components.NewButton(buttonLabel)
	button.SetStyles(normal, selectedCard, pressedCard)
	button.SetFocused(selected)
	button.OnClick = func() { m.activateTask(index) }
	card.SetPadding(tui.Padding{})
	card.AppendChild(button)
	return card
}

func (m *boardModel) footer() tui.Component {
	footer := components.NewDiv()
	footer.SetDirection(tui.Row)
	footer.SetSize(tui.Grow, tui.Px(2))
	footer.SetPadding(tui.Padding{Top: tui.Px(1), Left: tui.Px(2), Right: tui.Px(2)})
	footer.SetStyle(footerBackground)

	context := "USER  ·  connected with your SSH identity"
	hints := "←/→ board  ↑/↓ task  ↵/click  q"
	if m.selectedBoardType() == "user" {
		context = "PERSONAL  ·  click or Enter advances your task"
	} else if m.selectedBoardType() == "project" && m.identity.OwnsApp {
		context = "OWNER  ·  click or Enter advances review gates"
	} else if m.selectedBoardType() == "project" {
		context = "MEMBER  ·  click or Enter moves todo and active tasks"
	}
	left := fixedBox(tui.Grow, tui.Grow, footerBackground)
	left.AppendChild(text(context, footerBackground, components.AlignLeft))
	footer.AppendChild(left)
	right := fixedBox(tui.Px(36), tui.Grow, footerBackground)
	right.AppendChild(text(hints, footerBackground, components.AlignRight))
	footer.AppendChild(right)
	return footer
}

func emptyWorkspace() tui.Component {
	empty := fixedBox(tui.Grow, tui.Grow, panelBackground)
	empty.SetPadding(tui.Padding{Top: tui.Px(3), Left: tui.Px(4), Right: tui.Px(4)})
	empty.AppendChild(text("NO BOARDS YET\n\nCreate a personal or project board with an Agentboard action.", mutedStyle, components.AlignCenter))
	return empty
}

func banner(content string, style tui.Style) tui.Component {
	box := fixedBox(tui.Grow, tui.Px(2), style)
	box.SetPadding(tui.Padding{Top: tui.Px(1), Left: tui.Px(2), Right: tui.Px(2)})
	box.AppendChild(text(content, style, components.AlignLeft))
	return box
}

func fixedBox(width, height tui.Unit, style tui.Style) *components.Div {
	box := components.NewDiv()
	box.SetSize(width, height)
	box.SetStyle(style)
	return box
}

func spacer(width, height tui.Unit, style tui.Style) tui.Component {
	return fixedBox(width, height, style)
}

func text(content string, style tui.Style, align components.Align) *components.Text {
	label := components.NewText(content)
	label.SetStyle(style)
	label.SetAlign(align)
	return label
}

func styled(background, foreground [3]uint8, decorations ...tui.TextDecoration) tui.Style {
	var style tui.Style
	style.SetBackground(background[0], background[1], background[2])
	style.SetForeground(foreground[0], foreground[1], foreground[2])
	for _, decoration := range decorations {
		style.AddTextDecoration(decoration)
	}
	return style
}

func shortTaskID(id string) string {
	if suffix := strings.TrimPrefix(id, "task-"); suffix != id {
		return "#" + suffix
	}
	return id
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}
