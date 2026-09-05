package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"thutapi/internal/gmi"
)

// pngRefURL is a reference-image URL of the shape seedream's
// payload.image carries: an http(s) URL string. The wire tests assert
// the array carries it verbatim, not what it points at.
const pngRefURL = "https://storage.googleapis.com/example-bucket/sheet-0.png?X-Goog-Signature=fake"

// fakeResponse is what a successful request-queue call returns. The
// shape is generic on purpose — different models wrap the result
// differently, and T2 does not pretend to know them all.
const fakeResponse = `{"request_id":"req-abc","status":"completed","result":{"b64_json":"aGVsbG8="}}`

// captured is the request shape the test fakes inspect. Each test sets
// up an httptest server that decodes the inbound envelope once into
// this struct so the assertions stay readable. Payload stays a decoded
// JSON map rather than the client's own payload type, so the
// assertions read the wire and not the implementation.
type captured struct {
	method      string
	path        string
	auth        string
	contentType string
	accept      string
	envelope    struct {
		Model   string         `json:"model"`
		Payload map[string]any `json:"payload"`
	}
}

// fakeServer returns an httptest server plus a pointer to the captured
// last request. responseBody and status are what the server returns.
func fakeServer(t *testing.T, responseBody string, status int) (*httptest.Server, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.method = r.Method
		c.path = r.URL.Path
		c.auth = r.Header.Get("Authorization")
		c.contentType = r.Header.Get("Content-Type")
		c.accept = r.Header.Get("Accept")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		if err := json.Unmarshal(raw, &c.envelope); err != nil {
			t.Errorf("unmarshal envelope: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	return srv, c
}

// TestGenerateImage asserts the t2i wire shape: a single envelope with
// the prompt carried verbatim, a model id, and the standard auth/header
// triple. The fake returns a request-queue response; the client returns
// the raw bytes so the caller can decode.
func TestGenerateImage(t *testing.T) {
	srv, got := fakeServer(t, fakeResponse, http.StatusOK)
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "test-key")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	raw, err := c.GenerateImage(context.Background(), "a small girl in a red coat", "seedream-5.0-lite", ImageOptions{})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if !strings.Contains(string(raw), "request_id") {
		t.Errorf("response = %q, want it to contain request_id", raw)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if got.path != "/api/v1/ie/requestqueue/apikey/requests" {
		t.Errorf("path = %q, want the request-queue path", got.path)
	}
	if got.auth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", got.auth)
	}
	if !strings.HasPrefix(got.contentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", got.contentType)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json on the image endpoints", got.accept)
	}
	if got.envelope.Model != "seedream-5.0-lite" {
		t.Errorf("envelope.model = %q, want seedream-5.0-lite", got.envelope.Model)
	}
	if got.envelope.Payload["prompt"] != "a small girl in a red coat" {
		t.Errorf("envelope.payload[prompt] = %v, want the literal prompt", got.envelope.Payload["prompt"])
	}
	// The pinned production payload, on the wire even at its defaults:
	// 4:5 portrait at explicit pixels (never the shape-inferring "2K"
	// preset), jpeg, one image, no watermark — and no `image` key at
	// all, because text-to-image carries no reference
	// (t6b-live-record.md §5).
	if got.envelope.Payload["size"] != DefaultImageSize {
		t.Errorf("envelope.payload[size] = %v, want %q", got.envelope.Payload["size"], DefaultImageSize)
	}
	if got.envelope.Payload["output_format"] != DefaultImageFormat {
		t.Errorf("envelope.payload[output_format] = %v, want %q", got.envelope.Payload["output_format"], DefaultImageFormat)
	}
	if got.envelope.Payload["max_images"] != float64(DefaultMaxImages) {
		t.Errorf("envelope.payload[max_images] = %v, want %d", got.envelope.Payload["max_images"], DefaultMaxImages)
	}
	if got.envelope.Payload["watermark"] != false {
		t.Errorf("envelope.payload[watermark] = %v, want false", got.envelope.Payload["watermark"])
	}
	if _, ok := got.envelope.Payload["image"]; ok {
		t.Errorf("envelope.payload[image] = %v, want no image key on a text-to-image call", got.envelope.Payload["image"])
	}
}

