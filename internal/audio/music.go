package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The music bed (PLAN.md §T12): GenerateMusicBed asks minimax-music-3.0
// for a wordless instrumental bed and returns its bytes (downloaded on
// receipt — the URL in the outcome expires, invariant 7) plus the
// length the model made it (outcome.duration_ms — the model has no
// duration parameter and picks its own length, so the caller must know
// what came back). MixBed then fits that bed under a FINISHED film in
// one verified ffmpeg final pass and winds it down at the film's end.
//
// The bed-fit is the whole point of the mix, and it is anchored to a
// duration the CALLER knows, never measured from the finished file:
// MixBed takes FilmDuration — the film's total, computed by the render
// phase's own arithmetic — and the end fade is positioned so it
// reaches silence exactly at that total. A bed longer than the film is
// trimmed to the total and a shorter one is looped to cover it;
// -stream_loop -1 with amix=duration=first makes both cases one
// command, and the afade is anchored to the known total in both
// (operator directive 2026-09-06; the operator-verified 1.1 s final
// pass of PLAN.md §T12).
//
// The settled wire shape this file encodes was operator-ear-verified
// 2026-09-06 and recorded in dev-diary/adversarial-review/t12-round1.md:
// minimax-music-3.0 REQUIRES lyrics and WILL sing real words — an
// instrumental-directing prompt over real words came back sung. The
// usable wordless bed is made by sending GIBBERISH-VOCALISE lyrics (no
// language at all) with an explicit wordless-instrumental prompt, which
// is exactly the default this file pins. That shape is load-bearing and
// must not be "improved" without a live re-verification:
// DefaultMusicLyrics and DefaultMusicPrompt are the operator-verified
// combination.
//
// This file also carries the mix's OTHER Go-known duration:
// measureDuration, a pure-Go MP3/WAV length reader used at narration
// time so a narrated film's total is computable in Go. The runtime
// image ships ffmpeg but NOT ffprobe (Dockerfile copies only /ffmpeg),
// and the GMI TTS outcome carries no duration field
// (t2b-t5b-live-record.md's terminal TTS envelope is outcome
// {audio_url, format, status} — no duration_ms, unlike the music
// outcome), so a narration clip's length is knowable only by reading
// the clip's own bytes: measured as they download, in Go, ffprobe-free
// (operator directive 2026-09-06).
//
// The music call goes through internal/gmi/media (invariant 1) — this
// file consumes it through the one-method Music seam below, satisfied by
// *media.Client (contract row C1 of t12-round1.md, pinned at compile
// time in wire_test.go). Nothing here routes around internal/gmi.
// Configuration arrives through MusicConfig and MixConfig; this package
// reads no environment (invariant 2).

// DefaultMusicModel is the model GenerateMusicBed calls unless
// MusicConfig.Model names another: minimax-music-3.0, the request-queue
// music model (PLAN.md §T12). Config flows the model id explicitly, as
// for narration; the media client's own default mirrors it.
const DefaultMusicModel = "minimax-music-3.0"

// DefaultMusicFormat is the audio format the bed is requested as:
// mp3. The upstream also accepts wav and pcm; nothing here needs them.
const DefaultMusicFormat = "mp3"

// DefaultMusicLyrics is the lyrics GenerateMusicBed sends when
// MusicConfig.Lyrics is empty: lines of pure gibberish vocalise — no
// real words, no language. minimax-music-3.0 REQUIRES a lyrics field
// and sings whatever words it is given; real words under a narrated
// book would be a second voice competing with the narrator, and an
// instrumental-directing prompt alone does NOT stop the model singing
// (operator-ear-verified 2026-09-06, t12-round1.md). These lines are
// the settled usable shape: the model vocalises, it never forms words.
const DefaultMusicLyrics = "La la la la / Doo doo doo / Mm mm mm mm\n" +
	"La la la la la / Mm mm mm / Doo doo doo\n" +
	"Mm mm mm mm / La la la / Doo doo doo doo"

// DefaultMusicPrompt is the style prompt GenerateMusicBed sends when
// MusicConfig.Prompt is empty: an explicit wordless-vocalise
// instrumental directive. This text plus DefaultMusicLyrics is the
// operator-ear-verified combination that yields a usable wordless bed
// (2026-09-06, t12-round1.md); changing either half is a wire change
// that needs a live re-verification, not a wording edit.
const DefaultMusicPrompt = "wordless vocalise, NO real words, no language, instrumental with soft humming, gentle ambient music bed"

