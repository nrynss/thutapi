package bookgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/audio"
	"thutapi/internal/bookpdf"
	"thutapi/internal/bookvideo"
	"thutapi/internal/gmi/media"
	"thutapi/internal/gmi/text"
	"thutapi/internal/interview"
	"thutapi/internal/job"
	"thutapi/internal/mediastore"
	"thutapi/internal/store"
	"thutapi/internal/story"
	"thutapi/internal/stream"
)

// Fixture vocabulary note: every fixture emotion is "happy" — the one
// value in both the current and the post-T5c emotion vocabularies — so
// no fixture starts failing when story.Emotions changes.

// fullStory is the ordinary Phase-B result the pipeline must turn into
// a book: two drawable characters and the full PageCount pages, every
// page naming at least one cast member, all emotions "happy".
func fullStory() story.Story {
	const mira = "a small girl with two red plaits, round glasses and green wellington boots"
	const bramble = "a shaggy brown dog with one white ear and a red collar"
	st := story.Story{
		Title: "Mira and Bramble's Long Day",
		Cast: []story.CastMember{
			{Name: "Mira", Visual: mira},
			{Name: "Bramble", Visual: bramble},
		},
	}
	for n := 1; n <= story.PageCount; n++ {
		st.Pages = append(st.Pages, story.Page{
			N:          n,
			Text:       fmt.Sprintf("Page %d of the story.", n),
			Prompt:     fmt.Sprintf("Mira and Bramble reach the %dth adventure.", n),
			Characters: []string{"Mira", "Bramble"},
			Emotion:    "happy",
			Lines:      []story.Line{{Character: "Mira", Text: fmt.Sprintf("Look, page %d!", n)}},
		})
	}
	return st
}

// pngBytes returns bytes http.DetectContentType reports as image/png,
// distinct per tag, padded past decodeImage's bare-base64 floor.
func pngBytes(tag string) []byte {
	b := append([]byte("\x89PNG\r\n\x1a\n"), []byte(tag)...)
	for len(b) < 64 {
		b = append(b, '.')
	}
	return b
}

// mediaFixture serves the bytes behind every fake image URL — the
// request-queue answer is a URL now (t6b-live-record.md §3) and
// illustrate's decode dereferences it for real.
type mediaFixture struct {
	mu     sync.Mutex
	srv    *httptest.Server
	bodies map[string][]byte
	seq    int
}

func newMediaFixture() *mediaFixture {
	f := &mediaFixture{bodies: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		b, ok := f.bodies[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(b)
	}))
	return f
}

// serve registers pngBytes(tag) at a fresh path and returns its URL.
// The path is numeric — the tag (often a prompt) may contain
// characters that are not legal in a URL path; the body is distinct
// per tag, which is what the echo guards need.
func (f *mediaFixture) serve(tag string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	path := fmt.Sprintf("/m-%d", f.seq)
	f.bodies[path] = pngBytes(tag)
	return f.srv.URL + path
}

func (f *mediaFixture) Close() { f.srv.Close() }

// queueEnvelope wraps a media URL the way the GMI image queue answers
// (t6b-live-record.md §3): status success, the request echoed in
// payload, the result at outcome.media_urls[0].url.
func queueEnvelope(mediaURL string) []byte {
	body, err := json.Marshal(map[string]any{
		"request_id": "req-fixture",
		"status":     "success",
		"payload":    map[string]any{},
		"outcome": map[string]any{
			"media_urls":          []map[string]string{{"id": "0", "url": mediaURL}},
			"thumbnail_image_url": mediaURL + "/thumbnail",
		},
	})
	if err != nil {
		panic("fixture envelope cannot marshal")
	}
	return body
}

// audioEnvelope is a terminal TTS record naming the given audio URL in
// the live-verified shape (t2b-t5b-live-record.md): outcome.audio_url.
func audioEnvelope(audioURL string) []byte {
	return []byte(`{"request_id":"req-1","model":"minimax-tts-speech-2.8-hd","status":"success","payload":{},"outcome":{"audio_url":"` + audioURL + `","format":"mp3","status":"success"}}`)
}

