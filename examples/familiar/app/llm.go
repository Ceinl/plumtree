package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Ceinl/plumtree/sdk/fetch"
	"github.com/Ceinl/plumtree/sdk/hostexec"
	"github.com/Ceinl/plumtree/sdk/identity"
	"github.com/Ceinl/plumtree/sdk/secrets"
)

// Secrets the leaf reads. GLM_API_KEY is required; the rest fall back to
// defaults. Hosted curl also requires operator approval in the host allowlist.
const (
	secretAPIKey    = "GLM_API_KEY"
	secretModel     = "GLM_MODEL"
	secretBaseURL   = "GLM_BASE_URL"
	secretPersona   = "FAMILIAR_PERSONA"
	secretTransport = "FAMILIAR_TRANSPORT"

	defaultBaseURL = "https://api.z.ai/api/paas/v4/chat/completions"
	defaultModel   = "glm-4.6"
	maxReplyTokens = 1024
	temperature    = 0.7

	keyPlaceholder = "{key}"

	// apiMaxMessages and apiBudgetRunes bound the context sent upstream.
	apiMaxMessages = 16
	apiBudgetRunes = 12000
)

// Transports. The clean fetch capability sends no request headers by design
// (abi.FetchRequest v1), so an Authorization header cannot ride through it:
//
//   - transportFetch: use the gated egress capability. Choose
//     endpoints that need no key or take it in the URL — put {key} anywhere
//     in GLM_BASE_URL and the API key is substituted there.
//   - transportCurl (default): run `curl` through the host-command capability, which
//     can send headers. Requires the operator to allowlist curl.
const (
	transportFetch     = "fetch"
	transportCurl      = "curl"
	transportAnthropic = "anthropic"
)

// config is everything a completion needs from the host environment. It is
// assembled once per session; HasKey is only consulted when a prompt is sent,
// so a missing key surfaces as setup guidance rather than a broken leaf.
type config struct {
	BaseURL   string
	Model     string
	Persona   string
	APIKey    string
	HasKey    bool
	Transport string
	Hint      string
}

func defaultPersona() string {
	return "You are Familiar — the terminal-dwelling incarnation of ZCode, " +
		"a coding agent living inside a Plumtree leaf on the user's server.\n" +
		"You speak over SSH in a plain-text terminal: short paragraphs, no markdown tables, " +
		"no fenced code blocks unless the user asked for code itself.\n" +
		"Be concise, warm, and concrete; prefer one working example over abstract advice.\n" +
		"Memory notes about the user are provided; treat them as long-term context."
}

// lookupFunc reads one secret. Native dev reads process env; hosted sessions
// read the app's owner-managed secrets.
type lookupFunc func(ctx context.Context, key string) (value string, found bool, err error)

func secretLookup(ctx context.Context, key string) (string, bool, error) {
	result := secrets.Get(key).Run(ctx)
	return result.Value, result.Found, result.Err
}

// whoamiFunc reads the session identity.
type whoamiFunc func(ctx context.Context) (identity.Identity, error)

func identityWhoami(ctx context.Context) (identity.Identity, error) {
	result := identity.Whoami().Run(ctx)
	return result.Identity, result.Err
}

// loadConfig assembles completion config from secrets. Capability failures
// and missing keys become hints instead of errors.
func loadConfig(ctx context.Context, lookup lookupFunc) config {
	cfg := config{BaseURL: defaultBaseURL, Model: defaultModel, Persona: defaultPersona(), Transport: transportCurl}
	if value, found, err := lookup(ctx, secretModel); err == nil && found && strings.TrimSpace(value) != "" {
		cfg.Model = strings.TrimSpace(value)
	}
	if value, found, err := lookup(ctx, secretBaseURL); err == nil && found && strings.TrimSpace(value) != "" {
		cfg.BaseURL = strings.TrimSpace(value)
	}
	if value, found, err := lookup(ctx, secretPersona); err == nil && found && strings.TrimSpace(value) != "" {
		cfg.Persona = strings.TrimSpace(value)
	}
	if value, found, err := lookup(ctx, secretTransport); err == nil && found {
		switch strings.TrimSpace(value) {
		case transportFetch, transportCurl, transportAnthropic:
			cfg.Transport = strings.TrimSpace(value)
		}
	}
	key, found, err := lookup(ctx, secretAPIKey)
	switch {
	case err != nil:
		cfg.Hint = "secrets unavailable (" + err.Error() + ") — deploy from a paired owner to unlock them"
	case !found || strings.TrimSpace(key) == "":
		cfg.Hint = "no GLM_API_KEY — owner: `pt secret set GLM_API_KEY=<key>` · dev: `GLM_API_KEY=… pt dev`"
	default:
		cfg.APIKey = strings.TrimSpace(key)
		cfg.HasKey = true
	}
	return cfg
}

// completion is one finished model turn. Err is already phrased for a human.
type completion struct {
	Text string
	Err  string
}

// completeFunc performs one model turn over the configured endpoint.
type completeFunc func(ctx context.Context, cfg config, history []message, memories []string) completion