// DefaultBedGain is the bed's volume in the mix when MixConfig.Gain is
// unset: 0.15, low enough that the narration or the captioned silence
// stays the foreground (PLAN.md §T12's verified mix; the plan's
// operating range is 0.15-0.25).
const DefaultBedGain = 0.15

// DefaultBedFade is how long the end fade-out runs when MixConfig.Fade
// is unset: 2 seconds. The mix positions the fade so it reaches
// silence exactly at the film's end (it starts DefaultBedFade before
// the known total) — the book closes with the bed, never on a cut or a
// bed left playing (operator directive 2026-09-06; ~2 s wind-down).
const DefaultBedFade = 2 * time.Second

// DefaultMixBitrate is the mix output's audio bitrate: 128k, the same
// aac bitrate every film segment encodes at (PLAN.md §T12).
const DefaultMixBitrate = "128k"

// Sentinel errors this file declares. Every one is matched with
// errors.Is; none is produced by matching a provider's message
// (PLAN.md invariant 8). internal/gmi's sentinels pass through
// untouched. ErrUnsupportedAudio (audio.go) is reused for a downloaded
// bed served in a type nothing downstream can mix.
var (
	// ErrNoMusic reports a MusicConfig with a nil Music. No bed can
	// be generated without a music client.
	ErrNoMusic = errors.New("audio: no music client configured")

	// ErrNoBed reports a music response that carried no usable bed:
	// not the request-queue envelope, an envelope whose outcome has
	// neither media_urls nor audio_url, a URL that is not absolute
	// http(s), no duration_ms, or a download that did not answer 200.
	// The media client returns every response raw and the terminal
	// music record always carries the bed plus its length
	// (t12-round1.md), so a body without them is a shape this
	// package does not recognise — guessing could only pick the
	// request's own echo.
	ErrNoBed = errors.New("audio: music response carried no bed")

	// ErrInvalidMix reports a MixBed call with input that cannot be
	// mixed: an empty film, bed or output path, a non-positive film
	// duration, or an end fade at least as long as the film itself
	// (the fade must fit inside the known total so it can reach
	// silence at the film's end).
	ErrInvalidMix = errors.New("audio: invalid mix input")

	// ErrMixFailed reports that the mix's ffmpeg pass failed: the
	// runner's error is wrapped, so errors.Is reaches context
	// cancellation and the underlying exec error.
	ErrMixFailed = errors.New("audio: mix failed")

	// ErrClipDuration reports a narration clip whose length could not
	// be read out of its own bytes: not a WAV or MP3 structure this
	// reader recognises (a truncated header, no MPEG frame, an
	// unsupported version or layer). It is raised by NarrateBook at
	// download time (contract row C2 of t12-round1.md): a clip whose
	// Go-known duration is unknown cannot anchor the film total the
	// mix's wind-down needs, so the run stops before the PDF stage
	// spends anything else.
	ErrClipDuration = errors.New("audio: could not measure the clip duration")
)

// Music is audio's view of the GMI request-queue music client (PLAN.md
// invariant 3: the consumer declares the interface, as narrow as it
// uses). Tests substitute a fake, so nothing below needs HTTP to be
// exercised. internal/gmi/media's SynthesizeMusic declares the same
// (ctx, lyrics, prompt, model) and satisfies this seam — contract row
// C1 of dev-diary/adversarial-review/t12-round1.md, pinned at compile
// time by wire_test.go's var _ Music = (*media.Client)(nil).
type Music interface {
	// SynthesizeMusic runs one minimax-music-3.0 call and returns the
	// terminal request-queue response body raw. The bed is not in the
	// body: the terminal record carries it at outcome.media_urls[0].url
	// (beside outcome.audio_url) with its length at
	// outcome.duration_ms, which this file decodes and downloads on
	// receipt.
	SynthesizeMusic(ctx context.Context, lyrics, prompt, model string) ([]byte, error)
}

