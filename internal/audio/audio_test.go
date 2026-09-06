package audio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/mediastore"
	"thutapi/internal/store"
	"thutapi/internal/story"
)

// mp3FixtureFrameLen is the byte length of one frame in the fixture
// MP3s: MPEG-1 Layer III, 128 kbps, 44.1 kHz, mono, no padding —
// 144×128000/44100 = 417 bytes. The parser's CBR arithmetic counts
// dataBytes/frameLen frames, so a fixture of n full frames measures
// exactly n×1152/44100 seconds.
const mp3FixtureFrameLen = 417

// fixtureFrames is how many MPEG frames every narration fixture clip
// carries. The count is arbitrary but fixed: it makes every clip
// parseable by measureDuration (NarrateBook measures each clip as it
// downloads — contract row C2 of t12-round1.md) with a deterministic,
// assertable Go-known duration.
const fixtureFrames = 77

// makeMP3 builds a parseable mono MPEG-1 Layer III CBR clip: an ID3v2
// tag whose body is the distinct text (so the bytes differ per page —
// what lets a test assert the blob on disk is exactly the clip
// downloaded for that page's text) followed by fixtureFrames silent
// frames. The bytes are a real MP3 structure for measureDuration, but
// nothing downstream decodes them: the download path accepts the
// served Content-Type, and the measurement reads structure, not audio.
func makeMP3(text string) []byte {
	size := len(text)
	buf := make([]byte, 0, 10+size+fixtureFrames*mp3FixtureFrameLen)
	buf = append(buf, 'I', 'D', '3', 4, 0, 0,
		byte(size>>21&0x7f), byte(size>>14&0x7f), byte(size>>7&0x7f), byte(size&0x7f))
	buf = append(buf, text...)
	for i := 0; i < fixtureFrames; i++ {
		buf = append(buf, 0xFF, 0xFB, 0x90, 0xC0)
		buf = append(buf, make([]byte, mp3FixtureFrameLen-4)...)
	}
	return buf
}

// fixtureClipDuration is the Go-known duration of every makeMP3 clip:
// fixtureFrames × 1152 samples / 44100 Hz. NarrateBook pins it on
// Clip.Duration, so narration tests assert it exactly.
func fixtureClipDuration() time.Duration {
	return mp3DurationFrom(fixtureFrames, 1152, 44100)
}

// clipBytes is the deterministic audio payload an audio server serves
// for text: a parseable MP3 whose ID3v2 body is the text itself (see
// makeMP3). It must be distinct per page — which is what lets a test
// assert that the blob on disk is exactly the clip downloaded for that
// page's text.
func clipBytes(text string) []byte {
	return makeMP3(text)
}

// successEnvelope is a terminal request-queue TTS record naming the
// given audio URL, in the live-verified shape of
// t2b-t5b-live-record.md: status success, the request echoed in
// payload, the audio at outcome.audio_url.
func successEnvelope(audioURL string) []byte {
	return []byte(`{"request_id":"req-1","model":"minimax-tts-speech-2.8-hd","status":"success","payload":{"text":"echoed request","voice_id":"English_expressive_narrator"},"outcome":{"audio_url":"` + audioURL + `","format":"mp3","status":"success"}}`)
}

// ttsCall is one recorded SynthesizeSpeech invocation.
type ttsCall struct {
	text, emotion, voice, model string
}

// fakeTTS is a scripted speech client: it records every call and
// answers with a success envelope whose audio_url points at
// audioBase/clip/<escaped text>. It respects ctx cancellation like the
// real media client. Tests assert on the recorded args — the seam
// level at which this package's own contribution (per-page emotion,
// the default voice and model) is observable, since the wire payload
// itself is built inside internal/gmi/media.
type fakeTTS struct {
	// audioBase is the audio server prefix envelopes point at. Empty
	// with no err means a call is a test bug, reported loudly.
	audioBase string
	// err, when non-nil, is returned by every call (after recording).
	err error
	// raw, when non-nil, is returned verbatim instead of an envelope.
	raw []byte

	mu    sync.Mutex
	calls []ttsCall
}

func (f *fakeTTS) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, ttsCall{text: text, emotion: emotion, voice: voice, model: model})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.raw != nil {
		return f.raw, nil
	}
	if f.audioBase == "" {
		return nil, fmt.Errorf("fakeTTS: no audio base configured and no raw body set")
	}
	return successEnvelope(f.audioBase + "/clip/" + url.PathEscape(text)), nil
}