// clipBytes is the deterministic audio payload served for one page's
// text; the download path accepts the served Content-Type, so the
// bytes only need to be distinct per page.
// makeMP3 builds a parseable mono MPEG-1 Layer III CBR narration clip
// for the harness's fake TTS: an ID3v2 tag whose body is the distinct
// text (so the bytes differ per page) followed by 77 silent frames.
// NarrateBook measures every clip as it downloads (audio.Clip.Duration
// — contract row C2 of t12-round1.md), so the harness's clips must be
// real MP3 structures, not arbitrary bytes.
func makeMP3(text string) []byte {
	const frameLen = 417 // 144 x 128000 / 44100, mono MPEG-1 Layer III
	const frames = 77
	size := len(text)
	buf := make([]byte, 0, 10+size+frames*frameLen)
	buf = append(buf, 'I', 'D', '3', 4, 0, 0,
		byte(size>>21&0x7f), byte(size>>14&0x7f), byte(size>>7&0x7f), byte(size&0x7f))
	buf = append(buf, text...)
	for i := 0; i < frames; i++ {
		buf = append(buf, 0xFF, 0xFB, 0x90, 0xC0)
		buf = append(buf, make([]byte, frameLen-4)...)
	}
	return buf
}

// fixtureClipDuration is the Go-known duration every harness narration
// clip measures to: 77 frames x 1152 samples / 44100 Hz
// (2.011428571428... s; 2011428571 ns after truncation, exactly what
// the audio package's float measurement yields).
const fixtureClipDuration = 2011428571 * time.Nanosecond

func clipBytes(text string) []byte { return makeMP3(text) }

// orderRecorder is a mutex-guarded call log the stage fakes share, so
// an ordering pin observes stage boundaries without racing the fan-out.
type orderRecorder struct {
	mu  sync.Mutex
	log []string
}

func (o *orderRecorder) mark(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.log = append(o.log, s)
}

func (o *orderRecorder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.log...)
}

// firstIndex returns the position of the first entry equal to want, or
// -1.
func firstIndex(entries []string, want string) int {
	for i, e := range entries {
		if e == want {
			return i
		}
	}
	return -1
}

// scriptedChat is story.Structure's Chatter: it answers with the
// marshalled fixture story (or a scripted reply/error), can gate calls
// behind a channel for the double-fire pin, and can panic to pin the
// failed-event path.
type scriptedChat struct {
	mu     sync.Mutex
	calls  int
	story  story.Story
	raw    string // when set, returned verbatim instead of story
	err    error
	panics bool
	gate   chan struct{} // when set, every call blocks here first
}

func (c *scriptedChat) Chat(ctx context.Context, _ text.ChatRequest) (*text.ChatResponse, error) {
	c.mu.Lock()
	c.calls++
	gate := c.gate
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c.mu.Lock()
	panics, err, raw, st := c.panics, c.err, c.raw, c.story
	c.mu.Unlock()
	if panics {
		panic("scriptedChat: boom")
	}
	if err != nil {
		return nil, err
	}
	body := raw
	if body == "" {
		b, merr := json.Marshal(st)
		if merr != nil {
			return nil, merr
		}
		body = string(b)
	}
	return &text.ChatResponse{Choices: []text.Choice{{
		Message: text.AssistantMessage{TextBody: body},
	}}}, nil
}

// scriptedJudge is illustrate.Judge: every verdict matches, so a run
// with it configured spends exactly one verdict per rendered page.
type scriptedJudge struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (j *scriptedJudge) Chat(ctx context.Context, _ text.ChatRequest) (*text.ChatResponse, error) {
	j.mu.Lock()
	j.calls++
	err := j.err
	j.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &text.ChatResponse{Choices: []text.Choice{{
		Message: text.AssistantMessage{TextBody: `{"match": true, "reason": "fixture"}`},
	}}}, nil
}

// fakeImager is illustrate.Imager: every call answers with a queue
// envelope whose media URL serves fresh png bytes.
type fakeImager struct {
	order *orderRecorder
	mu    sync.Mutex
	calls []string // "imager:gen:<prompt>" / "imager:edit:<prompt>"
}