// MusicConfig configures the music-bed path. MusicConfig flows down
// from the caller; this package reads no environment (invariant 2).
// The zero value is not usable — Music is required and missing it is
// ErrNoMusic — and every field's empty value means the package default
// (see the field docs), so the default path is exercised by any caller
// that passes only what it changes.
type MusicConfig struct {
	// Music is the music client. Required — ErrNoMusic without one.
	Music Music

	// HTTPClient downloads the bed from the envelope's URL. Nil means
	// a client with a 30s timeout and the package's redirect policy.
	// It never talks to GMI — that stays behind internal/gmi
	// (invariant 1).
	HTTPClient *http.Client

	// Lyrics is the lyrics sent to the model. Empty means
	// DefaultMusicLyrics — the operator-verified gibberish-vocalise
	// shape. Real words are refused by design: the model sings them.
	Lyrics string

	// Prompt is the style prompt sent to the model. Empty means
	// DefaultMusicPrompt — the wordless-vocalise directive. Together
	// with the default lyrics this is the settled usable shape.
	Prompt string

	// Model is the music model. Empty means DefaultMusicModel.
	Model string
}

// MixConfig configures the bed-fit final pass. The zero value is
// usable and means the pinned production settings: the "ffmpeg"
// binary, DefaultBedGain, DefaultBedFade and an exec-backed runner.
type MixConfig struct {
	// FFmpegPath is the ffmpeg binary. Empty means "ffmpeg" on PATH
	// (the runtime image ships /usr/local/bin/ffmpeg).
	FFmpegPath string

	// Runner executes the ffmpeg pass. Nil means exec.CommandContext.
	Runner Runner

	// Gain is the bed's volume in the mix. Zero or negative means
	// DefaultBedGain (0.15) — the bed sits under the film's own
	// audio, never over it.
	Gain float64

	// Fade is the end fade-out's length. Zero or negative means
	// DefaultBedFade (2s). The fade is anchored to the known film
	// duration so it reaches silence exactly at the film's end; a
	// fade at least as long as the film is invalid (ErrInvalidMix).
	Fade time.Duration
}

// MixInput names the finished film, the bed and the output of one mix
// pass. FilmDuration is the film's total as the RENDER phase computed
// it — never a value probed from the finished file (operator directive
// 2026-09-06): every fade and fit decision anchors to it.
type MixInput struct {
	// FilmPath is the finished film (the MP4 the mix is a final pass
	// over; its audio track is narration or captioned-silent holds —
	// the mix neither knows nor cares which).
	FilmPath string
	// BedPath is the bed audio file to fit under the film (the bytes
	// GenerateMusicBed returned, written to a file by the caller).
	BedPath string
	// FilmDuration is the film's exact total duration, known before
	// the mix. It must be positive.
	FilmDuration time.Duration
	// OutputPath is where the mixed film is written.
	OutputPath string
}

// Bed is one generated music bed: the downloaded bytes (audio/mpeg in
// practice — see the download gate), their content type, and the
// length the model actually made (outcome.duration_ms). The length is
// information, not a request: minimax-music-3.0 has no duration
// parameter, and the mix does not need the bed's length — the fit is
// anchored to the FILM's known duration.
type Bed struct {
	Audio       []byte
	ContentType string
	Duration    time.Duration
}

// Runner executes one external command. The mix seam is declared by
// this consumer (invariant 3); bookvideo's runner is a different
// package's seam and is not reused here — audio never imports the
// video package.
type Runner interface {
	// Run executes name with args and returns combined output, or an
	// error when the command fails.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// defaultRunner runs the mix's ffmpeg pass with exec.CommandContext
// and wraps any failure in ErrMixFailed.
type defaultRunner struct{}

func (defaultRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrMixFailed, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v (%s)", ErrMixFailed, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// musicMaker is a resolved MusicConfig: every default already
// substituted, so no code below re-reads MusicConfig and no default can
// be applied twice or in two ways.
type musicMaker struct {
	music  Music
	http   *http.Client
	lyrics string
	prompt string
	model  string
}

// resolve substitutes MusicConfig's defaults and rejects a config
// without a music client. It is the single place a default is applied,
// which is why the default-path tests can pin them all at once.
func (cfg MusicConfig) resolve() (*musicMaker, error) {
	if cfg.Music == nil {
		return nil, ErrNoMusic
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultFetchTimeout, CheckRedirect: safeRedirectPolicy}
	}
	lyrics := cfg.Lyrics
	if strings.TrimSpace(lyrics) == "" {
		lyrics = DefaultMusicLyrics
	}
	prompt := cfg.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultMusicPrompt
	}
	model := cfg.Model
	if model == "" {
		model = DefaultMusicModel
	}
	return &musicMaker{music: cfg.Music, http: hc, lyrics: lyrics, prompt: prompt, model: model}, nil
}

