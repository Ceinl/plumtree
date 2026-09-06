package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ceinl/plumtree/sdk/app"
	"github.com/Ceinl/plumtree/sdk/bus"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/kv"
	"github.com/Ceinl/plumtree/sdk/plumtest"
	"github.com/Ceinl/plumtree/sdk/ui"
)

func fakeLookup(ctx context.Context, key string) (string, bool, error) {
	switch key {
	case secretAPIKey:
		return "test-key", true, nil
	case secretModel:
		return "test-model", true, nil
	case secretBaseURL:
		return "https://mock.local/v4/chat/completions", true, nil
	}
	return "", false, nil
}

func missingKeyLookup(ctx context.Context, key string) (string, bool, error) {
	if key == secretAPIKey {
		return "", false, nil
	}
	return fakeLookup(ctx, key)
}

func fakeWhoami(ctx context.Context) (identity.Identity, error) {
	return identity.Identity{User: "test-user", Authenticated: true, Kind: identity.KindSSHKey}, nil
}

// testModel builds a chat model with isolated KV and an instant fake
// completion so tests never touch the network, the process-global native KV
// store, or ambient secrets.
func testModel(t *testing.T, reply, err string) (*model, *kv.Memory) {
	t.Helper()
	mem := kv.NewMemory(nil)
	m := newModel()
	m.lookup = fakeLookup
	m.whoami = fakeWhoami
	m.wrapCtx = func(ctx context.Context) context.Context { return kv.WithAdapter(ctx, mem) }
	m.complete = func(cfg config, history []message, memories []string) app.Command {
		return app.Task(func(ctx context.Context) (app.Event, error) {
			return replyEvent{completion{Text: reply, Err: err}}, nil
		})
	}
	return m, mem
}

func typeRunes(runtime *plumtest.Runtime, text string) {
	for _, r := range text {
		runtime.Key(app.Key(r))
	}
}

func TestAskRevealsReply(t *testing.T) {
	m, mem := testModel(t, "terminal spirits answer in plain text", "")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	typeRunes(runtime, "hi there")
	runtime.Key(app.KeyEnter)
	runtime.Advance(2 * time.Second)
	runtime.ExpectText("terminal spirits answer in plain text")
	runtime.ExpectText("you › hi there")
	saved := mem.Value(convKey(m.uid))
	if !strings.Contains(string(saved), "hi there") || !strings.Contains(string(saved), "terminal spirits") {
		t.Fatalf("conversation not persisted: %s", saved)
	}
}

func TestEnterSkipsReveal(t *testing.T) {
	m, _ := testModel(t, "the whole answer arrives at once", "")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	typeRunes(runtime, "question")
	runtime.Key(app.KeyEnter)
	runtime.Key(app.KeyEnter) // first Enter sent, second skips the reveal
	runtime.ExpectText("the whole answer arrives at once")
}

func TestCompletionErrorBecomesNotice(t *testing.T) {
	m, _ := testModel(t, "", "upstream error (500): broken")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	typeRunes(runtime, "hello")
	runtime.Key(app.KeyEnter)
	runtime.Advance(500 * time.Millisecond)
	runtime.ExpectText("upstream error (500): broken")
}

func TestMissingKeyShowsSetupHint(t *testing.T) {
	m, _ := testModel(t, "unused", "")
	m.lookup = missingKeyLookup
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	runtime.ExpectText("no GLM_API_KEY")
	typeRunes(runtime, "hello")
	runtime.Key(app.KeyEnter)
	runtime.Advance(500 * time.Millisecond)
	runtime.ExpectText("no GLM_API_KEY")
}

func TestSlashCommands(t *testing.T) {
	m, mem := testModel(t, "answer", "")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	typeRunes(runtime, "/help")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("/new reset")
	typeRunes(runtime, "/model")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("model test-model")
	typeRunes(runtime, "/remember likes tea")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("noted")
	typeRunes(runtime, "/memories")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("1. likes tea")
	if !strings.Contains(string(mem.Value(memKey(m.uid))), "likes tea") {
		t.Fatal("memory not persisted")
	}
	typeRunes(runtime, "/forget 1")
	runtime.Key(app.KeyEnter)
	typeRunes(runtime, "/memories")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("nothing remembered yet")
	typeRunes(runtime, "/who")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("registered ssh key")
}