// TestEditImage asserts the i2i wire shape: payload.image is an ARRAY
// OF URL STRINGS carried verbatim — seedream takes references as URLs,
// not inline base64, and the pinned practice is to chain GMI's own
// public output URLs (t6b-live-record.md §4, project.md §2b) — with
// the prompt, the model id and the pinned production payload fields
// alongside.
func TestEditImage(t *testing.T) {
	srv, got := fakeServer(t, fakeResponse, http.StatusOK)
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "test-key")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	raw, err := c.EditImage(context.Background(), "same girl, now beside a dragon", "seedream-5.0-lite",
		[]string{pngRefURL}, ImageOptions{})
	if err != nil {
		t.Fatalf("EditImage: %v", err)
	}
	if !strings.Contains(string(raw), "request_id") {
		t.Errorf("response = %q, want it to contain request_id", raw)
	}

	if got.envelope.Model != "seedream-5.0-lite" {
		t.Errorf("envelope.model = %q, want seedream-5.0-lite", got.envelope.Model)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json on the image endpoints", got.accept)
	}
	if got.envelope.Payload["prompt"] != "same girl, now beside a dragon" {
		t.Errorf("envelope.payload[prompt] = %v, want the literal prompt", got.envelope.Payload["prompt"])
	}
	// Pin: the references go in as an array of URL strings, verbatim,
	// not as a data: URI and not as one bare string.
	imgs, _ := got.envelope.Payload["image"].([]any)
	if len(imgs) != 1 {
		t.Fatalf("envelope.payload[image] = %v, want a one-element array of URL strings", got.envelope.Payload["image"])
	}
	if imgs[0] != pngRefURL {
		t.Errorf("envelope.payload[image][0] = %v, want %q carried verbatim", imgs[0], pngRefURL)
	}
	if got.envelope.Payload["size"] != DefaultImageSize {
		t.Errorf("envelope.payload[size] = %v, want %q", got.envelope.Payload["size"], DefaultImageSize)
	}
	if got.envelope.Payload["output_format"] != DefaultImageFormat {
		t.Errorf("envelope.payload[output_format] = %v, want %q", got.envelope.Payload["output_format"], DefaultImageFormat)
	}
	if got.envelope.Payload["max_images"] != float64(DefaultMaxImages) {
		t.Errorf("envelope.payload[max_images] = %v, want %d", got.envelope.Payload["max_images"], DefaultMaxImages)
	}
	if got.envelope.Payload["watermark"] != false {
		t.Errorf("envelope.payload[watermark] = %v, want false", got.envelope.Payload["watermark"])
	}
}

// TestSynthesizeSpeech asserts the TTS wire shape, with the volumn
// typo pinned literally. The test fails the moment anyone "fixes" the
// spelling — which is exactly what we want, because GMI silently
// ignores the correctly-spelled key.
func TestSynthesizeSpeech(t *testing.T) {
	srv, got := fakeServer(t, fakeResponse, http.StatusOK)
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "test-key")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	_, err := c.SynthesizeSpeech(context.Background(), "Once upon a time", "English_expressive_narrator", "minimax-tts-speech-2.8-hd")
	if err != nil {
		t.Fatalf("SynthesizeSpeech: %v", err)
	}

	if got.envelope.Model != "minimax-tts-speech-2.8-hd" {
		t.Errorf("envelope.model = %q, want minimax-tts-speech-2.8-hd", got.envelope.Model)
	}
	if got.accept != "audio/*" {
		t.Errorf("Accept = %q, want audio/* on the TTS call — it returns audio bytes, not JSON", got.accept)
	}
	if got.envelope.Payload["text"] != "Once upon a time" {
		t.Errorf("envelope.payload[text] = %v, want the literal text", got.envelope.Payload["text"])
	}
	if got.envelope.Payload["voice_id"] != "English_expressive_narrator" {
		t.Errorf("envelope.payload[voice_id] = %v, want English_expressive_narrator", got.envelope.Payload["voice_id"])
	}

	// The whole point of this test: the literal key is "volumn" (no 'u'
	// in volume), and its value is exactly true. A "helpful" refactor
	// that fixes the spelling must fail this test before the wire
	// change goes anywhere.
	typoKey := "need_volumn_normalization"
	correctKey := "need_volume_normalization"
	if _, present := got.envelope.Payload[correctKey]; present {
		t.Errorf("payload contains %q — GMI silently ignores the correctly-spelled key; emit only %q",
			correctKey, typoKey)
	}
	v, present := got.envelope.Payload[typoKey]
	if !present {
		t.Fatalf("payload missing %q — the GMI typo is required, see project.md §4", typoKey)
	}
	if v != true {
		t.Errorf("payload[%q] = %v, want true", typoKey, v)
	}

	// The other audio flag is spelled correctly and must also be true.
	if got.envelope.Payload["need_noise_reduction"] != true {
		t.Errorf("payload[need_noise_reduction] = %v, want true", got.envelope.Payload["need_noise_reduction"])
	}
}

