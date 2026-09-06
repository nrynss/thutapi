package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// musicCall is one recorded SynthesizeMusic invocation.
type musicCall struct {
	lyrics, prompt, model string
}

// fakeMusic is a scripted music client: it records every call and
// answers with a success envelope whose media_urls[0].url points at
// audioBase/clip/bed (an audio/mpeg byte server). It respects ctx
// cancellation like the real media client. Tests assert on the
// recorded args — the seam level at which this package's own
// contribution (the gibberish-vocalise default lyrics and the
// wordless prompt) is observable, since the wire payload itself is
// built inside internal/gmi/media.
type fakeMusic struct {
	// audioBase is the audio server prefix envelopes point at. Empty
	// with no err means a call is a test bug, reported loudly.
	audioBase string
	// err, when non-nil, is returned by every call (after recording).
	err error
	// raw, when non-nil, is returned verbatim instead of an envelope.
	raw []byte
	// durationMS is the duration_ms the envelope declares (default 3000).
	durationMS int64

	mu    sync.Mutex
	calls []musicCall
}

func (f *fakeMusic) SynthesizeMusic(ctx context.Context, lyrics, prompt, model string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, musicCall{lyrics: lyrics, prompt: prompt, model: model})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.raw != nil {
		return f.raw, nil
	}
	if f.audioBase == "" {
		return nil, fmt.Errorf("fakeMusic: no audio base configured and no raw body set")
	}
	dur := f.durationMS
	if dur == 0 {
		dur = 3000
	}
	return musicEnvelope(f.audioBase+"/clip/bed", dur), nil
}

func (f *fakeMusic) recorded() []musicCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]musicCall(nil), f.calls...)
}

// musicEnvelope is a terminal request-queue music record in the
// observed shape of t12-music-record.json: the bed at
// outcome.media_urls[0].url (beside outcome.audio_url) with its
// length at outcome.duration_ms.
func musicEnvelope(bedURL string, durationMS int64) []byte {
	return []byte(`{"request_id":"req-music","model":"minimax-music-3.0","status":"success",` +
		`"payload":{"lyrics":"echoed lyrics","prompt":"echoed prompt","format":"mp3"},` +
		`"outcome":{"media_urls":[{"id":"0","url":"` + bedURL + `"}],"audio_url":"` + bedURL +
		`","duration_ms":` + fmt.Sprint(durationMS) + `,"format":"mp3","status":"success"}}`)
}

// bedBytes is what the bed server serves: distinct, non-empty bytes.
var bedBytes = []byte("bed-bytes-make-a-bed")

// newBedServer serves bedBytes as audio/mpeg at /clip/bed.
func newBedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(bedBytes)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGenerateMusicBed_DefaultsReachTheSeam pins the settled default
// shape through its default path: a MusicConfig that changes nothing
// must send DefaultMusicLyrics (the gibberish vocalise — real words
// would be sung) and DefaultMusicPrompt (the wordless directive) to
// minimax-music-3.0, and return the downloaded bytes with the
// envelope's duration. This is the operator-ear-verified wire shape of
// 2026-09-06 (t12-round1.md): changing either default is a wire change
// that needs a live re-verification.
func TestGenerateMusicBed_DefaultsReachTheSeam(t *testing.T) {
	srv := newBedServer(t)

	music := &fakeMusic{audioBase: srv.URL, durationMS: 56581}
	bed, err := GenerateMusicBed(context.Background(), MusicConfig{Music: music})
	if err != nil {
		t.Fatalf("GenerateMusicBed: %v", err)
	}

	calls := music.recorded()
	if len(calls) != 1 {
		t.Fatalf("music calls = %d, want 1", len(calls))
	}
	if calls[0].lyrics != DefaultMusicLyrics {
		t.Errorf("lyrics = %q, want the gibberish-vocalise DefaultMusicLyrics", calls[0].lyrics)
	}
	if calls[0].prompt != DefaultMusicPrompt {
		t.Errorf("prompt = %q, want the wordless-instrumental DefaultMusicPrompt", calls[0].prompt)
	}
	if calls[0].model != DefaultMusicModel {
		t.Errorf("model = %q, want %q", calls[0].model, DefaultMusicModel)
	}
	if !bytes.Equal(bed.Audio, bedBytes) {
		t.Errorf("bed bytes = %q, want the downloaded bed", bed.Audio)
	}
	if bed.ContentType != "audio/mpeg" {
		t.Errorf("content type = %q, want audio/mpeg", bed.ContentType)
	}
	if bed.Duration != 56581*time.Millisecond {
		t.Errorf("duration = %v, want the envelope's 56581ms", bed.Duration)
	}
}