// recorded returns a copy of the calls so far.
func (f *fakeTTS) recorded() []ttsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ttsCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// count reports how many calls have been recorded.
func (f *fakeTTS) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// clipServer serves one audio/mpeg clip per text at /clip/<text>.
// blocked, when non-nil, gates the response for the matching text
// until the channel is closed (see the persist-ordering pins).
type clipServer struct {
	*httptest.Server
	mu    sync.Mutex
	total int
	seen  int // the total handed out by the last hitsSince
}

// newClipServer serves clipBytes(text) for every GET /clip/<text>,
// counting downloads.
func newClipServer(t *testing.T, blocked map[string]chan struct{}) *clipServer {
	t.Helper()
	s := &clipServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := strings.TrimPrefix(r.URL.Path, "/clip/")
		s.mu.Lock()
		s.total++
		s.mu.Unlock()
		if gate := blocked[text]; gate != nil {
			<-gate
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(clipBytes(text))
	}))
	t.Cleanup(s.Close)
	return s
}

// audioURL returns the clip URL for text on this server.
func (s *clipServer) audioURL(text string) string {
	return s.URL + "/clip/" + url.PathEscape(text)
}

// totalHits reports how many downloads have been handled.
func (s *clipServer) totalHits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

// hitsSince reports how many downloads happened since the last call
// to this method.
func (s *clipServer) hitsSince() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.total - s.seen
	s.seen = s.total
	return d
}

// cleanupClose closes a release channel exactly once when the test
// ends, so a failing assertion cannot strand a blocked server.
func cleanupClose(t *testing.T, release chan struct{}) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
}

// narrationHarness is a real store + mediastore over temp dirs whose
// book rows carry pages 1..pageCount — internal/store's and
// internal/mediastore's test conventions (illustrate/persist_test.go
// builds the same harness before placing media). Persistence tests
// run against the real sqlite + blob store, never a fake: the anchor
// foreign keys, the partial unique narration slot and the blob
// lifecycle are exactly what NarrateBook must survive.
type narrationHarness struct {
	db       *store.DB
	blobs    *mediastore.Store
	mediaDir string
	bookID   string
	clips    *clipServer
}

func newNarrationHarness(t *testing.T, pageCount int) *narrationHarness {
	t.Helper()
	ctx := t.Context()
	db, err := store.Open(ctx, store.Config{Path: filepath.Join(t.TempDir(), "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mediaDir := filepath.Join(t.TempDir(), "media")
	blobs, err := mediastore.Open(ctx, mediastore.Config{Dir: mediaDir, DB: db})
	if err != nil {
		t.Fatalf("open mediastore: %v", err)
	}
	book, err := db.CreateBook(ctx, "narration book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	for n := 1; n <= pageCount; n++ {
		if err := db.CreatePage(ctx, store.Page{BookID: book.ID, N: n, Text: "row " + fmt.Sprint(n), Emotion: "happy"}); err != nil {
			t.Fatalf("create page %d: %v", n, err)
		}
	}
	return &narrationHarness{
		db:       db,
		blobs:    blobs,
		mediaDir: mediaDir,
		bookID:   book.ID,
		clips:    newClipServer(t, nil),
	}
}

// blobBytes reads the blob file behind a media id back from disk —
// the byte-level check that what the store row describes is what
// NarrateBook actually downloaded and persisted.
func (h *narrationHarness) blobBytes(t *testing.T, id string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.mediaDir, id))
	if err != nil {
		t.Fatalf("read blob %s: %v", id, err)
	}
	return b
}

// blobGone reports whether the blob file behind id has vanished from
// disk.
func (h *narrationHarness) blobGone(t *testing.T, id string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(h.mediaDir, id))
	return os.IsNotExist(err)
}

// cfg returns the Config a narration test wires: the fake TTS pointed
// at the harness clip server, plus the real store and blob store.
func (h *narrationHarness) cfg(fake *fakeTTS) Config {
	fake.audioBase = h.clips.URL
	return Config{TTS: fake, DB: h.db, Blobs: h.blobs}
}

// threePages is the standard narration fixture: pages 1..3, each with
// a distinct text and an emotion from story.Emotions.
func threePages() []story.Page {
	return []story.Page{
		{N: 1, Text: "Mira heard a sniffle under the porch.", Emotion: "surprised"},
		{N: 2, Text: "A small dragon blinked back at her.", Emotion: "happy"},
		{N: 3, Text: "Puff sneezed a tiny puff of smoke.", Emotion: "fearful"},
	}
}
