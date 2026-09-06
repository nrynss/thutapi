// Package media wraps the GMI request-queue endpoint at
// console.gmicloud.ai — the image/audio/video side of Thutapi's two GMI
// clients.
//
// Four methods today:
//
//   - GenerateImage: text-to-image. Prompt only; no reference image.
//     Used for character reference sheets (T6).
//   - EditImage: image-to-image. Prompt + reference image URLs. Used
//     for every page illustration, with the character sheets as the
//     refs (project.md §2 "Character consistency is the real technical
//     problem"). The refs are sent as an ARRAY OF URL STRINGS in
//     payload.image — seedream-5.0-lite takes references as URLs, not
//     inline base64 (t6b-live-record.md §4), and chains GMI's own
//     public output URLs, so nothing needs hosting mid-generation
//     (project.md §2b, amended 2026-09-05).
//   - SynthesizeSpeech: text-to-speech. Text + voice + the per-page
//     narration emotion (contract row C1 of t8-round1.md: an empty
//     emotion omits the payload key, keeping the question path's
//     payload byte-identical to the live-verified shape). The TTS
//     payload uses the typo'd flag need_volumn_normalization (no 'u'
//     in volume) — project.md §4 spells it out: match the typo or the
//     flag is silently ignored by the upstream.
//   - SynthesizeMusic: music generation (T12). Lyrics + an optional
//     style prompt + format mp3. minimax-music-3.0 REQUIRES lyrics —
//     it wants to write a song — and has no duration parameter
//     (PLAN.md §T12, settled 2026-09-05; the bed's fit to the film is
//     the mixer's job, not the request's).
//
// All four POST the same envelope {model, payload} to a single path.
// The base URL is GMI_MEDIA_BASE_URL when set, otherwise the production
// host. The GMI_API_KEY is read from env at call time, same as the
// text client (AGENTS.md "Secrets never enter the repo").
//
// Response shapes are deliberately untyped — the request-queue API
// returns a different result schema per model, and GMI has not
// published them all, so with two days left a typed struct would be a
// fabrication. Both response kinds are handed to callers as raw
// bytes; the decode belongs to the track that knows which model it
// asked for (T6, T8, T12). The reasoning is recorded where the criterion
// lives: PLAN.md §T2 "Done when". The retry contract of PLAN.md §T2
// (one transient retry, never a 4xx, a deadline on every call) is
// enforced in post.
package media

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

// defaultBaseURL is the production request-queue host. Overridable via
// GMI_MEDIA_BASE_URL for tests (httptest) and for a future regional
// mirror.
const defaultBaseURL = "https://console.gmicloud.ai"

// pathRequestQueue is the single endpoint all four methods hit. The
// request-queue API dispatches by the {model, payload} envelope, not by
// path — four different model ids, one path.
const pathRequestQueue = "/api/v1/ie/requestqueue/apikey/requests"

// defaultCallTimeout bounds one request-queue call — the initial
// attempt plus its single internal retry — when the caller's context
// carries no deadline (PLAN.md §T2: "Every call carries a context
// deadline"). It matches the HTTP client's per-request timeout.
const defaultCallTimeout = 120 * time.Second

// defaultImageModel is the text-to-image default. Flux2-Klein and
// Z-Image — §3's original "start on" pair — accept a request and then
// never generate, so the default is seedream-5.0-lite, measured
// working 2026-09-05 (project.md §3; t6b-live-record.md). The default
// is a convenience for the one-line smoke call; T6 owns the provider
// switch and is expected to pass the model explicitly. EditImage has
// no default at all — an empty model there is an error, because a
// silent fallback is exactly how the forbidden Qwen-Image-2512 shipped
// (adversarial-review/t2-round3.md H1).
const defaultImageModel = "seedream-5.0-lite"

// Accept headers per endpoint kind. The request-queue answers image
// calls with JSON and TTS with audio bytes; advertising JSON on the
// TTS call invites a 406 from any gateway that honours the header
// (adversarial-review/t2-round3.md L4).
const (
	acceptJSON  = "application/json"
	acceptAudio = "audio/*"
)

// defaultMusicModel is the model SynthesizeMusic calls when model is
// empty: minimax-music-3.0, the request-queue music model T12 mixes
// under the finished film (PLAN.md §T12). Same convention as
// SynthesizeSpeech's inline default: the model id is pinned here and
// on the wire by the raw-JSON tests.
const defaultMusicModel = "minimax-music-3.0"

