package illustrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/media"
	"thutapi/internal/story"
)

// ---------------------------------------------------------------
// fixtures and fakes
// ---------------------------------------------------------------

// page builds a story page with the emotion and text story.Validate
// wants, so a fixture reads as a page and not as a struct literal.
func page(n int, prompt string, characters ...string) story.Page {
	return story.Page{
		N:          n,
		Text:       fmt.Sprintf("Page %d of the story.", n),
		Prompt:     prompt,
		Characters: characters,
		Emotion:    "happy",
	}
}

const (
	miraVisual    = "a small girl with two red plaits, round glasses and green wellington boots"
	brambleVisual = "a shaggy brown dog with one white ear and a red collar"
)

// twoCastStory is the ordinary case: two drawable characters, two
// pages, no live hazards. Tests that are about something else use it
// so the something else is the only variable.
func twoCastStory() story.Story {
	return story.Story{
		Title: "Mira and Bramble",
		Cast: []story.CastMember{
			{Name: "Mira", Visual: miraVisual},
			{Name: "Bramble", Visual: brambleVisual},
		},
		Pages: []story.Page{
			page(1, "Mira opens the garden gate.", "Mira"),
			page(2, "Bramble digs a hole while Mira watches.", "Bramble", "Mira"),
		},
	}
}

// pngBytes returns bytes that http.DetectContentType reports as
// image/png: the eight-byte signature plus a tag that makes each
// fixture image distinguishable from every other one, which is how
// the image-lock tests prove the RIGHT sheet was attached.
//
// The result is padded to at least minBase64Len bytes so that its
// base64 encoding is long enough for the bare-base64 branch of
// decodeImage to consider it — a 12-byte fixture would be skipped as
// a request id and the test would be measuring the padding, not the
// decode.
func pngBytes(tag string) []byte {
	b := append([]byte("\x89PNG\r\n\x1a\n"), []byte(tag)...)
	for len(b) < minBase64Len {
		b = append(b, '.')
	}
	return b
}

// fixtureMedia serves the bytes behind every fixture media URL. A
// queue record's answer is a URL now (t6b-live-record.md §3), and the
// renderer takes the bytes on receipt — decodeImage dereferences the
// URL for real — so the fakes' responses must name a URL that
// actually answers. One server serves the whole package's fixtures;
// every tag gets its own path, so tests share it safely.
var fixtureMedia = newMediaFixture()

func TestMain(m *testing.M) {
	defer fixtureMedia.Close()
	os.Exit(m.Run())
}

type mediaFixture struct {
	mu     sync.Mutex
	srv    *httptest.Server
	bodies map[string][]byte // path → the bytes served there
	seq    int               // unique suffix for anonymous tags
}

func newMediaFixture() *mediaFixture {
	f := &mediaFixture{bodies: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		b, ok := f.bodies[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			// An unregistered path — the thumbnail tripwire among
			// them — is a 404, so a decode that ever consults it
			// fails loudly instead of quietly rendering it.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(b)
	}))
	return f
}

// url registers a path serving pngBytes(tag) and returns its URL.
// Safe to call from fan-out goroutines: the fakes build responses
// under errgroup.
func (f *mediaFixture) url(tag string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := "/" + tag
	if _, ok := f.bodies[path]; !ok {
		f.bodies[path] = pngBytes(tag)
	}
	return f.srv.URL + path
}

// serve registers the given bytes at a fresh path and returns its
// URL, for tests that need several URLs with distinct content.
func (f *mediaFixture) serve(tag string, b []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	path := fmt.Sprintf("/%s-%d", tag, f.seq)
	f.bodies[path] = b
	return f.srv.URL + path
}

func (f *mediaFixture) Close() { f.srv.Close() }

// queueEnvelope wraps a media URL the way the GMI request queue
// answers (t6b-live-record.md §3): a JSON record whose payload is the
// request echoed back — `{}` on the live i2i record — and whose
// outcome carries media_urls, a LIST OF OBJECTS {"id","url"}, beside
// thumbnail_image_url. The thumbnail names an unregistered path: if a
// decode ever consults it, the fetch 404s and the test fails loudly,
// which is the fixture-level tripwire for round 1 H1.
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
		panic(err) // a fixture that cannot marshal is a broken test, not a runtime path
	}
	return body
}

// imagerCall records one call the fake received.
type imagerCall struct {
	kind   string // "generate" or "edit"
	prompt string
	model  string
	refs   []string // the reference sheet URLs an edit call carried
}