func (f *fakeImager) mark(kind, prompt string) {
	f.mu.Lock()
	f.calls = append(f.calls, kind+":"+prompt)
	f.mu.Unlock()
	if f.order != nil {
		f.order.mark(kind)
	}
}

// count is how many image calls this fake has taken: the number a repair
// must not increase.
func (f *fakeImager) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeImager) GenerateImage(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
	f.mark("imager:gen", prompt)
	return queueEnvelope(fixture.serve("sheet:" + prompt)), nil
}

func (f *fakeImager) EditImage(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
	f.mark("imager:edit", prompt)
	return queueEnvelope(fixture.serve("page:" + prompt)), nil
}

func (f *fakeImager) kinds() (gen, edit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		switch {
		case strings.HasPrefix(c, "imager:gen:"):
			gen++
		case strings.HasPrefix(c, "imager:edit:"):
			edit++
		}
	}
	return gen, edit
}

// ttsCall is one recorded SynthesizeSpeech invocation.
type ttsCall struct {
	text, emotion, voice, model string
}

// fakeTTS is audio.TTS, scripted: it records every call and answers
// with an envelope whose audio_url points at the clip server. err
// fails every call; textErrs fails only the calls whose text it names,
// which is how a SINGLE page's narration is made to fail (§T10f/§T10g:
// that page goes captioned-silent and the book still lands).
type fakeTTS struct {
	order     *orderRecorder
	err       error
	textErrs  map[string]error
	audioBase string
	mu        sync.Mutex
	calls     []ttsCall
}

func (f *fakeTTS) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ttsCall{text: text, emotion: emotion, voice: voice, model: model})
	err := f.err
	if err == nil {
		err = f.textErrs[text]
	}
	f.mu.Unlock()
	if f.order != nil {
		f.order.mark("tts")
	}
	if err != nil {
		return nil, err
	}
	return audioEnvelope(f.audioBase + "/clip/" + url.PathEscape(text)), nil
}

func (f *fakeTTS) recorded() []ttsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ttsCall(nil), f.calls...)
}

// clipServer serves clipBytes(text) at GET /clip/<text>.
type clipServer struct {
	*httptest.Server
}

func newClipServer() *clipServer {
	s := &clipServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := strings.TrimPrefix(r.URL.Path, "/clip/")
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(clipBytes(text))
	}))
	return s
}

// musicCall is one recorded SynthesizeMusic invocation.
type musicCall struct {
	lyrics, prompt, model string
}

// fakeMusic is audio.Music, scripted: it records every call and answers
// with a music envelope whose media_urls[0].url points at the bed
// server. The bed step of the pipeline (audio.GenerateMusicBed) then
// downloads real bytes from that URL.
type fakeMusic struct {
	bedBase string
	err     error
	mu      sync.Mutex
	calls   []musicCall
}

func (f *fakeMusic) SynthesizeMusic(ctx context.Context, lyrics, prompt, model string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, musicCall{lyrics: lyrics, prompt: prompt, model: model})
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return musicEnvelope(f.bedBase + "/bed.mp3"), nil
}

func (f *fakeMusic) recorded() []musicCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]musicCall(nil), f.calls...)
}

// musicEnvelope is a terminal music record in the observed shape of
// t12-music-record.json: the bed at outcome.media_urls[0].url beside
// outcome.audio_url, with its length at outcome.duration_ms.
func musicEnvelope(bedURL string) []byte {
	return []byte(`{"request_id":"req-music","model":"minimax-music-3.0","status":"success",` +
		`"payload":{"lyrics":"echoed","prompt":"echoed","format":"mp3"},` +
		`"outcome":{"media_urls":[{"id":"0","url":"` + bedURL + `"}],"audio_url":"` + bedURL +
		`","duration_ms":3000,"format":"mp3","status":"success"}}`)
}

// bedServer serves the bed bytes the music step downloads.
type bedServer struct {
	*httptest.Server
}

func newBedServer() *bedServer {
	b := &bedServer{}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("the-wordless-bed-bytes"))
	}))
	return b
}