// realComplete sends one model turn over the configured transport: the
// OpenAI-compatible wire format via fetch or curl, or the Anthropic messages
// format via curl. Every failure becomes a hint; it never returns a Go error
// because a Task error would end the whole session.
func realComplete(ctx context.Context, cfg config, history []message, memories []string) completion {
	if ctx.Err() != nil {
		return completion{Err: "the session closed before the answer arrived"}
	}
	if !cfg.HasKey {
		return completion{Err: cfg.Hint}
	}
	endpoint, err := url.Parse(strings.ReplaceAll(cfg.BaseURL, keyPlaceholder, "key"))
	if err != nil || endpoint.Host == "" {
		return completion{Err: "GLM_BASE_URL must be a valid HTTPS URL"}
	}
	// Permit HTTP only on numeric loopback addresses for local development.
	loopback := net.ParseIP(endpoint.Hostname()).IsLoopback()
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback) {
		return completion{Err: "GLM_BASE_URL must use HTTPS (HTTP is allowed only on loopback)"}
	}
	switch cfg.Transport {
	case transportAnthropic:
		body, err := json.Marshal(buildAnthropicRequest(cfg, history, memories))
		if err != nil {
			return completion{Err: "could not build the request: " + err.Error()}
		}
		return completeViaAnthropic(ctx, cfg, body)
	case transportCurl:
		body, err := json.Marshal(buildRequest(cfg, history, memories))
		if err != nil {
			return completion{Err: "could not build the request: " + err.Error()}
		}
		return completeViaCurl(ctx, cfg, body)
	default:
		body, err := json.Marshal(buildRequest(cfg, history, memories))
		if err != nil {
			return completion{Err: "could not build the request: " + err.Error()}
		}
		return completeViaFetch(ctx, cfg, body)
	}
}

// completeViaFetch uses the gated egress capability. Because the capability
// sends no headers, the endpoint must accept a keyless request or take the
// key from the URL via the {key} placeholder.
func completeViaFetch(ctx context.Context, cfg config, body []byte) completion {
	endpoint := cfg.BaseURL
	if strings.Contains(endpoint, keyPlaceholder) {
		endpoint = strings.ReplaceAll(endpoint, keyPlaceholder, url.QueryEscape(cfg.APIKey))
	}
	result := fetch.Request("POST", endpoint, body).Run(ctx)
	if result.Err != nil {
		return completion{Err: egressHint(cfg, result.Err)}
	}
	return parseReply(cfg, result.Status, result.Body)
}

// completeViaCurl uses the host-command capability to run curl, which can
// send the Authorization header that header-auth APIs require.
func completeViaCurl(ctx context.Context, cfg config, body []byte) completion {
	args := []string{
		"-sS", "--max-time", "60", "-X", "POST", cfg.BaseURL,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer " + cfg.APIKey,
		"-d", string(body),
		"-w", "\n%{http_code}",
	}
	status, output, hint := runCurl(ctx, args)
	if hint != "" {
		return completion{Err: hint}
	}
	return parseReply(cfg, status, []byte(output))
}

// completeViaAnthropic speaks the Anthropic messages wire format, which is
// what Anthropic-protocol endpoints (api.anthropic.com and compatible proxies)
// expect. The key rides in Authorization; the beta header keeps OAuth-style
// plan tokens working alongside normal API keys.
func completeViaAnthropic(ctx context.Context, cfg config, body []byte) completion {
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/v1/messages"
	args := []string{
		"-sS", "--max-time", "60", "-X", "POST", endpoint,
		"-H", "Content-Type: application/json",
		"-H", "Authorization: Bearer " + cfg.APIKey,
		"-H", "anthropic-version: 2023-06-01",
		"-H", "anthropic-beta: oauth-2025-04-20",
		"-d", string(body),
		"-w", "\n%{http_code}",
	}
	status, output, hint := runCurl(ctx, args)
	if hint != "" {
		return completion{Err: hint}
	}
	return parseAnthropicReply(cfg, status, []byte(output))
}

// runCurl executes one curl invocation and splits curl's trailing
// `%{http_code}` marker from the response body. A non-empty hint replaces the
// reply on transport-level failure.
func runCurl(ctx context.Context, args []string) (status int, output, hint string) {
	result := hostexec.Run("curl", args...).Run(ctx)
	if result.Err != nil {
		return 0, "", hostCommandHint(result.Err)
	}
	if result.ExitCode != 0 {
		return 0, "", fmt.Sprintf("curl exited %d: %s", result.ExitCode, truncateRunes(strings.TrimSpace(string(result.Stderr)), 160))
	}
	output = strings.TrimLeft(string(result.Stdout), "\n")
	if index := strings.LastIndex(output, "\n"); index >= 0 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(output[index+1:])); err == nil {
			return parsed, output[:index], ""
		}
	}
	return 0, output, ""
}

func hostCommandHint(err error) string {
	switch {
	case errors.Is(err, hostexec.ErrUnavailable):
		return "host commands unavailable — the operator must allowlist `curl` for this server (runtime.hostCommandAllowlist)"
	case errors.Is(err, hostexec.ErrTooLarge):
		return "the request exceeded the host-command size bound"
	default:
		return "curl could not run: " + err.Error()
	}
}