// fakeImager is illustrate's Imager, scripted. It exists because
// PLAN.md invariant 3 lets this package declare its own narrow
// interface, so every path below is exercised without HTTP and
// without spending a cent.
type fakeImager struct {
	mu    sync.Mutex
	calls []imagerCall
	// order records call boundaries as they happen: "gen:start",
	// "gen:end", "edit:start", "edit:end". It is what pins "every
	// reference sheet completes before any page begins".
	order []string

	generate func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error)
	edit     func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error)
}

func (f *fakeImager) record(c imagerCall, mark string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	f.order = append(f.order, mark)
}

func (f *fakeImager) mark(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, s)
}

func (f *fakeImager) GenerateImage(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
	f.record(imagerCall{kind: "generate", prompt: prompt, model: model}, "gen:start")
	defer f.mark("gen:end")
	if f.generate != nil {
		return f.generate(ctx, prompt, model, opts)
	}
	return queueEnvelope(fixtureMedia.serve("sheet", pngBytes("sheet:"+prompt))), nil
}

func (f *fakeImager) EditImage(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
	f.record(imagerCall{kind: "edit", prompt: prompt, model: model, refs: refImages}, "edit:start")
	defer f.mark("edit:end")
	if f.edit != nil {
		return f.edit(ctx, prompt, model, refImages, opts)
	}
	return queueEnvelope(fixtureMedia.serve("page", pngBytes("page:"+prompt))), nil
}

// snapshot returns copies of the recorded calls and order, so a test
// reading them races with nothing.
func (f *fakeImager) snapshot() ([]imagerCall, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]imagerCall(nil), f.calls...), append([]string(nil), f.order...)
}

func (f *fakeImager) prompts(kind string) []string {
	calls, _ := f.snapshot()
	var out []string
	for _, c := range calls {
		if c.kind == kind {
			out = append(out, c.prompt)
		}
	}
	return out
}

// ---------------------------------------------------------------
// the happy path
// ---------------------------------------------------------------

func TestIllustrate(t *testing.T) {
	fake := &fakeImager{}
	book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.References) != 2 {
		t.Fatalf("References = %d, want 2", len(book.References))
	}
	if book.References[0].Name != "Mira" || book.References[1].Name != "Bramble" {
		t.Errorf("references out of cast order: %q, %q", book.References[0].Name, book.References[1].Name)
	}
	if len(book.Pages) != 2 {
		t.Fatalf("Pages = %d, want 2", len(book.Pages))
	}
	for i, p := range book.Pages {
		if p.N != i+1 {
			t.Errorf("Pages[%d].N = %d, want %d — page order must survive the fan-out", i, p.N, i+1)
		}
		if len(p.Image) == 0 {
			t.Errorf("page %d has no image bytes", p.N)
		}
		if p.ContentType != "image/png" {
			t.Errorf("page %d content type = %q, want image/png", p.N, p.ContentType)
		}
	}
	if book.Pages[0].Reference != "Mira" {
		t.Errorf("page 1 rendered against %q, want Mira", book.Pages[0].Reference)
	}
	if book.Pages[1].Reference != "Bramble" {
		t.Errorf("page 2 rendered against %q, want Bramble (its first-listed character)", book.Pages[1].Reference)
	}
	if len(book.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none", book.Skipped)
	}
}

// TestIllustrate_ReferencesFinishBeforeAnyPageStarts is lock 2's
// ordering half: PLAN.md §T6 says "one reference image per cast
// member FIRST, then every page as image-to-image against it". A page
// that started early would have no sheet to lock against.
func TestIllustrate_ReferencesFinishBeforeAnyPageStarts(t *testing.T) {
	s := twoCastStory()
	s.Pages = append(s.Pages,
		page(3, "Mira and Bramble run home.", "Mira", "Bramble"),
		page(4, "Bramble sleeps.", "Bramble"),
	)
	fake := &fakeImager{}
	if _, err := Illustrate(context.Background(), Config{Imager: fake}, s); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	_, order := fake.snapshot()
	lastGenEnd, firstEditStart := -1, -1
	for i, ev := range order {
		if ev == "gen:end" {
			lastGenEnd = i
		}
		if ev == "edit:start" && firstEditStart < 0 {
			firstEditStart = i
		}
	}
	if lastGenEnd < 0 || firstEditStart < 0 {
		t.Fatalf("order = %v, want both reference and page calls", order)
	}
	if lastGenEnd > firstEditStart {
		t.Errorf("a page render started at %d before the last reference sheet finished at %d: %v", firstEditStart, lastGenEnd, order)
	}
}

