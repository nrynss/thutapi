// Package media wraps the GMI request-queue endpoint at
// console.gmicloud.ai — the image/audio/video side of Thutapi's two GMI
// clients.
//
// Three methods today:
//
//   - GenerateImage: text-to-image. Prompt only; no reference image.
//     Used for character reference sheets (T6).
//   - EditImage: image-to-image. Prompt + reference image. Used for
//     every page illustration, with the character sheet as the ref
//     (project.md §2 "Character consistency is the real technical
//     problem"). The ref is sent inline as a base64 data: URI — GMI's
//     image input is inline, the opposite of source_audio which must
//     be a URL.
//   - SynthesizeSpeech: text-to-speech. Text + voice. The TTS payload
//     uses the typo'd flag need_volumn_normalization (no 'u' in volume)
//     — project.md §4 spells it out: match the typo or the flag is
//     silently ignored by the upstream.
//
// All three POST the same envelope {model, payload} to a single path.
// The base URL is GMI_MEDIA_BASE_URL when set, otherwise the production
// host. The GMI_API_KEY is read from env at call time, same as the
// text client (AGENTS.md "Secrets never enter the repo").
//
// Response shapes are deliberately untyped — the request-queue API
// returns a different result schema per model, and GMI has not
// published them all, so with two days left a typed struct would be a
// fabrication. Both response kinds are handed to callers as raw
// bytes; the decode belongs to the track that knows which model it
// asked for (T6, T8). The reasoning is recorded where the criterion
// lives: PLAN.md §T2 "Done when". The retry contract of PLAN.md §T2
// (one transient retry, never a 4xx, a deadline on every call) is
// enforced in post.
package media

