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
// returns {request_id, status, ...} for t2i/i2i (a poll) and a binary
// blob for TTS. Both are exposed as raw bytes; callers decode what
// they need. T2 ships the wire shape; later tracks (T6, T8) build on
// top.
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

// Client is the request-queue client. One per process; same lifetime as
// the text client. The HTTP timeout is 120s because image generation
// and TTS are slower than text — the request-queue API can take up to
// ~15s for a single synchronous image call, with headroom for retries.
type Client struct {
	baseURL    string
	httpClient *http.Client
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

// envelope is the wire shape for every request-queue call. Model is the
// GMI model id (e.g. "Qwen-Image-2512" for t2i, "minimax-tts-speech-2.8-hd"
// for TTS). Payload is a model-specific JSON object; we marshal it to a
// generic map so each method can pass its own fields without a parallel
// struct hierarchy. The wire is one round-trip per model anyway, and
// GMI has not published a typed schema for every model — a typed Go
// struct would be a fabrication.
type envelope struct {
	Model   string                 `json:"model"`
	Payload map[string]interface{} `json:"payload"`
}

// GenerateImage runs a text-to-image call and returns the raw response
// body. The caller (T6) knows which model they asked for and decodes
// the response shape that model returns.
//
// Model defaults to "Qwen-Image-2512" if empty — the cheapest in the
// catalog at $0.10 a book and t2i capable (project.md §3). T6 will
// override this for the chosen provider; the default exists so the
// smoke call is one line.
func (c *Client) GenerateImage(ctx context.Context, prompt, model string) ([]byte, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("media: GenerateImage prompt is empty")
	}
	if model == "" {
		model = "Qwen-Image-2512"
	}
	payload := map[string]interface{}{
		"prompt": prompt,
	}
	return c.post(ctx, model, payload)
}

// EditImage runs an image-to-image call. refImage is the reference
// image bytes (a character sheet for the consistency loop); it is
// inlined as a data: URI in the payload — see project.md §2b on the
// "Useful asymmetry": images go in as base64, source_audio is the
// opposite and must be a URL.
//
// The content type is sniffed from the first 512 bytes via
// http.DetectContentType; PNG is the common case and the only one T6
// will produce. Fall back to application/octet-stream if unknown so a
// future model can pass a different format without a code change here.
func (c *Client) EditImage(ctx context.Context, refImage []byte, prompt, model string) ([]byte, error) {
	if len(refImage) == 0 {
		return nil, errors.New("media: EditImage refImage is empty")
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("media: EditImage prompt is empty")
	}
	if model == "" {
		model = "Qwen-Image-2512"
	}

	mime := http.DetectContentType(refImage)
	if mime == "" {
		mime = "application/octet-stream"
	}
	encoded := base64.StdEncoding.EncodeToString(refImage)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mime, encoded)

	payload := map[string]interface{}{
		"prompt": prompt,
		"image":  dataURI,
	}
	return c.post(ctx, model, payload)
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
	return c.post(ctx, model, payload)
}

// post sends one envelope to the request-queue endpoint. The whole
// package's auth, error-classification and timeout story lives here so
// every method stays a one-payload-map call site.
func (c *Client) post(ctx context.Context, model string, payload map[string]interface{}) ([]byte, error) {
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("media: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

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
		return raw, classifyStatus(resp.StatusCode, raw)
	}
	return raw, nil
}

// classifyStatus maps an HTTP error code to a typed sentinel. Same
// policy as the text client: trust the sentinel, surface the upstream
// message verbatim for the operator. The request-queue API is its own
// shape — a 200 with a {"status":"failed"} body is still an error in
// practice, but T2 does not parse that: the caller (T6/T8) will, when
// it knows which model it asked for and what failure modes that model
// has.
func classifyStatus(code int, body []byte) error {
	msg := strings.TrimSpace(string(body))
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
	default:
		return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
	}
}