// defaultMusicFormat is the payload `format` every music call sends:
// mp3, the format the bed is downloaded and mixed as (PLAN.md §T12;
// wav and pcm are upstream alternatives nothing here uses).
const defaultMusicFormat = "mp3"

const voiceCloneModel = "minimax-audio-voice-clone-speech-2.8-turbo"

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
// the GMI model id (e.g. "seedream-5.0-lite" for t2i/i2i,
// "minimax-tts-speech-2.8-hd" for TTS). Payload is the model-specific
// JSON object: an imagePayload for the image methods, a map for TTS.
// It stays untyped-at-the-envelope on purpose — one envelope carries
// every model — while each model's payload itself is a named struct,
// never a map at a call site (AGENTS.md §Go style).
type envelope struct {
	Model   string `json:"model"`
	Payload any    `json:"payload"`
}

// DefaultImageSize is the payload `size` every image call sends when
// ImageOptions.Size is empty: 1792x2240, 4:5 portrait, pinned as an
// explicit pixel size and NOT the "2K" preset — the preset infers its
// shape from prose in the prompt, so a prompt edit would silently
// change a page's aspect ratio, and a book's pages must all be the
// same shape (t6b-live-record.md §5). It is inside seedream's hard
// pixel floor of 3,686,400; smaller page-sized images cannot be
// requested at all.
const DefaultImageSize = "1792x2240"

// DefaultImageFormat is the payload `output_format` every image call
// sends when ImageOptions.Format is empty: jpeg — 342 KB against
// 4.4 MB for the same-size PNG at these dimensions, which matters
// twice: the book fills page-by-page over SSE on a tablet, and T7
// inlines reference sheets as base64 into M3 chat requests
// (t6b-live-record.md §5).
const DefaultImageFormat = "jpeg"

// DefaultMaxImages is the payload `max_images` every image call sends
// when ImageOptions.MaxImages is zero. One: the model is one image per
// request — larger values are silently ignored (t6b-live-record.md
// item 1b) — and this package's callers decode exactly the first
// result URL.
const DefaultMaxImages = 1

// MaxReferenceImages is seedream's documented reference limit —
// payload.image accepts up to 14 URLs (t6b-live-record.md §4).
// EditImage refuses more rather than let the upstream decide what a
// fifteenth reference means.
const MaxReferenceImages = 14

// ImageOptions are the payload fields both image methods send beyond
// the prompt. The zero value is usable and means the pinned
// production settings: DefaultImageSize, DefaultImageFormat, no
// watermark, one image (AGENTS.md §Go style "zero value usable").
//
// Watermark's zero value false is the documented upstream default,
// but it is sent explicitly: a watermarked demo is not worth leaving
// to someone else's default (t6b-live-record.md §5).
type ImageOptions struct {
	// Size is the output pixel size as "WxH". Empty means
	// DefaultImageSize.
	Size string
	// Format is the output_format. Empty means DefaultImageFormat.
	Format string
	// Watermark is sent verbatim; false is the pinned value.
	Watermark bool
	// MaxImages is sent verbatim; zero means DefaultMaxImages. The
	// model ignores values above 1 — one image per request is the
	// operating reality (t6b-live-record.md item 1b).
	MaxImages int
}

// imagePayload is the wire shape both image methods send — a named
// struct, never a map (AGENTS.md §Go style). Watermark and MaxImages
// carry no omitempty on purpose: `watermark:false` and
// `max_images:1` are part of the pinned production payload, present
// on the wire even at their defaults, so a marshalling change cannot
// silently drop them. Image is omitted for text-to-image, which sends
// no reference at all.
type imagePayload struct {
	Prompt       string   `json:"prompt"`
	Image        []string `json:"image,omitempty"`
	Size         string   `json:"size"`
	OutputFormat string   `json:"output_format"`
	MaxImages    int      `json:"max_images"`
	Watermark    bool     `json:"watermark"`
}

// payload applies the ImageOptions defaults and returns the wire
// payload. image is nil for text-to-image.
func (o ImageOptions) payload(prompt string, image []string) imagePayload {
	size := o.Size
	if size == "" {
		size = DefaultImageSize
	}
	format := o.Format
	if format == "" {
		format = DefaultImageFormat
	}
	max := o.MaxImages
	if max <= 0 {
		max = DefaultMaxImages
	}
	return imagePayload{
		Prompt:       prompt,
		Image:        image,
		Size:         size,
		OutputFormat: format,
		MaxImages:    max,
		Watermark:    o.Watermark,
	}
}

