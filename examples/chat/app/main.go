// Command chat is a durable, live Plumtree chat room. SSH identity remembers
// display names, KV stores history, and pub/sub updates concurrent sessions.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Ceinl/plumtree/sdk"
	"github.com/Ceinl/plumtree/sdk/abi"
	"github.com/Ceinl/plumtree/sdk/tui"
	"github.com/Ceinl/plumtree/sdk/tui/components"
)

const (
	roomTopic     = "lobby"
	sequenceKey   = "chat/next"
	messagePrefix = "chat/messages/"
	profilePrefix = "chat/users/"
	maxInputRunes = 240
	maxNameRunes  = 24
	historyLimit  = 200
)

var whoami = sdk.Whoami

type profile struct {
	Name string `json:"name"`
}

type message struct {
	ID   uint64 `json:"id"`
	From string `json:"from"`
	Text string `json:"text"`
}

type chat struct {
	identity sdk.Identity
	userKey  string
	name     string
	input    []rune
	naming   bool
	messages []message
	seen     map[uint64]bool
	status   string
	height   int
}

func newChat() *chat {
	c := &chat{seen: make(map[uint64]bool), naming: true, height: 24}
	id, err := whoami()
	if err != nil {
		c.status = "identity unavailable; this visit cannot be remembered"
		c.loadHistory()
		return c
	}
	c.identity = id
	if id.Kind == sdk.IdentityAnonymous {
		c.status = "anonymous visits are not remembered; reconnect with an SSH key"
	} else {
		c.userKey = identityKey(id.User)
		if raw, ok, err := sdk.KVGet(profilePrefix + c.userKey); err == nil && ok {
			var saved profile
			if json.Unmarshal(raw, &saved) == nil && validName(saved.Name) {
				c.name = saved.Name
				c.naming = false
				c.status = "welcome back, " + saved.Name
			}
		}
	}
	c.loadHistory()
	return c
}

func identityKey(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:16])
}