// TestGenerateMusicBed_ExplicitOverrides pins that a caller-provided
// lyrics/prompt/model pass through verbatim instead of the defaults.
func TestGenerateMusicBed_ExplicitOverrides(t *testing.T) {
	srv := newBedServer(t)

	music := &fakeMusic{audioBase: srv.URL}
	_, err := GenerateMusicBed(context.Background(), MusicConfig{
		Music:  music,
		Lyrics: "custom vocalise",
		Prompt: "custom style",
		Model:  "another-model",
	})
	if err != nil {
		t.Fatalf("GenerateMusicBed: %v", err)
	}
	calls := music.recorded()
	if calls[0].lyrics != "custom vocalise" || calls[0].prompt != "custom style" || calls[0].model != "another-model" {
		t.Errorf("recorded call = %+v, want the explicit lyrics/prompt/model", calls[0])
	}
}

// TestGenerateMusicBed_NoMusicClient pins ErrNoMusic: nothing can be
// generated without a music client, and the refusal happens before any
// call.
func TestGenerateMusicBed_NoMusicClient(t *testing.T) {
	if _, err := GenerateMusicBed(context.Background(), MusicConfig{}); !errors.Is(err, ErrNoMusic) {
		t.Fatalf("err = %v, want ErrNoMusic", err)
	}
}

// TestGenerateMusicBed_SeamErrorPropagates pins that a music client
// failure reaches the caller wrapped, never swallowed.
func TestGenerateMusicBed_SeamErrorPropagates(t *testing.T) {
	music := &fakeMusic{err: errors.New("media: upstream exploded")}
	_, err := GenerateMusicBed(context.Background(), MusicConfig{Music: music})
	if err == nil || !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("err = %v, want the seam error wrapped", err)
	}
}

// TestDecodeBedURL_KeysOnOutcome pins the decode against the observed
// wire shape (t12-music-record.json): the bed comes from the OUTCOME
// subtree's media_urls[0].url and its length from duration_ms — never
// from the request echo in payload, whose text could be made to look
// like anything (the T6 round-1 H1 hazard, keyed by name).
func TestDecodeBedURL_KeysOnOutcome(t *testing.T) {
	decoy := `{"request_id":"req-music","status":"success",` +
		`"payload":{"lyrics":"https://storage.googleapis.com/echoed-not-real/x.mp3","prompt":"x","format":"mp3"},` +
		`"outcome":{"media_urls":[{"id":"0","url":"https://storage.googleapis.com/real-bed/y.mp3"}],` +
		`"audio_url":"https://storage.googleapis.com/real-bed/y.mp3","duration_ms":56581}}`
	url, dur, err := decodeBedURL([]byte(decoy))
	if err != nil {
		t.Fatalf("decodeBedURL(decoy) = %v", err)
	}
	if url != "https://storage.googleapis.com/real-bed/y.mp3" {
		t.Errorf("url = %q, want the outcome's media_urls[0].url, never the payload echo", url)
	}
	if dur != 56581*time.Millisecond {
		t.Errorf("duration = %v, want 56581ms", dur)
	}

	// The same observed record carried outcome.audio_url beside
	// media_urls; audio_url alone is accepted when media_urls is absent.
	audioOnly := `{"outcome":{"audio_url":"https://storage.googleapis.com/b/z.mp3","duration_ms":2000}}`
	url, dur, err = decodeBedURL([]byte(audioOnly))
	if err != nil {
		t.Fatalf("decodeBedURL(audio_url only) = %v", err)
	}
	if url != "https://storage.googleapis.com/b/z.mp3" || dur != 2*time.Second {
		t.Errorf("url/duration = %q/%v, want the audio_url fallback", url, dur)
	}
}