// GenerateImage runs a text-to-image call and returns the raw response
// body. The caller (T6) knows which model they asked for and decodes
// the response shape that model returns.
//
// Model defaults to defaultImageModel when empty — seedream-5.0-lite,
// the one measured-working image model (project.md §3;
// t6b-live-record.md). T6 owns the provider switch; the default exists
// so the smoke call is one line.
//
// opts carries the pinned production payload fields; its zero value is
// the pinned settings (see ImageOptions) and is what this package's
// callers pass.
func (c *Client) GenerateImage(ctx context.Context, prompt, model string, opts ImageOptions) ([]byte, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("media: GenerateImage prompt is empty")
	}
	if model == "" {
		model = defaultImageModel
	}
	return c.drive(ctx, model, acceptJSON, opts.payload(prompt, nil))
}

// EditImage runs an image-to-image call. refImages are the reference
// images as URL strings — seedream takes payload.image as an array of
// reference image URLs, up to MaxReferenceImages of them, and the
// pinned practice is to chain GMI's own public output URLs: a sheet
// renders, GMI returns a public URL, that URL feeds the next call
// (project.md §2b; t6b-live-record.md §4). Multi-reference is
// live-verified: two references come back rendered in one image.
//
// model must be explicit: image-to-image is the mechanism T6's
// character lock rides on, and an empty model returns an error
// wrapping gmi.ErrBadRequest instead of silently substituting a
// default. T6 owns the provider switch (PLAN.md §T6: seedream-5.0-lite;
// gemini-2.5-flash-image if characters drift; Flux2-Klein and Z-Image
// never generate).
//
// opts carries the pinned production payload fields, exactly as for
// GenerateImage.
func (c *Client) EditImage(ctx context.Context, prompt, model string, refImages []string, opts ImageOptions) ([]byte, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("media: EditImage prompt is empty")
	}
	if model == "" {
		return nil, fmt.Errorf("%w: media: EditImage needs an explicit model — image-to-image carries the character reference (PLAN.md §T6)", gmi.ErrBadRequest)
	}
	if len(refImages) == 0 {
		return nil, errors.New("media: EditImage needs at least one reference image URL — image-to-image with no reference is a silent text-to-image fallback")
	}
	if len(refImages) > MaxReferenceImages {
		return nil, fmt.Errorf("media: EditImage got %d reference image URLs, want at most %d", len(refImages), MaxReferenceImages)
	}
	for i, ref := range refImages {
		if strings.TrimSpace(ref) == "" {
			return nil, fmt.Errorf("media: EditImage reference image %d is empty", i)
		}
	}
	return c.drive(ctx, model, acceptJSON, opts.payload(prompt, refImages))
}

// SynthesizeSpeech runs one TTS call and returns the terminal
// request-queue response body raw. The audio is not in the body: the
// terminal record carries outcome.audio_url naming a public object the
// caller downloads on receipt (t2b-t5b-live-record.md).
//
// voice is the voice_id ("English_expressive_narrator" for the default
// narrator; a cloned voice id for T13). model defaults to
// minimax-tts-speech-2.8-hd when empty. emotion is the per-page
// narration emotion (story.Emotions — e.g. "happy"), sent verbatim as
// the payload's emotion key only when non-empty: questions carry none,
// and an empty emotion keeps the payload byte-identical to the
// live-verified question shape (t8-round1.md contract row C1).
// The emotion payload key placement is CONFIRMED against GMI's provider
// schema as of 2026-09-05 (t8b-live-record.md): its top-level placement
// beside voice_id and its accepted enum (calm, happy, sad, angry,
// fearful, disgusted, surprised, auto) match GMI's model-details
// endpoint (console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models/minimax-tts-speech-2.8-hd).
// T5c corrected the vocabulary (story.Emotions) to match this enum (calm
// in place of neutral; auto deliberately omitted). The empty-emotion path
// is unaffected: byte-identical to the live-verified question shape
// (t2b-t5b-live-record.md).
//
// The two audio flags pin the GMI API quirk in project.md §4:
//
//   - need_noise_reduction: true (spelled correctly)
//   - need_volumn_normalization: true (note the missing 'u' — spelled
//     "volumn" in GMI's API, match the typo or it is silently ignored)
//
// We keep the literal typo'd key in the Go source so a code review can
// see it, and the test pins the wire-level payload to the same string.
func (c *Client) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
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
	if emotion != "" {
		payload["emotion"] = emotion
	}
	return c.drive(ctx, model, acceptAudio, payload)
}

