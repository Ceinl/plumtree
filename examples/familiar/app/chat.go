package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/bus"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/ui"
)

// phase is where the conversation transport currently stands.
type phase int

const (
	phaseIdle phase = iota
	phaseThinking
	phaseRevealing
)

const (
	presenceTopic = "familiar/presence"
	tickKey       = app.SubscriptionKey("tick")
	presenceKey   = app.SubscriptionKey("presence")

	inputMaxRunes    = 4000
	revealChunkRunes = 12 // runes streamed per tick while a reply lands
	activeTick       = 70 * time.Millisecond
	idleTick         = 600 * time.Millisecond
	noticeTicks      = 60
	presenceTicks    = 50
)

// Events delivered to Update.
type tickEvent struct{}

type readyEvent struct {
	user     string
	uid      string
	name     string
	auth     bool
	kind     identity.Kind
	cfg      config
	history  []message
	memories []string
	err      string
}

type replyEvent struct{ completion }

type savedEvent struct{ err string }

type presenceEvent struct {
	From string
	Name string
	Text string
}

// noticeState is a transient status line above the input row.
type noticeState struct {
	text string
	role ui.Role
	left int
}

// model is the whole chat state. The function fields are seams for plumtest;
// production wiring happens in newModel.
type model struct {
	complete func(cfg config, history []message, memories []string) app.Command
	lookup   lookupFunc
	whoami   whoamiFunc
	wrapCtx  func(ctx context.Context) context.Context

	ready    bool
	user     string
	uid      string
	name     string
	auth     bool
	kind     identity.Kind
	cfg      config
	history  []message
	memories []string

	phase        phase
	spinner      int
	revealText   []rune
	revealPos    int
	input        []rune
	scroll       int
	notice       noticeState
	presence     presenceEvent
	presenceLeft int
	w, h         int
}

func (m *model) Init() app.Command {
	return m.task(func(ctx context.Context) (app.Event, error) {
		return m.loadSession(ctx), nil
	})
}

// loadSession reads identity, memory, and secrets in one pass. Nothing here
// may return a Go error — every problem degrades into a notice.
func (m *model) loadSession(ctx context.Context) readyEvent {
	id, err := m.whoami(ctx)
	if err != nil {
		id = identity.Identity{User: "anonymous:unknown", Kind: identity.KindAnonymous}
	}
	uid := uidFor(id.User)
	cfg := loadConfig(ctx, m.lookup)
	event := readyEvent{
		user:     id.User,
		uid:      uid,
		name:     loadName(ctx, uid),
		auth:     id.Authenticated,
		kind:     id.Kind,
		cfg:      cfg,
		history:  loadConversation(ctx, uid),
		memories: loadMemories(ctx, uid),
	}
	if cfg.Hint != "" {
		event.err = cfg.Hint
	}
	return event
}

func (m *model) Subscriptions() app.Subscription {
	interval := idleTick
	if m.phase != phaseIdle {
		interval = activeTick
	}
	return app.Merge(
		app.Every(tickKey, interval, tickEvent{}),
		bus.Messages(presenceKey, presenceTopic, mapPresence),
	)
}

// mapPresence decodes a presence broadcast; malformed or foreign-topic
// messages become an empty event that Update ignores.
func mapPresence(msg bus.Message) app.Event {
	if msg.Err != nil {
		return presenceEvent{}
	}
	var payload presencePayload
	if json.Unmarshal(msg.Data, &payload) != nil || payload.From == "" {
		return presenceEvent{}
	}
	return presenceEvent{From: payload.From, Name: payload.Name, Text: payload.Text}
}