// TestIllustrate_PagesChainTheirOwnReferenceSheetURLs is lock 2's
// content half: the reference URLs attached to a page must be the
// URLs of that page's own sheets — every character the page names,
// in named order, nobody else's — because those URLs are what
// payload.image carries and what the model locks each character to
// (t6b-live-record.md item 1b: multi-reference keeps every entity).
func TestIllustrate_PagesChainTheirOwnReferenceSheetURLs(t *testing.T) {
	miraURL := fixtureMedia.serve("sheet-mira", pngBytes("MIRA-SHEET"))
	brambleURL := fixtureMedia.serve("sheet-bramble", pngBytes("BRAMBLE-SHEET"))
	fake := &fakeImager{
		generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
			// Each sheet gets a distinguishable URL, keyed on the
			// character its prompt names.
			switch {
			case strings.Contains(prompt, "Mira"):
				return queueEnvelope(miraURL), nil
			case strings.Contains(prompt, "Bramble"):
				return queueEnvelope(brambleURL), nil
			}
			return nil, fmt.Errorf("unexpected reference prompt %q", prompt)
		},
	}
	book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	// The sheets the book carries are the bytes behind those URLs:
	// dereferenced on receipt, with the URL kept for chaining.
	for _, ref := range book.References {
		if ref.URL == "" {
			t.Errorf("reference %q carries no URL — there is nothing to chain into its page renders", ref.Name)
		}
		want := pngBytes("MIRA-SHEET")
		if ref.Name == "Bramble" {
			want = pngBytes("BRAMBLE-SHEET")
		}
		if string(ref.Image) != string(want) {
			t.Errorf("reference %q image = %.20q, want the bytes served at its URL", ref.Name, ref.Image)
		}
	}
	calls, _ := fake.snapshot()
	var edits []imagerCall
	for _, c := range calls {
		if c.kind == "edit" {
			edits = append(edits, c)
		}
	}
	if len(edits) != 2 {
		t.Fatalf("edit calls = %d, want 2", len(edits))
	}
	want := map[string][]string{
		"Mira opens the garden gate.":             {miraURL},
		"Bramble digs a hole while Mira watches.": {brambleURL, miraURL}, // named order: Bramble first
	}
	for _, e := range edits {
		matched := false
		for lead, urls := range want {
			if strings.HasPrefix(e.prompt, lead) {
				matched = true
				if strings.Join(e.refs, " ") != strings.Join(urls, " ") {
					t.Errorf("page %q was rendered against %v, want %v", lead, e.refs, urls)
				}
			}
		}
		if !matched {
			t.Errorf("unrecognised page prompt %q", e.prompt)
		}
	}
}

// TestIllustrate_NeverCallsGenerateImageForAPage is the negative half
// of lock 2. A silent text-to-image fallback is the failure mode this
// track exists to prevent, and it is invisible in the output: the
// picture comes back looking fine, with a different cast.
func TestIllustrate_NeverCallsGenerateImageForAPage(t *testing.T) {
	fake := &fakeImager{}
	s := twoCastStory()
	if _, err := Illustrate(context.Background(), Config{Imager: fake}, s); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	calls, _ := fake.snapshot()
	gens := 0
	for _, c := range calls {
		if c.kind == "generate" {
			gens++
		}
	}
	if gens != len(s.Cast) {
		t.Errorf("GenerateImage called %d times, want exactly %d (one per cast member); a page must never be text-to-image", gens, len(s.Cast))
	}
}

// ---------------------------------------------------------------
// defaults, exercised through their default paths
// ---------------------------------------------------------------

// TestIllustrate_DefaultModelReachesBothCallKinds is the rule that
// would have caught t2-round3.md H1 two rounds earlier: the default
// is asserted through the default path, on both call kinds, not just
// read off the constant.
func TestIllustrate_DefaultModelReachesBothCallKinds(t *testing.T) {
	fake := &fakeImager{}
	// Config.Model deliberately left empty.
	if _, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory()); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	calls, _ := fake.snapshot()
	if len(calls) == 0 {
		t.Fatal("no calls recorded")
	}
	for _, c := range calls {
		if c.model != DefaultModel {
			t.Errorf("%s call carried model %q, want the default %q", c.kind, c.model, DefaultModel)
		}
		if c.model == "" {
			t.Errorf("%s call carried an empty model; media.EditImage rejects one (t2-round3.md H1)", c.kind)
		}
		if IsForbiddenModel(c.model) {
			t.Errorf("%s call carried forbidden model %q", c.kind, c.model)
		}
	}
}

