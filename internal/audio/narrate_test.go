package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/store"
	"thutapi/internal/story"
)

// TestNarrateBook_DefaultsReachTheCalls pins the narration defaults
// through their default paths (AGENTS.md §Testing rule 2): a Config
// with empty Voice, Model and Limit still sends DefaultVoice and
// DefaultNarrationModel on every call, and a page whose text and
// emotion came from the fixture reaches the TTS seam exactly as the
// page carries it — the emotion mechanism pinned at the seam this
// package controls.
func TestNarrateBook_DefaultsReachTheCalls(t *testing.T) {
	h := newNarrationHarness(t, 3)
	fake := &fakeTTS{}
	cfg := h.cfg(fake)
	cfg.Limit = 1 // serialise: page order is the call order
	clips, err := NarrateBook(t.Context(), cfg, h.bookID, threePages())
	if err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
	calls := fake.recorded()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	for i, p := range threePages() {
		c := calls[i]
		if c.text != p.Text || c.emotion != p.Emotion {
			t.Errorf("call %d = (%q, %q), want the page's (%q, %q)", i, c.text, c.emotion, p.Text, p.Emotion)
		}
		if c.voice != DefaultVoice {
			t.Errorf("call %d voice = %q, want default %q", i, c.voice, DefaultVoice)
		}
		if c.model != DefaultNarrationModel {
			t.Errorf("call %d model = %q, want default %q", i, c.model, DefaultNarrationModel)
		}
	}
	if len(clips) != 3 {
		t.Fatalf("clips = %d, want 3", len(clips))
	}
	for i, clip := range clips {
		if clip.N != threePages()[i].N {
			t.Errorf("clip %d carries page %d, want %d", i, clip.N, threePages()[i].N)
		}
		// Contract row C2 of t12-round1.md: NarrateBook measures each
		// clip's length out of its own bytes and carries it forward —
		// the Go-known duration a narrated film's total is summed
		// from. Every fixture clip measures exactly fixtureClipDuration.
		if clip.Duration != fixtureClipDuration() {
			t.Errorf("clip %d duration = %v, want the measured fixture duration %v", i, clip.Duration, fixtureClipDuration())
		}
		row, err := h.db.PageMedia(t.Context(), h.bookID, clip.N, store.MediaNarration)
		if err != nil {
			t.Fatalf("page %d narration row missing: %v", clip.N, err)
		}
		if row.ID != clip.Media.ID {
			t.Errorf("clip %d media id %s differs from the placed row %s", i, clip.Media.ID, row.ID)
		}
	}
}

// TestNarrateBook_UnmeasurableClipFailsLoudly pins contract row C2's
// error branch: a clip that passes the content-type gate but whose
// bytes are not a readable MP3/WAV structure cannot carry the Go-known
// duration the film total needs, so the run fails with ErrClipDuration
// before persisting anything — loudly, never a silent zero.
func TestNarrateBook_UnmeasurableClipFailsLoudly(t *testing.T) {
	h := newNarrationHarness(t, 1)
	// A fake TTS that answers with an audio/mpeg envelope whose bytes
	// are arbitrary (content-type gate passes, structure is absent).
	// Note: h.cfg would re-point the fake at the harness's parseable
	// clip server, so the config is built by hand here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("this is not an mp3 at all"))
	}))
	defer srv.Close()
	fake := &fakeTTS{audioBase: srv.URL}
	pages := threePages()[:1]
	clips, err := NarrateBook(t.Context(), Config{TTS: fake, DB: h.db, Blobs: h.blobs}, h.bookID, pages)
	if !errors.Is(err, ErrClipDuration) {
		t.Fatalf("err = %v, want errors.Is(.., ErrClipDuration)", err)
	}
	// The page failed, so its slot comes back unspoken rather than the
	// whole run coming back empty (H3): the caller renders that page at
	// the captioned-silent tier instead of discarding the book.
	if len(clips) != 1 || clips[0].Spoken() {
		t.Fatalf("clips = %v, want one unspoken clip", clips)
	}
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaNarration); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("page 1 err = %v, want ErrNotFound (nothing persisted)", err)
	}
}

