package audio

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestSynthesizeQuestion_DefaultsAndBytes pins the question path
// through its default path: the turbo model and the library voice
// reach the TTS seam, the call carries no emotion (empty — the
// question payload stays byte-identical to the live-verified shape),
// and the returned bytes are the downloaded clip, ready to play.
// Nothing is persisted: the Config carries no store, and that is
// legal for questions.
func TestSynthesizeQuestion_DefaultsAndBytes(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	cfg := Config{TTS: fake}
	fake.audioBase = h.clips.URL
	b, err := SynthesizeQuestion(t.Context(), cfg, "What should Mira's dragon be called?")
	if err != nil {
		t.Fatalf("SynthesizeQuestion: %v", err)
	}
	if !bytes.Equal(b, clipBytes("What should Mira's dragon be called?")) {
		t.Errorf("bytes = %q, want the downloaded clip", b)
	}
	calls := fake.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.text != "What should Mira's dragon be called?" || c.emotion != "" {
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
	_, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, "   ")
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
	_, err := SynthesizeQuestion(t.Context(), Config{}, "hello?")
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
	_, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, "hello?")
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
	_, err := SynthesizeQuestion(t.Context(), Config{TTS: fake}, "hello?")
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
}

// TestSynthesizeQuestion_NoStoreNeeded pins the seam: questions are
// not book narration and write no store rows — a Config whose DB and
// Blobs are nil is the legal question configuration, and no media row
// appears anywhere.
func TestSynthesizeQuestion_NoStoreNeeded(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	if _, err := SynthesizeQuestion(t.Context(), h.cfg(fake), "hello?"); err != nil {
		t.Fatalf("SynthesizeQuestion: %v", err)
	}
	if n := h.clips.hitsSince(); n != 1 {
		t.Fatalf("downloads = %d, want 1", n)
	}
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("placed rows = %d, want 0 — the interview keeps no audio rows", len(all))
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
	_, err := SynthesizeQuestion(t.Context(), cfg, "hello?")
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
}
