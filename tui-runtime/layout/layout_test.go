package layout

import "testing"

func TestUnitResolve(t *testing.T) {
	cases := []struct {
		name  string
		unit  Unit
		total int
		want  int
	}{
		{"px", Unit{Type: UnitPx, Value: 42}, 100, 42},
		{"percent", Unit{Type: UnitPercent, Value: 50}, 200, 100},
		{"percent rounds down", Unit{Type: UnitPercent, Value: 33}, 10, 3},
		{"grow is zero", Unit{Type: UnitGrow, Value: 99}, 100, 0},
		{"zero value", Unit{}, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.unit.Resolve(tc.total); got != tc.want {
				t.Errorf("Resolve(%d) = %d, want %d", tc.total, got, tc.want)
			}
		})
	}
}

func TestStyleColors(t *testing.T) {
	var s Style
	s.SetForeground(1, 2, 3)
	s.SetBackground(4, 5, 6)
	if got, want := s.GetForeground(), "\x1b[38;2;1;2;3m"; got != want {
		t.Errorf("GetForeground() = %q, want %q", got, want)
	}
	if got, want := s.GetBackground(), "\x1b[48;2;4;5;6m"; got != want {
		t.Errorf("GetBackground() = %q, want %q", got, want)
	}
}

func TestStyleTextDecoration(t *testing.T) {
	var s Style
	if s.GetDecor() != "" {
		t.Errorf("empty style decor = %q, want empty", s.GetDecor())
	}
	s.AddTextDecoration(Bold)
	s.AddTextDecoration(Underline)
	if !s.HasTextDecoration(Bold) || !s.HasTextDecoration(Underline) {
		t.Error("expected Bold and Underline set")
	}
	if s.HasTextDecoration(Italic) {
		t.Error("Italic should not be set")
	}
	if got, want := s.GetDecor(), "\x1b[1;4m"; got != want {
		t.Errorf("GetDecor() = %q, want %q", got, want)
	}
	s.RemoveTextDecoration(Bold)
	if s.HasTextDecoration(Bold) {
		t.Error("Bold should be removed")
	}
	if got, want := s.GetDecor(), "\x1b[4m"; got != want {
		t.Errorf("GetDecor() = %q, want %q", got, want)
	}
}