// TestDefaultModelIsNotForbidden pins the constant itself. It is a
// one-line test for a one-line defect that survived two review rounds
// and would have removed the whole of lock 2.
func TestDefaultModelIsNotForbidden(t *testing.T) {
	if DefaultModel != "seedream-5.0-lite" {
		t.Errorf("DefaultModel = %q, want seedream-5.0-lite (t6b-live-record.md: the one model measured generating)", DefaultModel)
	}
	if IsForbiddenModel(DefaultModel) {
		t.Errorf("DefaultModel %q is on the forbidden list", DefaultModel)
	}
}

// TestIsForbiddenModel pins the guard over the NORMALISED id
// (t6-round1.md M1): a vendor or registry prefix, a trailing
// separator and a tag suffix name the same upstream model and are
// the same refusal, and so is any video-model spelling — PLAN.md
// §T6's forbidden table reads "H3 / any video model", so the stems
// cover the family, not one literal id. The dead ids T6b measured
// (Flux2-Klein, Z-Image — they accept and never generate) are refused
// under decorated spellings too. The sanctioned ids (DefaultModel,
// gemini-2.5-flash-image) stay admissible.
func TestIsForbiddenModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"Qwen-Image-2512", true},
		{"qwen-image-2512", true},   // a case variant is the same upstream model
		{" Qwen-Image-2512 ", true}, // and so is a padded one
		{"Qwen/Qwen-Image-2512", true},
		{"MiniMaxAI/Qwen-Image-2512", true},
		{"Qwen-Image-2512/", true},
		{"qwen-image-2512:latest", true},
		{"H3", true},
		{"H3-2", true},
		{"MiniMax-Hailuo-02", true},
		{"minimax-video-01", true},                  // the "video" stem: PLAN.md §T6's "any video model"
		{"MiniMaxAI/minimax-video-01:latest", true}, // decorated video ids refuse too
		{"Flux2-Klein", true},                       // T6b: accepts, never generates (t6b-live-record.md §1)
		{"registry/Flux2-Klein", true},              // a decorated dead id is the same dead id
		{"Z-Image", true},                           // T6b: same
		{"z-image:latest", true},
		{"gemini-2.5-flash-image", false},
		{"", false}, // empty is not forbidden; it is defaulted before this check
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			if got := IsForbiddenModel(tc.model); got != tc.want {
				t.Errorf("IsForbiddenModel(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}

// TestIllustrate_ForbiddenModelRefusedBeforeAnyCall pins that the
// refusal costs an error and not a book: Qwen-Image-2512 is a real,
// callable id, so an image-to-image call against it succeeds and
// silently drops the reference. The only place to stop it is here.
func TestIllustrate_ForbiddenModelRefusedBeforeAnyCall(t *testing.T) {
	for _, model := range []string{"Qwen-Image-2512", "qwen-image-2512", "MiniMaxAI/Qwen-Image-2512", "H3", "H3-2", "Flux2-Klein", "Z-Image"} {
		t.Run(model, func(t *testing.T) {
			fake := &fakeImager{}
			_, err := Illustrate(context.Background(), Config{Imager: fake, Model: model}, twoCastStory())
			if !errors.Is(err, ErrForbiddenModel) {
				t.Fatalf("err = %v, want ErrForbiddenModel", err)
			}
			if calls, _ := fake.snapshot(); len(calls) != 0 {
				t.Errorf("%d call(s) were made with a forbidden model: %+v", len(calls), calls)
			}
		})
	}
}

// TestIllustrate_DefaultLimitBoundsTheFanOut exercises Config.Limit's
// default path and proves the bound is real in both directions: no
// more than DefaultLimit renders run at once (AGENTS.md §Concurrency
// forbids an unbounded go loop), and no fewer, or the barrier below
// never opens.
func TestIllustrate_DefaultLimitBoundsTheFanOut(t *testing.T) {
	s := twoCastStory()
	s.Pages = nil
	for n := 1; n <= 8; n++ {
		s.Pages = append(s.Pages, page(n, fmt.Sprintf("Scene %d with Mira.", n), "Mira"))
	}

	var inFlight, peak atomic.Int32
	gate := make(chan struct{})
	var once sync.Once
	fake := &fakeImager{
		edit: func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			if n >= int32(DefaultLimit) {
				once.Do(func() { close(gate) })
			}
			select {
			case <-gate:
			case <-time.After(10 * time.Second):
				return nil, errors.New("timed out waiting for the fan-out to reach the limit")
			}
			return queueEnvelope(fixtureMedia.serve("page", pngBytes("page:"+prompt))), nil
		},
	}
	// Config.Limit deliberately left zero.
	if _, err := Illustrate(context.Background(), Config{Imager: fake}, s); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if got := peak.Load(); got != int32(DefaultLimit) {
		t.Errorf("peak concurrent page renders = %d, want exactly %d", got, DefaultLimit)
	}
}

// TestIllustrate_ExplicitLimitIsHonoured is the non-default half: the
// switch has to actually switch, or the default test proves nothing.
func TestIllustrate_ExplicitLimitIsHonoured(t *testing.T) {
	s := twoCastStory()
	s.Pages = nil
	for n := 1; n <= 6; n++ {
		s.Pages = append(s.Pages, page(n, fmt.Sprintf("Scene %d with Mira.", n), "Mira"))
	}
	var inFlight, peak atomic.Int32
	fake := &fakeImager{
		edit: func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			return queueEnvelope(fixtureMedia.serve("page", pngBytes("page:"+prompt))), nil
		},
	}
	if _, err := Illustrate(context.Background(), Config{Imager: fake, Limit: 1}, s); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if got := peak.Load(); got != 1 {
		t.Errorf("peak concurrent page renders with Limit 1 = %d, want 1", got)
	}
}

