// Package text wraps the OpenAI-compatible chat completions endpoint at
// api.gmi-serving.com — the text side of Thutapi's two GMI clients.
//
// Three facts are load-bearing and pinned in client_test.go:
//
//  1. Model id must carry the full "MiniMaxAI/" prefix; bare "MiniMax-M3"
//     404s against this host with "No matching target server found". Do
//     not strip the prefix; do not auto-correct it on the caller side.
//  2. Reasoning is enabled only by the body field
//     thinking:{"type":"enabled"}. reasoning_effort is ignored by
//     MiniMax models — emitting it is harmless but pointless, and the
//     test asserts it is absent.
//  3. Bearer auth against GMI_API_KEY; the key is read from the
//     environment at call time, never embedded in the client.
package text

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"thutapi/internal/gmi"
)

// defaultBaseURL is the OpenAI-compatible text endpoint. Overridable via
// GMI_TEXT_BASE_URL so a fake server can be injected in tests (httptest
// gives a localhost URL the default env var never matches).
const defaultBaseURL = "https://api.gmi-serving.com"

// pathChatCompletions is the OpenAI-compatible chat completions route.
// Same shape as OpenAI's /v1/chat/completions; GMI does not implement
// /v1/responses.
const pathChatCompletions = "/v1/chat/completions"

// defaultCallTimeout bounds one Chat call — the initial attempt plus
// its single internal retry — when the caller's context carries no
// deadline (PLAN.md §T2: "Every call carries a context deadline"). It
// matches the HTTP client's per-request timeout.
const defaultCallTimeout = 60 * time.Second

