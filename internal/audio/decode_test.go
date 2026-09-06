package audio

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDecodeAudioURL_KeysOnOutcome pins the decode against the raw
// wire: the live terminal TTS record (t2b-t5b-live-record.md §The
// finding) is decoded by its outcome subtree alone, and an audio-like
// URL echoed anywhere else — the payload beside the outcome is the
// request coming back — can never be mistaken for the result (the
// exact echo hazard that produced T6's round-1 H1).
func TestDecodeAudioURL_KeysOnOutcome(t *testing.T) {
	const live = `{"request_id":"2ba8cd0d-1a2b-4c3d-9e4f-5a6b7c8d9e0f","model":"minimax-tts-speech-2.8-hd","status":"success","payload":{"need_noise_reduction":true,"need_volumn_normalization":true,"text":"Once upon a time, a small dragon lost her shoe.","voice_id":"English_expressive_narrator"},"outcome":{"audio_url":"https://storage.googleapis.com/gmi-video-assests-prod/8f3c/book-page-3.mp3","format":"mp3","status":"success"},"created_at":1788581287,"updated_at":1788581309,"queued_at":1788581287}`
	got, err := decodeAudioURL([]byte(live))
	if err != nil {
		t.Fatalf("decodeAudioURL(live record) = %v", err)
	}
	const want = "https://storage.googleapis.com/gmi-video-assests-prod/8f3c/book-page-3.mp3"
	if got != want {
		t.Errorf("audio_url = %q, want %q", got, want)
	}

	// The payload echo carries an audio-looking URL of its own; the
	// outcome is the only place a result can be.
	decoy := `{"status":"success","payload":{"text":"say this happily","voice_id":"English_expressive_narrator","emotion":"happy","audio_url":"https://storage.googleapis.com/echoed-not-real/x.mp3"},"outcome":{"audio_url":"https://storage.googleapis.com/real-result/y.mp3","format":"mp3","status":"success"}}`
	got, err = decodeAudioURL([]byte(decoy))
	if err != nil {
		t.Fatalf("decodeAudioURL(decoy) = %v", err)
	}
	if got != "https://storage.googleapis.com/real-result/y.mp3" {
		t.Errorf("audio_url = %q, want the outcome's URL, never the payload echo", got)
	}
}

