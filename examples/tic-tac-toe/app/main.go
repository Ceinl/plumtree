// Command tic-tac-toe is a shared mouse-driven game. The first two live
// identities lease the X and O seats; later connections watch until one frees.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/tui"
)

const (
	gameKey           = "tic-tac-toe/state"
	gameTopic         = "tic-tac-toe/changed"
	heartbeatInterval = 2 * time.Second
	seatLease         = 12 * time.Second
	boardWidth        = 29
	boardHeight       = 11
)

type player struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	SeenUnix int64  `json:"seenUnix"`
}

type gameState struct {
	Players [2]player `json:"players"`
	Board   [9]uint8  `json:"board"`
	Turn    uint8     `json:"turn"`
	Winner  uint8     `json:"winner"` // 0=playing, 1=X, 2=O, 3=draw
}

type game struct {
	id        string
	label     string
	state     gameState
	width     int
	height    int
	heartbeat sdk.CommandID
	status    string
}

func newGame() *game {
	g := &game{width: 80, height: 24}
	identity, err := sdk.Whoami()
	if err != nil {
		g.status = "identity unavailable: " + err.Error()
		return g
	}
	sum := sha256.Sum256([]byte(identity.User))
	g.id = hex.EncodeToString(sum[:16])
	g.label = "guest-" + g.id[:6]
	if identity.OwnsApp {
		g.label = "owner-" + g.id[:6]
	}
	if err := g.refresh(true); err != nil {
		g.status = "join failed: " + err.Error()
	}
	return g
}

func defaultState() gameState { return gameState{Turn: 1} }

func decodeState(raw []byte, found bool) (gameState, error) {
	if !found {
		return defaultState(), nil
	}
	var state gameState
	if err := json.Unmarshal(raw, &state); err != nil {
		return gameState{}, err
	}
	if state.Turn != 1 && state.Turn != 2 {
		state.Turn = 1
	}
	return state, nil
}

func (g *game) mutate(change func(*gameState) bool) error {
	for attempt := 0; attempt < 24; attempt++ {
		raw, found, err := sdk.KVGet(gameKey)
		if err != nil {
			return err
		}
		state, err := decodeState(raw, found)
		if err != nil {
			return err
		}
		if !change(&state) {
			g.state = state
			return nil
		}
		next, err := json.Marshal(state)
		if err != nil {
			return err
		}
		var expected [sha256.Size]byte
		if found {
			expected = sdk.KVHash(raw)
		}
		if err := sdk.KVCompareAndSwap(gameKey, expected, next); err == nil {
			g.state = state
			_ = sdk.Publish(gameTopic, []byte("changed"))
			return nil
		} else if !errors.Is(err, sdk.ErrKVConflict) {
			return err
		}
	}
	return sdk.ErrKVConflict
}

func resetBoard(state *gameState) {
	state.Board = [9]uint8{}
	state.Winner = 0
	state.Turn = 1
}

func expireSeats(state *gameState, now time.Time) bool {
	changed := false
	for i := range state.Players {
		seat := &state.Players[i]
		if seat.ID != "" && now.Sub(time.UnixMilli(seat.SeenUnix)) > seatLease {
			*seat = player{}
			changed = true
		}
	}
	if changed {
		resetBoard(state)
	}
	return changed
}

func (g *game) refresh(claimSeat bool) error {
	now := time.Now()
	return g.mutate(func(state *gameState) bool {
		changed := expireSeats(state, now)
		role := playerRole(*state, g.id)
		if role == 0 && claimSeat && g.id != "" {
			for i := range state.Players {
				if state.Players[i].ID == "" {
					state.Players[i] = player{ID: g.id, Label: g.label, SeenUnix: now.UnixMilli()}
					role = i + 1
					resetBoard(state)
					changed = true
					break
				}
			}
		}
		if role != 0 && state.Players[role-1].SeenUnix != now.UnixMilli() {
			state.Players[role-1].SeenUnix = now.UnixMilli()
			changed = true
		}
		return changed
	})
}

func (g *game) reload() {
	raw, found, err := sdk.KVGet(gameKey)
	if err != nil {
		g.status = "sync failed: " + err.Error()
		return
	}
	state, err := decodeState(raw, found)
	if err != nil {
		g.status = "sync failed: " + err.Error()
		return
	}
	g.state = state
}

func playerRole(state gameState, id string) int {
	for i, seat := range state.Players {
		if id != "" && seat.ID == id {
			return i + 1
		}
	}
	return 0
}

