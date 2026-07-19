package main

import (
	"fmt"
	"strings"

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

func (m *boardModel) ownerAdvance(index int) {
	if !m.identity.OwnsApp || index < 0 || index >= len(m.tasks) {
		return
	}
	board, task := m.boards[m.selected], m.tasks[index]
	if _, err := advanceTask(board, m.identity, task.ID, task.Status, actorOwner); err != nil {
		m.err = err.Error()
	}
	m.reload()
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
			m.ownerAdvance(m.taskIndex)
		}
	}
}

func (m *boardModel) View() tui.Component {
	m.initialize()
	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.SetSize(tui.Grow, tui.Grow)
	root.AppendChild(components.NewText("Agentboard · ←/→ boards · ↑/↓ tasks · Enter owner transition · q quit"))
	if m.err != "" {
		root.AppendChild(components.NewText("Error: " + m.err))
	}
	if len(m.boards) == 0 {
		root.AppendChild(components.NewText("No accessible board"))
		return root
	}

	picker := components.NewDiv()
	picker.SetDirection(tui.Row)
	picker.SetSize(tui.Grow, tui.Px(3))
	for index, board := range m.boards {
		index, board := index, board
		label := board.Name
		if index == m.selected {
			label = "[" + label + "]"
		}
		button := components.NewButton(label)
		button.OnClick = func() { m.selectBoard(index) }
		picker.AppendChild(button)
	}
	root.AppendChild(picker)

	board := m.boards[m.selected]
	meta := fmt.Sprintf("%s board id=%s", board.Type, board.ID)
	if board.Project != "" {
		meta += " project=" + board.Project + " members=" + strings.Join(board.Members, ",")
	}
	root.AppendChild(components.NewText(meta))

	columns := components.NewDiv()
	columns.SetDirection(tui.Row)
	columns.SetSize(tui.Grow, tui.Grow)
	for _, status := range workflow {
		column := components.NewDiv()
		column.SetDirection(tui.Column)
		column.SetSize(tui.Grow, tui.Grow)
		column.AppendChild(components.NewText(strings.ToUpper(status)))
		for index, task := range m.tasks {
			if task.Status != status {
				continue
			}
			label := fmt.Sprintf("%s %s", task.ID, task.Title)
			if index == m.taskIndex {
				label = "> " + label
			}
			if m.identity.OwnsApp && (task.Status == "pending" || task.Status == "in-review") {
				index := index
				button := components.NewButton(label)
				button.OnClick = func() { m.ownerAdvance(index) }
				column.AppendChild(button)
			} else {
				column.AppendChild(components.NewText(label))
			}
		}
		columns.AppendChild(column)
	}
	root.AppendChild(columns)
	if m.identity.OwnsApp {
		root.AppendChild(components.NewText(`Members: ssh cein/agentboard@plumtree.dev 'action add_project_member {"project":"slug","identity":"SHA256:..."}'`))
	}
	return root
}
