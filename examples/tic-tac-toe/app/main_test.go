package main

import (
	"testing"
	"time"
)

func TestWinner(t *testing.T) {
	for _, board := range [][9]uint8{
		{1, 1, 1},
		{2, 0, 0, 2, 0, 0, 2},
		{1, 0, 0, 0, 1, 0, 0, 0, 1},
	} {
		mark := uint8(1)
		if board[0] == 2 {
			mark = 2
		}
		if !winner(board, mark) {
			t.Fatalf("winner(%v, %d) = false", board, mark)
		}
	}
	if winner([9]uint8{1, 2, 1, 2, 1}, 1) {
		t.Fatal("incomplete board reported a winner")
	}
}

func TestApplyMoveAndRoundReset(t *testing.T) {
	state := defaultState()
	if !applyMove(&state, 1, 4) || state.Board[4] != 1 || state.Turn != 2 {
		t.Fatalf("first move produced %+v", state)
	}
	if applyMove(&state, 1, 0) {
		t.Fatal("player moved twice")
	}
	state = gameState{Board: [9]uint8{1, 1}, Turn: 1}
	if !applyMove(&state, 1, 2) || state.Winner != 1 {
		t.Fatalf("winning move produced %+v", state)
	}
	if !applyMove(&state, 2, 0) || state.Winner != 0 || state.Turn != 1 {
		t.Fatalf("round reset produced %+v", state)
	}
}

func TestBoardCell(t *testing.T) {
	left, top := boardOrigin(80, 24)
	tests := []struct {
		x, y int
		cell int
		ok   bool
	}{
		{left + 4, top + 1, 0, true},
		{left + 14, top + 5, 4, true},
		{left + 24, top + 9, 8, true},
		{left + 9, top + 1, 0, false},
		{left - 1, top, 0, false},
	}
	for _, test := range tests {
		cell, ok := boardCell(test.x, test.y, 80, 24)
		if cell != test.cell || ok != test.ok {
			t.Errorf("boardCell(%d,%d) = %d,%t want %d,%t", test.x, test.y, cell, ok, test.cell, test.ok)
		}
	}
}

func TestExpireSeats(t *testing.T) {
	now := time.Unix(100, 0)
	state := defaultState()
	state.Players[0] = player{ID: "stale", SeenUnix: now.Add(-seatLease - time.Second).UnixMilli()}
	state.Players[1] = player{ID: "live", SeenUnix: now.Add(-time.Second).UnixMilli()}
	state.Board[0] = 1
	if !expireSeats(&state, now) {
		t.Fatal("stale seat was not expired")
	}
	if state.Players[0].ID != "" || state.Players[1].ID != "live" || state.Board[0] != 0 {
		t.Fatalf("expired state = %+v", state)
	}
}
