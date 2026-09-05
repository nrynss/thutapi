// Package audio synthesises and persists the spoken halves of Thutapi
// (PLAN.md §T8): the interview questions a child hears, and the
// narration clips a finished book is muxed from.
//
// # The two paths
//
// SynthesizeQuestion speaks one interview question and returns the
// audio bytes. The interview's text streams over SSE immediately and
// the TTS call is fired in parallel — text never waits on audio
// (project.md §4) — so the seam is a plain function returning bytes:
// the caller decides when to fire it (a job, a goroutine), and this
// package never blocks the text path itself. Questions are not book
// narration; nothing here anchors them into store rows. They use the
// turbo model — latency beats fidelity (PLAN.md §T8).
//
// NarrateBook speaks every page of a book — one clip per page, in
// page order — and persists each clip into the store: blob bytes into
// mediastore and a metadata row anchored as store.MediaNarration at
// (book, page). That persisted state is the whole deliverable: what
// T10 owes nothing beyond "one persisted clip per page, in page
// order" (PLAN.md §T8, 2026-09-05 reshape). Book and page rows are
// the caller's, exactly as for T7's BookWriter — store.CreateBook and
// store.CreatePage must have run before NarrateBook places a clip, or
// the place fails with store.ErrInvalid. Narration uses the hd model.
//
// # The emotion mechanism
//
// A page's narration carries its per-page Emotion (story.Page), the
// value M3 authored in Phase B (project.md §4: "Per-page emotion
// comes from M3's JSON"). T5 chose the vocabulary from the provider's
// own: story.Emotions is "the emotion vocabulary of MiniMax Speech
// 2.8, because T8 passes the value straight into
// minimax-tts-speech-2.8-hd, where an out-of-set word is silently
// ignored" (internal/story/story.go). This package therefore passes
// the page's emotion to the TTS call as the request payload's emotion
// field (empty for questions, which carry none — their payload stays
// byte-identical to the live-verified request shape in
// t2b-t5b-live-record.md), and refuses an out-of-set emotion loudly
// before any call: the provider would otherwise swallow it silently.
//
// The emotion key itself is asserted-but-unverified live as of
// 2026-09-05: its top-level placement on the minimax-tts-speech-2.8-hd
// payload and this in-set vocabulary are pinned on the wire but have
// never been confirmed against the upstream — the TTS pool answered
// the settlement call with HTTP 503 "Upstream capacity temporarily
// exhausted" on 2026-09-05, so no live echo of an emotion-carrying
// call exists. If upstream ignores or rejects the key, narration
// renders emotion-less with every check green (the silent-ignore
// class; t8-round2.md H1). The committed settlement probe is
// TestLiveSynthesizeSpeech_EmotionKeyEchoed (internal/gmi/media/
// live_test.go, //go:build live); these notes flip when it passes
// against a recovered upstream (t8-remediation-round2.md). The
// empty-emotion path is unaffected: it stays byte-identical to the
// live-verified question shape (t2b-t5b-live-record.md).
//
// The cast-voice fields (story.Voice.Pitch, SoundEffects) do NOT
// reach the TTS call here. project.md §4's mechanism for them —
// re-calling a voice clone with pitch / timbre / sound_effects — is
// the voice-clone track (T13), which AGENTS.md's two-day cut list
// cuts first, along with multi-character voices. T8 speaks the
// default narrator voice and nothing else.
//
// # The response shape
//
// SynthesizeSpeech — internal/gmi/media — does not return audio
// bytes; the request queue answers with its own envelope and the
// audio is a URL inside it (live-verified 2026-09-05 in
// t2b-t5b-live-record.md):
//
//	{"request_id":"…","model":"minimax-tts-speech-2.8-hd",
//	 "status":"success",
//	 "payload":{… the request, echoed back …},
//	 "outcome":{"audio_url":"https://storage.googleapis.com/…/….mp3",
//	            "format":"mp3","status":"success"}}
//
// Every decode here keys on the outcome subtree alone — never on
// "the first thing in the body that looks like media", which is how
// the echoed payload produced T6's round-1 H1. The audio_url is
// downloaded on receipt: those URLs point at storage.googleapis.com
// and are assumed to expire (PLAN.md invariant 7).
//
// # The contract row, landed
//
// internal/gmi/media's SynthesizeSpeech(ctx, text, emotion, voice,
// model) (client.go) sends {text, voice_id, the two audio flags} plus
// the emotion key only when emotion is non-empty, so an empty emotion
// keeps the payload byte-identical to the live-verified question
// shape. *media.Client satisfies this package's TTS seam — contract
// row C1 of dev-diary/adversarial-review/t8-round1.md, landed in the
// round-1 remediation and pinned at compile time in wire_test.go;
// nothing in this package routes around internal/gmi (PLAN.md
// invariant 1).
//
// Configuration arrives through Config; this package reads no
// environment (PLAN.md invariant 2). Errors cross the boundary as
// the sentinels below or as internal/gmi's, matched with errors.Is
// and never by substring (invariant 8); the one retry inside the
// media client is the only retry — this package adds no second layer
// (internal/gmi/errors.go). All page clips are generated and
// persisted with golang.org/x/sync/errgroup bounded to Config.Limit
// — never an unbounded go loop (AGENTS.md §Concurrency) — and the
// first failure cancels the rest. Fan-out is the point: eight ~24 s
// TTS calls must not run as a serial loop (PLAN.md §T8).
package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/sync/errgroup"

	"thutapi/internal/mediastore"
	"thutapi/internal/store"
	"thutapi/internal/story"
)