func (m *model) Update(event app.Event) app.Command {
	switch ev := event.(type) {
	case readyEvent:
		m.ready = true
		m.user, m.uid, m.name = ev.user, ev.uid, ev.name
		m.auth, m.kind, m.cfg = ev.auth, ev.kind, ev.cfg
		m.history, m.memories = ev.history, ev.memories
		if ev.err != "" {
			m.setNotice(ev.err, ui.Error)
		}
	case tickEvent:
		m.spinner++
		if m.notice.left > 0 {
			m.notice.left--
			if m.notice.left == 0 {
				m.notice = noticeState{}
			}
		}
		if m.presenceLeft > 0 {
			m.presenceLeft--
			if m.presenceLeft == 0 {
				m.presence = presenceEvent{}
			}
		}
		if m.phase == phaseRevealing {
			m.revealPos = min(len(m.revealText), m.revealPos+revealChunkRunes)
			if m.revealPos >= len(m.revealText) {
				m.phase = phaseIdle
			}
		}
	case replyEvent:
		if m.phase != phaseThinking {
			break
		}
		if ev.Err != "" {
			m.phase = phaseIdle
			m.setNotice(ev.Err, ui.Error)
			break
		}
		m.history = append(m.history, message{Role: "assistant", Content: ev.Text})
		m.revealText = []rune(ev.Text)
		m.revealPos = 0
		m.phase = phaseRevealing
		m.scroll = 0
		return m.saveHistory()
	case savedEvent:
		if ev.err != "" {
			m.setNotice("could not save: "+ev.err, ui.Error)
		}
	case app.ResizeEvent:
		m.w, m.h = max(20, ev.Width), max(8, ev.Height)
		m.scroll = 0
	case app.MouseEvent:
		switch ev.Action {
		case app.MouseWheelUp:
			m.scroll += 3
		case app.MouseWheelDown:
			m.scroll = max(0, m.scroll-3)
		}
	case app.PasteEvent:
		text := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, ev.Text)
		for _, r := range text {
			if len(m.input) >= inputMaxRunes {
				break
			}
			m.input = append(m.input, r)
		}
	case app.KeyEvent:
		return m.key(ev)
	case presenceEvent:
		if ev.From != "" && ev.From != m.uid {
			m.presence = ev
			m.presenceLeft = presenceTicks
		}
	}
	return app.Noop()
}

func (m *model) key(ev app.KeyEvent) app.Command {
	switch ev.Key {
	case app.KeyCtrlC:
		return app.Quit(app.WithGoodbye("the familiar disperses — summon it again anytime ✦"))
	case app.KeyEnter:
		if m.phase == phaseRevealing {
			m.revealPos = len(m.revealText)
			m.phase = phaseIdle
			return app.Noop()
		}
		if m.phase == phaseThinking {
			return app.Noop()
		}
		text := strings.TrimSpace(string(m.input))
		m.input = nil
		return m.submit(text)
	case app.KeyBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case app.KeyEscape:
		if m.phase == phaseRevealing {
			m.revealPos = len(m.revealText)
			m.phase = phaseIdle
		} else if m.scroll > 0 {
			m.scroll = 0
		} else if len(m.input) > 0 {
			m.input = nil
		}
	case app.KeyPageUp:
		m.scroll += 10
	case app.KeyPageDown:
		m.scroll = max(0, m.scroll-10)
	case app.KeyHome:
		m.scroll = 1 << 30
	case app.KeyEnd:
		m.scroll = 0
	default:
		if r := ev.Rune(); r != 0 && len(m.input) < inputMaxRunes {
			m.input = append(m.input, r)
		}
	}
	return app.Noop()
}

func (m *model) submit(text string) app.Command {
	if text == "" {
		return app.Noop()
	}
	if strings.HasPrefix(text, "/") {
		return m.slash(text)
	}
	return m.ask(text, true)
}