func TestNewClearsConversation(t *testing.T) {
	m, mem := testModel(t, "short answer", "")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	typeRunes(runtime, "first question")
	runtime.Key(app.KeyEnter)
	runtime.Advance(2 * time.Second)
	typeRunes(runtime, "/new")
	runtime.Key(app.KeyEnter)
	runtime.ExpectText("conversation forgotten")
	runtime.ExpectText("say something")
	if saved := mem.Value(convKey(m.uid)); len(saved) != 0 {
		t.Fatalf("conversation not cleared: %s", saved)
	}
}

func TestRetryResendsLastQuestion(t *testing.T) {
	m, mem := testModel(t, "same answer", "")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	typeRunes(runtime, "only question")
	runtime.Key(app.KeyEnter)
	runtime.Advance(2 * time.Second)
	typeRunes(runtime, "/retry")
	runtime.Key(app.KeyEnter)
	runtime.Advance(2 * time.Second)
	runtime.ExpectText("same answer")
	var history []message
	if err := json.Unmarshal(mem.Value(convKey(m.uid)), &history); err != nil {
		t.Fatalf("history unreadable: %v", err)
	}
	if len(history) != 2 || history[0].Content != "only question" {
		t.Fatalf("retry should keep exactly one exchange, got %+v", history)
	}
}

func TestScrollingHidesLiveTail(t *testing.T) {
	m, _ := testModel(t, "", "")
	m.complete = func(cfg config, history []message, memories []string) app.Command {
		return app.Task(func(ctx context.Context) (app.Event, error) {
			return replyEvent{completion{Text: "marker answer " + strings.Repeat("x", 40)}}, nil
		})
	}
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 12))
	for i := 0; i < 12; i++ {
		typeRunes(runtime, "question "+string(rune('a'+i)))
		runtime.Key(app.KeyEnter)
		runtime.Key(app.KeyEnter) // skip reveal
	}
	runtime.ExpectText("question l")
	runtime.Mouse(0, 0, app.MouseNone, app.MouseWheelUp)
	runtime.Mouse(0, 0, app.MouseNone, app.MouseWheelUp)
	runtime.Mouse(0, 0, app.MouseNone, app.MouseWheelUp)
	runtime.ExpectNoText("question l")
	runtime.Key(app.KeyEnd)
	runtime.ExpectText("question l")
}