// TestNarrateBook_ExplicitConfigWinsOverDefaults pins that a caller
// may switch the voice and model (the narrator's voice is the default
// only until a track with a real voice arrives): non-empty Config
// values travel verbatim.
func TestNarrateBook_ExplicitConfigWinsOverDefaults(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	cfg := h.cfg(fake)
	cfg.Voice = "a-voice-id"
	cfg.Model = "minimax-tts-speech-2.8-turbo"
	if _, err := NarrateBook(t.Context(), cfg, h.bookID, threePages()[:1]); err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
	c := fake.recorded()[0]
	if c.voice != "a-voice-id" || c.model != "minimax-tts-speech-2.8-turbo" {
		t.Errorf("call = (%q, %q), want the configured voice and model", c.voice, c.model)
	}
}

// TestNarrateBook_ValidationFailsBeforeAnyCall pins that everything
// that can fail without spending money fails first: an unusable
// configuration or page set never reaches the TTS seam (the fake
// counts zero calls), and each failure asserts its sentinel.
func TestNarrateBook_ValidationFailsBeforeAnyCall(t *testing.T) {
	h := newNarrationHarness(t, 3)
	good := threePages()

	tests := []struct {
		name    string
		mutate  func(cfg *Config, pages *[]story.Page)
		wantErr error
	}{
		{name: "no tts configured", mutate: func(cfg *Config, _ *[]story.Page) { cfg.TTS = nil }, wantErr: ErrNoTTS},
		{name: "no db configured", mutate: func(cfg *Config, _ *[]story.Page) { cfg.DB = nil }, wantErr: ErrNoStore},
		{name: "no blob store configured", mutate: func(cfg *Config, _ *[]story.Page) { cfg.Blobs = nil }, wantErr: ErrNoStore},
		{name: "no pages", mutate: func(_ *Config, pages *[]story.Page) { *pages = nil }, wantErr: ErrNoPages},
		{name: "page number below 1", mutate: func(_ *Config, pages *[]story.Page) { (*pages)[1].N = 0 }, wantErr: ErrInvalidPage},
		{name: "duplicate page number", mutate: func(_ *Config, pages *[]story.Page) { (*pages)[2].N = 1 }, wantErr: ErrInvalidPage},
		{name: "empty page text", mutate: func(_ *Config, pages *[]story.Page) { (*pages)[0].Text = "  " }, wantErr: ErrNoText},
		{name: "out-of-set emotion", mutate: func(_ *Config, pages *[]story.Page) { (*pages)[0].Emotion = "curious" }, wantErr: ErrInvalidPage},
		{name: "empty emotion", mutate: func(_ *Config, pages *[]story.Page) { (*pages)[0].Emotion = "" }, wantErr: ErrInvalidPage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeTTS{}
			pages := append([]story.Page(nil), good...)
			cfg := h.cfg(fake)
			tt.mutate(&cfg, &pages)
			clips, err := NarrateBook(t.Context(), cfg, h.bookID, pages)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want errors.Is(.., %v)", err, tt.wantErr)
			}
			if clips != nil {
				t.Fatalf("clips = %v, want nil on error", clips)
			}
			if n := fake.count(); n != 0 {
				t.Fatalf("TTS calls = %d, want 0 — a run that can fail before spending must fail before spending", n)
			}
		})
	}

	// The out-of-set rejection names the vocabulary, so the operator
	// sees the fix, not a mystery.
	fake := &fakeTTS{}
	pages := append([]story.Page(nil), good...)
	pages[0].Emotion = "neutral"
	_, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, pages)
	if !errors.Is(err, ErrInvalidPage) || !strings.Contains(err.Error(), "neutral") || !strings.Contains(err.Error(), "happy") {
		t.Fatalf("err = %v, want ErrInvalidPage naming the word and the vocabulary", err)
	}
}

// TestNarrateBook_SerialRunSpeaksPagesInTheOrderGiven pins the
// determinism contract: with Limit 1 the fan-out serialises in
// submission order, so the TTS seam sees the pages in exactly the
// order given and the returned clips carry that same order — even
// when the caller re-narrates a subset out of ascending order (a
// single-page re-run after an edit).
func TestNarrateBook_SerialRunSpeaksPagesInTheOrderGiven(t *testing.T) {
	h := newNarrationHarness(t, 3)
	fake := &fakeTTS{}
	pages := threePages()
	reordered := []story.Page{pages[2], pages[0], pages[1]} // 3, 1, 2
	cfg := h.cfg(fake)
	cfg.Limit = 1

	clips, err := NarrateBook(t.Context(), cfg, h.bookID, reordered)
	if err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
	calls := fake.recorded()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	for i, want := range reordered {
		if calls[i].text != want.Text || calls[i].emotion != want.Emotion {
			t.Errorf("call %d spoke (%q, %q), want page %d's (%q, %q)", i, calls[i].text, calls[i].emotion, want.N, want.Text, want.Emotion)
		}
		if clips[i].N != want.N {
			t.Errorf("clip %d = page %d, want page %d (input order, not ascending)", i, clips[i].N, want.N)
		}
		if clips[i].Media.PageN != want.N || clips[i].Media.Kind != store.MediaNarration {
			t.Errorf("clip %d row = %+v, want narration anchored at page %d", i, clips[i].Media, want.N)
		}
	}
}