// DefaultNarrationModel is the model NarrateBook speaks with unless
// Config.Model names another: minimax-tts-speech-2.8-hd, the fidelity
// model for the finished book (PLAN.md §T8; project.md §4 saves -hd
// for the book).
const DefaultNarrationModel = "minimax-tts-speech-2.8-hd"

// DefaultQuestionModel is the model SynthesizeQuestion speaks with,
// fixed by the spec: minimax-tts-speech-2.8-turbo, latency beats
// fidelity (PLAN.md §T8; project.md §4).
const DefaultQuestionModel = "minimax-tts-speech-2.8-turbo"

// DefaultVoice is the voice_id every call uses unless Config.Voice
// names another: the library narrator (PLAN.md §T8). Voice cloning
// and the cast voices are T13, which the two-day cut list cuts.
const DefaultVoice = "English_expressive_narrator"

// DefaultLimit is how many narration calls run at once when
// Config.Limit is unset. Four mirrors internal/illustrate's bound on
// the image fan-out: still a fan-out (eight ~24 s calls finish in
// two waves, not eight), without saturating the queue.
const DefaultLimit = 4

// Sentinel errors this package declares. Every one is matched with
// errors.Is; none is produced by matching a provider's message
// (PLAN.md invariant 8). internal/gmi's sentinels pass through
// untouched — a 404 from the request queue reaches the caller as
// gmi.ErrModelNotFound, wrapped with which page or question it was
// for.
var (
	// ErrNoTTS reports a Config with a nil TTS. Nothing can be
	// spoken without a speech client.
	ErrNoTTS = errors.New("audio: no tts client configured")

	// ErrNoStore reports a narration Config missing the store or
	// the blob store. Narration's deliverable is persisted clips, so
	// both are required; the message names which one is nil. The
	// question path never needs them.
	ErrNoStore = errors.New("audio: no store configured")

	// ErrNoPages reports a NarrateBook call with no pages. There is
	// nothing to speak, and returning an empty result would look
	// like success.
	ErrNoPages = errors.New("audio: no pages to narrate")

	// ErrInvalidPage reports a page that cannot be narrated: a page
	// number below 1, the same page number twice in one run (two
	// clips would race for one MediaNarration slot), or an emotion
	// outside story.Emotions — an out-of-set word is silently
	// ignored by Speech 2.8 (story.go), so it is refused loudly
	// here, before any call is made.
	ErrInvalidPage = errors.New("audio: invalid page")

	// ErrNoText reports an empty text to speak — a blank question or
	// a page whose narration text is empty. The message names which.
	ErrNoText = errors.New("audio: text is empty")

	// ErrNoAudio reports a TTS response that carried no usable
	// audio: not the request-queue envelope, an envelope whose
	// outcome carries no audio_url, a URL that is not absolute
	// http(s), a download that did not answer 200, or a download
	// whose served content type is not audio at all. The media
	// client returns every response raw and the terminal TTS record
	// always carries outcome.audio_url (t2b-t5b-live-record.md), so
	// a body without one is a shape this package does not recognise,
	// and guessing could only pick the request's own echo.
	ErrNoAudio = errors.New("audio: response carried no audio")

	// ErrUnsupportedAudio reports fetched bytes served as an audio
	// type outside the closed set internal/mediastore accepts
	// (audio/mpeg, audio/wav): a clip that cannot be persisted or
	// played is not a successful synthesis. It also bounds a single
	// download at MaxAudioBytes.
	ErrUnsupportedAudio = errors.New("audio: unsupported audio type")
)