// TestSynthesizeSpeech_TypoPinnedInRawJSON is the strongest version of
// the volumn-typo pin: it serialises the captured payload back to JSON
// and asserts the literal substring appears. Any encoding-level
// shenanigans (case changes, escaping) would surface here.
func TestSynthesizeSpeech_TypoPinnedInRawJSON(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		raw = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeResponse))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	if _, err := c.SynthesizeSpeech(context.Background(), "hi", "English_expressive_narrator", "minimax-tts-speech-2.8-hd"); err != nil {
		t.Fatalf("SynthesizeSpeech: %v", err)
	}

	const needle = `"need_volumn_normalization":true`
	if !strings.Contains(string(raw), needle) {
		t.Errorf("raw body missing %q\nbody: %s", needle, raw)
	}
}

// TestUnauthorized verifies a 401 surfaces as gmi.ErrUnauthorized on
// the media path too. Same contract as the text client.
func TestUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "wrong")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	_, err := c.GenerateImage(context.Background(), "x", "", ImageOptions{})
	if !errors.Is(err, gmi.ErrUnauthorized) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrUnauthorized)", err)
	}
}

// TestBadRequest_400 pins M1 on the media path: a 400 must surface as
// gmi.ErrBadRequest, NOT gmi.ErrTransient.
func TestBadRequest_400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"prompt rejected by safety filter"}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "any-key")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	_, err := c.GenerateImage(context.Background(), "x", "", ImageOptions{})
	if err == nil {
		t.Fatal("GenerateImage returned nil error on 400")
	}
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrBadRequest)", err)
	}
	if errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, must NOT also be errors.Is(.., gmi.ErrTransient)", err)
	}
}

// TestMissingAPIKey verifies the media client fails closed when the
// env var is absent.
func TestMissingAPIKey(t *testing.T) {
	t.Setenv("GMI_API_KEY", "")
	t.Setenv("GMI_MEDIA_BASE_URL", "http://unused.invalid")

	c := New()
	_, err := c.GenerateImage(context.Background(), "x", "", ImageOptions{})
	if !errors.Is(err, gmi.ErrUnauthorized) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrUnauthorized)", err)
	}
}

// TestEmptyInputs verifies the three methods reject empty inputs at
// the validator — fail fast, do not waste a round-trip.
func TestEmptyInputs(t *testing.T) {
	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", "http://unused.invalid")

	c := New()
	if _, err := c.GenerateImage(context.Background(), "", "", ImageOptions{}); err == nil {
		t.Error("GenerateImage accepted empty prompt")
	}
	if _, err := c.EditImage(context.Background(), "x", "seedream-5.0-lite", nil, ImageOptions{}); err == nil {
		t.Error("EditImage accepted no reference image URLs")
	}
	if _, err := c.EditImage(context.Background(), "", "seedream-5.0-lite", []string{pngRefURL}, ImageOptions{}); err == nil {
		t.Error("EditImage accepted empty prompt")
	}
	if _, err := c.SynthesizeSpeech(context.Background(), "", "v", ""); err == nil {
		t.Error("SynthesizeSpeech accepted empty text")
	}
	if _, err := c.SynthesizeSpeech(context.Background(), "x", "", ""); err == nil {
		t.Error("SynthesizeSpeech accepted empty voice")
	}
}

// TestGenerateImage_DefaultModelPinnedInRawJSON exercises the
// empty-model default through its default path and asserts the model
// id that reaches the wire, at the raw-JSON level. Every test before
// round 3 passed an explicit model, so the default — which carried
// the forbidden Qwen-Image-2512 through two review rounds
// (t2-round3.md H1/M2) — was never executed.
func TestGenerateImage_DefaultModelPinnedInRawJSON(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		raw = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeResponse))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	if _, err := c.GenerateImage(context.Background(), "a small girl in a red coat", "", ImageOptions{}); err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}

	const want = `"model":"seedream-5.0-lite"`
	if !strings.Contains(string(raw), want) {
		t.Errorf("raw body missing %q — the default model is the decision under test\nbody: %s", want, raw)
	}
	if strings.Contains(string(raw), "Qwen-Image-2512") {
		t.Errorf("raw body contains the forbidden model id Qwen-Image-2512 (project.md §3): %s", raw)
	}
}