// TestNarrateBook_FanOutRunsThePagesConcurrently pins that eight
// ~24 s TTS calls do not run as a serial loop (PLAN.md §T8): with the
// limit at eight, all eight calls are in flight at once before any
// has returned. A serial implementation never reaches the barrier and
// the test times out.
func TestNarrateBook_FanOutRunsThePagesConcurrently(t *testing.T) {
	h := newNarrationHarness(t, 8)
	pages := make([]story.Page, 8)
	for i := range pages {
		pages[i] = story.Page{N: i + 1, Text: fmt.Sprintf("page %d narration", i+1), Emotion: "happy"}
	}

	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	gate := &gateTTS{base: h.clips.URL, entered: entered, release: release}
	cfg := h.cfg(&fakeTTS{})
	cfg.TTS = gate
	cfg.Limit = 8

	done := make(chan error, 1)
	go func() {
		_, err := NarrateBook(t.Context(), cfg, h.bookID, pages)
		done <- err
	}()
	for range pages {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("not all 8 narration calls were in flight at once — the run is serialising the fan-out")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
	// Every page persisted.
	for _, p := range pages {
		if _, err := h.db.PageMedia(t.Context(), h.bookID, p.N, store.MediaNarration); err != nil {
			t.Errorf("page %d narration missing: %v", p.N, err)
		}
	}
}

// gateTTS is the fan-out fixture: every call reports entry and then
// waits for the shared release, so the test can prove how many calls
// are in flight simultaneously.
type gateTTS struct {
	base    string
	entered chan<- struct{}
	release <-chan struct{}
}

func (g *gateTTS) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
	g.entered <- struct{}{}
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return successEnvelope(g.base + "/clip/" + url.PathEscape(text)), nil
}

// TestNarrateBook_FailureDegradesOnlyItsOwnPage pins the per-page
// degradation contract (§T10f/§T10g, H3): a page that fails synthesis
// costs THAT PAGE its voice and nothing else. The run returns one clip
// per page in page order — the failing page's unspoken, its neighbours'
// spoken and persisted — alongside an error that names the page and
// passes the upstream sentinel through errors.Is. The failure does not
// cancel the siblings, so every page still reaches the TTS seam.
//
// This test replaces TestNarrateBook_FailureFailsTheRun, which pinned
// the opposite (nil clips, siblings cancelled). That contract is what
// discarded a real eight-page book on 2026-09-06 when page 4's TTS
// missed its poll deadline: the caller had nothing to render.
func TestNarrateBook_FailureDegradesOnlyItsOwnPage(t *testing.T) {
	h := newNarrationHarness(t, 3)
	pages := threePages()
	cfg := h.cfg(&fakeTTS{})
	cfg.Limit = 1

	// errUpstream stands in for an internal/gmi sentinel the media
	// client would return; the wrap must keep it matchable. It is
	// deliberately NOT a transient one — no error class is special.
	errUpstream := errors.New("gmi: request-queue poll deadline exceeded")
	scripted := &scriptedTTS{base: h.clips.URL, errs: map[string]error{pages[1].Text: errUpstream}}
	cfg.TTS = scripted
	clips, err := NarrateBook(t.Context(), cfg, h.bookID, pages)
	if !errors.Is(err, errUpstream) {
		t.Fatalf("err = %v, want errors.Is(.., errUpstream)", err)
	}
	if !strings.Contains(err.Error(), "page 2") {
		t.Errorf("err = %v, want it to name the failing page", err)
	}
	if len(clips) != len(pages) {
		t.Fatalf("clips = %v, want one per page even with a failure", clips)
	}
	for i, c := range clips {
		if c.N != pages[i].N {
			t.Errorf("clip %d is for page %d, want page %d", i, c.N, pages[i].N)
		}
		if want := pages[i].N != 2; c.Spoken() != want {
			t.Errorf("clip for page %d Spoken() = %v, want %v", c.N, c.Spoken(), want)
		}
	}
	if got := scripted.count(); got != 3 {
		t.Errorf("TTS calls = %d, want 3 (page 2's failure must not cancel pages 1 and 3)", got)
	}
	// Pages 1 and 3 persisted; only page 2 has no narration row.
	for _, n := range []int{1, 3} {
		if _, err := h.db.PageMedia(t.Context(), h.bookID, n, store.MediaNarration); err != nil {
			t.Errorf("page %d narration missing despite succeeding: %v", n, err)
		}
	}
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 2, store.MediaNarration); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("page 2 err = %v, want ErrNotFound", err)
	}
}