// Client is the text/chat completions client. Construct one per process;
// it holds only a *http.Client with conservative timeouts and the
// resolved base URL.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New returns a Client whose base URL is GMI_TEXT_BASE_URL when set,
// otherwise the production default. The HTTP client has a 60s timeout
// per request — long enough for a structuring call (one M3 call over the
// whole transcript, see project.md §Phase B) without holding a
// goroutine forever if the upstream hangs.
func New() *Client {
	base := defaultBaseURL
	if v := os.Getenv("GMI_TEXT_BASE_URL"); v != "" {
		base = v
	}
	return &Client{
		baseURL: base,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Message mirrors the OpenAI chat completions message shape. Content is
// the OpenAI union type: a plain string for text-only turns, an array of
// content blocks for multimodal turns (text + image_url data: URIs, see
// project.md §2b for the consistency-verification use case).
type Message struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// TextPart is one block of a multimodal content array. Type is always
// "text" or "image_url"; ImageURL is set only for image_url blocks.
type TextPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *ImageURLRef `json:"image_url,omitempty"`
}

// ImageURLRef is the {url, detail} object inside an image_url block.
// The url is a data: URI in our usage — see project.md §2b: images go
// in as inline base64 because no public hosting is required, while
// source_audio (the TTS sibling) is the opposite and must be a URL.
type ImageURLRef struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// Reasoning enables MiniMax-style chain-of-thought. MiniMax models
// ignore reasoning_effort; thinking:{"type":"enabled"} is the only
// switch that actually engages reasoning. Type is "enabled" or
// "disabled"; an empty Reasoning{} omits the field and reasoning stays
// off (the project.md §M3 phase settings table calls this out — phase A
// keeps it off, phase B turns it on).
type Reasoning struct {
	Type string `json:"type"`
}

// ChatRequest is the body POSTed to /v1/chat/completions. Model carries
// the full MiniMaxAI/MiniMax-M3 id; Thinking serialises a non-empty
// Reasoning as {"thinking":{...}} per the GMI quirk in project.md §M3
// phase settings (an empty Reasoning{} is dropped — see Reasoning).
type ChatRequest struct {
	Model    string     `json:"model"`
	Messages []Message  `json:"messages"`
	Thinking *Reasoning `json:"thinking,omitempty"`
}

// ContentPart is one element of a multimodal assistant content array,
// as produced by AssistantMessage.UnmarshalJSON when the wire content
// is a JSON array. Type is "text" for text blocks; a block of any
// other type survives with only its Type set — Thutapi reads
// assistant text, and encoding drops what it does not model.
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// AssistantMessage is the assistant turn inside a response choice.
// The wire content is the OpenAI union type — a plain string for
// text-only turns, an array of content parts for multimodal turns —
// and UnmarshalJSON resolves it into exactly one of TextBody (the
// string arm) or Parts (the array arm), so consumers never switch on
// an interface{}. The type is decode-only; outbound requests use
// Message.
type AssistantMessage struct {
	Role     string
	TextBody string
	Parts    []ContentPart
}

// UnmarshalJSON decodes the assistant turn, resolving the content
// union: a JSON string populates TextBody, a JSON array populates
// Parts. Content of any other shape (object, number) is an error — a
// silently empty reply is worse than a loud one.
func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	var wire struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.Role = wire.Role
	m.TextBody = ""
	m.Parts = nil
	if len(wire.Content) == 0 || string(wire.Content) == "null" {
		return nil
	}
	var plain string
	if err := json.Unmarshal(wire.Content, &plain); err == nil {
		m.TextBody = plain
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(wire.Content, &parts); err != nil {
		return fmt.Errorf("text: assistant content is neither a string nor an array of parts: %w", err)
	}
	m.Parts = parts
	return nil
}

// Text returns the assistant's reply as flat text: the string arm as
// itself, the array arm as its text parts concatenated in order. It
// returns an error when the message carries no text at all, so a
// caller cannot mistake an empty reply for a successful decode.
func (m AssistantMessage) Text() (string, error) {
	if m.TextBody != "" {
		return m.TextBody, nil
	}
	var b strings.Builder
	for _, p := range m.Parts {
		b.WriteString(p.Text)
	}
	if b.Len() == 0 {
		return "", errors.New("text: assistant message carries no text content")
	}
	return b.String(), nil
}

// Choice is one entry in ChatResponse.Choices.
type Choice struct {
	Index        int              `json:"index"`
	Message      AssistantMessage `json:"message"`
	FinishReason string           `json:"finish_reason,omitempty"`
}

// ChatResponse is the parsed body of a successful /v1/chat/completions
// response. We do not surface usage, id, model, object or created —
// Thutapi tracks cost via the campaign's free-tier window and the
// caller only needs the assistant text and the finish reason.
type ChatResponse struct {
	Choices []Choice `json:"choices"`
}

// Chat posts a ChatRequest and returns the parsed response. The API key
// is read from GMI_API_KEY on every call, so a key rotation takes effect
// without restarting the process. When ctx carries no deadline, a
// default per-call deadline (defaultCallTimeout) is applied — PLAN.md
// §T2: "Every call carries a context deadline."
//
// Errors:
//   - ErrUnauthorized on HTTP 401/403 (the key is missing or wrong).
//   - ErrPaymentRequired on HTTP 402 — billing is operator-actionable,
//     not a payload fix.
//   - ErrRateLimited on HTTP 429.
//   - ErrModelNotFound on HTTP 404 — almost always a missing "MiniMaxAI/"
//     prefix on the model id.
//   - ErrBadRequest on HTTP 400, 413, 422 and any other 4xx without its
//     own sentinel — the payload is wrong and a retry sends the same
//     bytes to the same rejection.
//   - ErrTransient on HTTP 5xx, network errors, or a body that fails to
//     parse (the latter is treated as transient because the call did not
//     yield a usable answer).
//
// A transient failure is retried exactly once, internally (PLAN.md §T2:
// one retry on the failures classified ErrTransient; never a 4xx). Any
// retry budget beyond that single internal attempt belongs to the
// caller — this endpoint can leak expensive upstream spend, so going
// around again is the caller's decision.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		return nil, errors.New("text: ChatRequest.Model is empty")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("text: ChatRequest.Messages is empty")
	}

	apiKey := os.Getenv("GMI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("%w: GMI_API_KEY not set", gmi.ErrUnauthorized)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}

	// An empty Reasoning{} means reasoning is off: nil the pointer so
	// omitempty drops the field instead of sending {"type":""} — the
	// promise Reasoning's doc makes, pinned at the raw-byte level by
	// TestChat_EmptyReasoningOmittedOnWire. req is a value copy, so the
	// caller's ChatRequest is untouched.
	if req.Thinking != nil && *req.Thinking == (Reasoning{}) {
		req.Thinking = nil
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("text: marshal request: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, pathChatCompletions)
	if err != nil {
		return nil, fmt.Errorf("text: join path: %w", err)
	}

	const maxAttempts = 2 // the call plus the one PLAN.md §T2 retry
	for attempt := 1; ; attempt++ {
		out, err := c.attemptChat(ctx, endpoint, apiKey, body)
		if err == nil {
			return out, nil
		}
		if attempt >= maxAttempts || !errors.Is(err, gmi.ErrTransient) || ctx.Err() != nil {
			return nil, err
		}
	}
}

// attemptChat performs one HTTP round-trip against
// /v1/chat/completions and decodes the response. It never returns a
// non-nil response alongside a non-nil error.
func (c *Client) attemptChat(ctx context.Context, endpoint, apiKey string, body []byte) (*ChatResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("text: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", gmi.ErrTransient, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, classifyStatus(resp.StatusCode, resp.Body)
	}

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode body: %v", gmi.ErrTransient, err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%w: response has no choices", gmi.ErrTransient)
	}
	return &out, nil
}

// classifyStatus maps an HTTP error code to a typed sentinel. The body
// is read into the wrapped error so an operator reading logs sees the
// upstream message verbatim, but the sentinel is what callers branch on.
// We do not try to parse the upstream error string — GMI returns
// different shapes for different endpoints and parsing them is a
// per-endpoint job, not a shared one.
func classifyStatus(code int, body io.Reader) error {
	raw, _ := io.ReadAll(body)
	msg := strings.TrimSpace(string(raw))
	if msg == "" {
		msg = http.StatusText(code)
	}
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("%w: %s", gmi.ErrUnauthorized, msg)
	case code == http.StatusPaymentRequired:
		// Billing is operator-actionable and gets its own sentinel;
		// it is not a payload fix and never a retry.
		return fmt.Errorf("%w: %s", gmi.ErrPaymentRequired, msg)
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", gmi.ErrRateLimited, msg)
	case code == http.StatusNotFound:
		return fmt.Errorf("%w: %s", gmi.ErrModelNotFound, msg)
	case code >= 400 && code < 500:
		// Any other 4xx — 400, 413, 422, 418, ... — is the payload's
		// fault. Never labelled ErrTransient: a retryable label on a
		// 4xx invites exactly the retry PLAN.md §T2 forbids.
		return fmt.Errorf("%w: %s", gmi.ErrBadRequest, msg)
	case code >= 500:
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	default:
		// Anything else (3xx not followed, etc) is unknown; treat as
		// transient so the caller's retry loop has a chance to
		// observe recovery.
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	}
}