// TestSynthesizeSpeech_DefaultModelPinnedInRawJSON pins the TTS
// default through its default path: an empty model must put
// minimax-tts-speech-2.8-hd on the wire.
func TestSynthesizeSpeech_DefaultModelPinnedInRawJSON(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		raw = body
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeResponse))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	if _, err := c.SynthesizeSpeech(context.Background(), "hello", "English_expressive_narrator", ""); err != nil {
		t.Fatalf("SynthesizeSpeech: %v", err)
	}

	const want = `"model":"minimax-tts-speech-2.8-hd"`
	if !strings.Contains(string(raw), want) {
		t.Errorf("raw body missing %q\nbody: %s", want, raw)
	}
}

// TestEditImage_EmptyModelRejected pins the H1 fix: image-to-image
// has no model default. An empty model is a gmi.ErrBadRequest and the
// upstream is never called — a silent default here is what shipped a
// model that cannot take the reference.
func TestEditImage_EmptyModelRejected(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fakeResponse))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	_, err := c.EditImage(context.Background(), "same girl, now beside a dragon", "", []string{pngRefURL}, ImageOptions{})
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrBadRequest)", err)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hit %d time(s) with an empty model, want 0", hits.Load())
	}
}

// TestRetry_5xxHitTwice is the round-3 H2 Pin: a 5xx is retried
// exactly once, so the upstream sees two hits and the caller one
// gmi.ErrTransient.
func TestRetry_5xxHitTwice(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	_, err := c.GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if hits.Load() != 2 {
		t.Errorf("upstream hit %d time(s) on 5xx, want 2 (one call + one retry per PLAN.md §T2)", hits.Load())
	}
}

// TestRetry_FailedStatusThenSuccess pins the other half of the H2
// contract: a request-queue 200 with {"status":"failed"} is retried
// once, and the completed response reaches the caller as raw bytes.
func TestRetry_FailedStatusThenSuccess(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		if hits.Load() == 1 {
			_, _ = w.Write([]byte(`{"request_id":"req-1","status":"failed","error":"scheduling failed"}`))
			return
		}
		_, _ = w.Write([]byte(fakeResponse))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	raw, err := c.GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("upstream hit %d time(s) on a failed status, want 2 (one call + one retry per PLAN.md §T2)", hits.Load())
	}
	if !strings.Contains(string(raw), "req-abc") {
		t.Errorf("raw = %q, want the completed response handed back as raw bytes", raw)
	}
}

// TestRetry_FailedStatusBudgetSpent: with the upstream always
// reporting failed, the retry is spent and the caller gets
// gmi.ErrTransient with a nil body — never an error body dressed up
// as media (t2-round3.md L3).
func TestRetry_FailedStatusBudgetSpent(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"request_id":"req-1","status":"failed","error":"scheduling failed"}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	raw, err := c.GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if hits.Load() != 2 {
		t.Errorf("upstream hit %d time(s), want 2 (budget is exactly one retry)", hits.Load())
	}
	if raw != nil {
		t.Errorf("body = %q alongside a non-nil error, want nil", raw)
	}
}

// TestPayloadTooLarge_413 is the round-3 M1 Pin: 413 is the expected
// answer to an oversized inline reference image. It is the payload's
// fault, so it must surface as gmi.ErrBadRequest, never
// gmi.ErrTransient; it must not be retried; and it must not hand the
// caller an error body.
func TestPayloadTooLarge_413(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"payload too large"}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	raw, err := c.GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{})
	if err == nil {
		t.Fatal("GenerateImage returned nil error on 413")
	}
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrBadRequest)", err)
	}
	if errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, must NOT also be errors.Is(.., gmi.ErrTransient)", err)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d time(s) on a 4xx, want 1 (never retry a 4xx)", hits.Load())
	}
	if raw != nil {
		t.Errorf("body = %q alongside a non-nil error, want nil", raw)
	}
}

// TestPaymentRequired_402: billing is operator-actionable
// (PLAN.md §T11.2), gets its own sentinel, and is never retried — a
// retry storm against a now-paid endpoint is the failure mode the
// sentinel exists to prevent.
func TestPaymentRequired_402(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"free window closed"}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	_, err := c.GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{})
	if !errors.Is(err, gmi.ErrPaymentRequired) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrPaymentRequired)", err)
	}
	if errors.Is(err, gmi.ErrTransient) || errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("err = %v, must NOT also be errors.Is(.., gmi.ErrTransient) or errors.Is(.., gmi.ErrBadRequest)", err)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d time(s) on a 4xx, want 1 (never retry a 4xx)", hits.Load())
	}
}