// TestDecodeBedURL_FailureBranches pins every no-bed shape with its
// sentinel: an empty body, a body that is not an envelope, an outcome
// with no URL, a non-http URL and a missing duration.
func TestDecodeBedURL_FailureBranches(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty body", ""},
		{"raw bytes, not an envelope", "not-json-at-all"},
		{"envelope with no outcome", `{"request_id":"r1","status":"success"}`},
		{"outcome with neither url spelling nor duration", `{"outcome":{"format":"mp3"}}`},
		{"non-http url", `{"outcome":{"media_urls":[{"id":"0","url":"gs://bucket/b.mp3"}],"duration_ms":3000}}`},
		{"missing duration", `{"outcome":{"media_urls":[{"id":"0","url":"https://x/y.mp3"}]}}`},
		{"zero duration", `{"outcome":{"media_urls":[{"id":"0","url":"https://x/y.mp3"}],"duration_ms":0}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeBedURL([]byte(tt.raw))
			if !errors.Is(err, ErrNoBed) {
				t.Fatalf("err = %v, want errors.Is(.., ErrNoBed)", err)
			}
		})
	}
}

// TestGenerateMusicBed_DownloadErrors pins the download-on-receipt
// error branches: a bed server that answers non-200, serves a
// non-audio content type, or overflows MaxAudioBytes must fail loudly
// rather than hand back unusable bytes.
func TestGenerateMusicBed_DownloadErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		music := &fakeMusic{raw: musicEnvelope(srv.URL+"/bed.mp3", 3000)}
		if _, err := GenerateMusicBed(context.Background(), MusicConfig{Music: music}); !errors.Is(err, ErrNoBed) {
			t.Fatalf("err = %v, want ErrNoBed on a 404 bed fetch", err)
		}
	})

	t.Run("non-audio content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("not audio"))
		}))
		defer srv.Close()
		music := &fakeMusic{raw: musicEnvelope(srv.URL+"/bed.mp3", 3000)}
		if _, err := GenerateMusicBed(context.Background(), MusicConfig{Music: music}); !errors.Is(err, ErrNoBed) {
			t.Fatalf("err = %v, want ErrNoBed for a non-audio bed", err)
		}
	})

	t.Run("oversized bed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(make([]byte, MaxAudioBytes+1))
		}))
		defer srv.Close()
		music := &fakeMusic{raw: musicEnvelope(srv.URL+"/bed.mp3", 3000)}
		if _, err := GenerateMusicBed(context.Background(), MusicConfig{Music: music}); !errors.Is(err, ErrUnsupportedAudio) {
			t.Fatalf("err = %v, want ErrUnsupportedAudio for an oversized bed", err)
		}
	})
}

// fakeMixRunner records the mix command and can fail it.
type fakeMixRunner struct {
	mu    sync.Mutex
	calls [][]string
	err   error
}

func (f *fakeMixRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	return nil, f.err
}

func (f *fakeMixRunner) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

// TestMixBed_CommandPinned_FadeEndsAtFilmTotal pins the mix command
// byte-for-byte for a KNOWN film total: the afade start is total-fade
// and its duration is fade, so the bed reaches silence EXACTLY at the
// film's end — and the same command serves a longer-bed and a
// shorter-bed film, because the fit is anchored to the film's total,
// never the bed's length (operator directive 2026-09-06). The verified
// filter pins ride along: normalize=0, duration=first, volume on the
// bed chain only, -stream_loop -1, -c:v copy.
func TestMixBed_CommandPinned_FadeEndsAtFilmTotal(t *testing.T) {
	total := 56723*time.Millisecond + 220*time.Microsecond // a real concat's odd tail
	runner := &fakeMixRunner{}
	err := MixBed(context.Background(), MixConfig{Runner: runner}, MixInput{
		FilmPath:     "/film/book.mp4",
		BedPath:      "/bed/music.mp3",
		FilmDuration: total,
		OutputPath:   "/out/mixed.mp4",
	})
	if err != nil {
		t.Fatalf("MixBed: %v", err)
	}
	got := runner.last()
	if got == nil {
		t.Fatal("no mix command recorded")
	}
	// The command pin is exact; the arithmetic the operator cares about
	// is asserted after it: st + d == the film total.
	const wantFilter = "[1:a]volume=0.15,afade=t=out:st=54.723:d=2.000[bed];[0:a][bed]amix=inputs=2:duration=first:normalize=0[a]"
	if got[8] != wantFilter {
		t.Errorf("filter = %q, want %q", got[8], wantFilter)
	}
	const mark = "afade=t=out:st="
	i := strings.Index(wantFilter, mark)
	rest := wantFilter[i+len(mark):]
	startStr, durStr, _ := strings.Cut(rest, ":d=")
	var fadeStart, fadeDur float64
	if _, err := fmt.Sscanf(startStr, "%f", &fadeStart); err != nil {
		t.Fatalf("parse fade start %q: %v", startStr, err)
	}
	if _, err := fmt.Sscanf(durStr, "%f", &fadeDur); err != nil {
		t.Fatalf("parse fade duration %q: %v", durStr, err)
	}
	if end := fadeStart + fadeDur; end > total.Seconds()+0.001 || end < total.Seconds()-0.001 {
		t.Errorf("fade end = %.3fs, want the film total %.3fs — the bed must reach silence exactly at film end", end, total.Seconds())
	}

	want := []string{
		"ffmpeg",
		"-i", "/film/book.mp4",
		"-stream_loop", "-1",
		"-i", "/bed/music.mp3",
		"-filter_complex", wantFilter,
		"-map", "0:v",
		"-map", "[a]",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "+faststart",
		"-y",
		"/out/mixed.mp4",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("command = %q, want %q", got, want)
	}
}

// TestMixBed_CustomGainAndFade pins the configurable fade and gain
// through their override path: a caller that sets Fade and Gain sees
// them on the wire, and the fade still ends at the total.
func TestMixBed_CustomGainAndFade(t *testing.T) {
	runner := &fakeMixRunner{}
	err := MixBed(context.Background(), MixConfig{Runner: runner, Gain: 0.2, Fade: 3 * time.Second}, MixInput{
		FilmPath:     "book.mp4",
		BedPath:      "bed.mp3",
		FilmDuration: 10 * time.Second,
		OutputPath:   "out.mp4",
	})
	if err != nil {
		t.Fatalf("MixBed: %v", err)
	}
	wantFilter := "[1:a]volume=0.2,afade=t=out:st=7.000:d=3.000[bed];[0:a][bed]amix=inputs=2:duration=first:normalize=0[a]"
	if got := runner.last()[8]; got != wantFilter {
		t.Errorf("filter = %q, want %q", got, wantFilter)
	}
}

// TestMixBed_ValidationErrors pins every invalid input with
// ErrInvalidMix: empty paths, a non-positive total and a fade that
// cannot fit inside the film.
func TestMixBed_ValidationErrors(t *testing.T) {
	valid := MixInput{FilmPath: "f.mp4", BedPath: "b.mp3", FilmDuration: 10 * time.Second, OutputPath: "o.mp4"}
	runner := &fakeMixRunner{}
	cfg := MixConfig{Runner: runner}
	cases := []struct {
		name string
		mut  func(*MixInput)
	}{
		{"empty film path", func(m *MixInput) { m.FilmPath = "" }},
		{"empty bed path", func(m *MixInput) { m.BedPath = "" }},
		{"empty output path", func(m *MixInput) { m.OutputPath = "" }},
		{"zero film duration", func(m *MixInput) { m.FilmDuration = 0 }},
		{"negative film duration", func(m *MixInput) { m.FilmDuration = -time.Second }},
		{"fade as long as the film", func(m *MixInput) { m.FilmDuration = 2 * time.Second }}, // default fade 2s >= 2s
		{"fade longer than the film", func(m *MixInput) { m.FilmDuration = 1 * time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.mut(&in)
			if err := MixBed(context.Background(), cfg, in); !errors.Is(err, ErrInvalidMix) {
				t.Fatalf("err = %v, want ErrInvalidMix", err)
			}
		})
	}
	if len(runner.calls) != 0 {
		t.Errorf("runner calls = %d, want 0 for validation failures", len(runner.calls))
	}
}

// TestMixBed_RunnerErrorPinsErrMixFailed pins that an ffmpeg failure
// surfaces as ErrMixFailed (wrapped, so errors.Is still reaches the
// runner's own error).
func TestMixBed_RunnerErrorPinsErrMixFailed(t *testing.T) {
	boom := errors.New("ffmpeg exit 1")
	runner := &fakeMixRunner{err: boom}
	err := MixBed(context.Background(), MixConfig{Runner: runner}, MixInput{
		FilmPath:     "f.mp4",
		BedPath:      "b.mp3",
		FilmDuration: 10 * time.Second,
		OutputPath:   "o.mp4",
	})
	if !errors.Is(err, ErrMixFailed) {
		t.Fatalf("err = %v, want ErrMixFailed", err)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the runner error in the chain", err)
	}
}

// TestMixBed_RealFFmpeg_WindDown pins the mix's behaviour against real
// ffmpeg (skipped when absent): with a KNOWN total, a shorter bed is
// looped and a longer bed is trimmed, and in both cases the bed runs
// at full gain through the body and reaches near-silence exactly at
// the total — the wind-down the operator requires. The output duration
// is the film's (video copied).
func TestMixBed_RealFFmpeg_WindDown(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
	dir := t.TempDir()
	film := filepath.Join(dir, "film.mp4")
	// 6 s of silent film: colour video + anullsrc audio.
	mustRun(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=320x240:r=25:d=6",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo:d=6",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", film)
	shortBed := filepath.Join(dir, "bed-short.mp3")
	longBed := filepath.Join(dir, "bed-long.mp3")
	mustRun(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=220:duration=2", "-c:a", "libmp3lame", shortBed)
	mustRun(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "sine=frequency=220:duration=12", "-c:a", "libmp3lame", longBed)

	const total = 6 * time.Second
	for _, tc := range []struct {
		name string
		bed  string
	}{
		{"shorter bed loops to cover the total", shortBed},
		{"longer bed trimmed at the total", longBed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, "out-"+strings.ReplaceAll(tc.name, " ", "-")+".mp4")
			err := MixBed(context.Background(), MixConfig{}, MixInput{
				FilmPath:     film,
				BedPath:      tc.bed,
				FilmDuration: total,
				OutputPath:   out,
			})
			if err != nil {
				t.Fatalf("MixBed: %v", err)
			}
			dur := probeSeconds(t, out)
			if dur < 5.9 || dur > 6.1 {
				t.Errorf("mixed duration = %.3fs, want the film's 6.0s (video copied, audio re-encoded)", dur)
			}
			// The wind-down, measured on the bed alone (the film is
			// silent): full bed level through the body, a fade over the
			// last two seconds (4-6s), near-silence at the end. A fade
			// anchored anywhere but the known total fails one of these:
			// ending early silences the tail from mid-film on; ending
			// late (or never) leaves the tail at full bed level.
			full := peakWindow(t, out, 1.0, 3.9)
			if full < 100 {
				t.Fatalf("bed level through the body = %d, want a full-gain bed under the film", full)
			}
			if body := peakWindow(t, out, 4.1, 4.9); body < full/3 {
				t.Errorf("bed level just after the fade starts (4.1-4.9s) = %d, want > %d: the fade must run over the LAST two seconds", body, full/3)
			}
			if tail := peakWindow(t, out, 5.7, 6.0); tail > full/6 {
				t.Errorf("bed level at the film's end (5.7-6.0s) = %d, want <= %d: the fade must reach silence at the total", tail, full/6)
			}
		})
	}
}

// mustRun runs a command, failing the test on error.
func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
}

// probeSeconds returns the container duration of a media file via
// ffprobe.
func probeSeconds(t *testing.T, path string) float64 {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found in PATH")
	}
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var d float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d); err != nil {
		t.Fatalf("parse ffprobe duration %q: %v", out, err)
	}
	return d
}

// peakWindow decodes a media file's audio to mono 8 kHz s16 and
// returns the peak absolute sample value in the half-open window
// [from, to) seconds.
func peakWindow(t *testing.T, path string, from, to float64) int {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", path, "-ac", "1", "-ar", "8000", "-f", "s16le", "-")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	lo, hi := int(from*8000)*2, int(to*8000)*2
	if lo < 0 {
		lo = 0
	}
	peak := 0
	for i := lo; i+1 < len(out) && i < hi; i += 2 {
		s := int(int16(binary.LittleEndian.Uint16(out[i : i+2])))
		if s < 0 {
			s = -s
		}
		if s > peak {
			peak = s
		}
	}
	return peak
}

// infoFixture returns bytes mirroring the measurable region of GMI's
// real narration clips (data/live/cache/audio/page-01.mp3): an ID3v2
// tag, then a mono MPEG-1 Layer III first frame whose payload carries a
// REAL "Info" header (flags 0x0f: the frames, bytes, TOC and quality
// fields all present — the shape the real clips declare) followed —
// when wantTag is true — by a "Lavc" encoder tag whose 24-bit gapless
// field at tag+21 packs delay 576 in the top 12 bits and padding 1584
// in the bottom 12 (the word 0x240630, byte-identical to page-01's).
// declaredFrames filler frames follow so the stream is as parseable as
// the real clip.
//
// The declared count deliberately EXCEEDS the playable decode exactly as
// the Lavc-encoded GMI clips do (T12 round-1 H1): 155 declared frames
// would be 155×1152/44100 = 4.04898 s, but ffmpeg — the consumer every
// narrated film goes through — plays declared frames minus the 2160 pad
// samples, 176400/44100 = 4.000000 s, which is what ffprobe reports for
// page-01 and what the render decodes. measureDuration must return the
// PLAYABLE length. This fixture is also the L1 pin: it exercises the
// Info/Xing branch the committed CBR-only makeMP3 fixtures never reach.
func infoFixture(declaredFrames uint32, wantTag bool) []byte {
	frameLen := 417 // MPEG-1 L3 128 kbps mono 44.1 kHz (mp3FixtureFrameLen)
	total := 10 + 4 + 17 + frameLen + int(declaredFrames-1)*frameLen
	buf := make([]byte, 0, total)
	buf = append(buf, 'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0) // ID3v2.4, empty body
	buf = append(buf, 0xFF, 0xFB, 0x90, 0xC0)             // frame 1 header: MPEG-1 L3 128k 44.1k mono
	buf = append(buf, make([]byte, 17)...)                // mono side info
	buf = append(buf, 'I', 'n', 'f', 'o')
	buf = append(buf, 0, 0, 0, 0x0f) // flags: frames + bytes + TOC + quality
	buf = append(buf, byte(declaredFrames>>24), byte(declaredFrames>>16), byte(declaredFrames>>8), byte(declaredFrames))
	buf = append(buf, 0, 0, 0, 0)           // bytes field (not read by the measurement)
	buf = append(buf, make([]byte, 100)...) // TOC
	buf = append(buf, 0, 0, 0, 0)           // quality
	if wantTag {
		tag := make([]byte, 36)
		copy(tag, "Lavc63.1.101")
		tag[21], tag[22], tag[23] = 0x24, 0x06, 0x30 // delay 576 << 12 | padding 1584
		buf = append(buf, tag...)
	}
	// Bring frame 1 up to its full length, then declaredFrames-1 filler
	// frames so the stream is structurally complete.
	for len(buf) < 10+frameLen {
		buf = append(buf, 0)
	}
	for i := uint32(1); i < declaredFrames; i++ {
		buf = append(buf, 0xFF, 0xFB, 0x90, 0xC0)
		buf = append(buf, make([]byte, frameLen-4)...)
	}
	return buf
}

// TestMeasureDuration_InfoGaplessTagTrimsToPlayableLength pins T12
// round-1 H1 against a fixture that mirrors GMI's real page-01.mp3: the
// Info header declares 155 frames but the Lavc gapless tag's 576+1584
// pad samples make the PLAYABLE length exactly 4.000000 s (what ffprobe
// reports and the film render decodes). The declared product alone —
// the pre-fix answer — would be 4.04898 s, ~49 ms over, which mis-anchors
// the narrated tier's end fade by ~2 frames per clip.
func TestMeasureDuration_InfoGaplessTagTrimsToPlayableLength(t *testing.T) {
	b := infoFixture(155, true)
	got, err := measureDuration(b)
	if err != nil {
		t.Fatalf("measureDuration(info fixture): %v", err)
	}
	if want := 4 * time.Second; got != want {
		t.Errorf("measureDuration = %v, want %v: 155 declared frames minus 2160 gapless pad samples is the playable 176400/44100 s — the untrimmed declared product 4.04898 s is the H1 overstatement", got, want)
	}
	// Guard the fixture itself: the declared product must actually differ
	// from the playable length, or this test could never catch a revert
	// to the untrimmed calculation.
	if untrimmed := mp3DurationFrom(155, 1152, 44100); untrimmed == 4*time.Second {
		t.Fatal("fixture broken: 155 declared frames must exceed the 4.000000 s playable length")
	}
}

// TestMeasureDuration_InfoWithoutEncoderTagMeasuresDeclaredFrames pins
// that a Xing/Info header with NO LAME/Lavf/Lavc tag after it keeps the
// untrimmed declared-count answer: without the gapless field there is
// nothing to subtract, and ffmpeg plays the full declared count.
func TestMeasureDuration_InfoWithoutEncoderTagMeasuresDeclaredFrames(t *testing.T) {
	b := infoFixture(155, false)
	got, err := measureDuration(b)
	if err != nil {
		t.Fatalf("measureDuration(bare Info fixture): %v", err)
	}
	if want := mp3DurationFrom(155, 1152, 44100); got != want {
		t.Errorf("measureDuration = %v, want %v: a bare Info header declares 155 full frames", got, want)
	}
}

// TestMeasureDuration_GaplessPadsExceedingFramesRejected pins the error
// branch of the pad subtraction: a gapless trim larger than the declared
// frames is corrupt and must fail loudly rather than hand back a
// negative or zero length.
func TestMeasureDuration_GaplessPadsExceedingFramesRejected(t *testing.T) {
	b := infoFixture(1, true) // 1×1152 samples minus 2160 pad samples
	if _, err := measureDuration(b); !errors.Is(err, ErrClipDuration) {
		t.Fatalf("err = %v, want errors.Is(.., ErrClipDuration) when the gapless pads exceed the declared frames", err)
	}
}