func TestStoreConversationTrims(t *testing.T) {
	mem := kv.NewMemory(nil)
	ctx := kv.WithAdapter(context.Background(), mem)
	history := make([]message, 0, 40)
	for i := 0; i < 40; i++ {
		history = append(history, message{Role: "user", Content: strings.Repeat("q", 100)})
	}
	if err := saveConversation(ctx, "uid", history); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := loadConversation(ctx, "uid")
	if len(loaded) != convMaxMessages {
		t.Fatalf("expected %d messages after trim, got %d", convMaxMessages, len(loaded))
	}
	if err := saveConversation(ctx, "uid", []message{{Role: "user", Content: strings.Repeat("x", convMaxBytes)}}); err != nil {
		t.Fatalf("oversize save: %v", err)
	}
	if loaded = loadConversation(ctx, "uid"); len(loaded) == 0 {
		t.Fatal("oversize conversation trimmed to nothing")
	}
	if err := clearConversation(ctx, "uid"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if loaded = loadConversation(ctx, "uid"); loaded != nil {
		t.Fatalf("conversation not cleared: %+v", loaded)
	}
}

func TestMemoriesAndNameRoundTrip(t *testing.T) {
	mem := kv.NewMemory(nil)
	ctx := kv.WithAdapter(context.Background(), mem)
	if err := saveName(ctx, "uid", "mira"); err != nil {
		t.Fatalf("save name: %v", err)
	}
	if got := loadName(ctx, "uid"); got != "mira" {
		t.Fatalf("name = %q", got)
	}
	if err := saveMemories(ctx, "uid", []string{"likes tea", "", strings.Repeat("x", 500)}); err != nil {
		t.Fatalf("save memories: %v", err)
	}
	memories := loadMemories(ctx, "uid")
	if len(memories) != 2 {
		t.Fatalf("expected empty entries dropped, got %d", len(memories))
	}
	if len([]rune(memories[1])) != memMaxRunes {
		t.Fatalf("memory not truncated to %d runes", memMaxRunes)
	}
}

func TestWrapText(t *testing.T) {
	wrapped := wrapText("hello world again", 11)
	if len(wrapped) != 2 || wrapped[0] != "hello world" || wrapped[1] != "again" {
		t.Fatalf("wrap = %q", wrapped)
	}
	wrapped = wrapText(strings.Repeat("x", 25), 10)
	if len(wrapped) != 3 {
		t.Fatalf("hard break = %q", wrapped)
	}
	wrapped = wrapText("one\ntwo", 40)
	if len(wrapped) != 2 || wrapped[0] != "one" || wrapped[1] != "two" {
		t.Fatalf("paragraphs = %q", wrapped)
	}
}

func TestParseReply(t *testing.T) {
	cfg := config{BaseURL: defaultBaseURL, Model: defaultModel}
	done := parseReply(cfg, 200, []byte(`{"choices":[{"message":{"content":"hello there"}}]}`))
	if done.Err != "" || done.Text != "hello there" {
		t.Fatalf("parse = %+v", done)
	}
	done = parseReply(cfg, 404, []byte(`{"error":{"message":"model not found","code":"1211"}}`))
	if !strings.Contains(done.Err, "model") || !strings.Contains(done.Err, "GLM_MODEL") {
		t.Fatalf("404 hint = %q", done.Err)
	}
	done = parseReply(cfg, 401, []byte(`{"error":{"message":"token expired or incorrect"}}`))
	if !strings.Contains(done.Err, "GLM_API_KEY rejected") {
		t.Fatalf("401 hint = %q", done.Err)
	}
	done = parseReply(cfg, 200, []byte(`not json at all`))
	if !strings.Contains(done.Err, "unreadable reply") {
		t.Fatalf("bad json = %q", done.Err)
	}
	done = parseReply(cfg, 200, []byte(`{"choices":[{"message":{"content":"  "}}]}`))
	if !strings.Contains(done.Err, "empty reply") {
		t.Fatalf("empty reply = %q", done.Err)
	}
}

func TestBuildRequestShape(t *testing.T) {
	cfg := config{BaseURL: defaultBaseURL, Model: "glm-4.6", Persona: "be brief", APIKey: "k", HasKey: true}
	history := []message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
	request := buildRequest(cfg, history, []string{"likes tea"})
	if request.Model != "glm-4.6" || request.MaxTokens != maxReplyTokens || request.Stream {
		t.Fatalf("request fields = %+v", request)
	}
	if len(request.Messages) != 3 || request.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v", request.Messages)
	}
	if !strings.Contains(request.Messages[0].Content, "likes tea") {
		t.Fatal("memories missing from system prompt")
	}
	raw, err := json.Marshal(request)
	if err != nil || !strings.Contains(string(raw), `"max_tokens":1024`) {
		t.Fatalf("wire format = %s (%v)", raw, err)
	}
}

func TestAPIHistoryBudget(t *testing.T) {
	var history []message
	for i := 0; i < 30; i++ {
		history = append(history, message{Role: "user", Content: strings.Repeat("q", 1000)})
	}
	trimmed := apiHistory(history)
	if len(trimmed) > apiMaxMessages {
		t.Fatalf("kept %d messages", len(trimmed))
	}
	total := 0
	for _, msg := range trimmed {
		total += len(msg.Content)
	}
	if total > apiBudgetRunes {
		t.Fatalf("budget exceeded: %d", total)
	}
	if len(trimmed) == 0 || trimmed[len(trimmed)-1].Content != history[len(history)-1].Content {
		t.Fatal("newest message must be kept")
	}
}