// TestIllustrate_ExplicitModelIsHonoured is the provider switch
// project.md §3 asks for: one field, and every call moves.
func TestIllustrate_ExplicitModelIsHonoured(t *testing.T) {
	fake := &fakeImager{}
	const model = "gemini-2.5-flash-image"
	if _, err := Illustrate(context.Background(), Config{Imager: fake, Model: model}, twoCastStory()); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	calls, _ := fake.snapshot()
	for _, c := range calls {
		if c.model != model {
			t.Errorf("%s call carried model %q, want %q", c.kind, c.model, model)
		}
	}
}

// TestIllustrate_NilProgressIsFine exercises Config.Progress's zero
// value: the callback is optional and its absence must not panic.
func TestIllustrate_NilProgressIsFine(t *testing.T) {
	fake := &fakeImager{}
	if _, err := Illustrate(context.Background(), Config{Imager: fake, Progress: nil}, twoCastStory()); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
}

func TestIllustrate_ProgressReportsEveryRender(t *testing.T) {
	fake := &fakeImager{}
	var mu sync.Mutex
	var events []Progress
	cfg := Config{Imager: fake, Progress: func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, p)
	}}
	s := twoCastStory()
	if _, err := Illustrate(context.Background(), cfg, s); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	wantTotal := len(s.Cast) + len(s.Pages)
	if len(events) != wantTotal {
		t.Fatalf("progress events = %d, want %d", len(events), wantTotal)
	}
	seenDone := make(map[int]bool)
	refs, pages := 0, 0
	for _, e := range events {
		if e.Total != wantTotal {
			t.Errorf("event %+v: Total = %d, want %d", e, e.Total, wantTotal)
		}
		if seenDone[e.Done] {
			t.Errorf("Done = %d reported twice; the counter must be serialised", e.Done)
		}
		seenDone[e.Done] = true
		switch e.Stage {
		case StageReference:
			refs++
			if e.Name == "" || e.N != 0 {
				t.Errorf("reference event %+v: want a Name and no page number", e)
			}
		case StageIllustration:
			pages++
			if e.Name != "" || e.N == 0 {
				t.Errorf("illustration event %+v: want a page number and no Name", e)
			}
		default:
			t.Errorf("unknown stage %q", e.Stage)
		}
	}
	if refs != len(s.Cast) || pages != len(s.Pages) {
		t.Errorf("got %d reference and %d page events, want %d and %d", refs, pages, len(s.Cast), len(s.Pages))
	}
}

// ---------------------------------------------------------------
// error branches
// ---------------------------------------------------------------

