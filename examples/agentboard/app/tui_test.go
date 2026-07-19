package main

import (
	"strings"
	"testing"

	"github.com/Ceinl/plumtree/sdk"
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
		"Enter advances pending and review gates",
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
