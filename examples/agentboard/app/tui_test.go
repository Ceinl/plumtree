package main

import (
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/tui-runtime/layout"
	"github.com/Ceinl/plumtree/tui-runtime/screen"
)

func TestBoardViewUsesCompactReadableLayout(t *testing.T) {
	model := boardModel{
		initialized: true,
		identity: sdk.Identity{
			Kind:          sdk.IdentitySSHKey,
			Authenticated: true,
			OwnsApp:       true,
		},
		boards: []Board{{
			ID:      "opaque-board-id-that-must-not-be-rendered",
			Type:    "project",
			Project: "plumtree",
			Name:    "Plumtree",
			Members: []string{"member-hash-one", "member-hash-two"},
		}},
		tasks: []Task{
			{ID: "task-000001", Title: "Approve capability demo", Status: "pending", Revision: 1},
			{ID: "task-000002", Title: "Polish the terminal board", Status: "todo", Revision: 3},
			{ID: "task-000003", Title: "Deploy the example", Status: "done", Revision: 4},
		},
		taskIndex: 1,
	}

	frame := renderBoardFrame(&model, 140, 30)
	for _, want := range []string{
		"AGENTBOARD",
		"OWNER MODE",
		"# Plumtree",
		"PROJECT  /  plumtree  ·  3 tasks",
		"shared with 2 members",
		"PENDING  1",
		"TO DO  1",
		"DONE  1",
		"→  Approve",
		"#000001",
		"left edge reverses",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("frame does not contain %q:\n%s", want, frame)
		}
	}
	for _, secret := range []string{
		"opaque-board-id-that-must-not-be-rendered",
		"member-hash-one",
		"member-hash-two",
	} {
		if strings.Contains(frame, secret) {
			t.Fatalf("frame leaks internal identifier %q:\n%s", secret, frame)
		}
	}

	lines := strings.Split(frame, "\n")
	headerLine := lineContaining(lines, "PENDING  1")
	if headerLine < 0 || headerLine > 9 {
		t.Fatalf("workflow lanes should begin near the top, got row %d:\n%s", headerLine, frame)
	}
}

func TestTaskCardsAcceptMouseClicks(t *testing.T) {
	cleanStore(t)
	t.Cleanup(func() { cleanStore(t) })
	identity := sdk.Identity{User: memberFingerprint, Kind: sdk.IdentitySSHKey}
	personal, err := ensurePersonalBoard(identity)
	if err != nil {
		t.Fatal(err)
	}
	first, err := createTask(personal, identity, "First", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createTask(personal, identity, "Second", "")
	if err != nil {
		t.Fatal(err)
	}
	model := boardModel{
		initialized: true,
		identity:    identity,
		boards:      []Board{personal},
		tasks:       []Task{first, second},
	}

	component := model.View()
	component.Layout(0, 0, 140, 30)
	handler, ok := component.(layout.MouseHandler)
	if !ok {
		t.Fatal("board root does not route mouse input")
	}
	// The second pending card occupies the first lane below the first card and
	// its spacer. Clicking selects and advances this identity's personal task.
	if !handler.HandleMouse(layout.MouseEvent{X: 10, Y: 13, Action: layout.MouseDown}) {
		t.Fatal("task card did not consume mouse down")
	}
	if !handler.HandleMouse(layout.MouseEvent{X: 10, Y: 13, Action: layout.MouseUp}) {
		t.Fatal("task card did not consume mouse up")
	}
	if model.taskIndex != 1 {
		t.Fatalf("selected task = %d, want 1", model.taskIndex)
	}
	if model.tasks[1].Status != "todo" {
		t.Fatalf("clicked task status = %q, want todo", model.tasks[1].Status)
	}
	component = model.View()
	component.Layout(0, 0, 140, 30)
	handler = component.(layout.MouseHandler)
	if !handler.HandleMouse(layout.MouseEvent{X: 31, Y: 9, Action: layout.MouseDown}) ||
		!handler.HandleMouse(layout.MouseEvent{X: 31, Y: 9, Action: layout.MouseUp}) {
		t.Fatal("task back edge did not consume click")
	}
	if model.tasks[1].Status != "pending" {
		t.Fatalf("retreated task status = %q, want pending", model.tasks[1].Status)
	}
}

func TestRoleCorrectTUITransitions(t *testing.T) {
	member := boardModel{boards: []Board{{Type: "project"}}}
	if member.canAdvance("pending") || member.canAdvance("in-review") ||
		!member.canAdvance("todo") || !member.canAdvance("in-progress") {
		t.Fatal("member transition affordances do not match agent workflow")
	}
	owner := boardModel{identity: sdk.Identity{OwnsApp: true}, boards: []Board{{Type: "project"}}}
	if !owner.canAdvance("pending") || !owner.canAdvance("in-review") ||
		owner.canAdvance("todo") || owner.canAdvance("in-progress") {
		t.Fatal("owner transition affordances do not match review workflow")
	}
	personal := boardModel{boards: []Board{{Type: "user"}}}
	for _, status := range []string{"pending", "todo", "in-progress", "in-review"} {
		if !personal.canAdvance(status) {
			t.Fatalf("personal transition from %s is unavailable", status)
		}
	}
	if personal.canAdvance("done") {
		t.Fatal("completed personal task can advance")
	}
	for _, status := range []string{"todo", "in-progress", "in-review", "done"} {
		if !personal.canRetreat(status) {
			t.Fatalf("personal retreat from %s is unavailable", status)
		}
	}
	if personal.canRetreat("pending") {
		t.Fatal("pending personal task can retreat")
	}
}

func renderBoardFrame(model *boardModel, width, height int) string {
	component := model.View()
	component.Layout(0, 0, width, height)
	buffer := screen.NewScreen(width, height)
	component.Render(buffer)

	var frame strings.Builder
	for rowIndex, row := range buffer.Snapshot() {
		for _, cell := range row {
			frame.WriteRune(cell.Ch)
		}
		if rowIndex+1 < height {
			frame.WriteByte('\n')
		}
	}
	return frame.String()
}

func lineContaining(lines []string, needle string) int {
	for index, line := range lines {
		if strings.Contains(line, needle) {
			return index
		}
	}
	return -1
}