// fakeMixRunner is audio.Runner for the music step: it records the mix
// command, writes a distinct MIXED film to the output path (so the
// persist leg reads a real file and tests can tell the mixed film from
// the plain render), and can fail.
type fakeMixRunner struct {
	err error
	mu  sync.Mutex
	ins []mixCall
}

type mixCall struct {
	args []string
}

func (f *fakeMixRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.ins = append(f.ins, mixCall{args: append([]string{name}, args...)})
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	// The output path is the last arg; write the MIXED film marker.
	out := args[len(args)-1]
	if err := os.WriteFile(out, []byte("mixed-film:"+out), 0o600); err != nil {
		return nil, err
	}
	return []byte("ok"), nil
}

func (f *fakeMixRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ins)
}

func (f *fakeMixRunner) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ins) == 0 {
		return nil
	}
	return f.ins[len(f.ins)-1].args
}

// fakePDFRenderer is the pdfRenderer seam: it records the Input (title,
// byline and pages) and returns deterministic PDF bytes.
type fakePDFRenderer struct {
	order *orderRecorder
	err   error
	mu    sync.Mutex
	ins   []bookpdf.Input
}

func (f *fakePDFRenderer) Render(ctx context.Context, in bookpdf.Input) ([]byte, error) {
	f.mu.Lock()
	f.ins = append(f.ins, in)
	err := f.err
	f.mu.Unlock()
	if f.order != nil {
		f.order.mark("pdf")
	}
	if err != nil {
		return nil, err
	}
	return []byte("%PDF-1.4 " + in.Title + ":" + in.Byline), nil
}

func (f *fakePDFRenderer) inputs() []bookpdf.Input {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]bookpdf.Input, len(f.ins))
	copy(out, f.ins)
	return out
}

// fakeRenderer is the videoRenderer seam: it records the Input (title,
// byline and the page image/audio bytes the film was built from) and
// writes the film bytes to in.OutputPath, so the persist leg reads a
// real file. It returns dur as the render's computed total — the value
// the music step's mix must anchor to (contract row C3 of
// t12-round1.md).
type fakeRenderer struct {
	order *orderRecorder
	err   error
	dur   time.Duration
	mu    sync.Mutex
	ins   []bookvideo.Input
}

func (f *fakeRenderer) Render(ctx context.Context, in bookvideo.Input) (time.Duration, error) {
	f.mu.Lock()
	f.ins = append(f.ins, in)
	err := f.err
	dur := f.dur
	f.mu.Unlock()
	if f.order != nil {
		f.order.mark("render")
	}
	if err != nil {
		return 0, err
	}
	return dur, os.WriteFile(in.OutputPath, []byte("film:"+in.Title+":"+in.Byline), 0o600)
}

func (f *fakeRenderer) inputs() []bookvideo.Input {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]bookvideo.Input, len(f.ins))
	copy(out, f.ins)
	return out
}

// fakeFilmStore is the filmStore seam backed by the REAL store and
// blob directory: it writes the blob file named by a fresh id and
// creates the real media row — exactly what *mediastore.Store.Persist
// will do once its closed content-type set gains video/mp4 (contract
// row C2). The seam isolates only that content-type gate, so the whole
// film lifecycle — row, placement, serving, supersession — runs
// against the real store.
type fakeFilmStore struct {
	db       *store.DB
	mediaDir string
	order    *orderRecorder
	err      error
	mu       sync.Mutex
	deleted  []string
}

func newFakeFilmStore(t *testing.T, db *store.DB, mediaDir string) *fakeFilmStore {
	t.Helper()
	return &fakeFilmStore{db: db, mediaDir: mediaDir}
}

func (f *fakeFilmStore) Persist(ctx context.Context, src io.Reader, contentType string) (string, error) {
	if f.order != nil {
		f.order.mark("film")
	}
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(src)
	if err != nil {
		return "", err
	}
	id, err := store.NewID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(f.mediaDir, id), b, 0o600); err != nil {
		return "", err
	}
	if _, err := f.db.CreateMedia(ctx, store.Media{ID: id, ContentType: contentType, SizeBytes: int64(len(b))}); err != nil {
		return "", err
	}
	return id, nil
}