// TTS is audio's view of the GMI request-queue speech client (PLAN.md
// invariant 3: the consumer declares the interface, as narrow as it
// uses). Tests substitute a fake, so nothing below needs HTTP to be
// exercised.
//
// The method carries the per-page emotion: narration passes the
// page's story.Page.Emotion, questions pass "" — an empty emotion
// must keep the payload byte-identical to today's live-verified
// question shape (t2b-t5b-live-record.md echoes the payload the
// client sent; it has no emotion key). internal/gmi/media's
// SynthesizeSpeech declares the same (ctx, text, emotion, voice,
// model) and satisfies this seam — contract row C1 of
// dev-diary/adversarial-review/t8-round1.md, landed in the round-1
// remediation and pinned at compile time by wire_test.go's
// var _ TTS = (*media.Client)(nil).
type TTS interface {
	// SynthesizeSpeech runs one TTS call and returns the terminal
	// request-queue response body raw. The audio is not in the
	// body: the terminal record carries outcome.audio_url, which
	// this package downloads on receipt.
	SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error)
}

// Config configures the two audio paths. Config flows down from the
// caller; this package reads no environment (PLAN.md invariant 2).
// The zero value is not usable — TTS is required and missing it is
// ErrNoTTS — and every field's empty value means the package default
// (see the field docs), so the default paths are exercised by any
// caller that passes only what it changes.
type Config struct {
	// TTS is the speech client. Required on every path.
	TTS TTS

	// DB and Blobs persist narration clips: the metadata row
	// through DB (store.MediaNarration at (book, page)) and the
	// bytes through Blobs. Required for NarrateBook — missing one
	// is ErrNoStore — and never touched by SynthesizeQuestion.
	DB    *store.DB
	Blobs *mediastore.Store

	// HTTPClient downloads outcome.audio_url. Nil means a client
	// with a 30s timeout and the package's redirect policy. It
	// never talks to GMI — that stays behind internal/gmi (PLAN.md
	// invariant 1).
	HTTPClient *http.Client

	// Limit bounds how many narration calls are in flight at once.
	// Zero or negative means DefaultLimit. Questions are one call
	// and ignore it.
	Limit int

	// Voice is the narration voice_id. Empty means DefaultVoice.
	Voice string

	// Model is the narration model. Empty means DefaultNarrationModel.
	Model string
}

// Clip is one page's persisted narration: which page it speaks (N)
// and the metadata row the store placed for it. Rows are returned in
// the order the pages were given, so clip i always describes pages[i]
// — the one deterministic contract a caller can rely on under the
// fan-out.
type Clip struct {
	// N is the page number, copied from the story page.
	N int
	// Media is the placed store row: its ID names the blob file on
	// disk, ContentType and SizeBytes describe it.
	Media store.Media
}

// speaker is a resolved Config: every default already substituted, so
// no code below re-reads Config and no default can be applied twice
// or in two ways.
type speaker struct {
	tts   TTS
	http  *http.Client
	limit int
	voice string
	model string
}

// resolve substitutes Config's defaults and rejects a Config that
// cannot speak. It is the single place a default is applied, which is
// why the default-path tests can pin all of them at once.
func (cfg Config) resolve() (*speaker, error) {
	if cfg.TTS == nil {
		return nil, ErrNoTTS
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultFetchTimeout, CheckRedirect: safeRedirectPolicy}
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	voice := cfg.Voice
	if voice == "" {
		voice = DefaultVoice
	}
	model := cfg.Model
	if model == "" {
		model = DefaultNarrationModel
	}
	return &speaker{tts: cfg.TTS, http: hc, limit: limit, voice: voice, model: model}, nil
}