// GenerateMusicBed asks the music client for one wordless bed and
// returns its downloaded bytes plus the length the model made. The
// wire shape sent is the operator-verified combination — the lyrics
// default is gibberish vocalise (never real words, which the model
// would sing) and the prompt default is the explicit wordless
// directive (t12-round1.md); an override is an explicit change to a
// settled, ear-verified shape.
//
// The bytes are downloaded on receipt (PLAN.md invariant 7): the
// terminal record names the bed at a public storage URL that is
// assumed to expire, so the caller gets bytes, not a link. The decode
// keys on the outcome subtree alone — outcome.media_urls[0].url (with
// outcome.audio_url accepted when media_urls is absent, both observed
// on the same record) plus outcome.duration_ms — never on "the first
// thing in the body that looks like media", which is how the echoed
// payload produced T6's round-1 H1.
func GenerateMusicBed(ctx context.Context, cfg MusicConfig) (Bed, error) {
	m, err := cfg.resolve()
	if err != nil {
		return Bed{}, err
	}
	raw, err := m.music.SynthesizeMusic(ctx, m.lyrics, m.prompt, m.model)
	if err != nil {
		return Bed{}, fmt.Errorf("audio: generate music bed: %w", err)
	}
	bedURL, dur, err := decodeBedURL(raw)
	if err != nil {
		return Bed{}, fmt.Errorf("audio: generate music bed: %w", err)
	}
	b, ct, err := m.fetchBed(ctx, bedURL)
	if err != nil {
		return Bed{}, fmt.Errorf("audio: generate music bed: %w", err)
	}
	return Bed{Audio: b, ContentType: ct, Duration: dur}, nil
}

// decodeBedURL reads the bed out of a terminal music response: the
// outcome subtree's media_urls[0].url (or audio_url) and duration_ms.
// The decode is keyed on the outcome alone — the payload beside it is
// the request echoed verbatim, and a walker that took "the first thing
// in the body that looks like media" is exactly how T6's round-1 H1
// shipped. A body with no URL is a shape this package does not
// recognise and fails loudly with ErrNoBed (the media client returns
// every response raw, and the terminal music record always carries one
// — t12-music-record.json, t12-round1.md).
//
// duration_ms is best-effort: an absent or non-positive value returns a
// zero duration and no error, because nothing consumes it (see below).
func decodeBedURL(raw []byte) (string, time.Duration, error) {
	if len(raw) == 0 {
		return "", 0, fmt.Errorf("%w: the response body was empty", ErrNoBed)
	}
	var env queueRecord
	if err := json.Unmarshal(raw, &env); err != nil || !hasOutcome(env.Outcome) {
		return "", 0, fmt.Errorf("%w: the response is not a request-queue music envelope: %s", ErrNoBed, excerpt(raw))
	}
	var out outcomeBed
	if err := json.Unmarshal(env.Outcome, &out); err != nil {
		return "", 0, fmt.Errorf("%w: the outcome subtree is not a music record: %s", ErrNoBed, excerpt(env.Outcome))
	}
	u := ""
	if len(out.MediaURLs) > 0 {
		u = out.MediaURLs[0].URL
	}
	if u == "" {
		// The same observed record carried outcome.audio_url beside
		// media_urls (t12-music-record.json); accept it as the
		// alternative spelling when media_urls is absent.
		u = out.AudioURL
	}
	if !audioURL(u) {
		return "", 0, fmt.Errorf("%w: no absolute http(s) bed URL in outcome: %s", ErrNoBed, excerpt(env.Outcome))
	}
	// A missing or non-positive duration_ms is reported as a zero Duration,
	// NOT an error. Bed.Duration has no consumer: the mix loops the bed and
	// anchors its fade to the RENDERER's total (MixBed's FilmDuration), and
	// mixFilm reads only Bed.Audio. Failing here binned a finished book —
	// PDF and film already rendered and paid for — over a field nothing
	// reads.
	if out.DurationMS <= 0 {
		return u, 0, nil
	}
	return u, time.Duration(out.DurationMS) * time.Millisecond, nil
}