func (f *fakeFilmStore) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	f.deleted = append(f.deleted, id)
	f.mu.Unlock()
	if err := f.db.DeleteMedia(ctx, id); err != nil {
		return err
	}
	return os.Remove(filepath.Join(f.mediaDir, id))
}

// fixture is the package-level image server, like illustrate's
// fixtureMedia.
var fixture = newMediaFixture()

func TestMain(m *testing.M) {
	defer fixture.Close()
	os.Exit(m.Run())
}

// pipelineHarness is one test's full wiring: a real store, a real blob
// store under a throwaway media dir, a real broker and job runner, and
// scripted stage fakes — nothing but the four external seams is fake,
// so the ordering, row and event pins run against the real machinery.
type pipelineHarness struct {
	t        *testing.T
	db       *store.DB
	blobs    *mediastore.Store
	mediaDir string
	broker   *stream.Broker
	runner   *job.Runner
	h        *Handler
	chat     *scriptedChat
	judge    *scriptedJudge
	imager   *fakeImager
	tts      *fakeTTS
	pdf      *fakePDFRenderer
	render   *fakeRenderer
	film     *fakeFilmStore
	order    *orderRecorder
	clips    *clipServer
	logs     *syncBuffer

	// stages collects the stage events waitEvent stepped over, in the
	// order they arrived, so a test can assert the pipeline announced
	// itself without every other test having to expect them.
	stages []string
}

// syncBuffer is a slog sink safe to write from the job goroutine and
// read from the test goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newPipelineHarness(t *testing.T) *pipelineHarness {
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
	broker := stream.New(stream.Config{Buffer: 64})
	runner := job.New(broker)
	order := &orderRecorder{}
	clips := newClipServer()
	t.Cleanup(clips.Close)
	chat := &scriptedChat{story: fullStory()}
	judge := &scriptedJudge{}
	imager := &fakeImager{order: order}
	tts := &fakeTTS{order: order, audioBase: clips.URL}
	pdf := &fakePDFRenderer{order: order}
	render := &fakeRenderer{order: order}
	film := newFakeFilmStore(t, db, mediaDir)
	film.order = order

	// Logs are captured, not discarded: a failed run's ERROR line is the
	// ONLY record of what went wrong (the child sees a bare failed {}), so
	// it is a contract worth asserting — see
	// TestPipeline_FailedRunLogsTheError.
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, nil))
	h, err := New(Config{
		DB:       db,
		Blobs:    blobs,
		MediaDir: mediaDir,
		Chat:     chat,
		Judge:    judge,
		Imager:   imager,
		TTS:      tts,
		Broker:   broker,
		Jobs:     runner,
		PDF:      pdf,
		Video:    render,
		Film:     film,
		Log:      log,
		// The retry is real behaviour and the degrade tests must still
		// pass THROUGH it, so it is shrunk rather than switched off:
		// three attempts, microseconds apart.
		Throttle: audio.ThrottleConfig{Backoff: time.Microsecond},
	})
	if err != nil {
		t.Fatalf("new bookgen handler: %v", err)
	}
	return &pipelineHarness{
		t: t, db: db, blobs: blobs, mediaDir: mediaDir, broker: broker, runner: runner,
		h: h, chat: chat, judge: judge, imager: imager, tts: tts, pdf: pdf, render: render,
		film: film, order: order, clips: clips, logs: logs,
	}
}

