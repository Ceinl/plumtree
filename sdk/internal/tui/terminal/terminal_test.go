package terminal

import (
	"errors"
	"os"
	"testing"
)

func TestEnterRejectsNonTTY(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	term := New(int(r.Fd()))
	if err := term.Enter(); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("expected ErrNotTerminal, got %v", err)
	}
}

func TestRefreshSizeDefaultsAfterFailure(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	term := New(int(r.Fd()))
	if err := term.RefreshSize(); err == nil {
		t.Fatalf("expected refresh error for non-TTY")
	}
	if term.W != DefaultWidth || term.H != DefaultHeight {
		t.Fatalf("expected default size %dx%d, got %dx%d", DefaultWidth, DefaultHeight, term.W, term.H)
	}
}
