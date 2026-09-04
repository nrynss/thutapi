package media

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"thutapi/internal/gmi"
)

// fakePNG is a minimal valid PNG: the 8-byte signature followed by
// enough of an IHDR chunk that http.DetectContentType classifies it as
// image/png. We never decode it; the test only checks the wire shape.
var fakePNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, // signature
	0x00, 0x00, 0x00, 0x0d, // IHDR length = 13
	'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, // width = 1
	0x00, 0x00, 0x00, 0x01, // height = 1
	0x08, 0x06, 0x00, 0x00, 0x00, // bit depth 8, color type 6, etc.
}

// fakeResponse is what a successful request-queue call returns. The
// shape is generic on purpose — different models wrap the result
// differently, and T2 does not pretend to know them all.
const fakeResponse = `{"request_id":"req-abc","status":"completed","result":{"b64_json":"aGVsbG8="}}`

// captured is the request shape the test fakes inspect. Each test sets
// up an httptest server that decodes the inbound envelope once into
// this struct so the assertions stay readable.
type captured struct {
	method      string
	path        string
	auth        string
	contentType string
	envelope    envelope
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
	raw, err := c.GenerateImage(context.Background(), "a small girl in a red coat", "Z-Image-Turbo")
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
	if got.envelope.Model != "Z-Image-Turbo" {
		t.Errorf("envelope.model = %q, want Z-Image-Turbo", got.envelope.Model)
	}
	if got.envelope.Payload["prompt"] != "a small girl in a red coat" {
		t.Errorf("envelope.payload[prompt] = %v, want the literal prompt", got.envelope.Payload["prompt"])
	}
}

// TestEditImage asserts the i2i wire shape: the reference image is
// inlined as a data: URI in the payload (project.md §2b "Useful
// asymmetry"), the prompt is carried, and the model id is the one the
// caller chose.
func TestEditImage(t *testing.T) {
	srv, got := fakeServer(t, fakeResponse, http.StatusOK)
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "test-key")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)

	c := New()
	raw, err := c.EditImage(context.Background(), fakePNG, "same girl, now beside a dragon", "Flux2-Klein")
	if err != nil {
		t.Fatalf("EditImage: %v", err)
	}
	if !strings.Contains(string(raw), "request_id") {
		t.Errorf("response = %q, want it to contain request_id", raw)
	}

	if got.envelope.Model != "Flux2-Klein" {
		t.Errorf("envelope.model = %q, want Flux2-Klein", got.envelope.Model)
	}
	if got.envelope.Payload["prompt"] != "same girl, now beside a dragon" {
		t.Errorf("envelope.payload[prompt] = %v, want the literal prompt", got.envelope.Payload["prompt"])
	}
	// Pin: the reference image goes in as inline base64, not as a URL.
	// http.DetectContentType classifies the fake PNG as image/png.
	dataURI, _ := got.envelope.Payload["image"].(string)
	if !strings.HasPrefix(dataURI, "data:image/png;base64,") {
		t.Errorf("envelope.payload[image] = %q, want a data:image/png;base64,... URI", dataURI)
	}
	if dataURI == "" || len(dataURI) < len("data:image/png;base64,")+8 {
		t.Errorf("envelope.payload[image] = %q, want a non-trivial base64 payload", dataURI)
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
	_, err := c.GenerateImage(context.Background(), "x", "")
	if !errors.Is(err, gmi.ErrUnauthorized) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrUnauthorized)", err)
	}
}

// TestMissingAPIKey verifies the media client fails closed when the
// env var is absent.
func TestMissingAPIKey(t *testing.T) {
	t.Setenv("GMI_API_KEY", "")
	t.Setenv("GMI_MEDIA_BASE_URL", "http://unused.invalid")

	c := New()
	_, err := c.GenerateImage(context.Background(), "x", "")
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
	if _, err := c.GenerateImage(context.Background(), "", ""); err == nil {
		t.Error("GenerateImage accepted empty prompt")
	}
	if _, err := c.EditImage(context.Background(), nil, "x", ""); err == nil {
		t.Error("EditImage accepted nil refImage")
	}
	if _, err := c.EditImage(context.Background(), fakePNG, "", ""); err == nil {
		t.Error("EditImage accepted empty prompt")
	}
	if _, err := c.SynthesizeSpeech(context.Background(), "", "v", ""); err == nil {
		t.Error("SynthesizeSpeech accepted empty text")
	}
	if _, err := c.SynthesizeSpeech(context.Background(), "x", "", ""); err == nil {
		t.Error("SynthesizeSpeech accepted empty voice")
	}
}