// outcomeBed is the outcome subtree's observed shape for a music
// record (t12-music-record.json, operator probe 2026-09-06):
// media_urls — a LIST OF OBJECTS {"id","url"} — beside audio_url, and
// duration_ms, the bed's length in milliseconds (the model has no
// duration parameter, so this is the only place its length is known).
// Only these fields are read.
type outcomeBed struct {
	MediaURLs  []mediaEntry `json:"media_urls"`
	AudioURL   string       `json:"audio_url"`
	DurationMS int64        `json:"duration_ms"`
}

// mediaEntry is one outcome.media_urls entry: an object with an id and
// a URL. The first entry's URL is the bed (one bed per request); it is
// fetched because such links expire (PLAN.md invariant 7).
type mediaEntry struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// fetchBed GETs the bed URL and returns its bytes and content type.
// The URL comes from the authenticated queue's own answer and the read
// is capped at MaxAudioBytes; only http(s) is followed and redirects
// are re-checked per hop by safeRedirectPolicy, exactly as T8's
// fetchAudio. The served Content-Type decides the accepted type via
// audioType — the closed set mediastore can persist (audio/mpeg in
// practice; a bed served otherwise is refused loudly).
func (m *musicMaker) fetchBed(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("audio: fetch bed %s: %w", rawURL, err)
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("audio: fetch bed %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: fetch bed %s: HTTP %d", ErrNoBed, rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxAudioBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("audio: fetch bed %s: %w", rawURL, err)
	}
	if len(b) > MaxAudioBytes {
		return nil, "", fmt.Errorf("%w: fetch bed %s: larger than %d bytes", ErrUnsupportedAudio, rawURL, MaxAudioBytes)
	}
	ct, ok, terr := audioType(resp.Header.Get("Content-Type"), b)
	if terr != nil {
		return nil, "", fmt.Errorf("audio: fetch bed %s: %w", rawURL, terr)
	}
	if !ok {
		return nil, "", fmt.Errorf("%w: fetch bed %s: answered with content type %q, want audio/mpeg or audio/wav", ErrNoBed, rawURL, ct)
	}
	return b, ct, nil
}

// MixBed runs the bed-fit final pass: it mixes the bed under the
// finished film at in.FilmPath and writes the mixed film to
// in.OutputPath. FilmDuration is the film's exact total, known before
// the mix from the render phase's arithmetic — never probed from the
// finished file (operator directive 2026-09-06) — and EVERY fit
// decision anchors to it:
//
//   - the end fade starts FilmDuration-Fade and reaches silence exactly
//     at FilmDuration, so the bed closes WITH the book;
//   - -stream_loop -1 makes a bed shorter than the film loop to cover
//     the total, and amix=duration=first trims a longer one at the
//     total — the fade is positioned by the film's length, never the
//     bed's, in both cases.
//
// This is the operator-verified single final pass (PLAN.md §T12: video
// copied, only audio re-encoded, ~1.1 s wall). The filter pins that
// must never drift: amix normalize=0 (amix halves the narration by
// default the moment a bed appears), amix duration=first (the film's
// audio is the timeline), and the volume + fade applied to the bed
// chain only.
func MixBed(ctx context.Context, cfg MixConfig, in MixInput) error {
	if strings.TrimSpace(in.FilmPath) == "" || strings.TrimSpace(in.BedPath) == "" || strings.TrimSpace(in.OutputPath) == "" {
		return fmt.Errorf("%w: mix needs a film path, a bed path and an output path", ErrInvalidMix)
	}
	if in.FilmDuration <= 0 {
		return fmt.Errorf("%w: film duration %v must be positive — the fade is anchored to the film's known total", ErrInvalidMix, in.FilmDuration)
	}
	gain := cfg.Gain
	if gain <= 0 {
		gain = DefaultBedGain
	}
	fade := cfg.Fade
	if fade <= 0 {
		fade = DefaultBedFade
	}
	if fade >= in.FilmDuration {
		return fmt.Errorf("%w: fade %v must be shorter than the film %v, so it can reach silence at the film's end", ErrInvalidMix, fade, in.FilmDuration)
	}
	fadeStart := in.FilmDuration.Seconds() - fade.Seconds()
	filter := "[1:a]volume=" + strconv.FormatFloat(gain, 'f', -1, 64) +
		",afade=t=out:st=" + strconv.FormatFloat(fadeStart, 'f', 3, 64) +
		":d=" + strconv.FormatFloat(fade.Seconds(), 'f', 3, 64) +
		"[bed];[0:a][bed]amix=inputs=2:duration=first:normalize=0[a]"
	args := []string{
		"-i", in.FilmPath,
		"-stream_loop", "-1",
		"-i", in.BedPath,
		"-filter_complex", filter,
		"-map", "0:v",
		"-map", "[a]",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", DefaultMixBitrate,
		"-movflags", "+faststart",
		"-y",
		in.OutputPath,
	}
	name := cfg.FFmpegPath
	if name == "" {
		name = "ffmpeg"
	}
	runner := cfg.Runner
	if runner == nil {
		runner = defaultRunner{}
	}
	if _, err := runner.Run(ctx, name, args...); err != nil {
		if !errors.Is(err, ErrMixFailed) {
			return fmt.Errorf("%w: %w", ErrMixFailed, err)
		}
		return err
	}
	return nil
}