func winner(board [9]uint8, mark uint8) bool {
	lines := [8][3]int{
		{0, 1, 2}, {3, 4, 5}, {6, 7, 8},
		{0, 3, 6}, {1, 4, 7}, {2, 5, 8},
		{0, 4, 8}, {2, 4, 6},
	}
	for _, line := range lines {
		if board[line[0]] == mark && board[line[1]] == mark && board[line[2]] == mark {
			return true
		}
	}
	return false
}

func boardFull(board [9]uint8) bool {
	for _, cell := range board {
		if cell == 0 {
			return false
		}
	}
	return true
}

func applyMove(state *gameState, role, cell int) bool {
	if role < 1 || role > 2 || cell < 0 || cell >= len(state.Board) {
		return false
	}
	if state.Winner != 0 {
		resetBoard(state)
		state.Turn = uint8(3 - role)
		return true
	}
	if int(state.Turn) != role || state.Board[cell] != 0 {
		return false
	}
	mark := uint8(role)
	state.Board[cell] = mark
	switch {
	case winner(state.Board, mark):
		state.Winner = mark
	case boardFull(state.Board):
		state.Winner = 3
	default:
		state.Turn = uint8(3 - role)
	}
	return true
}

func (g *game) click(cell int) {
	role := playerRole(g.state, g.id)
	if role == 0 {
		g.status = "spectators cannot move"
		return
	}
	err := g.mutate(func(state *gameState) bool {
		if playerRole(*state, g.id) != role {
			return false
		}
		return applyMove(state, role, cell)
	})
	if err != nil {
		g.status = "move failed: " + err.Error()
		return
	}
	if g.state.Winner == 0 && int(g.state.Turn) != role {
		g.status = "move accepted"
	}
}

func (g *game) release() {
	if g.id == "" {
		return
	}
	_ = g.mutate(func(state *gameState) bool {
		role := playerRole(*state, g.id)
		if role == 0 {
			return false
		}
		state.Players[role-1] = player{}
		resetBoard(state)
		return true
	})
}

func (g *game) Update(event sdk.Event) {
	switch event := event.(type) {
	case sdk.ResizeMsg:
		g.width, g.height = event.W, event.H
	case sdk.TimerMsg:
		if event.ID == g.heartbeat {
			if err := g.refresh(true); err != nil {
				g.status = "heartbeat failed: " + err.Error()
			}
		}
	case sdk.MessageMsg:
		if event.Topic == gameTopic {
			g.reload()
		}
	case sdk.MouseMsg:
		if event.Action == sdk.MouseUp {
			if cell, ok := boardCell(event.X, event.Y, g.width, g.height); ok {
				g.click(cell)
			}
		}
	case sdk.KeyMsg:
		if event.Key == 'q' || event.Key == sdk.KeyEsc || event.Key == sdk.KeyCtrlC {
			sdk.Quit()
		}
	}
}

func (g *game) View() tui.Component {
	return &boardView{
		state:  g.state,
		role:   playerRole(g.state, g.id),
		status: g.status,
	}
}

func boardOrigin(width, height int) (int, int) {
	return (width - boardWidth) / 2, (height - boardHeight) / 2
}

func boardCell(x, y, width, height int) (int, bool) {
	left, top := boardOrigin(width, height)
	rx, ry := x-left, y-top
	if rx < 0 || ry < 0 || rx >= boardWidth || ry >= boardHeight {
		return 0, false
	}
	if rx == 9 || rx == 19 || ry == 3 || ry == 7 {
		return 0, false
	}
	col := rx / 10
	row := ry / 4
	if col > 2 || row > 2 {
		return 0, false
	}
	return row*3 + col, true
}

type boardView struct {
	state      gameState
	role       int
	status     string
	x, y, w, h int
	parent     tui.Component
}

func (*boardView) GetStyle() tui.Style         { return tui.Style{} }
func (*boardView) IsDirty() bool               { return true }
func (*boardView) MakeDirty()                  {}
func (*boardView) ClearDirty()                 {}
func (v *boardView) SetParent(p tui.Component) { v.parent = p }
func (v *boardView) Layout(x, y, w, h int)     { v.x, v.y, v.w, v.h = x, y, w, h }