func TestIllustrate_ErrorBranches(t *testing.T) {
	drawable := []story.CastMember{{Name: "Mira", Visual: miraVisual}}
	tests := []struct {
		name string
		cfg  Config
		s    story.Story
		want error
	}{
		{
			name: "nil imager",
			cfg:  Config{},
			s:    twoCastStory(),
			want: ErrNoImager,
		},
		{
			name: "forbidden model",
			cfg:  Config{Imager: &fakeImager{}, Model: "Qwen-Image-2512"},
			s:    twoCastStory(),
			want: ErrForbiddenModel,
		},
		{
			name: "empty cast",
			cfg:  Config{Imager: &fakeImager{}},
			s:    story.Story{Title: "x", Pages: []story.Page{page(1, "p", "Mira")}},
			want: ErrInvalidCast,
		},
		{
			name: "cast member with an empty name",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast:  []story.CastMember{{Name: "  ", Visual: miraVisual}},
				Pages: []story.Page{page(1, "p", "Mira")},
			},
			want: ErrInvalidCast,
		},
		{
			name: "cast member with an empty visual",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast:  []story.CastMember{{Name: "Mira", Visual: "   "}},
				Pages: []story.Page{page(1, "p", "Mira")},
			},
			want: ErrInvalidCast,
		},
		{
			name: "cast names differing only by case",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast: []story.CastMember{
					{Name: "Mira", Visual: miraVisual},
					{Name: "mira", Visual: brambleVisual},
				},
				Pages: []story.Page{page(1, "p", "Mira")},
			},
			want: ErrInvalidCast,
		},
		{
			name: "no pages",
			cfg:  Config{Imager: &fakeImager{}},
			s:    story.Story{Title: "x", Cast: drawable},
			want: ErrNoPages,
		},
		{
			name: "no cast member has an appearance",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast:  []story.CastMember{{Name: "Narrator", Visual: liveNarratorVisual2}},
				Pages: []story.Page{page(1, "p", "Narrator")},
			},
			want: ErrNoReference,
		},
		{
			name: "page names nobody",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast:  drawable,
				Pages: []story.Page{page(1, "an empty landscape")},
			},
			want: ErrNoReference,
		},
		{
			name: "page names someone outside the cast",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast:  drawable,
				Pages: []story.Page{page(1, "p", "Mira"), page(2, "p", "Gerald")},
			},
			want: ErrNoReference,
		},
		{
			name: "page names only members with no sheet",
			cfg:  Config{Imager: &fakeImager{}},
			s: story.Story{
				Cast: []story.CastMember{
					{Name: "Mira", Visual: miraVisual},
					{Name: "Narrator", Visual: liveNarratorVisual1},
				},
				Pages: []story.Page{page(1, "p", "Mira"), page(2, "the narrator muses", "Narrator")},
			},
			want: ErrNoReference,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			book, err := Illustrate(context.Background(), tc.cfg, tc.s)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(book.References) != 0 || len(book.Pages) != 0 || len(book.Skipped) != 0 {
				t.Errorf("book = %+v on error, want the zero Book", book)
			}
			if f, ok := tc.cfg.Imager.(*fakeImager); ok {
				if calls, _ := f.snapshot(); len(calls) != 0 {
					t.Errorf("%d paid call(s) made before the failure: %+v", len(calls), calls)
				}
			}
		})
	}
}

// TestIllustrate_GMISentinelsSurvive pins PLAN.md invariant 8 across
// this package's boundary: an internal/gmi sentinel reaches the
// caller matchable with errors.Is, never as a matched substring —
// and, per the zero-Book contract, an error never carries a partial
// book with it (t6-round1.md M2: these render paths are exactly
// where a caller is most likely to be handed a half-book).
func TestIllustrate_GMISentinelsSurvive(t *testing.T) {
	sentinels := []error{
		gmi.ErrModelNotFound,
		gmi.ErrUnauthorized,
		gmi.ErrRateLimited,
		gmi.ErrPaymentRequired,
		gmi.ErrBadRequest,
		gmi.ErrTransient,
	}
	for _, want := range sentinels {
		t.Run(want.Error(), func(t *testing.T) {
			t.Run("from a reference sheet", func(t *testing.T) {
				fake := &fakeImager{generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
					return nil, fmt.Errorf("media: %w: upstream said so", want)
				}}
				book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want %v", err, want)
				}
				assertZeroBook(t, book)
			})
			t.Run("from a page", func(t *testing.T) {
				fake := &fakeImager{edit: func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
					return nil, fmt.Errorf("media: %w: upstream said so", want)
				}}
				book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want %v", err, want)
				}
				assertZeroBook(t, book)
			})
		})
	}
}

