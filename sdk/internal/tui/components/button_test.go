package components

import "testing"

func TestButtonHitTest(t *testing.T) {
	b := NewButton("x")
	b.Layout(2, 3, 4, 2) // covers x in [2,6), y in [3,5)
	if !b.HitTest(2, 3) {
		t.Error("top-left corner should hit")
	}
	if !b.HitTest(5, 4) {
		t.Error("inside should hit")
	}
	if b.HitTest(6, 4) {
		t.Error("just past right edge should miss")
	}
	if b.HitTest(2, 5) {
		t.Error("just past bottom edge should miss")
	}
}

func TestButtonMouseClick(t *testing.T) {
	b := NewButton("ok")
	b.Layout(0, 0, 6, 1)
	clicks := 0
	b.OnClick = func() { clicks++ }

	if !b.HandleMouseDown(2, 0) {
		t.Fatal("down inside should be consumed")
	}
	if !b.Focused() {
		t.Error("press should focus the button")
	}
	if !b.HandleMouseUp(3, 0) {
		t.Fatal("up after press should be consumed")
	}
	if clicks != 1 {
		t.Errorf("clicks = %d, want 1", clicks)
	}

	// Release outside the button cancels the click.
	b.HandleMouseDown(1, 0)
	if !b.HandleMouseUp(99, 0) {
		t.Fatal("up while pressed should be consumed even outside")
	}
	if clicks != 1 {
		t.Errorf("release outside should not click; clicks = %d", clicks)
	}
}

func TestButtonMouseDownMisses(t *testing.T) {
	b := NewButton("ok")
	b.Layout(0, 0, 4, 1)
	if b.HandleMouseDown(10, 10) {
		t.Error("down outside should not be consumed")
	}
	if b.HandleMouseUp(10, 10) {
		t.Error("up with no press should not be consumed")
	}
}

func TestButtonActivate(t *testing.T) {
	b := NewButton("ok")
	clicks := 0
	b.OnClick = func() { clicks++ }
	b.Activate()
	if clicks != 1 {
		t.Errorf("Activate should call OnClick once, got %d", clicks)
	}
	b.OnClick = nil
	b.Activate() // must not panic
}

func TestButtonStateStyle(t *testing.T) {
	b := NewButton("ok")
	if b.GetStyle() != b.normal {
		t.Error("default state should use normal style")
	}
	b.SetFocused(true)
	if b.GetStyle() != b.focus {
		t.Error("focused state should use focus style")
	}
	b.isPressed = true
	if b.GetStyle() != b.pressed {
		t.Error("pressed state should use pressed style")
	}
}