// NarrateBook speaks one clip per page of book bookID, in page order,
// and persists each clip as it lands: the bytes go to mediastore
// first, then the metadata row is placed at (book, narration, page N)
// — the same blob-first-then-place ordering T7's BookWriter uses, so
// a re-run keeps serving the previous clip until its replacement is
// on disk. A slot that already holds a narration (a second run over
// the same book rows — a re-narrate after an edit) is replaced: the
// new blob is persisted, then the old occupant's row and file are
// deleted, then the place is retried. Pages are spoken in the order
// given and the returned clips carry that same order; each clip is
// anchored by its page's own N.
//
// Everything that can fail without spending money fails first: the
// configuration, the store presence, and every page — a page with no
// text, an out-of-set emotion, a duplicate or zero page number is
// refused before any call. The pages then fan out through errgroup
// bounded to Config.Limit, and the first failure cancels the rest;
// on any error the returned clips are nil and the run is total —
// a book half-narrated is not a book.
//
// The book and page rows must already exist — store.CreateBook and
// store.CreatePage are the caller's, exactly as for T7's BookWriter —
// or the first place fails with store.ErrInvalid. The caller decides
// which pages to speak and when: pass the whole book for a full
// narration, or the changed page alone after an edit.
func NarrateBook(ctx context.Context, cfg Config, bookID string, pages []story.Page) ([]Clip, error) {
	sp, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, ErrNoPages
	}
	if err := validatePages(pages); err != nil {
		return nil, err
	}
	if cfg.DB == nil || cfg.Blobs == nil {
		return nil, fmt.Errorf("%w: narrating a book needs both a store.DB and a mediastore.Store; the question path needs neither", ErrNoStore)
	}
	w := &narrationWriter{db: cfg.DB, blobs: cfg.Blobs, bookID: bookID}

	clips := make([]Clip, len(pages))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(sp.limit)
	for i, p := range pages {
		g.Go(func() error {
			m, err := sp.narratePage(gctx, w, p)
			if err != nil {
				return fmt.Errorf("audio: narrate page %d: %w", p.N, err)
			}
			clips[i] = Clip{N: p.N, Media: m}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return clips, nil
}

// narratePage runs one page through the whole narration pipeline:
// synthesise, decode the envelope, download the audio on receipt, and
// persist the clip at (book, narration, page N).
func (sp *speaker) narratePage(ctx context.Context, w *narrationWriter, p story.Page) (store.Media, error) {
	raw, err := sp.tts.SynthesizeSpeech(ctx, p.Text, p.Emotion, sp.voice, sp.model)
	if err != nil {
		return store.Media{}, err
	}
	audioURL, err := decodeAudioURL(raw)
	if err != nil {
		return store.Media{}, err
	}
	b, ct, err := sp.fetchAudio(ctx, audioURL)
	if err != nil {
		return store.Media{}, err
	}
	return w.storeClip(ctx, p.N, b, ct)
}

// SynthesizeQuestion speaks one interview question and returns the
// audio bytes (audio/mpeg in practice — see fetchAudio), downloaded on
// receipt from the envelope's outcome.audio_url. Nothing is persisted:
// questions are not book narration and the interview keeps no audio
// rows (project.md §4 streams the text and plays the clip; the
// transcript the store keeps is text turns).
//
// The model is DefaultQuestionModel and the voice DefaultVoice —
// questions are the library voice, spoken for latency. The empty text
// is ErrNoText. The returned bytes are ready to play; the caller
// fires this call in parallel with streaming the question text over
// SSE (project.md §4: text never waits on audio).
func SynthesizeQuestion(ctx context.Context, cfg Config, text string) ([]byte, error) {
	sp, err := cfg.resolve()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("%w: question text is empty", ErrNoText)
	}
	raw, err := sp.tts.SynthesizeSpeech(ctx, text, "", DefaultVoice, DefaultQuestionModel)
	if err != nil {
		return nil, fmt.Errorf("audio: synthesize question: %w", err)
	}
	audioURL, err := decodeAudioURL(raw)
	if err != nil {
		return nil, fmt.Errorf("audio: synthesize question: %w", err)
	}
	b, _, err := sp.fetchAudio(ctx, audioURL)
	if err != nil {
		return nil, fmt.Errorf("audio: synthesize question: %w", err)
	}
	return b, nil
}

// validatePages refuses a page set that cannot be narrated, before a
// single paid call: no text to speak, an emotion the provider would
// silently ignore, or an anchor two clips would race for. Page order
// is not checked — a caller may re-narrate a single page — but the
// pages' own numbers must be usable and unique within the run.
func validatePages(pages []story.Page) error {
	seen := make(map[int]bool, len(pages))
	for _, p := range pages {
		if p.N < 1 {
			return fmt.Errorf("%w: a page carries number %d, want at least 1 (store anchors narration at 1-based page numbers)", ErrInvalidPage, p.N)
		}
		if seen[p.N] {
			return fmt.Errorf("%w: page %d appears twice — two clips would race for one MediaNarration slot", ErrInvalidPage, p.N)
		}
		seen[p.N] = true
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("%w: page %d text is empty", ErrNoText, p.N)
		}
		if !isEmotion(p.Emotion) {
			return fmt.Errorf("%w: page %d emotion %q is not one of %s (an out-of-set word is silently ignored by Speech 2.8 — story.Emotions)", ErrInvalidPage, p.N, p.Emotion, strings.Join(story.Emotions, ", "))
		}
	}
	return nil
}