// TestClassifyStatus_Table walks the classifier's arms through the
// endpoint: every 4xx lands on a non-transient sentinel and is hit
// exactly once, the 5xx and the unmapped-default arms are transient
// and retried once. The 418 and 302 rows are the arms rounds 1–3
// never executed.
func TestClassifyStatus_Table(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		want     error
		notWant  []error
		wantHits int
	}{
		{"401 unauthorized", http.StatusUnauthorized, gmi.ErrUnauthorized, []error{gmi.ErrTransient}, 1},
		{"402 payment required", http.StatusPaymentRequired, gmi.ErrPaymentRequired, []error{gmi.ErrTransient, gmi.ErrBadRequest}, 1},
		{"403 forbidden", http.StatusForbidden, gmi.ErrUnauthorized, []error{gmi.ErrTransient}, 1},
		{"404 model not found", http.StatusNotFound, gmi.ErrModelNotFound, []error{gmi.ErrTransient}, 1},
		{"413 payload too large", http.StatusRequestEntityTooLarge, gmi.ErrBadRequest, []error{gmi.ErrTransient}, 1},
		{"418 teapot is the 4xx catch-all", http.StatusTeapot, gmi.ErrBadRequest, []error{gmi.ErrTransient}, 1},
		{"429 rate limited", http.StatusTooManyRequests, gmi.ErrRateLimited, []error{gmi.ErrTransient}, 1},
		{"302 not followed is the default arm", http.StatusFound, gmi.ErrTransient, nil, 2},
		{"502 bad gateway", http.StatusBadGateway, gmi.ErrTransient, nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"upstream says no"}`))
			}))
			defer srv.Close()

			t.Setenv("GMI_API_KEY", "k")
			t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

			_, err := New().GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{})
			if err == nil {
				t.Fatal("GenerateImage returned nil error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want errors.Is(.., %v)", err, tc.want)
			}
			for _, nw := range tc.notWant {
				if errors.Is(err, nw) {
					t.Errorf("err = %v, must NOT also be errors.Is(.., %v)", err, nw)
				}
			}
			if hits.Load() != int64(tc.wantHits) {
				t.Errorf("upstream hit %d time(s), want %d", hits.Load(), tc.wantHits)
			}
		})
	}
}

// TestNoRetryWhenContextDead: a caller deadline that fires mid-attempt
// must not buy a second attempt on an already-dead context.
func TestNoRetryWhenContextDead(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(800 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	c := New()
	_, err := c.GenerateImage(ctx, "x", "Z-Image", ImageOptions{})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d time(s) with a dead context, want 1 (no retry on a spent context)", hits.Load())
	}
}

// deadlineRecorder is an http.RoundTripper that records the context
// deadline the client put on the outbound request — the only place
// the PLAN.md §T2 "every call carries a context deadline" default is
// observable without waiting out a real timeout.
type deadlineRecorder struct {
	deadline time.Time
	hasOne   bool
}

func (r *deadlineRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.deadline, r.hasOne = req.Context().Deadline()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(fakeResponse)),
		Header:     make(http.Header),
	}, nil
}

// TestDefaultDeadlineApplied: a caller passing context.Background()
// still gets a bounded call — the client applies defaultCallTimeout.
func TestDefaultDeadlineApplied(t *testing.T) {
	t.Setenv("GMI_API_KEY", "k")

	rec := &deadlineRecorder{}
	c := &Client{baseURL: "http://unused.invalid", httpClient: &http.Client{Transport: rec}}
	if _, err := c.GenerateImage(context.Background(), "x", "Z-Image", ImageOptions{}); err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if !rec.hasOne {
		t.Fatal("outbound request carries no deadline; PLAN.md §T2 requires one on every call")
	}
	if left := time.Until(rec.deadline); left <= 0 || left > defaultCallTimeout {
		t.Errorf("deadline in %v, want within (0, %v]", left, defaultCallTimeout)
	}
}

// TestCallerDeadlineRespected: a caller-supplied deadline wins — the
// default must not extend it.
func TestCallerDeadlineRespected(t *testing.T) {
	t.Setenv("GMI_API_KEY", "k")

	rec := &deadlineRecorder{}
	c := &Client{baseURL: "http://unused.invalid", httpClient: &http.Client{Transport: rec}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.GenerateImage(ctx, "x", "Z-Image", ImageOptions{}); err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if !rec.hasOne {
		t.Fatal("outbound request carries no deadline")
	}
	if left := time.Until(rec.deadline); left <= 0 || left > 5*time.Second {
		t.Errorf("deadline in %v, want within the caller's 5s, not the %v default", left, defaultCallTimeout)
	}
}