import (
	"bytes"
	"context"
	"encoding/base64"
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

// defaultBaseURL is the production request-queue host. Overridable via
// GMI_MEDIA_BASE_URL for tests (httptest) and for a future regional
// mirror.
const defaultBaseURL = "https://console.gmicloud.ai"

// pathRequestQueue is the single endpoint all three methods hit. The
// request-queue API dispatches by the {model, payload} envelope, not by
// path — three different model ids, one path.
const pathRequestQueue = "/api/v1/ie/requestqueue/apikey/requests"

// defaultCallTimeout bounds one request-queue call — the initial
// attempt plus its single internal retry — when the caller's context
// carries no deadline (PLAN.md §T2: "Every call carries a context
// deadline"). It matches the HTTP client's per-request timeout.
const defaultCallTimeout = 120 * time.Second

// defaultImageModel is the text-to-image default, per project.md §3
// "Start on Flux2-Klein or Z-Image". The default is a convenience for
// the one-line smoke call; T6 owns the provider switch and is
// expected to pass the model explicitly. EditImage has no default at
// all — an empty model there is an error, because a silent fallback
// is exactly how the forbidden Qwen-Image-2512 shipped
// (adversarial-review/t2-round3.md H1).
const defaultImageModel = "Flux2-Klein"

// Accept headers per endpoint kind. The request-queue answers image
// calls with JSON and TTS with audio bytes; advertising JSON on the
// TTS call invites a 406 from any gateway that honours the header
// (adversarial-review/t2-round3.md L4).
const (
	acceptJSON  = "application/json"
	acceptAudio = "audio/*"
)

// Client is the request-queue client. One per process; same lifetime as
// the text client. The HTTP timeout is 120s because image generation
// and TTS are slower than text — the request-queue API can take up to
// ~15s for a single synchronous image call, with headroom for retries.
//
// Since T3b the methods drive a call to a terminal state: a submit POST,
// then (when the queue answers queued/processing) the poll loop in
// polling.go, paced by the Client's PollConfig.
type Client struct {
	baseURL    string
	httpClient *http.Client
	poll       PollConfig
}

// New returns a Client whose base URL is GMI_MEDIA_BASE_URL when set,
// otherwise the production default.
func New() *Client {
	base := defaultBaseURL
	if v := os.Getenv("GMI_MEDIA_BASE_URL"); v != "" {
		base = v
	}
	return &Client{
		baseURL: base,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// envelope is the wire shape for every request-queue call. Model is
// the GMI model id (e.g. "Flux2-Klein" for t2i/i2i,
// "minimax-tts-speech-2.8-hd" for TTS). Payload is a model-specific
// JSON object; we marshal it to a generic map so each method can pass
// its own fields without a parallel struct hierarchy. The wire is one
// round-trip per model anyway, and GMI has not published a typed
// schema for every model — a typed Go struct would be a fabrication.
type envelope struct {
	Model   string                 `json:"model"`
	Payload map[string]interface{} `json:"payload"`
}

// GenerateImage runs a text-to-image call and returns the raw response
// body. The caller (T6) knows which model they asked for and decodes
// the response shape that model returns.
//
// Model defaults to defaultImageModel when empty (project.md §3:
// "Start on Flux2-Klein or Z-Image"). §3's heading is the rule the
// old default got backwards: image models are chosen on
// consistency, not price — four of them tie at $0.10 a book, so cost
// selects nothing. T6 owns the provider switch; the default exists
// so the smoke call is one line.
func (c *Client) GenerateImage(ctx context.Context, prompt, model string) ([]byte, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("media: GenerateImage prompt is empty")
	}
	if model == "" {
		model = defaultImageModel
	}
	payload := map[string]interface{}{
		"prompt": prompt,
	}
	return c.drive(ctx, model, acceptJSON, payload)
}

// EditImage runs an image-to-image call. refImage is the reference
// image bytes (a character sheet for the consistency loop); it is
// inlined as a data: URI in the payload — see project.md §2b on the
// "Useful asymmetry": images go in as base64, source_audio is the
// opposite and must be a URL.
//
// model must be explicit: image-to-image is the mechanism T6's
// character lock rides on, and an empty model returns an error
// wrapping gmi.ErrBadRequest instead of silently substituting a
// default. T6 owns the provider switch (PLAN.md §T6: Flux2-Klein or
// Z-Image; gemini-2.5-flash-image if characters drift).
//
// The content type is sniffed via http.DetectContentType, which
// always returns a valid MIME type. The data: URI carries the bare
// type: a sniffed parameter ("text/plain; charset=utf-8") is cut, and
// the ;base64 marker is what says how the bytes are encoded.
func (c *Client) EditImage(ctx context.Context, refImage []byte, prompt, model string) ([]byte, error) {
	if len(refImage) == 0 {
		return nil, errors.New("media: EditImage refImage is empty")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("media: EditImage prompt is empty")
	}
	if model == "" {
		return nil, fmt.Errorf("%w: media: EditImage needs an explicit model — image-to-image carries the character reference (PLAN.md §T6)", gmi.ErrBadRequest)
	}

	mime := http.DetectContentType(refImage)
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	encoded := base64.StdEncoding.EncodeToString(refImage)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mime, encoded)

	payload := map[string]interface{}{
		"prompt": prompt,
		"image":  dataURI,
	}
	return c.drive(ctx, model, acceptJSON, payload)
}

// SynthesizeSpeech runs a TTS call and returns the raw audio bytes.
// voice is the voice_id ("English_expressive_narrator" for the default
// narrator; a cloned voice id for T13). The two audio flags pin the
// GMI API quirk in project.md §4:
//
//   - need_noise_reduction: true (spelled correctly)
//   - need_volumn_normalization: true (note the missing 'u' — spelled
//     "volumn" in GMI's API, match the typo or it is silently ignored)
//
// We keep the literal typo'd key in the Go source so a code review can
// see it, and the test pins the wire-level payload to the same string.
func (c *Client) SynthesizeSpeech(ctx context.Context, text, voice, model string) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("media: SynthesizeSpeech text is empty")
	}
	if strings.TrimSpace(voice) == "" {
		return nil, errors.New("media: SynthesizeSpeech voice is empty")
	}
	if model == "" {
		model = "minimax-tts-speech-2.8-hd"
	}
	payload := map[string]interface{}{
		"text":                      text,
		"voice_id":                  voice,
		"need_noise_reduction":      true,
		"need_volumn_normalization": true, // sic — see project.md §4
	}
	return c.drive(ctx, model, acceptAudio, payload)
}