func validName(name string) bool {
	runes := []rune(strings.TrimSpace(name))
	if len(runes) < 1 || len(runes) > maxNameRunes {
		return false
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (c *chat) rememberName(name string) bool {
	name = strings.TrimSpace(name)
	if !validName(name) {
		c.status = fmt.Sprintf("name must be 1-%d printable characters", maxNameRunes)
		return false
	}
	if c.userKey == "" {
		c.name, c.naming = name, false
		c.status = "name set for this visit; use an SSH key to be remembered"
		return true
	}
	raw, _ := json.Marshal(profile{Name: name})
	if err := sdk.KVSet(profilePrefix+c.userKey, raw); err != nil {
		c.status = "could not remember name: " + err.Error()
		return false
	}
	c.name, c.naming = name, false
	c.status = "name remembered for this SSH identity"
	return true
}

func (c *chat) loadHistory() {
	keys, err := sdk.KVList(messagePrefix, abi.KVMaxList)
	if err != nil {
		c.status = "could not load history: " + err.Error()
		return
	}
	for _, key := range keys {
		c.loadMessage(key)
	}
}

func (c *chat) loadMessage(key string) {
	raw, ok, err := sdk.KVGet(key)
	if err != nil || !ok {
		return
	}
	var msg message
	if json.Unmarshal(raw, &msg) != nil || msg.ID == 0 || c.seen[msg.ID] {
		return
	}
	c.seen[msg.ID] = true
	c.messages = append(c.messages, msg)
	sort.Slice(c.messages, func(i, j int) bool { return c.messages[i].ID < c.messages[j].ID })
	if len(c.messages) > historyLimit {
		pruned := c.messages[:len(c.messages)-historyLimit]
		for _, old := range pruned {
			delete(c.seen, old.ID)
		}
		c.messages = c.messages[len(c.messages)-historyLimit:]
	}
}

func nextMessageID() (uint64, error) {
	for attempts := 0; attempts < 16; attempts++ {
		raw, ok, err := sdk.KVGet(sequenceKey)
		if err != nil {
			return 0, err
		}
		var current uint64
		if ok {
			current, err = strconv.ParseUint(string(raw), 10, 64)
			if err != nil {
				return 0, err
			}
		}
		next := current + 1
		var expected [sha256.Size]byte
		if ok {
			expected = sdk.KVHash(raw)
		}
		if err := sdk.KVCompareAndSwap(sequenceKey, expected, []byte(strconv.FormatUint(next, 10))); err == nil {
			return next, nil
		} else if !errors.Is(err, sdk.ErrKVConflict) {
			return 0, err
		}
	}
	return 0, sdk.ErrKVConflict
}

func (c *chat) send(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.HasPrefix(text, "/name ") {
		c.rememberName(strings.TrimSpace(strings.TrimPrefix(text, "/name ")))
		return
	}
	id, err := nextMessageID()
	if err != nil {
		c.status = "send failed: " + err.Error()
		return
	}
	msg := message{ID: id, From: c.name, Text: text}
	raw, _ := json.Marshal(msg)
	key := fmt.Sprintf("%s%020d", messagePrefix, id)
	if err := sdk.KVSet(key, raw); err != nil {
		c.status = "send failed: " + err.Error()
		return
	}
	if id > historyLimit {
		_ = sdk.KVDelete(fmt.Sprintf("%s%020d", messagePrefix, id-historyLimit))
	}
	// The key is the live nudge; KV remains the source of truth if a bus event
	// is dropped. The publisher also receives this event.
	if err := sdk.Publish(roomTopic, []byte(key)); err != nil {
		c.loadMessage(key)
		c.status = "saved, but live delivery failed: " + err.Error()
	}
}

func (c *chat) Update(ev sdk.Event) {
	switch e := ev.(type) {
	case sdk.ResizeMsg:
		c.height = e.H
	case sdk.MessageMsg:
		if e.Topic == roomTopic && strings.HasPrefix(string(e.Data), messagePrefix) {
			// Re-read the bounded log so one later nudge also repairs any earlier
			// best-effort bus delivery that this session missed.
			c.loadHistory()
		}
	case sdk.KeyMsg:
		switch e.Key {
		case sdk.KeyCtrlC, sdk.KeyEsc:
			sdk.Quit()
		case sdk.KeyBackspace, sdk.KeyDelete:
			if len(c.input) > 0 {
				c.input = c.input[:len(c.input)-1]
			}
		case sdk.KeyEnter:
			text := string(c.input)
			c.input = nil
			if c.naming {
				c.rememberName(text)
			} else {
				c.send(text)
			}
		default:
			if r := e.Rune(); r != 0 && utf8.ValidRune(r) && !unicode.IsControl(r) && len(c.input) < maxInputRunes {
				c.input = append(c.input, r)
			}
		}
	}
}

func (c *chat) View() tui.Component {
	var bg, titleStyle, muted tui.Style
	bg.SetBackground(12, 16, 28)
	bg.SetForeground(220, 226, 240)
	titleStyle.SetBackground(12, 16, 28)
	titleStyle.SetForeground(102, 225, 255)
	titleStyle.AddTextDecoration(tui.Bold)
	muted.SetBackground(12, 16, 28)
	muted.SetForeground(137, 151, 175)

	root := components.NewDiv()
	root.SetDirection(tui.Column)
	root.SetSize(tui.Grow, tui.Grow)
	root.SetPadding(tui.Padding{Top: tui.Px(1), Right: tui.Px(2), Bottom: tui.Px(1), Left: tui.Px(2)})
	root.SetStyle(bg)

	title := components.NewText("PLUMTREE LOBBY  //  durable + live")
	title.SetStyle(titleStyle)
	root.AppendChild(chatBox(tui.Px(1), bg, title))

	available := c.height - 7
	if available < 3 {
		available = 3
	}
	start := len(c.messages) - available
	if start < 0 {
		start = 0
	}
	lines := make([]string, 0, len(c.messages)-start)
	for _, msg := range c.messages[start:] {
		lines = append(lines, fmt.Sprintf("%-16s  %s", msg.From, msg.Text))
	}
	if len(lines) == 0 {
		lines = append(lines, "No messages yet. Say hello.")
	}
	root.AppendChild(chatBox(tui.Grow, bg, components.NewText(strings.Join(lines, "\n"))))

	prompt := "message"
	if c.naming {
		prompt = "choose a display name"
	}
	root.AppendChild(chatBox(tui.Px(1), bg, components.NewText(fmt.Sprintf("> %s: %s_", prompt, string(c.input)))))
	status := components.NewText(c.status + "  (Enter sends · /name renames · Esc quits)")
	status.SetStyle(muted)
	root.AppendChild(chatBox(tui.Px(1), bg, status))
	return root
}

func chatBox(height tui.Unit, style tui.Style, child tui.Component) *components.Div {
	box := components.NewDiv()
	box.SetSize(tui.Grow, height)
	box.SetStyle(style)
	box.AppendChild(child)
	return box
}

func main() {
	c := newChat()
	if err := sdk.Subscribe(roomTopic); err != nil {
		c.status = "live updates unavailable: " + err.Error()
	}
	sdk.RunTUI(c, sdk.Meta{Name: "chat", Type: "tui"})
}