// CloneVoice submits the confirmed Speech 2.8 voice-clone envelope and returns
// the provider's terminal response raw. The caller owns response decoding
// until a consented live request establishes the returned voice-id shape.
func (c *Client) CloneVoice(ctx context.Context, sourceAudio, text, voiceID string) ([]byte, error) {
	if strings.TrimSpace(sourceAudio) == "" || strings.TrimSpace(text) == "" || strings.TrimSpace(voiceID) == "" {
		return nil, errors.New("media: CloneVoice source_audio, text, and voice_id are required")
	}
	return c.drive(ctx, voiceCloneModel, acceptAudio, voiceClonePayload{
		SourceAudio:             sourceAudio,
		Text:                    text,
		VoiceID:                 voiceID,
		NeedNoiseReduction:      true,
		NeedVolumnNormalization: true,
	})
}

type voiceClonePayload struct {
	SourceAudio             string `json:"source_audio"`
	Text                    string `json:"text"`
	VoiceID                 string `json:"voice_id"`
	NeedNoiseReduction      bool   `json:"need_noise_reduction"`
	NeedVolumnNormalization bool   `json:"need_volumn_normalization"`
}

// SynthesizeMusic runs one minimax-music-3.0 call and returns the
// terminal request-queue response body raw. The audio is not in the
// body: the terminal music record carries the bed at
// outcome.media_urls[0].url (beside outcome.audio_url) and its length
// at outcome.duration_ms — the caller downloads the URL on receipt
// (PLAN.md invariant 7), exactly as T8 downloads a TTS clip.
//
// lyrics is REQUIRED by the upstream model and must be non-empty:
// minimax-music-3.0 wants to write a song, and a wordless bed is made
// by directing the lyrics themselves (the gibberish-vocalise default
// lives in internal/audio — the settled operator-verified shape,
// PLAN.md §T12). prompt is the optional style description and is sent
// verbatim when non-empty; format is pinned to defaultMusicFormat
// (mp3). model defaults to defaultMusicModel when empty.
//
// This is contract row C1 of dev-diary/adversarial-review/t12-round1.md:
// the music POST goes through this request-queue client (PLAN.md
// invariant 1), and internal/audio's GenerateMusicBed consumes it
// through a one-method seam pinned at compile time (audio/wire_test.go).
func (c *Client) SynthesizeMusic(ctx context.Context, lyrics, prompt, model string) ([]byte, error) {
	if strings.TrimSpace(lyrics) == "" {
		return nil, errors.New("media: SynthesizeMusic lyrics are empty")
	}
	if model == "" {
		model = defaultMusicModel
	}
	// acceptAudio, not acceptJSON: music is an audio model, and the
	// TTS precedent is that a gateway honouring Accept answers a
	// JSON advertisement on an audio call with 406
	// (adversarial-review/t2-round3.md L4). The body is still the
	// queue's JSON envelope — the record is decoded by the caller.
	return c.drive(ctx, model, acceptAudio, musicPayload{Lyrics: lyrics, Prompt: prompt, Format: defaultMusicFormat})
}

// musicPayload is the wire shape of a minimax-music-3.0 call: a named
// struct, never a map (AGENTS.md §Go style). Lyrics is required by the
// model (PLAN.md §T12's settled schema table); prompt is the optional
// style description, omitted when empty; format is pinned to mp3 and
// always present. sample_rate and bitrate exist upstream but are not
// sent — nothing here needs anything but the model's own defaults.
type musicPayload struct {
	Lyrics string `json:"lyrics"`
	Prompt string `json:"prompt,omitempty"`
	Format string `json:"format"`
}

// post sends one envelope to the request-queue endpoint. The whole
// package's auth, error-classification, deadline and retry story
// lives here so every method stays a one-payload call site.
//
// PLAN.md §T2's contract, enforced here: a caller context without a
// deadline gets defaultCallTimeout; a transient failure (5xx,
// transport error, a request-queue "failed" status) is retried
// exactly once; a 4xx never is.
func (c *Client) post(ctx context.Context, model, accept string, payload any) ([]byte, error) {
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
	// stay raw for the caller (T6/T8) — and retry it like a 5xx. Every
	// request-queue answer is the same JSON envelope, TTS included —
	// the audio sits at outcome.audio_url, not in the body
	// (t2b-t5b-live-record.md) — so a TTS record's status is peeked
	// like any other's.
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
