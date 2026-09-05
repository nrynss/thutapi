package bookgen

import (
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
func clipBytes(text string) []byte { return []byte("clip-for:" + text) }

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
// with an envelope whose audio_url points at the clip server.
type fakeTTS struct {
	order     *orderRecorder
	err       error
	audioBase string
	mu        sync.Mutex
	calls     []ttsCall
}

func (f *fakeTTS) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, ttsCall{text: text, emotion: emotion, voice: voice, model: model})
	err := f.err
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

// fakeRenderer is the videoRenderer seam: it records the Input (title,
// byline and the page image/audio bytes the film was built from) and
// writes the film bytes to in.OutputPath, so the persist leg reads a
// real file.
type fakeRenderer struct {
	order *orderRecorder
	err   error
	mu    sync.Mutex
	ins   []bookvideo.Input
}

func (f *fakeRenderer) Render(ctx context.Context, in bookvideo.Input) error {
	f.mu.Lock()
	f.ins = append(f.ins, in)
	err := f.err
	f.mu.Unlock()
	if f.order != nil {
		f.order.mark("render")
	}
	if err != nil {
		return err
	}
	return os.WriteFile(in.OutputPath, []byte("film:"+in.Title+":"+in.Byline), 0o600)
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
	render   *fakeRenderer
	film     *fakeFilmStore
	order    *orderRecorder
	clips    *clipServer
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
	render := &fakeRenderer{order: order}
	film := newFakeFilmStore(t, db, mediaDir)
	film.order = order

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
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
		Video:    render,
		Film:     film,
		Log:      log,
	})
	if err != nil {
		t.Fatalf("new bookgen handler: %v", err)
	}
	return &pipelineHarness{
		t: t, db: db, blobs: blobs, mediaDir: mediaDir, broker: broker, runner: runner,
		h: h, chat: chat, judge: judge, imager: imager, tts: tts, render: render,
		film: film, order: order, clips: clips,
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
func (ph *pipelineHarness) waitEvent(s *stream.Subscription, name string) map[string]any {
	ph.t.Helper()
	select {
	case ev, ok := <-s.Events:
		if !ok {
			ph.t.Fatalf("subscription closed while waiting for %q", name)
		}
		if ev.Name != name {
			ph.t.Fatalf("event = %q (%s), want %q", ev.Name, ev.Data, name)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			ph.t.Fatalf("decode %s event %q: %v", name, ev.Data, err)
		}
		return payload
	case <-time.After(15 * time.Second):
		ph.t.Fatalf("timed out waiting for %q event", name)
		return nil
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