func TestMapPresence(t *testing.T) {
	valid, err := json.Marshal(presencePayload{From: "abc", Name: "mira", Kind: "ask"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	event := mapPresence(bus.Message{Topic: presenceTopic, Data: valid})
	got, ok := event.(presenceEvent)
	if !ok || got.From != "abc" || got.Name != "mira" || got.Text != "asked a question" {
		t.Fatalf("mapped = %+v", event)
	}
	if event := mapPresence(bus.Message{Data: []byte("not json")}); event.(presenceEvent).From != "" {
		t.Fatal("malformed payload must map to an empty event")
	}
	if event := mapPresence(bus.Message{Data: []byte(`{"kind":"ask"}`)}); event.(presenceEvent).From != "" {
		t.Fatal("payload without sender must map to an empty event")
	}
}

func TestCLIAsk(t *testing.T) {
	tree := buildCLI(func(ctx context.Context, cfg config, history []message, memories []string) completion {
		if cfg.Model != "test-model" {
			t.Errorf("config not loaded: %+v", cfg)
		}
		if len(history) != 1 || history[0].Role != "user" || history[0].Content != "what is the answer" {
			t.Errorf("history = %+v", history)
		}
		return completion{Text: "42"}
	}, fakeLookup)
	runtime := plumtest.InvokeCLI(t, tree, plumtest.Shell("ask what is the answer"))
	runtime.ExpectExit(0)
	if got := runtime.Stdout(); got != "42\n" {
		t.Fatalf("stdout = %q", got)
	}
	saved := runtime.GetKV(convKey(uidFor("local")))
	if !strings.Contains(string(saved), "what is the answer") || !strings.Contains(string(saved), "42") {
		t.Fatalf("conversation not persisted: %s", saved)
	}
}

func TestCLIMissingKey(t *testing.T) {
	tree := buildCLI(unexpectedCompletion, missingKeyLookup)
	runtime := plumtest.InvokeCLI(t, tree, plumtest.Shell("ask hello"))
	runtime.ExpectExit(1)
	if !strings.Contains(runtime.Stderr(), "no GLM_API_KEY") {
		t.Fatalf("stderr = %q", runtime.Stderr())
	}
}

// unexpectedCompletion fails a test run if a CLI path that must never reach
// the network actually does.
func unexpectedCompletion(ctx context.Context, cfg config, history []message, memories []string) completion {
	return completion{Err: "test bug: completion invoked"}
}

func TestCLIHistoryClearStatus(t *testing.T) {
	uid := uidFor("local")
	history := []message{{Role: "user", Content: "earlier question"}, {Role: "assistant", Content: "earlier answer"}}
	raw, err := json.Marshal(history)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	historyTree := buildCLI(unexpectedCompletion, fakeLookup)
	runtime := plumtest.InvokeCLI(t, historyTree,
		plumtest.Shell("history --count 5"),
		plumtest.KV(convKey(uid), raw))
	runtime.ExpectExit(0)
	if got := runtime.Stdout(); got != "you › earlier question\nfamiliar › earlier answer\n" {
		t.Fatalf("stdout = %q", got)
	}

	clearTree := buildCLI(unexpectedCompletion, fakeLookup)
	cleared := plumtest.InvokeCLI(t, clearTree, plumtest.Shell("clear"), plumtest.KV(convKey(uid), raw))
	cleared.ExpectExit(0)
	if got := cleared.Stdout(); got != "forgotten.\n" {
		t.Fatalf("stdout = %q", got)
	}

	statusTree := buildCLI(unexpectedCompletion, fakeLookup)
	status := plumtest.InvokeCLI(t, statusTree, plumtest.Shell("status"))
	status.ExpectExit(0)
	for _, want := range []string{"model     test-model\n", "endpoint  mock.local\n", "transport curl\n", "api key   set\n", "persona   default\n", "identity  local\n", "uid       " + uid + "\n"} {
		if !strings.Contains(status.Stdout(), want) {
			t.Fatalf("stdout %q missing %q", status.Stdout(), want)
		}
	}
}

func TestRealCompleteErrorPaths(t *testing.T) {
	cfg := config{BaseURL: "https://mock.local/v4/chat", Model: "m", Hint: "no GLM_API_KEY — set it"}
	done := realComplete(context.Background(), cfg, nil, nil)
	if done.Err != cfg.Hint {
		t.Fatalf("missing key = %+v", done)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cfg.HasKey = true
	cfg.APIKey = "k"
	done = realComplete(canceled, cfg, nil, nil)
	if done.Err == "" {
		t.Fatal("canceled context must produce a completion error, not a Go error")
	}
}

func TestNoticeExpiryAndPresenceIgnore(t *testing.T) {
	m, _ := testModel(t, "answer", "")
	m.uid = "test-uid"
	m.setNotice("temporary", ui.Muted)
	if m.notice.text == "" {
		t.Fatal("notice not set")
	}
	m.Update(tickEvent{})
	if m.notice.left != noticeTicks-1 {
		t.Fatalf("notice.left = %d", m.notice.left)
	}
	m.Update(presenceEvent{From: m.uid, Text: "self"})
	if m.presenceLeft != 0 {
		t.Fatal("own presence must be ignored")
	}
	m.Update(presenceEvent{From: "someone-else", Name: "mira", Text: "hi"})
	if m.presenceLeft == 0 || m.presence.Name != "mira" {
		t.Fatal("remote presence must be shown")
	}
}

func TestRealCompleteViaFetchMockServer(t *testing.T) {
	var gotHeader, gotPath string
	var gotRequest wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotRequest); err != nil {
			t.Errorf("request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"mock reply"}}]}`))
	}))
	defer server.Close()
	cfg := config{BaseURL: server.URL + "/v4/chat/completions", Model: "mock-1", Persona: defaultPersona(), APIKey: "k-test", HasKey: true, Transport: transportFetch}
	done := realComplete(context.Background(), cfg, []message{{Role: "user", Content: "ping"}}, []string{"likes tea"})
	if done.Err != "" || done.Text != "mock reply" {
		t.Fatalf("completion = %+v", done)
	}
	if gotHeader != "" {
		t.Fatalf("fetch transport must not send headers, got %q", gotHeader)
	}
	if gotPath != "/v4/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotRequest.Model != "mock-1" || len(gotRequest.Messages) != 2 {
		t.Fatalf("wire request = %+v", gotRequest)
	}
	if !strings.Contains(gotRequest.Messages[0].Content, "likes tea") {
		t.Fatal("memories missing from wire system prompt")
	}
}