// post sends one envelope to the request-queue endpoint. The whole
// package's auth, error-classification, deadline and retry story
// lives here so every method stays a one-payload-map call site.
//
// PLAN.md §T2's contract, enforced here: a caller context without a
// deadline gets defaultCallTimeout; a transient failure (5xx,
// transport error, a request-queue "failed" status) is retried
// exactly once; a 4xx never is.
func (c *Client) post(ctx context.Context, model, accept string, payload map[string]interface{}) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}

	apiKey := os.Getenv("GMI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("%w: GMI_API_KEY not set", gmi.ErrUnauthorized)
	}

	env := envelope{Model: model, Payload: payload}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("media: marshal envelope: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, pathRequestQueue)
	if err != nil {
		return nil, fmt.Errorf("media: join path: %w", err)
	}

	const maxAttempts = 2 // the call plus the one PLAN.md §T2 retry
	for attempt := 1; ; attempt++ {
		raw, err := c.attempt(ctx, endpoint, accept, apiKey, body)
		if err == nil {
			return raw, nil
		}
		if attempt >= maxAttempts || !errors.Is(err, gmi.ErrTransient) || ctx.Err() != nil {
			return nil, err
		}
	}
}

// attempt performs one HTTP round-trip against the request queue and
// classifies the outcome. raw is nil whenever err is non-nil — a
// caller checking err never mistakes an error body for media.
func (c *Client) attempt(ctx context.Context, endpoint, accept, apiKey string, body []byte) ([]byte, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("media: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", accept)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", gmi.ErrTransient, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", gmi.ErrTransient, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, classifyStatus(resp.StatusCode, raw)
	}

	// PLAN.md §T2 counts a request-queue "failed" status as transient.
	// Peek only the status field — the model-specific result fields
	// stay raw for the caller (T6/T8) — and retry it like a 5xx. TTS
	// returns audio bytes, which are not JSON; the peek then no-ops.
	var status queueStatus
	if err := json.Unmarshal(raw, &status); err == nil && strings.ToLower(status.Status) == "failed" {
		return nil, fmt.Errorf("%w: request queue reported failed: %s", gmi.ErrTransient, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// queueStatus is the minimal peek at a request-queue body: the
// {"request_id","status"} fields the API reports. Status steers the
// retry contract in attempt and the poll loop in polling.go; RequestID
// is what the poll loop polls. Only these two fields are decoded; the
// response is otherwise handed to callers as raw bytes.
type queueStatus struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// classifyStatus maps an HTTP error code to a typed sentinel. Same
// policy as the text client: trust the sentinel, surface the upstream
// message verbatim for the operator. A 200 with a {"status":"failed"}
// body is attempt's business (it is the retry contract's input, not
// the classifier's). Every 4xx without a sentinel of its own lands on
// ErrBadRequest — never on ErrTransient: a retryable label on a 4xx
// invites exactly the retry PLAN.md §T2 forbids (t2-round3.md M1).
func classifyStatus(code int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = http.StatusText(code)
	}
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("%w: %s", gmi.ErrUnauthorized, msg)
	case code == http.StatusPaymentRequired:
		// Billing is operator-actionable and gets its own sentinel
		// (PLAN.md §T11.2); it is not a payload fix and never a retry.
		return fmt.Errorf("%w: %s", gmi.ErrPaymentRequired, msg)
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", gmi.ErrRateLimited, msg)
	case code == http.StatusNotFound:
		return fmt.Errorf("%w: %s", gmi.ErrModelNotFound, msg)
	case code >= 400 && code < 500:
		// Any other 4xx — 400, 413, 422, 418, ... — is the payload's
		// fault: the same bytes fail the same way.
		return fmt.Errorf("%w: %s", gmi.ErrBadRequest, msg)
	case code >= 500:
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	default:
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	}
}