// scriptedTTS fails the call whose text is in errs, answering
// everyone else from base.
type scriptedTTS struct {
	base string
	errs map[string]error

	mu    sync.Mutex
	calls []string
}

func (s *scriptedTTS) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.calls = append(s.calls, text)
	s.mu.Unlock()
	if err := s.errs[text]; err != nil {
		return nil, err
	}
	return successEnvelope(s.base + "/clip/" + url.PathEscape(text)), nil
}

func (s *scriptedTTS) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// TestNarrateBook_DownloadedBytesPersist pins download-on-receipt end
// to end: the blob behind each placed row holds exactly the bytes the
// audio server served for that page's text — the store row is not
// placed first, and the GMI URL is never stored in place of bytes.
func TestNarrateBook_DownloadedBytesPersist(t *testing.T) {
	h := newNarrationHarness(t, 3)
	fake := &fakeTTS{}
	if _, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, threePages()); err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
	if n := h.clips.hitsSince(); n != 3 {
		t.Fatalf("audio downloads = %d, want 3", n)
	}
	for _, p := range threePages() {
		row, err := h.db.PageMedia(t.Context(), h.bookID, p.N, store.MediaNarration)
		if err != nil {
			t.Fatalf("page %d row: %v", p.N, err)
		}
		if row.ContentType != "audio/mpeg" || row.SizeBytes != int64(len(clipBytes(p.Text))) {
			t.Errorf("page %d row = %+v, want an audio/mpeg clip of %d bytes", p.N, row, len(clipBytes(p.Text)))
		}
		if got := h.blobBytes(t, row.ID); !bytes.Equal(got, clipBytes(p.Text)) {
			t.Errorf("page %d blob holds %q, want the served clip bytes", p.N, got)
		}
	}
}

// TestNarrateBook_DecodeFailureFailsThePage pins the narration path's
// decode guard: a page whose TTS response is not the request-queue
// audio envelope fails that page with ErrNoAudio — the media client
// returns every body raw, and the terminal TTS record is always an
// envelope, so a body without outcome.audio_url is a shape change
// that must surface, never pass through as silence.
func TestNarrateBook_DecodeFailureFailsThePage(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{raw: []byte("this is not a queue envelope")}
	clips, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, threePages()[:1])
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
	// The page failed, so its slot comes back unspoken rather than the
	// whole run coming back empty (H3): the caller renders that page at
	// the captioned-silent tier instead of discarding the book.
	if len(clips) != 1 || clips[0].Spoken() {
		t.Fatalf("clips = %v, want one unspoken clip", clips)
	}
}

// TestNarrate_PersistFailureFailsThePage pins the narration path's
// persist guard: when the blob store cannot record the downloaded
// clip (here: its database is closed), the page fails the run with
// an error naming the persist step — a clip that never reached the
// store is not a narrated page.
func TestNarrate_PersistFailureFailsThePage(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	if err := h.db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	clips, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, threePages()[:1])
	if err == nil {
		t.Fatal("err = nil, want the persist failure to fail the page")
	}
	if !strings.Contains(err.Error(), "persist narration clip") {
		t.Errorf("err = %v, want it to name the persist step", err)
	}
	// The page failed, so its slot comes back unspoken rather than the
	// whole run coming back empty (H3): the caller renders that page at
	// the captioned-silent tier instead of discarding the book.
	if len(clips) != 1 || clips[0].Spoken() {
		t.Fatalf("clips = %v, want one unspoken clip", clips)
	}
}