// The narrated term of the film's known total (operator directive
// 2026-09-06): measureDuration reads a narration clip's length out of
// its own bytes, in pure Go, because nothing else in the pipeline can
// say how long a clip is — the runtime image has no ffprobe and the
// TTS outcome carries no duration (see the package doc). NarrateBook
// measures each clip as it downloads and carries the length forward on
// Clip.Duration (audio.go, contract row C2 of t12-round1.md); the film
// render's total then sums title hold + clip durations + silent holds +
// end hold, all Go-known, and the mix's end fade anchors to it.
//
// The reader handles exactly the two content types fetchAudio admits:
// a WAV (RIFF/WAVE, measured from its fmt/data chunks — exact) and an
// MP3. MP3 length is read from the frame structure, never by decoding
// audio: the ID3v2 tag is skipped by its declared syncsafe size (and a
// trailing ID3v1 tag stripped), the first MPEG audio frame is located,
// and then — when the stream carries a Xing/Info header whose declared
// frame count is followed by a LAME/Lavf/Lavc encoder tag, the count
// times samples-per-frame MINUS the tag's encoder-delay and tail-padding
// samples is the length; a Xing/Info header with no such tag measures
// its declared count untrimmed; otherwise the stream is treated as CBR
// and the byte count after the tags over the per-frame size gives the
// frame count. The trim is the playable-length correction: GMI's clips
// are Lavc-encoded CBR streams whose Info header declares ~1.5-2 frames
// MORE than play — the encoder counts frames it pads, and ffmpeg (the
// film's consumer) plays declared frames minus delay+padding
// (data/live/cache/audio/page-01.mp3: 155 declared frames, 576+1584
// pad samples → 4.000000 s — exactly what ffprobe reports and the
// render decodes — while the raw declared product 4.04898 s counts ~2
// unplayable frames). T12 round-1 H1 lived here. A header-less CBR
// stream is measured to within one MPEG frame (~26 ms), far inside
// what a 2 s end fade needs.
func measureDuration(b []byte) (time.Duration, error) {
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE" {
		return wavDuration(b)
	}
	return mp3Duration(b)
}

// wavDuration reads a WAV's length from its fmt and data chunks: the
// byte rate (sample rate × frame size) the fmt chunk declares, and the
// data chunk's byte count. PCM WAVs are exact, so this is the one
// measurement without any frame-boundary rounding.
func wavDuration(b []byte) (time.Duration, error) {
	if len(b) < 12 {
		return 0, fmt.Errorf("%w: wav shorter than its header", ErrClipDuration)
	}
	var byteRate int64
	for off := 12; off+8 <= len(b); {
		chunk := string(b[off : off+4])
		size := int64(u32(b[off+4 : off+8]))
		switch chunk {
		case "fmt ":
			if off+8+int(size) > len(b) || size < 16 {
				return 0, fmt.Errorf("%w: truncated fmt chunk", ErrClipDuration)
			}
			byteRate = int64(u32(b[off+8+8 : off+8+12]))
		case "data":
			if byteRate <= 0 {
				return 0, fmt.Errorf("%w: data chunk before a readable fmt chunk", ErrClipDuration)
			}
			return time.Duration(float64(size) / float64(byteRate) * float64(time.Second)), nil
		}
		off += 8 + int(size) + (int(size) & 1) // chunks are word-aligned
	}
	return 0, fmt.Errorf("%w: no data chunk found", ErrClipDuration)
}

