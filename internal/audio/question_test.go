package audio

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// questionText is the standard question fixture.
const questionText = "What should Mira's dragon be called?"

// TestSynthesizeQuestion_DefaultsAndBytes pins the question path
// through its default path: the turbo model and the library voice
// reach the TTS seam, the call carries no emotion (empty — the
// question payload stays byte-identical to the live-verified shape),
// and the returned bytes are the downloaded clip, ready to play. The
// Config carries no store, so nothing persists and the returned
// media id is empty — the plain-bytes mode every questions-without-
// store caller keeps.
func TestSynthesizeQuestion_DefaultsAndBytes(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	cfg := Config{TTS: fake}
	fake.audioBase = h.clips.URL
	b, id, err := SynthesizeQuestion(t.Context(), cfg, questionText)
	if err != nil {
		t.Fatalf("SynthesizeQuestion: %v", err)
	}
	if !bytes.Equal(b, clipBytes(questionText)) {
		t.Errorf("bytes = %q, want the downloaded clip", b)
	}
	if id != "" {
		t.Errorf("media id = %q, want empty — a Config without a store persists nothing", id)
	}
	calls := fake.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.text != questionText || c.emotion != "" {
		t.Errorf("call = (%q, %q), want the question text and no emotion", c.text, c.emotion)
	}
	if c.voice != DefaultVoice {
		t.Errorf("voice = %q, want default %q", c.voice, DefaultVoice)
	}
	if c.model != DefaultQuestionModel {
		t.Errorf("model = %q, want default %q — questions use turbo, latency beats fidelity", c.model, DefaultQuestionModel)
	}
}

// TestSynthesizeQuestion_EmptyTextIsErrNoText pins the one input
// guard the question path owns: blank text is refused before any call
// (the seam would also refuse, but the guard keeps the failure here,
// where the caller can see it).
func TestSynthesizeQuestion_EmptyTextIsErrNoText(t *testing.T) {
	fake := &fakeTTS{}
	_, _, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, "   ")
	if !errors.Is(err, ErrNoText) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoText)", err)
	}
	if n := fake.count(); n != 0 {
		t.Fatalf("TTS calls = %d, want 0", n)
	}
}

// TestSynthesizeQuestion_NilTTSIsErrNoTTS pins the one config guard
// the question path shares: nothing can be spoken without a client.
func TestSynthesizeQuestion_NilTTSIsErrNoTTS(t *testing.T) {
	_, _, err := SynthesizeQuestion(t.Context(), Config{}, "hello?")
	if !errors.Is(err, ErrNoTTS) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoTTS)", err)
	}
}

// TestSynthesizeQuestion_UpstreamErrorPassesThrough pins that an
// internal/gmi sentinel from the TTS seam reaches the caller
// matchable with errors.Is, wrapped with which path failed.
func TestSynthesizeQuestion_UpstreamErrorPassesThrough(t *testing.T) {
	errUpstream := errors.New("gmi: rate limited")
	fake := &fakeTTS{err: errUpstream}
	_, _, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, "hello?")
	if !errors.Is(err, errUpstream) {
		t.Fatalf("err = %v, want errors.Is(.., errUpstream)", err)
	}
	if !strings.Contains(err.Error(), "synthesize question") {
		t.Errorf("err = %v, want it to name the question path", err)
	}
}

// TestSynthesizeQuestion_DecodeFailureIsLoud pins that a response
// that is not the request-queue audio envelope — the media client
// returns every body raw, and the terminal TTS record is always an
// envelope (t2b-t5b-live-record.md) — surfaces as ErrNoAudio rather
// than being handed to the player as if it were audio.
func TestSynthesizeQuestion_DecodeFailureIsLoud(t *testing.T) {
	fake := &fakeTTS{raw: []byte("ID3\x04\x00this is an mp3, not an envelope")}
	_, _, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, "hello?")
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
}

// TestSynthesizeQuestion_NoStoreConfigReturnsPlainBytes pins the
// boundary the persist-on-store contract keeps intact (PLAN.md §The
// flow, wire contract 2; supersedes round-1 D3's
// TestSynthesizeQuestion_NoStoreNeeded): persistence is conditional
// on a configured store exactly as narration's persist is on the
// caller wiring one. A Config with both DB and Blobs nil is the legal
// questions-without-store mode — the call returns the downloaded
// bytes and an empty media id, and nothing is persisted: the Config
// holds no store handle for any write to go through.
func TestSynthesizeQuestion_NoStoreConfigReturnsPlainBytes(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	fake.audioBase = h.clips.URL // the download server, without the store halves
	b, id, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, questionText)
	if err != nil {
		t.Fatalf("SynthesizeQuestion: %v", err)
	}
	if !bytes.Equal(b, clipBytes(questionText)) {
		t.Errorf("bytes = %q, want the downloaded clip", b)
	}
	if id != "" {
		t.Errorf("media id = %q, want empty — a Config without a store must not persist", id)
	}
	if n := h.clips.hitsSince(); n != 1 {
		t.Fatalf("downloads = %d, want 1", n)
	}
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("placed rows = %d, want 0 — the book gained no media", len(all))
	}
}