// TestIllustrate_NoSecondRetryLayer pins internal/gmi/errors.go's
// contract: the media client already retries once internally, and
// this package must not stack a second layer on top of it. One
// failure per page is one call, not two.
func TestIllustrate_NoSecondRetryLayer(t *testing.T) {
	var gens, edits atomic.Int32
	fake := &fakeImager{
		generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
			gens.Add(1)
			return nil, fmt.Errorf("boom: %w", gmi.ErrTransient)
		},
	}
	if _, err := Illustrate(context.Background(), Config{Imager: fake, Limit: 1}, twoCastStory()); !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want gmi.ErrTransient", err)
	}
	if got := gens.Load(); got > 2 {
		t.Errorf("GenerateImage called %d times for 2 cast members; this package must add no retry of its own", got)
	}

	gens.Store(0)
	fake2 := &fakeImager{
		edit: func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
			edits.Add(1)
			return nil, fmt.Errorf("boom: %w", gmi.ErrTransient)
		},
	}
	if _, err := Illustrate(context.Background(), Config{Imager: fake2, Limit: 1}, twoCastStory()); !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want gmi.ErrTransient", err)
	}
	if got := edits.Load(); got > 2 {
		t.Errorf("EditImage called %d times for 2 pages; this package must add no retry of its own", got)
	}
}

// TestIllustrate_FirstFailureCancelsTheRest pins the errgroup
// contract: a failed render must stop the fan-out, not let the rest
// of the book keep spending.
func TestIllustrate_FirstFailureCancelsTheRest(t *testing.T) {
	s := twoCastStory()
	s.Pages = nil
	for n := 1; n <= 8; n++ {
		s.Pages = append(s.Pages, page(n, fmt.Sprintf("Scene %d with Mira.", n), "Mira"))
	}
	var seen, sawCancel atomic.Int32
	fake := &fakeImager{
		edit: func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
			if ctx.Err() != nil {
				// This is the assertion that matters: every render
				// after the first failure is handed an already-dead
				// context, so the media client aborts instead of
				// spending on the rest of the book.
				sawCancel.Add(1)
				return nil, ctx.Err()
			}
			if seen.Add(1) == 1 {
				return nil, fmt.Errorf("boom: %w", gmi.ErrBadRequest)
			}
			return queueEnvelope(fixtureMedia.serve("page", pngBytes("page"))), nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Illustrate(ctx, Config{Imager: fake, Limit: 1}, s); !errors.Is(err, gmi.ErrBadRequest) {
		t.Fatalf("err = %v, want gmi.ErrBadRequest", err)
	}
	if sawCancel.Load() == 0 {
		t.Errorf("no page render saw a cancelled context after the first failure; the group is not cancelling (seen=%d of %d)", seen.Load(), len(s.Pages))
	}
	if got := seen.Load(); got != 1 {
		t.Errorf("%d page renders ran to completion after the first failure, want 0", got-1)
	}
}

// TestIllustrate_ContextCancellationSurfaces covers the caller's own
// cancellation: the ctx error must reach the caller rather than being
// swallowed into a partial book.
func TestIllustrate_ContextCancellationSurfaces(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &fakeImager{generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
		cancel()
		return nil, ctx.Err()
	}}
	_, err := Illustrate(ctx, Config{Imager: fake, Limit: 1}, twoCastStory())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestIllustrate_LiveHazardStoryEndToEnd runs the two live Phase-B
// hazards through the whole package at once: a narrator that costs no
// image and two rivers that share one sheet.
func TestIllustrate_LiveHazardStoryEndToEnd(t *testing.T) {
	s := story.Story{
		Title: "The Grumpy River",
		Cast: []story.CastMember{
			{Name: "Narrator", Visual: liveNarratorVisual2},
			{Name: "Mira", Visual: miraVisual},
			{Name: "Grumpy River", Visual: liveGrumpyRiverVisual},
			{Name: "Happy River", Visual: liveHappyRiverVisual},
		},
		Pages: []story.Page{
			page(1, "Mira meets the sulking river.", "Narrator", "Mira", "Grumpy River"),
			page(2, "The river cheers up.", "Narrator", "Happy River"),
		},
	}
	fake := &fakeImager{}
	book, err := Illustrate(context.Background(), Config{Imager: fake}, s)
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.References) != 2 {
		t.Fatalf("References = %v, want 2 (Mira and one river) — a narrator sheet is a wasted paid call and two river sheets are two rivers", refNames(book.References))
	}
	if book.References[0].Name != "Mira" || book.References[1].Name != "Grumpy River" {
		t.Errorf("References = %v, want [Mira Grumpy River]", refNames(book.References))
	}
	if book.Pages[1].Reference != "Grumpy River" {
		t.Errorf("page 2 (Happy River) rendered against %q, want Grumpy River's sheet", book.Pages[1].Reference)
	}
	// The narrator has no appearance, so nothing of theirs is pasted;
	// the rivers' own visuals still are, verbatim.
	for _, p := range fake.prompts("edit") {
		if strings.Contains(p, liveNarratorVisual2) {
			t.Errorf("a page prompt pasted the narrator's non-appearance:\n%s", p)
		}
	}
	// The variant shares a sheet but keeps its own visual verbatim, or
	// the mood change it describes cannot render. Edit prompts arrive
	// in fan-out order, not page order, so search rather than index.
	foundVariantVisual := false
	for _, p := range fake.prompts("edit") {
		if strings.Contains(p, liveHappyRiverVisual) {
			foundVariantVisual = true
		}
	}
	if !foundVariantVisual {
		t.Errorf("no page prompt carried Happy River's own visual verbatim:\n%s", strings.Join(fake.prompts("edit"), "\n---\n"))
	}
	wantSkips := []Skip{
		{Name: "Narrator", Visual: liveNarratorVisual2, Reason: SkipNoAppearance},
		{Name: "Happy River", Visual: liveHappyRiverVisual, Reason: SkipVariant, SheetOf: "Grumpy River"},
	}
	if len(book.Skipped) != len(wantSkips) {
		t.Fatalf("Skipped = %+v, want %+v", book.Skipped, wantSkips)
	}
	for i := range wantSkips {
		if book.Skipped[i] != wantSkips[i] {
			t.Errorf("Skipped[%d] = %+v, want %+v", i, book.Skipped[i], wantSkips[i])
		}
	}
}