// isEmotion reports whether e is in story.Emotions, matched exactly:
// the taught set is lowercase, and a case drift would be silently
// ignored by the provider (story.Validate enforces the same rule).
func isEmotion(e string) bool {
	for _, want := range story.Emotions {
		if e == want {
			return true
		}
	}
	return false
}

// narrationWriter persists one book's narration clips: the bytes to
// mediastore, the metadata row to store, anchored MediaNarration at
// (book, page). The ordering is the point (PLAN.md invariant 7 and
// §T8): download and blob first, place second, so the book keeps
// serving the previous clip until the replacement is on disk.
type narrationWriter struct {
	db     *store.DB
	blobs  *mediastore.Store
	bookID string
}

// storeClip persists one page's clip: blob first, then the place.
// blobs.Persist creates the blob file and its unplaced metadata row;
// SetMediaPlace then anchors it at (book, narration, page N). The
// placed row is read back so the caller holds exactly what the store
// now serves — the clip's id, content type and size.
func (w *narrationWriter) storeClip(ctx context.Context, n int, audio []byte, contentType string) (store.Media, error) {
	const op = "persist narration clip"
	id, err := w.blobs.Persist(ctx, bytes.NewReader(audio), contentType)
	if err != nil {
		return store.Media{}, fmt.Errorf("audio: %s for page %d: %w", op, n, err)
	}
	if err := w.place(ctx, id, store.MediaPlace{BookID: w.bookID, Kind: store.MediaNarration, PageN: n}); err != nil {
		return store.Media{}, fmt.Errorf("audio: %s for page %d: %w", op, n, err)
	}
	m, err := w.db.PageMedia(ctx, w.bookID, n, store.MediaNarration)
	if err != nil {
		return store.Media{}, fmt.Errorf("audio: %s for page %d: read back the placed row: %w", op, n, err)
	}
	return m, nil
}

// place attaches the blob id to its MediaNarration slot, replacing
// the slot's previous occupant when it is already taken. Within one
// run a slot cannot collide — validatePages refuses duplicate page
// numbers — but a second NarrateBook over the same book rows (a
// re-narrate after an edit) finds the slot occupied. Replacing is the
// honest semantic for "the current narration of this page": the new
// blob is persisted first, so the book keeps serving the old clip
// until the new one is on disk; the old occupant's row and file are
// then deleted (mediastore.Delete removes both) and the place is
// retried. A crash between the delete and the retried place leaves at
// worst an unplaced orphan blob — exactly what PLAN.md §T11's
// retention sweep owns — and an empty slot the next run fills.
func (w *narrationWriter) place(ctx context.Context, id string, place store.MediaPlace) error {
	err := w.db.SetMediaPlace(ctx, id, place)
	if err == nil {
		return nil
	}
	if !errors.Is(err, store.ErrConflict) {
		return err
	}
	old, oerr := w.db.PageMedia(ctx, place.BookID, place.PageN, store.MediaNarration)
	if oerr != nil {
		return fmt.Errorf("slot occupied but its occupant could not be read: %w (place error: %v)", oerr, err)
	}
	if derr := w.blobs.Delete(ctx, old.ID); derr != nil {
		return fmt.Errorf("replacing %s: %w", old.ID, derr)
	}
	if err := w.db.SetMediaPlace(ctx, id, place); err != nil {
		return fmt.Errorf("after replacing %s: %w", old.ID, err)
	}
	return nil
}
