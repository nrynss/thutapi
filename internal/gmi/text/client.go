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
// the full MiniMaxAI/MiniMax-M3 id; Reasoning, when non-nil, is
// serialised as {"thinking":{...}} per the GMI quirk in project.md §M3
// phase settings.
type ChatRequest struct {
	Model    string     `json:"model"`
	Messages []Message  `json:"messages"`
	Thinking *Reasoning `json:"thinking,omitempty"`
}

// ContentPart is one element of a ChatResponse.Choices[].Message.Content
// when the response carries an array. TextPart's Type/Text fields are
// populated for text blocks; other types are passed through verbatim.
type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// AssistantMessage is the assistant turn inside a response choice.
type AssistantMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string OR []ContentPart
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
// without restarting the process.
//
// Errors:
//   - ErrUnauthorized on HTTP 401/403 (the key is missing or wrong).
//   - ErrRateLimited on HTTP 429.
//   - ErrModelNotFound on HTTP 404 — almost always a missing "MiniMaxAI/"
//     prefix on the model id.
//   - ErrTransient on HTTP 5xx, network errors, or a body that fails to
//     parse (the latter is treated as transient because the call did not
//     yield a usable answer).
//
// The function does not retry. The caller's retry budget is theirs; the
// text client is the one place where a stale context can leak expensive
// upstream spend, so it stays conservative.
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

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("text: marshal request: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, pathChatCompletions)
	if err != nil {
		return nil, fmt.Errorf("text: join path: %w", err)
	}

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
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", gmi.ErrRateLimited, msg)
	case code == http.StatusNotFound:
		return fmt.Errorf("%w: %s", gmi.ErrModelNotFound, msg)
	case code >= 500:
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	default:
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	}
}