// mp3Duration reads an MP3's length from its frames: ID3 tags are
// skipped, the first MPEG frame located, and the length taken from a
// Xing/Info frame count when present — trimmed to the playable length
// by the encoder tag's delay/padding samples when the tag is there — or
// a CBR byte-count estimate otherwise (see measureDuration).
func mp3Duration(b []byte) (time.Duration, error) {
	off := 0
	if len(b) >= 10 && string(b[:3]) == "ID3" {
		if b[3] == 0xFF {
			return 0, fmt.Errorf("%w: an ID3v1-only stream has no frame header", ErrClipDuration)
		}
		size := syncsafe(b[6:10])
		if size < 0 {
			return 0, fmt.Errorf("%w: invalid ID3v2 tag size", ErrClipDuration)
		}
		off = 10 + size
		if b[5]&0x10 != 0 { // ID3v2.4 footer flag: another 10 bytes
			off += 10
		}
	}
	end := len(b)
	if end-off >= 128 && string(b[end-128:end-125]) == "TAG" {
		end -= 128 // ID3v1 tag
	}
	// Locate the first MPEG audio frame: sync 0xFFE anywhere after the
	// tags (junk between tag and frames is not unknown).
	i := off
	for i+4 <= end {
		if b[i] == 0xFF && b[i+1]&0xE0 == 0xE0 {
			break
		}
		i++
	}
	if i+4 > end {
		return 0, fmt.Errorf("%w: no MPEG audio frame found", ErrClipDuration)
	}
	ver, layer, brIdx, srIdx, padding, chMode := mp3Header(b[i : i+4])
	if layer != 1 || brIdx == 0 || brIdx == 15 || srIdx == 3 {
		return 0, fmt.Errorf("%w: unsupported or corrupt MPEG frame header", ErrClipDuration)
	}
	sr := mp3SampleRate(ver, srIdx)
	if sr <= 0 {
		return 0, fmt.Errorf("%w: unsupported MPEG version in frame header", ErrClipDuration)
	}
	spf := 1152
	if ver != 3 { // MPEG-2 and MPEG-2.5 Layer III: 576 samples per frame
		spf = 576
	}
	bitrate := mp3Bitrate(ver, brIdx) * 1000
	// Xing/Info: a VBR (or header-carrying CBR) stream declares its
	// frame count at the first frame's payload start, after the side
	// info. The declaration counts frames the encoder padded, so the
	// LAME/Lavf/Lavc tag that follows the header fields carries the
	// encoder-delay and tail-padding sample counts (one 24-bit field:
	// delay in the top 12 bits, padding in the bottom 12); ffmpeg's mp3
	// demuxer — the consumer every narrated film goes through — plays
	// declared frames minus those pads, which is the playable length.
	// GMI's clips carry exactly this (page-01.mp3: 155 frames, pads
	// 576+1584 → 4.000000 s, matching ffprobe and the decoded render;
	// the untrimmed declared product 4.04898 s counts ~2 unplayable
	// frames — T12 round-1 H1). Files whose Xing/Info header has no
	// encoder tag measure their declared count untrimmed (mp3GaplessPads
	// returns zero pads then).
	sideInfo := 17 // MPEG-1 mono
	if chMode != 3 {
		sideInfo = 32
	}
	if ver != 3 {
		sideInfo = 9
		if chMode != 3 {
			sideInfo = 17
		}
	}
	payload := i + 4 + sideInfo
	if payload+12 <= end && (string(b[payload:payload+4]) == "Xing" || string(b[payload:payload+4]) == "Info") {
		// flags at +4; when flag bit 0 is set the frame count at +8.
		if flags := u32(b[payload+4 : payload+8]); flags&1 != 0 {
			if frames := int64(u32(b[payload+8 : payload+12])); frames > 0 {
				startPad, endPad := mp3GaplessPads(b, payload, flags, end)
				samples := frames*int64(spf) - startPad - endPad
				if samples <= 0 {
					return 0, fmt.Errorf("%w: gapless delay/padding %d+%d exceeds the %d declared frames", ErrClipDuration, startPad, endPad, frames)
				}
				return durationFromSamples(samples, int64(sr)), nil
			}
		}
	}
	// CBR estimate: every frame is frameLen bytes (per-frame padding is
	// one byte; ignoring it costs less than one frame over the whole
	// stream), so the byte count after the tags over the frame length
	// is the frame count.
	frameLen := int64(spf/8*bitrate/int(sr)) + int64(padding)
	dataBytes := int64(end - i)
	frames := dataBytes / frameLen
	if frames <= 0 {
		return 0, fmt.Errorf("%w: audio data shorter than one MPEG frame", ErrClipDuration)
	}
	return mp3DurationFrom(frames, int64(spf), int64(sr)), nil
}