// ask sends one user prompt upstream. appendUser is false for /retry, which
// re-sends the last question without duplicating it in the transcript.
func (m *model) ask(text string, appendUser bool) app.Command {
	if !m.ready {
		m.setNotice("still waking — one moment", ui.Muted)
		return app.Noop()
	}
	if m.phase != phaseIdle {
		return app.Noop()
	}
	if appendUser {
		m.history = append(m.history, message{Role: "user", Content: text})
	}
	m.phase = phaseThinking
	m.scroll = 0
	publish := bus.Publish(presenceTopic, presencePayload{
		From: m.uid,
		Name: m.label(),
		Kind: "ask",
		Text: truncateRunes(text, 80),
	}.encode()).Ignore()
	ask := m.complete(m.cfg, apiHistory(m.history), m.memories)
	if appendUser {
		return app.Batch(publish, m.saveHistory(), ask)
	}
	return app.Batch(publish, ask)
}

func (m *model) saveHistory() app.Command {
	return m.task(func(ctx context.Context) (app.Event, error) {
		if err := saveConversation(ctx, m.uid, m.history); err != nil {
			return savedEvent{err.Error()}, nil
		}
		return nil, nil
	})
}

func (m *model) slash(text string) app.Command {
	fields := strings.Fields(text)
	name := fields[0]
	args := strings.TrimSpace(text[len(name):])
	switch name {
	case "/help":
		m.setNotice("ask anything · /new reset · /name N · /remember F · /forget N|all · /memories · /retry · /model · /who · /quit", ui.Muted)
	case "/new":
		m.history = nil
		m.revealText = nil
		m.revealPos = 0
		m.phase = phaseIdle
		m.setNotice("conversation forgotten", ui.Muted)
		return m.task(func(ctx context.Context) (app.Event, error) {
			if err := clearConversation(ctx, m.uid); err != nil {
				return savedEvent{err.Error()}, nil
			}
			return nil, nil
		})
	case "/name":
		if args == "" {
			m.setNotice("usage: /name <display name>", ui.Error)
			return app.Noop()
		}
		m.name = truncateRunes(args, nameMaxRunes)
		m.setNotice("you are "+m.name, ui.Muted)
		return m.task(func(ctx context.Context) (app.Event, error) {
			if err := saveName(ctx, m.uid, m.name); err != nil {
				return savedEvent{err.Error()}, nil
			}
			return nil, nil
		})
	case "/remember":
		if args == "" {
			m.setNotice("usage: /remember <fact>", ui.Error)
			return app.Noop()
		}
		if len(m.memories) >= memMax {
			m.setNotice("memory is full — /forget something first", ui.Error)
			return app.Noop()
		}
		m.memories = append(m.memories, truncateRunes(args, memMaxRunes))
		m.setNotice("noted", ui.Muted)
		return m.saveMemories()
	case "/forget":
		switch {
		case args == "all":
			m.memories = nil
			m.setNotice("memory cleared", ui.Muted)
			return m.saveMemories()
		default:
			index, err := strconv.Atoi(args)
			if err != nil || index < 1 || index > len(m.memories) {
				m.setNotice("usage: /forget <number 1-"+strconv.Itoa(len(m.memories))+"> or /forget all", ui.Error)
				return app.Noop()
			}
			m.memories = append(m.memories[:index-1], m.memories[index:]...)
			m.setNotice("forgotten", ui.Muted)
			return m.saveMemories()
		}
	case "/memories":
		if len(m.memories) == 0 {
			m.setNotice("nothing remembered yet — /remember adds a note", ui.Muted)
			return app.Noop()
		}
		shown := m.memories
		extra := ""
		if len(shown) > 3 {
			shown = shown[:3]
			extra = " … +" + strconv.Itoa(len(m.memories)-3) + " more"
		}
		for i, memory := range shown {
			shown[i] = strconv.Itoa(i+1) + ". " + memory
		}
		m.setNotice(strings.Join(shown, "\n")+extra, ui.Muted)
	case "/retry":
		if m.phase != phaseIdle {
			return app.Noop()
		}
		last := -1
		for i, msg := range m.history {
			if msg.Role == "user" {
				last = i
			}
		}
		if last < 0 {
			m.setNotice("nothing to retry", ui.Error)
			return app.Noop()
		}
		question := m.history[last].Content
		m.history = m.history[:last]
		return m.ask(question, true)
	case "/model":
		keyState := "missing"
		if m.cfg.HasKey {
			keyState = "set"
		}
		m.setNotice("model "+m.cfg.Model+" · "+hostOf(m.cfg.BaseURL)+" · via "+m.cfg.Transport+" · api key "+keyState, ui.Muted)
	case "/who":
		kind := "anonymous visitor (ephemeral conversation)"
		switch {
		case m.auth:
			kind = "registered ssh key"
		case m.kind == identity.KindSSHKey:
			kind = "ssh key (not registered on this server)"
		}
		m.setNotice("you are "+m.label()+" ("+kind+") · "+m.uid, ui.Muted)
	case "/quit", "/q":
		return app.Quit(app.WithGoodbye("the familiar disperses — summon it again anytime ✦"))
	default:
		m.setNotice("unknown command "+name+" — /help lists them", ui.Error)
	}
	return app.Noop()
}