// makeEndedInterview creates the book + interview rows the interview
// start path creates (working title, then the byline question zero
// wrote), fills a realistic transcript, and persists the closing turn
// that makes the interview ended and restart-stable.
func (ph *pipelineHarness) makeEndedInterview(byline string) (ivID, bookID string) {
	ph.t.Helper()
	ctx := ph.t.Context()
	book, err := ph.db.CreateBook(ctx, interview.WorkingTitle)
	if err != nil {
		ph.t.Fatalf("create book: %v", err)
	}
	if byline != "" {
		book.Byline = byline
		if err := ph.db.UpdateBook(ctx, book); err != nil {
			ph.t.Fatalf("set byline: %v", err)
		}
	}
	iv, err := ph.db.CreateInterview(ctx)
	if err != nil {
		ph.t.Fatalf("create interview: %v", err)
	}
	iv.BookID = book.ID
	iv.Turns = []store.Turn{
		{Role: interview.RoleInterviewer, Text: "Who is your hero?"},
		{Role: interview.RoleChild, Text: "Mira"},
		{Role: interview.RoleInterviewer, Text: "What does Mira want?"},
		{Role: interview.RoleChild, Text: "To find the golden acorn"},
		{Role: interview.RoleInterviewer, Text: "Who helps her?"},
		{Role: interview.RoleChild, Text: "Bramble the dog"},
		{Role: interview.RoleClosing, Text: "What a lovely story! Let's make your book."},
	}
	if err := ph.db.UpdateInterview(ctx, iv); err != nil {
		ph.t.Fatalf("fill transcript: %v", err)
	}
	return iv.ID, book.ID
}

// subscribe opens a subscription on the book topic and registers its
// cleanup.
func (ph *pipelineHarness) subscribe(bookID string) *stream.Subscription {
	ph.t.Helper()
	s := ph.broker.Subscribe(ph.t.Context(), Topic(bookID))
	ph.t.Cleanup(s.Cancel)
	return s
}

// waitEvent reads the next event and requires it to be name, returning
// its decoded payload (the interview suite's waitEvent shape).
// waitEvent returns the next event named name, and fails on any other
// product event before it. stage events are the one exception: they
// interleave with the product events by design (one per pipeline stage,
// published as the run enters it), and every assertion here but the stage
// ordering test itself is about what the book did, not how far along it
// was. They are collected into ph.stages instead, where a test that does
// care can assert their order — asking for "stage" by name still returns
// the next one, so nothing is hidden from a test that wants it.
func (ph *pipelineHarness) waitEvent(s *stream.Subscription, name string) map[string]any {
	ph.t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-s.Events:
			if !ok {
				ph.t.Fatalf("subscription closed while waiting for %q", name)
			}
			if ev.Name == "stage" && name != "stage" {
				ph.stages = append(ph.stages, ev.Data)
				continue
			}
			if ev.Name != name {
				ph.t.Fatalf("event = %q (%s), want %q", ev.Name, ev.Data, name)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
				ph.t.Fatalf("decode %s event %q: %v", name, ev.Data, err)
			}
			return payload
		case <-deadline:
			ph.t.Fatalf("timed out waiting for %q event", name)
			return nil
		}
	}
}

// waitJob polls the runner until the job is terminal and returns its
// Result.
func (ph *pipelineHarness) waitJob(id string) job.Result {
	ph.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		res, err := ph.runner.Result(id)
		if err != nil {
			ph.t.Fatalf("job result %s: %v", id, err)
		}
		if res.Status != job.StatusRunning {
			return res
		}
		if time.Now().After(deadline) {
			ph.t.Fatalf("job %s did not terminate within 15s", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mux returns a mux with the real route registration patterns
// (cmd/thutapi/newServer's lines) so HTTP tests exercise the same
// surface the box serves.
func (ph *pipelineHarness) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /media/{id}", ph.blobs)
	mux.HandleFunc("POST /interviews/{id}/generate", ph.h.Generate)
	mux.HandleFunc("GET /interviews/{id}/generate/events", ph.h.Events)
	return mux
}

// postGenerate POSTs the generate route and decodes the response.
func (ph *pipelineHarness) postGenerate(srv *httptest.Server, ivID string) (int, generateResponse, errorEvent) {
	ph.t.Helper()
	var out generateResponse
	var eerr errorEvent
	resp, err := http.Post(srv.URL+"/interviews/"+ivID+"/generate", "application/json", nil)
	if err != nil {
		ph.t.Fatalf("POST generate: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusAccepted {
		if err := json.Unmarshal(body, &out); err != nil {
			ph.t.Fatalf("decode generate response %q: %v", body, err)
		}
	} else {
		if err := json.Unmarshal(body, &eerr); err != nil {
			ph.t.Fatalf("decode generate error %q: %v", body, err)
		}
	}
	return resp.StatusCode, out, eerr
}