// TestIllustrate_DrawableCharactersWithAbsenceWordsRender is H3's
// whole-book pin: a ghost with no face, a faceless rag doll and a
// snowman are the cast of a six-year-old's story, and under the old
// substring match the word "no face" alone was enough for Illustrate
// to return no book at all. Every page names only its hazard
// character, so a single false skip fails the run.
func TestIllustrate_DrawableCharactersWithAbsenceWordsRender(t *testing.T) {
	s := story.Story{
		Title: "Boo's Quiet Day",
		Cast: []story.CastMember{
			{Name: "Boo", Visual: "a shy little ghost with no face, just two floating eyes and a wobbly white sheet"},
			{Name: "Dolly", Visual: "a faceless rag doll with button eyes sewn on crooked"},
			{Name: "Snowy", Visual: "a snowman in a top hat who is never seen without his red scarf"},
		},
		Pages: []story.Page{
			page(1, "Boo floats through the wall.", "Boo"),
			page(2, "Dolly waves a crooked arm.", "Dolly"),
			page(3, "Snowy sleds down the hill.", "Snowy"),
		},
	}
	fake := &fakeImager{}
	book, err := Illustrate(context.Background(), Config{Imager: fake}, s)
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.References) != 3 {
		t.Fatalf("References = %v, want 3 — each of these characters is drawable in one line", refNames(book.References))
	}
	if len(book.Pages) != 3 {
		t.Fatalf("Pages = %d, want 3", len(book.Pages))
	}
	for i, want := range []string{"Boo", "Dolly", "Snowy"} {
		if book.Pages[i].Reference != want {
			t.Errorf("page %d rendered against %q, want %q", book.Pages[i].N, book.Pages[i].Reference, want)
		}
	}
	if len(book.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none: absence words mid-description are not absence assertions", book.Skipped)
	}
}

func refNames(refs []Reference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

// assertZeroBook fails t unless book is the zero Book: Illustrate's
// contract is that an error never carries a partial book
// (t6-round1.md M2 — unpinned on the render paths, where a mutant
// returning the sheets beside the error survived the whole suite).
func assertZeroBook(t *testing.T, book Book) {
	t.Helper()
	if book.References != nil || book.Pages != nil || book.Skipped != nil {
		t.Errorf("book = %+v beside a non-nil error, want the zero Book", book)
	}
}

// TestIllustrate_UndecodableResponses covers the branch between a
// successful call and a usable picture: the provider answered, and
// what came back has no image in it. Both call kinds must fail
// loudly, with the zero Book, rather than carry an empty Reference
// into the next phase.
func TestIllustrate_UndecodableResponses(t *testing.T) {
	t.Run("reference sheet", func(t *testing.T) {
		fake := &fakeImager{generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
			return []byte(`{"request_id":"r1","status":"success","outcome":{}}`), nil
		}}
		book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
		if !errors.Is(err, ErrNoImage) {
			t.Fatalf("err = %v, want ErrNoImage", err)
		}
		assertZeroBook(t, book)
	})
	t.Run("page", func(t *testing.T) {
		fake := &fakeImager{edit: func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
			return gifBytes(), nil
		}}
		book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
		if !errors.Is(err, ErrUnsupportedImage) {
			t.Fatalf("err = %v, want ErrUnsupportedImage", err)
		}
		assertZeroBook(t, book)
	})
}