func (v *boardView) Render(screen *tui.Screen) {
	var background, grid, xStyle, oStyle, title, muted tui.Style
	background.SetBackground(9, 13, 25)
	background.SetForeground(206, 215, 235)
	grid.SetBackground(9, 13, 25)
	grid.SetForeground(89, 108, 143)
	xStyle.SetBackground(9, 13, 25)
	xStyle.SetForeground(255, 105, 135)
	xStyle.AddTextDecoration(tui.Bold)
	oStyle.SetBackground(9, 13, 25)
	oStyle.SetForeground(86, 220, 210)
	oStyle.AddTextDecoration(tui.Bold)
	title.SetBackground(9, 13, 25)
	title.SetForeground(249, 211, 108)
	title.AddTextDecoration(tui.Bold)
	muted.SetBackground(9, 13, 25)
	muted.SetForeground(139, 153, 181)

	fillCells(screen, v.x, v.y, v.w, v.h, ' ', background)
	left, top := boardOrigin(v.w, v.h)
	left += v.x
	top += v.y

	drawCentered(screen, v.x, v.w, top-4, "MOUSE TIC-TAC-TOE", title)
	players := fmt.Sprintf("X %-14s    O %-14s", playerName(v.state.Players[0]), playerName(v.state.Players[1]))
	drawCentered(screen, v.x, v.w, top-2, players, muted)

	for y := 0; y < boardHeight; y++ {
		for x := 0; x < boardWidth; x++ {
			switch {
			case (x == 9 || x == 19) && (y == 3 || y == 7):
				putCell(screen, left+x, top+y, '+', grid)
			case x == 9 || x == 19:
				putCell(screen, left+x, top+y, '|', grid)
			case y == 3 || y == 7:
				putCell(screen, left+x, top+y, '-', grid)
			}
		}
	}
	for cell, mark := range v.state.Board {
		if mark == 0 {
			continue
		}
		col, row := cell%3, cell/3
		x, y := left+col*10+1, top+row*4
		style := xStyle
		glyph := []string{"X     X", "   X   ", "X     X"}
		if mark == 2 {
			style = oStyle
			glyph = []string{" OOOOO ", "O     O", " OOOOO "}
		}
		for line, text := range glyph {
			drawTextCells(screen, x, y+line, text, style)
		}
	}

	drawCentered(screen, v.x, v.w, top+boardHeight+1, gameStatus(v.state, v.role), title)
	if v.status != "" {
		drawCentered(screen, v.x, v.w, top+boardHeight+3, v.status, muted)
	}
	drawCentered(screen, v.x, v.w, v.y+v.h-1, "click a square  //  q disconnects", muted)
}

func playerName(p player) string {
	if p.Label == "" {
		return "(waiting)"
	}
	return p.Label
}

func gameStatus(state gameState, role int) string {
	switch state.Winner {
	case 1:
		return "X wins — click the board for a new round"
	case 2:
		return "O wins — click the board for a new round"
	case 3:
		return "draw — click the board for a new round"
	}
	if state.Players[0].ID == "" || state.Players[1].ID == "" {
		return "waiting for two players"
	}
	if role == 0 {
		return fmt.Sprintf("spectating — %s to move", markName(state.Turn))
	}
	if int(state.Turn) == role {
		return "your turn — click an empty square"
	}
	return fmt.Sprintf("you are %s — waiting for %s", markName(uint8(role)), markName(state.Turn))
}

func markName(mark uint8) string {
	if mark == 1 {
		return "X"
	}
	return "O"
}

func fillCells(s *tui.Screen, x, y, w, h int, r rune, style tui.Style) {
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			putCell(s, x+col, y+row, r, style)
		}
	}
}

func drawCentered(s *tui.Screen, x, width, y int, text string, style tui.Style) {
	drawTextCells(s, x+(width-len([]rune(text)))/2, y, text, style)
}

func drawTextCells(s *tui.Screen, x, y int, text string, style tui.Style) {
	for i, r := range []rune(text) {
		putCell(s, x+i, y, r, style)
	}
}

func putCell(s *tui.Screen, x, y int, r rune, style tui.Style) {
	s.Set(x, y, r, style.GetForeground(), style.GetBackground(), style.GetDecor())
}

func main() {
	game := newGame()
	_ = sdk.Subscribe(gameTopic)
	game.heartbeat, _ = sdk.Schedule(sdk.Every(heartbeatInterval))
	sdk.RunTUI(game, sdk.Meta{Name: "tic-tac-toe", Type: "tui"})
	game.release()
}