func (m *model) saveMemories() app.Command {
	return m.task(func(ctx context.Context) (app.Event, error) {
		if err := saveMemories(ctx, m.uid, m.memories); err != nil {
			return savedEvent{err.Error()}, nil
		}
		return nil, nil
	})
}

func (m *model) setNotice(text string, role ui.Role) {
	m.notice = noticeState{text: truncateRunes(text, 240), role: role, left: noticeTicks}
}

// label is the display name shown in the banner and in presence broadcasts.
// The gateway distinguishes registered keys (authenticated), unregistered but
// proved keys (stable identity), and per-connection anonymous visitors.
func (m *model) label() string {
	if m.name != "" {
		return m.name
	}
	switch {
	case m.auth:
		return "key-" + m.uid[:min(6, len(m.uid))]
	case m.kind == identity.KindSSHKey:
		return "pub-" + m.uid[:min(6, len(m.uid))]
	default:
		return "anon-" + m.uid[:min(6, len(m.uid))]
	}
}

// turns counts user prompts in the resumed conversation.
func (m *model) turns() int {
	count := 0
	for _, msg := range m.history {
		if msg.Role == "user" {
			count++
		}
	}
	return count
}

// task wraps finite work so its context carries test adapters and its errors
// never reach the runtime (a Task error would end the session).
func (m *model) task(run func(ctx context.Context) (app.Event, error)) app.Command {
	return app.Task(func(ctx context.Context) (app.Event, error) {
		return run(m.wrap(ctx))
	})
}

// tuiComplete adapts a raw completion into the runtime command shape the chat
// model consumes. plumtest substitutes the `complete` field directly, so the
// network never runs in tests.
func tuiComplete(complete completeFunc) func(config, []message, []string) app.Command {
	return func(cfg config, history []message, memories []string) app.Command {
		return app.Task(func(ctx context.Context) (app.Event, error) {
			return replyEvent{complete(ctx, cfg, history, memories)}, nil
		})
	}
}

func (m *model) wrap(ctx context.Context) context.Context {
	if m.wrapCtx != nil {
		return m.wrapCtx(ctx)
	}
	return ctx
}

type presencePayload struct {
	From string `json:"from"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func (p presencePayload) encode() []byte {
	raw, _ := json.Marshal(p)
	return raw
}

// apiHistory bounds the conversation sent upstream: recent messages only,
// with a hard character budget measured from the newest turn backwards.
func apiHistory(history []message) []message {
	if len(history) > apiMaxMessages {
		history = history[len(history)-apiMaxMessages:]
	}
	total := 0
	for i := len(history) - 1; i >= 0; i-- {
		total += len(history[i].Content)
		if total > apiBudgetRunes {
			return history[i+1:]
		}
	}
	return history
}

// spinnerFrame cycles the thinking indicator.
func spinnerFrame(tick int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[tick%len(frames)]
}
