//go:build !wasip1

package sdk

import (
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/tui"
)

type commandTestModel struct {
	events []Event
}

func (m *commandTestModel) Update(event Event) { m.events = append(m.events, event) }
func (*commandTestModel) View() tui.Component  { return nil }

func TestNativeTimerWakesAndDeliversThroughUpdate(t *testing.T) {
	defer stopCommands()
	id, err := Schedule(After(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-nativeCommands.wake:
	case <-time.After(time.Second):
		t.Fatal("timer did not wake native loop")
	}

	model := &commandTestModel{}
	if !drainCommands(model) {
		t.Fatal("timer wake had no command")
	}
	if len(model.events) != 1 || model.events[0] != (TimerMsg{ID: id}) {
		t.Fatalf("events = %#v, want TimerMsg{%d}", model.events, id)
	}
}