// TestSynthesizeQuestion_HalfStoreIsErrNoStore pins the config guard
// the persist-on-store contract adds: exactly one of DB and Blobs is
// a half-wired store, and silence would silently drop the persist —
// so it is refused loudly before any paid call, naming the missing
// half. (Both nil is not a half store; it is the legal plain-bytes
// mode, pinned above.)
func TestSynthesizeQuestion_HalfStoreIsErrNoStore(t *testing.T) {
	h := newNarrationHarness(t, 1)
	tests := []struct {
		name    string
		cfg     Config
		missing string // the half the error message must name
	}{
		{name: "db without blob store", cfg: Config{TTS: &fakeTTS{}, DB: h.db}, missing: "Blobs is nil"},
		{name: "blob store without db", cfg: Config{TTS: &fakeTTS{}, Blobs: h.blobs}, missing: "DB is nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := SynthesizeQuestion(t.Context(), tt.cfg, questionText)
			if !errors.Is(err, ErrNoStore) {
				t.Fatalf("err = %v, want errors.Is(.., ErrNoStore)", err)
			}
			if !strings.Contains(err.Error(), tt.missing) {
				t.Errorf("err = %v, want it to name the missing half (%s)", err, tt.missing)
			}
		})
	}
}

// TestSynthesizeQuestion_PersistsOnConfiguredStore pins wire contract
// 2 (PLAN.md §The flow): with a store configured, SynthesizeQuestion
// persists the downloaded clip — blob in mediastore, one unplaced
// media row — and returns the bytes AND the id. The row is unplaced
// on purpose: question audio is interview-scoped and the store's
// kinds (reference, illustration, narration) all anchor to a book, so
// no anchor exists or is invented. The id then serves over the real
// GET /media/{id} route with the exact bytes and content type — the
// /media/<id> URL the question_audio SSE event will carry.
func TestSynthesizeQuestion_PersistsOnConfiguredStore(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	cfg := h.cfg(fake) // DB and Blobs both wired
	b, id, err := SynthesizeQuestion(t.Context(), cfg, questionText)
	if err != nil {
		t.Fatalf("SynthesizeQuestion: %v", err)
	}
	if id == "" {
		t.Fatal("media id = \"\", want a persisted id — a configured store must persist the clip")
	}
	if !bytes.Equal(b, clipBytes(questionText)) {
		t.Errorf("bytes = %q, want the downloaded clip", b)
	}

	// The row exists and is unplaced: no book, no kind, no anchor.
	row, err := h.db.Media(t.Context(), id)
	if err != nil {
		t.Fatalf("media row %s: %v", id, err)
	}
	if row.BookID != "" || row.Kind != "" || row.PageN != 0 || row.CastName != "" {
		t.Errorf("row = %+v, want an unplaced row — question audio has no book anchor", row)
	}
	if row.ContentType != "audio/mpeg" || row.SizeBytes != int64(len(clipBytes(questionText))) {
		t.Errorf("row = %+v, want the audio/mpeg clip of %d bytes", row, len(clipBytes(questionText)))
	}
	if got := h.blobBytes(t, id); !bytes.Equal(got, b) {
		t.Errorf("blob on disk = %q, want the persisted clip bytes", got)
	}

	// Nothing was placed into any book: BookMedia stays empty.
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("placed rows = %d, want 0 — question clips are unplaced, never book media", len(all))
	}

	// The id serves over the route the box registers (PLAN.md
	// invariant 5 — cmd/thutapi newServer), byte-identical with the
	// persisted content type.
	mux := http.NewServeMux()
	mux.Handle("GET /media/{id}", h.blobs)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/media/" + id) //nolint:noctx,gosec // local httptest
	if err != nil {
		t.Fatalf("GET /media/%s: %v", id, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read served body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "audio/mpeg" {
		t.Errorf("served: HTTP %d ct=%q, want 200 audio/mpeg", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !bytes.Equal(body, clipBytes(questionText)) {
		t.Errorf("served %d bytes, want the exact persisted clip (%d bytes)", len(body), len(clipBytes(questionText)))
	}

	// A second question is a second clip: every call persists its own
	// fresh unplaced row — there is no slot to replace or collide on.
	b2, id2, err := SynthesizeQuestion(t.Context(), cfg, "And where does the dragon sleep?")
	if err != nil {
		t.Fatalf("second SynthesizeQuestion: %v", err)
	}
	if id2 == "" || id2 == id {
		t.Fatalf("second media id = %q, want a fresh distinct id", id2)
	}
	if !bytes.Equal(b2, clipBytes("And where does the dragon sleep?")) {
		t.Errorf("second bytes = %q, want its own clip", b2)
	}
	if _, err := h.db.Media(t.Context(), id2); err != nil {
		t.Fatalf("second media row %s: %v", id2, err)
	}
}

// TestSynthesizeQuestion_PersistFailureFailsTheCall pins the persist
// branch: when the store cannot record the downloaded clip (here its
// database is closed), the call fails with an error naming the
// persist step and no bytes — a clip that never reached the store is
// not a playable question, and the caller treats the failure as
// "question_audio never arrives" (PLAN.md §The flow, wire contract 2:
// the event may never come and the UI stays silent).
func TestSynthesizeQuestion_PersistFailureFailsTheCall(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	cfg := h.cfg(fake)
	if err := h.db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	b, id, err := SynthesizeQuestion(t.Context(), cfg, questionText)
	if err == nil {
		t.Fatal("err = nil, want the persist failure to fail the call")
	}
	if !strings.Contains(err.Error(), "persist question clip") {
		t.Errorf("err = %v, want it to name the persist step", err)
	}
	if b != nil {
		t.Fatalf("bytes = %q, want nil on failure", b)
	}
	if id != "" {
		t.Fatalf("media id = %q, want empty on failure", id)
	}
}

// TestSynthesizeQuestion_FetchFailureIsErrNoAudio pins the question
// path's download guard: an audio_url that is already gone (the URLs
// expire) surfaces as ErrNoAudio — the question speaks nothing
// rather than playing silence or an error page.
func TestSynthesizeQuestion_FetchFailureIsErrNoAudio(t *testing.T) {
	fake := &fakeTTS{}
	cfg := Config{TTS: fake}
	fake.audioBase = newNotFoundServer(t).URL
	_, _, err := SynthesizeQuestion(t.Context(), cfg, "hello?")
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
}