func TestRealCompleteURLKeyPlaceholder(t *testing.T) {
	var gotURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()
	cfg := config{BaseURL: server.URL + "/{key}/chat", APIKey: "sk secret/123", HasKey: true, Transport: transportFetch}
	done := realComplete(context.Background(), cfg, nil, nil)
	if done.Err != "" || done.Text != "ok" {
		t.Fatalf("completion = %+v", done)
	}
	if !strings.Contains(gotURL, "/sk+secret%2F123/chat") {
		t.Fatalf("key not substituted in URL: %q", gotURL)
	}
}

func TestRealCompleteViaCurl(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"curl reply"}}]}`))
	}))
	defer server.Close()
	cfg := config{BaseURL: server.URL, Model: "mock-1", APIKey: "secret-key", HasKey: true, Transport: transportCurl}
	done := realComplete(context.Background(), cfg, []message{{Role: "user", Content: "hi"}}, nil)
	if done.Err != "" {
		t.Fatalf("curl transport: %s", done.Err)
	}
	if done.Text != "curl reply" {
		t.Fatalf("text = %q", done.Text)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"model":"mock-1"`) {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestRealCompleteModelNotFoundHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"model not found","code":"1211"}}`))
	}))
	defer server.Close()
	cfg := config{BaseURL: server.URL, Model: "glm-4.6", APIKey: "k", HasKey: true, Transport: transportFetch}
	done := realComplete(context.Background(), cfg, nil, nil)
	if !strings.Contains(done.Err, "GLM_MODEL") {
		t.Fatalf("404 hint = %q", done.Err)
	}
}

func TestRealCompleteViaAnthropicMock(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	var gotPath, gotAuth, gotVersion, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("anthropic-version")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"anthropic says hi"}]}`))
	}))
	defer server.Close()
	cfg := config{BaseURL: server.URL, Model: "mock-claude", APIKey: "secret-key", HasKey: true, Transport: transportAnthropic}
	done := realComplete(context.Background(), cfg, []message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hey"}}, []string{"likes tea"})
	if done.Err != "" || done.Text != "anthropic says hi" {
		t.Fatalf("completion = %+v", done)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if gotVersion == "" {
		t.Fatal("anthropic-version header missing")
	}
	var request anthropicRequest
	if err := json.Unmarshal([]byte(gotBody), &request); err != nil {
		t.Fatalf("body: %v", err)
	}
	if request.Model != "mock-claude" || request.MaxTokens != maxReplyTokens || request.Stream {
		t.Fatalf("request fields = %+v", request)
	}
	if request.System == "" || !strings.Contains(request.System, "likes tea") {
		t.Fatal("memories missing from anthropic system field")
	}
	if len(request.Messages) != 2 || request.Messages[0].Role != "user" || request.Messages[1].Role != "assistant" {
		t.Fatalf("messages = %+v", request.Messages)
	}
}

func TestParseAnthropicReply(t *testing.T) {
	cfg := config{BaseURL: "https://api.anthropic.com", Model: "m"}
	done := parseAnthropicReply(cfg, 200, []byte(`{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`))
	if done.Err != "" || done.Text != "ab" {
		t.Fatalf("parse = %+v", done)
	}
	done = parseAnthropicReply(cfg, 400, []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"model not entitled"}}`))
	if !strings.Contains(done.Err, "model not entitled") {
		t.Fatalf("error hint = %q", done.Err)
	}
	done = parseAnthropicReply(cfg, 200, []byte(`{"content":[]}`))
	if !strings.Contains(done.Err, "empty reply") {
		t.Fatalf("empty = %q", done.Err)
	}
}

func TestMemoriesDisplayDoesNotChangeStoredNotes(t *testing.T) {
	m, mem := testModel(t, "answer", "")
	runtime := plumtest.Start(t, m, plumtest.Viewport(80, 24))
	for _, command := range []string{"/remember likes tea", "/memories", "/memories", "/remember likes Go"} {
		typeRunes(runtime, command)
		runtime.Key(app.KeyEnter)
	}
	want := []string{"likes tea", "likes Go"}
	if !slices.Equal(m.memories, want) {
		t.Fatalf("notes changed: %q", m.memories)
	}
	var stored []string
	if err := json.Unmarshal(mem.Value(memKey(m.uid)), &stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored, want) {
		t.Fatalf("stored notes changed: %q", stored)
	}
}

func TestConversationBoundsEncodedBytesWithoutChangingCaller(t *testing.T) {
	for _, content := range []string{strings.Repeat("x", convMaxBytes*2), strings.Repeat("界", convMaxBytes), strings.Repeat("\x00", convMaxBytes)} {
		for _, count := range []int{1, 2} {
			mem := kv.NewMemory(nil)
			ctx := kv.WithAdapter(context.Background(), mem)
			history := make([]message, count)
			for i := range history {
				history[i] = message{Role: "assistant", Content: content}
			}
			if err := saveConversation(ctx, "uid", history); err != nil {
				t.Fatal(err)
			}
			if raw := mem.Value(convKey("uid")); len(raw) > convMaxBytes {
				t.Fatalf("stored %d bytes", len(raw))
			}
			if len(loadConversation(ctx, "uid")) != count {
				t.Fatal("lost retained turns")
			}
			for _, msg := range history {
				if msg.Content != content {
					t.Fatal("caller history changed")
				}
			}
		}
	}
}

func TestConfiguredTransports(t *testing.T) {
	for _, transport := range []string{"", transportFetch, transportCurl, transportAnthropic} {
		cfg := loadConfig(context.Background(), func(ctx context.Context, key string) (string, bool, error) {
			if key == secretTransport {
				return transport, transport != "", nil
			}
			return fakeLookup(ctx, key)
		})
		want := transport
		if want == "" {
			want = transportCurl
		}
		if cfg.Transport != want {
			t.Fatalf("transport %q: got %q", transport, cfg.Transport)
		}
	}
}

func TestCompletionRejectsInsecureRemoteEndpoints(t *testing.T) {
	for _, transport := range []string{transportFetch, transportCurl, transportAnthropic} {
		for _, endpoint := range []string{"http://example.com/{key}", "file:///tmp/key", "https:///missing-host"} {
			done := realComplete(context.Background(), config{HasKey: true, APIKey: "secret", BaseURL: endpoint, Transport: transport}, nil, nil)
			if !strings.Contains(done.Err, "HTTPS") {
				t.Fatalf("%s %s: %+v", transport, endpoint, done)
			}
		}
	}
}