// mp3DurationFrom converts a frame count at a sample rate into a
// duration: frames × samples-per-frame / sample rate.
func mp3DurationFrom(frames, spf, sr int64) time.Duration {
	return durationFromSamples(frames*spf, sr)
}

// durationFromSamples converts a decoded sample count at a sample rate
// into a duration.
func durationFromSamples(samples, sr int64) time.Duration {
	return time.Duration(float64(samples) / float64(sr) * float64(time.Second))
}

// mp3GaplessPads reads the encoder-delay and tail-padding sample counts
// a Xing/Info stream stores in the LAME/Lavf/Lavc tag that directly
// follows the header's present fields (the frames/bytes/TOC/quality
// fields each occupy their flag-gated slot, then the tag: a 24-bit
// value at tag+21 whose top 12 bits are the delay and bottom 12 the
// padding — the layout ffmpeg's mp3 demuxer parses, mp3dec.c
// mp3_parse_info_tag). Zero pads are returned when no such tag is
// present, which leaves the declared count untrimmed.
func mp3GaplessPads(b []byte, payload int, flags uint32, end int) (startPad, endPad int64) {
	off := payload + 8
	if flags&1 != 0 {
		off += 4 // frames field
	}
	if flags&2 != 0 {
		off += 4 // bytes field
	}
	if flags&4 != 0 {
		off += 100 // TOC
	}
	if flags&8 != 0 {
		off += 4 // quality
	}
	if off+24 > end {
		return 0, 0
	}
	tag := string(b[off : off+4])
	if tag != "LAME" && tag != "Lavf" && tag != "Lavc" {
		return 0, 0
	}
	v := u24(b[off+21 : off+24])
	return int64(v >> 12), int64(v & 0xFFF)
}

// mp3Header decodes a four-byte MPEG audio frame header into its
// fields: version (3 = MPEG-1, 2 = MPEG-2, 0 = MPEG-2.5), layer (the
// two-bit field value; 1 = Layer III, the only layer this reader
// accepts), the bitrate and sample-rate indices, the padding flag and
// the channel mode.
func mp3Header(h []byte) (ver, layer, brIdx, srIdx, padding, chMode int) {
	ver = int(h[1]>>3) & 0x3
	layer = int(h[1]>>1) & 0x3
	brIdx = int(h[2] >> 4)
	srIdx = int(h[2]>>2) & 0x3
	padding = int(h[2]>>1) & 0x1
	chMode = int(h[3] >> 6)
	return
}

// mp3Bitrate returns the Layer III bitrate in kbps for a version and
// bitrate index. Index 0 (free) and 15 (bad) are rejected by the
// caller.
func mp3Bitrate(ver, idx int) int {
	if ver == 3 { // MPEG-1 Layer III
		table := [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
		return table[idx]
	}
	table := [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	return table[idx]
}

// mp3SampleRate returns the sample rate in Hz for a version and
// sample-rate index; index 3 (reserved) yields 0 and is rejected by
// the caller.
func mp3SampleRate(ver, idx int) int {
	rate := 0
	switch ver {
	case 3:
		rate = [...]int{44100, 48000, 32000, 0}[idx]
	case 2:
		rate = [...]int{22050, 24000, 16000, 0}[idx]
	case 0:
		rate = [...]int{11025, 12000, 8000, 0}[idx]
	}
	return rate
}

// syncsafe decodes a four-byte ID3v2 syncsafe integer (7 bits per
// byte). A negative result marks a non-syncsafe encoding (a high bit
// set), which the ID3v2 spec forbids.
func syncsafe(b []byte) int {
	if b[0]&0x80 != 0 || b[1]&0x80 != 0 || b[2]&0x80 != 0 || b[3]&0x80 != 0 {
		return -1
	}
	return int(b[0])<<21 | int(b[1])<<14 | int(b[2])<<7 | int(b[3])
}

// u32 reads a big-endian uint32.
func u32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// u24 reads a big-endian 24-bit integer.
func u24(b []byte) uint32 {
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}
