package illustrate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"thutapi/internal/gmi"
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

// queueEnvelope wraps image bytes the way the GMI request queue
// answers: a JSON record with the picture inside it, not raw bytes.
// Confirmed live on 2026-09-05 — a completed TTS request came back as
// {"request_id":...,"status":"success","outcome":{"audio_url":...}}.
func queueEnvelope(img []byte) []byte {
	body, err := json.Marshal(map[string]any{
		"request_id": "req-fixture",
		"status":     "success",
		"outcome": map[string]any{
			"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(img),
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
	ref    []byte
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

	generate func(ctx context.Context, prompt, model string) ([]byte, error)
	edit     func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error)
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

func (f *fakeImager) GenerateImage(ctx context.Context, prompt, model string) ([]byte, error) {
	f.record(imagerCall{kind: "generate", prompt: prompt, model: model}, "gen:start")
	defer f.mark("gen:end")
	if f.generate != nil {
		return f.generate(ctx, prompt, model)
	}
	return queueEnvelope(pngBytes("ref:" + prompt)), nil
}

func (f *fakeImager) EditImage(ctx context.Context, refImage []byte, prompt, model string) ([]byte, error) {
	f.record(imagerCall{kind: "edit", prompt: prompt, model: model, ref: refImage}, "edit:start")
	defer f.mark("edit:end")
	if f.edit != nil {
		return f.edit(ctx, refImage, prompt, model)
	}
	return queueEnvelope(pngBytes("page:" + prompt)), nil
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

// TestIllustrate_PagesUseTheirOwnReferenceSheetBytes is lock 2's
// content half: the bytes attached to a page must be the bytes of
// that page's reference sheet, not some other character's.
func TestIllustrate_PagesUseTheirOwnReferenceSheetBytes(t *testing.T) {
	fake := &fakeImager{
		generate: func(ctx context.Context, prompt, model string) ([]byte, error) {
			// Each sheet gets distinguishable bytes, keyed on the
			// character its prompt names.
			switch {
			case strings.Contains(prompt, "Mira"):
				return queueEnvelope(pngBytes("MIRA-SHEET")), nil
			case strings.Contains(prompt, "Bramble"):
				return queueEnvelope(pngBytes("BRAMBLE-SHEET")), nil
			}
			return nil, fmt.Errorf("unexpected reference prompt %q", prompt)
		},
	}
	if _, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory()); err != nil {
		t.Fatalf("Illustrate: %v", err)
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
	want := map[string][]byte{
		"Mira opens the garden gate.":             pngBytes("MIRA-SHEET"),
		"Bramble digs a hole while Mira watches.": pngBytes("BRAMBLE-SHEET"),
	}
	for _, e := range edits {
		matched := false
		for lead, sheet := range want {
			if strings.HasPrefix(e.prompt, lead) {
				matched = true
				if string(e.ref) != string(sheet) {
					t.Errorf("page %q was rendered against %q, want %q", lead, e.ref, sheet)
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
	if DefaultModel != "Flux2-Klein" {
		t.Errorf("DefaultModel = %q, want Flux2-Klein (project.md §3 \"Start on\")", DefaultModel)
	}
	if IsForbiddenModel(DefaultModel) {
		t.Errorf("DefaultModel %q is on the forbidden list", DefaultModel)
	}
}

func TestIsForbiddenModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"Qwen-Image-2512", true},
		{"qwen-image-2512", true},   // a case variant is the same upstream model
		{" Qwen-Image-2512 ", true}, // and so is a padded one
		{"H3", true},
		{"Flux2-Klein", false},
		{"Z-Image", false},
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
	for _, model := range []string{"Qwen-Image-2512", "qwen-image-2512", "H3"} {
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
		edit: func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error) {
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
			return queueEnvelope(pngBytes("page:" + prompt)), nil
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
		edit: func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error) {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			return queueEnvelope(pngBytes("page:" + prompt)), nil
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
// caller matchable with errors.Is, never as a matched substring.
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
				fake := &fakeImager{generate: func(ctx context.Context, prompt, model string) ([]byte, error) {
					return nil, fmt.Errorf("media: %w: upstream said so", want)
				}}
				_, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want %v", err, want)
				}
			})
			t.Run("from a page", func(t *testing.T) {
				fake := &fakeImager{edit: func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error) {
					return nil, fmt.Errorf("media: %w: upstream said so", want)
				}}
				_, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want %v", err, want)
				}
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
		generate: func(ctx context.Context, prompt, model string) ([]byte, error) {
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
		edit: func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error) {
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
		edit: func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error) {
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
			return queueEnvelope(pngBytes("page")), nil
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
	fake := &fakeImager{generate: func(ctx context.Context, prompt, model string) ([]byte, error) {
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

func refNames(refs []Reference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

// TestIllustrate_UndecodableResponses covers the branch between a
// successful call and a usable picture: the provider answered, and
// what came back has no image in it. Both call kinds must fail loudly
// rather than carry an empty Reference into the next phase.
func TestIllustrate_UndecodableResponses(t *testing.T) {
	t.Run("reference sheet", func(t *testing.T) {
		fake := &fakeImager{generate: func(ctx context.Context, prompt, model string) ([]byte, error) {
			return []byte(`{"request_id":"r1","status":"success","outcome":{}}`), nil
		}}
		_, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
		if !errors.Is(err, ErrNoImage) {
			t.Fatalf("err = %v, want ErrNoImage", err)
		}
	})
	t.Run("page", func(t *testing.T) {
		fake := &fakeImager{edit: func(ctx context.Context, ref []byte, prompt, model string) ([]byte, error) {
			return gifBytes(), nil
		}}
		_, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
		if !errors.Is(err, ErrUnsupportedImage) {
			t.Fatalf("err = %v, want ErrUnsupportedImage", err)
		}
	})
}