// buildRequest renders the OpenAI chat-completion payload.
func buildRequest(cfg config, history []message, memories []string) wireRequest {
	messages := make([]wireMessage, 0, len(history)+1)
	messages = append(messages, wireMessage{Role: "system", Content: systemPrompt(cfg, memories)})
	for _, msg := range history {
		messages = append(messages, wireMessage{Role: msg.Role, Content: msg.Content})
	}
	return wireRequest{Model: cfg.Model, Messages: messages, MaxTokens: maxReplyTokens, Temperature: temperature}
}

// buildAnthropicRequest renders the Anthropic messages payload: the persona
// and memories ride in the top-level system field, and the conversation is
// user/assistant turns only.
func buildAnthropicRequest(cfg config, history []message, memories []string) anthropicRequest {
	messages := make([]wireMessage, 0, len(history))
	for _, msg := range history {
		role := msg.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		messages = append(messages, wireMessage{Role: role, Content: msg.Content})
	}
	return anthropicRequest{Model: cfg.Model, System: systemPrompt(cfg, memories), Messages: messages, MaxTokens: maxReplyTokens, Temperature: temperature}
}

func systemPrompt(cfg config, memories []string) string {
	system := cfg.Persona
	if len(memories) > 0 {
		system += "\n\nThings you remember about this user:\n"
		for _, memory := range memories {
			system += "- " + memory + "\n"
		}
	}
	return system
}

type anthropicRequest struct {
	Model       string        `json:"model"`
	System      string        `json:"system,omitempty"`
	Messages    []wireMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseAnthropicReply(cfg config, status int, body []byte) completion {
	var decoded anthropicResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return completion{Err: fmt.Sprintf("unreadable reply from %s (HTTP %d)", hostOf(cfg.BaseURL), status)}
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return completion{Err: anthropicErrorHint(cfg, status, decoded.Error.Message)}
	}
	if status < 200 || status >= 300 {
		return completion{Err: fmt.Sprintf("upstream status %d from %s", status, hostOf(cfg.BaseURL))}
	}
	var text strings.Builder
	for _, block := range decoded.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return completion{Err: "the model returned an empty reply"}
	}
	return completion{Text: strings.TrimRight(text.String(), "\n")}
}

func anthropicErrorHint(cfg config, status int, message string) string {
	switch {
	case status == 401 || status == 403:
		return fmt.Sprintf("plan key rejected (%d) — re-authenticate the source CLI and update GLM_API_KEY", status)
	case status == 404:
		return fmt.Sprintf("model %q is unknown at %s — set GLM_MODEL to an entitled model id", cfg.Model, hostOf(cfg.BaseURL))
	case status == 429:
		return "rate limited upstream — wait a moment and ask again"
	default:
		return fmt.Sprintf("upstream error (%d): %s", status, truncateRunes(message, 200))
	}
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type wireRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type wireResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string      `json:"message"`
		Code    json.Number `json:"code"`
	} `json:"error"`
}

func parseReply(cfg config, status int, body []byte) completion {
	var decoded wireResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return completion{Err: fmt.Sprintf("unreadable reply from %s (HTTP %d)", hostOf(cfg.BaseURL), status)}
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return completion{Err: apiErrorHint(cfg, status, decoded.Error.Message)}
	}
	if status < 200 || status >= 300 {
		return completion{Err: fmt.Sprintf("upstream status %d from %s", status, hostOf(cfg.BaseURL))}
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return completion{Err: "the model returned an empty reply"}
	}
	return completion{Text: strings.TrimRight(decoded.Choices[0].Message.Content, "\n")}
}

func apiErrorHint(cfg config, status int, message string) string {
	switch {
	case status == 401 || status == 403:
		hint := fmt.Sprintf("GLM_API_KEY rejected (%d) — check `pt secret set GLM_API_KEY`", status)
		if cfg.Transport != transportCurl {
			hint += " · fetch sends no headers: put {key} in GLM_BASE_URL or set FAMILIAR_TRANSPORT=curl"
		}
		return hint
	case status == 404:
		return fmt.Sprintf("model %q is unknown at %s — set GLM_MODEL (and GLM_BASE_URL if needed)", cfg.Model, hostOf(cfg.BaseURL))
	case status == 429:
		return "rate limited upstream — wait a moment and ask again"
	default:
		return fmt.Sprintf("upstream error (%d): %s", status, truncateRunes(message, 200))
	}
}

func egressHint(cfg config, err error) string {
	host := hostOf(cfg.BaseURL)
	switch {
	case errors.Is(err, fetch.ErrDenied):
		return "egress denied to " + host + " — owner: `pt egress add " + host + "`"
	case errors.Is(err, fetch.ErrUnavailable):
		return "fetch capability unavailable — this deployment has no egress; deploy from a paired owner"
	case errors.Is(err, fetch.ErrTooLarge):
		return "the exchange exceeded the 1 MiB egress bound"
	default:
		return "request failed: " + err.Error()
	}
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Host
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if limit < 0 || len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return string(runes[:1])
	}
	return string(runes[:limit-1]) + "…"
}