// TestDecodeAudioURL_FailureBranches pins every no-audio shape with
// its sentinel: an empty body, a body that is not an envelope, a raw
// audio file (the media client's stale doc claims it returns bytes;
// the live terminal record is an envelope, so a byte body is a shape
// change that must fail loudly, not be passed through), an envelope
// with no or null outcome, an outcome without audio_url, and a URL
// this package will not dereference.
func TestDecodeAudioURL_FailureBranches(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty body", raw: ""},
		{name: "not json", raw: "the queue sent a wall of text"},
		{name: "raw mp3 bytes not an envelope", raw: "ID3\x04\x00\x00\x00\x00\x00\x00frames"},
		{name: "json without an outcome", raw: `{"request_id":"r","status":"success"}`},
		{name: "queued record with null outcome", raw: `{"request_id":"r","status":"queued","outcome":null}`},
		{name: "outcome without audio_url", raw: `{"status":"success","outcome":{"format":"mp3","status":"success"}}`},
		{name: "empty audio_url", raw: `{"status":"success","outcome":{"audio_url":"","format":"mp3"}}`},
		{name: "file url refused", raw: `{"status":"success","outcome":{"audio_url":"file:///etc/passwd","format":"mp3"}}`},
		{name: "scheme-less url refused", raw: `{"status":"success","outcome":{"audio_url":"storage.googleapis.com/x.mp3","format":"mp3"}}`},
		{name: "hostless url refused", raw: `{"status":"success","outcome":{"audio_url":"https:///x.mp3","format":"mp3"}}`},
		{name: "outcome not an object", raw: `{"status":"success","outcome":"audio"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeAudioURL([]byte(tt.raw))
			if !errors.Is(err, ErrNoAudio) {
				t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
			}
			if got != "" {
				t.Fatalf("url = %q, want empty on error", got)
			}
		})
	}
}

// defaultSpeaker is a resolved Config with the package's default
// fetch client — what fetchAudio runs on when Config.HTTPClient is
// nil.
func defaultSpeaker(t *testing.T) *speaker {
	t.Helper()
	sp, err := (Config{TTS: &fakeTTS{}}).resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return sp
}

// TestFetchAudio_DownloadsOnReceipt pins the download side of
// download-on-receipt: the bytes and the served content type come
// back, and the wav spellings the storage edge may serve normalise to
// the one type internal/mediastore stores.
func TestFetchAudio_DownloadsOnReceipt(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		body    []byte
		wantCT  string
		wantErr error
	}{
		{name: "mp3", header: "audio/mpeg", body: clipBytes("p1"), wantCT: "audio/mpeg"},
		{name: "wav", header: "audio/wav", body: []byte("RIFF-wave"), wantCT: "audio/wav"},
		{name: "x-wav alias", header: "audio/x-wav; charset=binary", body: []byte("RIFF-wave"), wantCT: "audio/wav"},
		{name: "sniff fallback for an ID3 mp3", header: "", body: []byte("ID3\x04\x00\x00\x00tag"), wantCT: "audio/mpeg"},
		{name: "sniff fallback for a wav", header: "", body: []byte("RIFF\x00\x00\x00\x00WAVEfmt "), wantCT: "audio/wav"},
		{name: "unsupported audio type", header: "audio/ogg", body: []byte("OggS"), wantErr: ErrUnsupportedAudio},
		{name: "not audio at all", header: "text/html", body: []byte("<html>error page</html>"), wantErr: ErrNoAudio},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.header != "" {
					w.Header().Set("Content-Type", tt.header)
				}
				_, _ = w.Write(tt.body)
			}))
			defer srv.Close()
			sp := defaultSpeaker(t)
			b, ct, err := sp.fetchAudio(t.Context(), srv.URL)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is(.., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchAudio: %v", err)
			}
			if !strings.EqualFold(string(b), string(tt.body)) {
				t.Errorf("bytes = %q, want the served body", b)
			}
			if ct != tt.wantCT {
				t.Errorf("content type = %q, want %q", ct, tt.wantCT)
			}
		})
	}
}

// TestFetchAudio_ErrorBranches pins the loud failures of a download:
// a non-200 answer, an oversized body, a truncated body and a dead
// transport are all errors, each classified as its sentinel says —
// and a transport failure is not dressed up as a media defect.
func TestFetchAudio_ErrorBranches(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer notFound.Close()

	tooBig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(clipBytes("p1"))
		_, _ = w.Write(make([]byte, MaxAudioBytes))
	}))
	defer tooBig.Close()

	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(clipBytes("short")) // fewer bytes than declared
	}))
	defer truncated.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing is listening now, so the GET fails at the transport

	sp := defaultSpeaker(t)
	t.Run("non-200 is ErrNoAudio", func(t *testing.T) {
		_, _, err := sp.fetchAudio(t.Context(), notFound.URL)
		if !errors.Is(err, ErrNoAudio) {
			t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
		}
	})
	t.Run("oversized body is ErrUnsupportedAudio", func(t *testing.T) {
		_, _, err := sp.fetchAudio(t.Context(), tooBig.URL)
		if !errors.Is(err, ErrUnsupportedAudio) {
			t.Fatalf("err = %v, want errors.Is(.., ErrUnsupportedAudio)", err)
		}
	})
	t.Run("truncated body is an error", func(t *testing.T) {
		_, _, err := sp.fetchAudio(t.Context(), truncated.URL)
		if err == nil {
			t.Fatal("err = nil, want a read error on a truncated body")
		}
	})
	t.Run("dead transport is an error, not a media sentinel", func(t *testing.T) {
		_, _, err := sp.fetchAudio(t.Context(), deadURL)
		if err == nil {
			t.Fatal("err = nil, want a transport error")
		}
		if errors.Is(err, ErrNoAudio) || errors.Is(err, ErrUnsupportedAudio) {
			t.Fatalf("err = %v, want a bare transport error — a dead host is not a media defect", err)
		}
	})
}

// TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio pins the
// non-200 status guard on its own (t8-round1.md L1). The suite's
// existing 404 rows answer text/plain, so the content-type gate would
// map them to the same sentinel even if the status block were deleted;
// this fixture serves the 404 as audio/mpeg, which sails through the
// type gate — only the status check stands between a non-200 error
// body and a clip persisted as narration. Deleting the status block
// (decode.go:138-140) turns this test green: the swallow mutant.
func TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("page gone"))
	}))
	defer srv.Close()
	sp := defaultSpeaker(t)
	if _, _, err := sp.fetchAudio(t.Context(), srv.URL); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio) — a non-200 body must never be returned as a clip", err)
	}
}

// TestFetchAudio_RedirectPolicy pins the download client's guardrails
// (safeRedirectPolicy): only http/https hops are followed, the chain
// is capped, and a same-scheme redirect to another host — the
// legitimate storage-edge hop — still works.
func TestFetchAudio_RedirectPolicy(t *testing.T) {
	sp := defaultSpeaker(t)

	t.Run("redirect to another scheme is refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		}))
		defer srv.Close()
		if _, _, err := sp.fetchAudio(t.Context(), srv.URL); err == nil {
			t.Fatal("err = nil, want a refused non-http redirect")
		}
	})

	t.Run("a redirect chain is capped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/hop", http.StatusFound)
		}))
		defer srv.Close()
		if _, _, err := sp.fetchAudio(t.Context(), srv.URL); err == nil {
			t.Fatal("err = nil, want the redirect cap to fire")
		}
	})

	t.Run("a same-scheme cross-host hop is followed", func(t *testing.T) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(clipBytes("hop"))
		}))
		defer target.Close()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer srv.Close()
		b, ct, err := sp.fetchAudio(t.Context(), srv.URL)
		if err != nil {
			t.Fatalf("fetchAudio: %v", err)
		}
		if string(b) != string(clipBytes("hop")) || ct != "audio/mpeg" {
			t.Errorf("bytes/type = %q/%q, want the redirected clip", b, ct)
		}
	})
}

// TestFetchAudio_RedirectCapThreeDistinctFromGoDefault pins the
// documented 3-hop cap against Go's own default ceiling of 10
// (t8-round1.md L2). A chain that answers 302 four times and then 200
// succeeds under the stdlib default — the 4th redirect is followed to
// the clip — and must fail here, because the 4th hop exceeds
// maxRedirectHops(3). Deleting the hop-cap block (decode.go:191-193)
// turns this test green: the swallow mutant. The existing
// self-redirect fixture cannot see the difference (its chain never
// reaches a 200, so Go's own ceiling errors it too).
func TestFetchAudio_RedirectCapThreeDistinctFromGoDefault(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits <= 4 {
			http.Redirect(w, r, "/hop", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(clipBytes("after the cap"))
	}))
	defer srv.Close()
	sp := defaultSpeaker(t)
	if _, _, err := sp.fetchAudio(t.Context(), srv.URL); err == nil {
		t.Fatalf("err = nil, want the %d-hop chain refused — the 4th redirect exceeds maxRedirectHops", maxRedirectHops)
	}
}

// discardLog silences httptest's server log for a deliberate
// connection abort; the abort is the fixture, not a fault.
func discardLog() *log.Logger { return log.New(io.Discard, "", 0) }

// TestFetchAudio_ContextCancellation pins ctx-first behaviour: the
// download honours the caller's context (a canceled context fails the
// fetch rather than hanging on a slow edge).
func TestFetchAudio_ContextCancellation(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked
	}))
	srv.Config.ErrorLog = discardLog()
	srv.Start()
	// ONE defer, in this order: releasing the parked handler must happen
	// BEFORE Close. Two separate defers run LIFO, so Close ran first and
	// waited forever on the still-parked connection — a deadlock that only
	// showed under whole-package load and made `go test ./... -race` red
	// about 60% of the time (M4).
	defer func() {
		close(blocked)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	sp := defaultSpeaker(t)
	go func() {
		_, _, err := sp.fetchAudio(ctx, srv.URL)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("err = nil, want a context-canceled fetch to fail")
		}
	case <-timeoutCh(t):
		t.Fatal("fetchAudio did not honour context cancellation")
	}
}

// TestAudioType_Direct pins audioType's own decisions — the branches
// an HTTP response header makes unreachable through fetchAudio, where
// Go's server sniffs and fills a missing Content-Type itself:
// parameter stripping, the header-absent sniff fallback, the wav
// spellings, and the empty-type corner.
func TestAudioType_Direct(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		body    []byte
		wantCT  string
		wantOK  bool
		wantErr error
	}{
		{name: "mpeg with parameters", header: "audio/mpeg; charset=binary", body: nil, wantCT: "audio/mpeg", wantOK: true},
		{name: "uppercase type", header: "AUDIO/MPEG", body: nil, wantCT: "audio/mpeg", wantOK: true},
		{name: "sniff fallback detects an ID3 mp3", header: "", body: []byte("ID3\x04\x00\x00\x00tagdata"), wantCT: "audio/mpeg", wantOK: true},
		{name: "sniff fallback detects a wav", header: "", body: []byte("RIFF\x00\x00\x00\x00WAVEfmt "), wantCT: "audio/wav", wantOK: true},
		{name: "wav spelling", header: "audio/x-wav", body: nil, wantCT: "audio/wav", wantOK: true},
		{name: "wave spelling", header: "audio/wave", body: nil, wantCT: "audio/wav", wantOK: true},
		{name: "empty after parameter strip", header: ";", body: nil, wantCT: "", wantOK: false},
		{name: "unsupported audio type", header: "audio/ogg", body: nil, wantErr: ErrUnsupportedAudio},
		{name: "not audio", header: "text/html", body: []byte("<html>"), wantCT: "text/html", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct, ok, err := audioType(tt.header, tt.body)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want errors.Is(.., %v)", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("audioType = %v", err)
			}
			if ct != tt.wantCT || ok != tt.wantOK {
				t.Errorf("audioType(%q) = (%q, %v), want (%q, %v)", tt.header, ct, ok, tt.wantCT, tt.wantOK)
			}
		})
	}
}

// TestFetchAudio_UnusableURLIsLoud pins the build-request branch: a
// raw URL that cannot become an http request fails the fetch. The
// decode gate already refuses such URLs, so this branch is reached
// only by calling fetchAudio directly — exactly the defensive seam a
// fault-injection test wants.
func TestFetchAudio_UnusableURLIsLoud(t *testing.T) {
	sp := defaultSpeaker(t)
	// A space in the host fails url.Parse inside the request builder;
	// the decode gate already refuses such URLs, so this branch is
	// reached only by calling fetchAudio directly — exactly the
	// defensive seam a fault-injection test wants.
	if _, _, err := sp.fetchAudio(t.Context(), "http://exa mple.com/x"); err == nil {
		t.Fatal("err = nil, want an unusable URL to fail the fetch")
	}
}

// TestDecodeAudioURL_LongBodyExcerptTruncates pins that an error
// message carries a bounded excerpt, never a megabyte-long paste.
func TestDecodeAudioURL_LongBodyExcerptTruncates(t *testing.T) {
	long := strings.Repeat("x", 500)
	_, err := decodeAudioURL([]byte(long))
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
	// The excerpt is capped at 200 bytes plus an ellipsis; a message
	// that carried the whole body would be ~600 bytes long.
	if len(err.Error()) > 300 {
		t.Errorf("error is %d bytes — the excerpt is not bounded", len(err.Error()))
	}
}

// timeoutCh returns a channel that fires after a generous test bound,
// so a hung fetch fails the test instead of hanging it.
func timeoutCh(t *testing.T) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	go func() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		<-timer.C
		close(ch)
	}()
	return ch
}
