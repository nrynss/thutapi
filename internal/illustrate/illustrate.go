// Package illustrate renders a story.Story into pictures: one
// reference sheet per drawable cast member, then one illustration per
// page, image-to-image against those sheets (PLAN.md §T6).
//
// # The three locks
//
// Character consistency, not image quality, is the technical problem
// (project.md §2): 2D cartoon is the easy style and every model does
// it, but the book falls apart if Mira's hair changes on page 4.
// Three locks hold the cast still, and each is pinned by a test that
// asserts against the marshalled request bytes rather than a Go
// struct:
//
//  1. Verbatim text lock. A cast member's Visual string is pasted
//     into every prompt that draws them, byte for byte.
//     ReferencePrompt and PagePrompt only ever concatenate: nothing
//     here paraphrases, truncates, normalises, case-folds or
//     regenerates a visual.
//  2. Image lock. Every reference sheet is generated first, and every
//     page is then produced with EditImage against the reference
//     sheets of every character it names — payload.image carries all
//     their sheet URLs (t6b-live-record.md item 1b: multi-reference
//     keeps both entities). A
//     page that names no character with a reference sheet is
//     ErrNoReference — a loud failure, never a quiet text-to-image
//     fallback, because a t2i page looks fine and has the wrong cast.
//  3. Style lock. StyleSuffix closes every prompt, unchanged.
//
// # The model
//
// DefaultModel is seedream-5.0-lite (project.md §3 as amended by
// t6b-live-record.md: the only image model measured generating on the
// live request queue — synchronous success on the POST itself). §3's
// original "start on" pair do not get a Config.Model mention as an
// option, because they are not options: Flux2-Klein and Z-Image accept
// a request and then never run — no error, a job parked in `queued`
// forever — so they sit on the forbidden list next to
// Qwen-Image-2512, for a different reason. Config.Model is still the
// one-line switch, to gemini-2.5-flash-image if the cast drifts.
// Price selects nothing: the working models price the same per book,
// and §3's column is per book, not per image.
//
// Qwen-Image-2512 is refused with ErrForbiddenModel before any call
// is made. It is text-to-image only: an image-to-image call against
// it succeeds, returns a plausible picture, and silently ignores the
// reference — which removes lock 2 with no error to notice and no log
// line to read. It is a real, callable model id, so nothing upstream
// rejects it; it shipped as T2's default for both image methods and
// survived two review rounds (adversarial-review/t2-round3.md H1/M2).
// Flux2-Klein and Z-Image are the same refusal: they accept and never
// finish, so a book generation would hang to its context deadline on
// every page (t6b-live-record.md §1). IsForbiddenModel is the check,
// it runs before the money does, and it normalises the id first, so a
// vendor prefix or registry tag on the same upstream model is the
// same refusal. Video models ("H3" / any video model, PLAN.md §T6's
// forbidden table) are refused the same way, by stem.
//
// # Shape of a run
//
// Illustrate validates the cast and every page prompt first, so a
// story that cannot be illustrated fails before a single paid call.
// It then generates all reference sheets, waits for every one of them
// (a page render has nothing to lock against until its sheet exists),
// and only then fans the pages out. Both phases use
// golang.org/x/sync/errgroup with SetLimit — never an unbounded go
// loop (AGENTS.md §Concurrency) — and the first failure cancels the
// rest.
//
// Configuration arrives through Config; this package reads no
// environment (PLAN.md invariant 2). The image client arrives as
// Imager, an interface declared here rather than in
// internal/gmi/media (invariant 3), so every path below is testable
// without HTTP. Errors cross the boundary as the sentinels below or
// as internal/gmi's, matched with errors.Is and never by substring
// (invariant 8); the one retry inside the media client is the only
// retry — this package adds no second layer (internal/gmi/errors.go).
//
// Illustrate returns the image bytes rather than persisting them.
// That is deliberate: T7's consistency check regenerates a page that
// drifted, and persisting inside the render loop would store pictures
// T7 is about to discard. The bytes are materialised on receipt —
// a request-queue result that names a storage.googleapis.com URL is
// downloaded immediately, since those links expire (PLAN.md
// invariant 7) — and Reference.ContentType / Illustration.ContentType
// are already in the closed set internal/mediastore accepts, so the
// caller's Persist cannot fail on a type this package let through.
package illustrate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"thutapi/internal/gmi/media"
	"thutapi/internal/story"
)

// DefaultModel is the image model every call uses unless Config.Model
// names another: seedream-5.0-lite, the one model measured generating
// on the live request queue (project.md §3 as amended by
// t6b-live-record.md — synchronous success on the POST itself). This
// constant and Config.Model together are the one-line provider switch
// §3 asks for — the choice may flip to gemini-2.5-flash-image if the
// cast drifts. Flux2-Klein and Z-Image are forbidden, not options:
// they accept a request and never run (t6b-live-record.md §1).
const DefaultModel = "seedream-5.0-lite"

// DefaultLimit is how many renders run at once when Config.Limit is
// unset: PLAN.md §T6's "bounded to ~4 concurrent".
const DefaultLimit = 4

// Stage names the kind of render a Progress event reports. The values
// match store.MediaKind's "reference" and "illustration" so a caller
// wiring progress to the media store has nothing to translate.
const (
	// StageReference is one cast member's reference sheet.
	StageReference = "reference"
	// StageIllustration is one page's picture.
	StageIllustration = "illustration"
)

// Sentinel errors this package declares. Every one is matched with
// errors.Is; none is ever produced by matching a provider's message
// (PLAN.md invariant 8). internal/gmi's sentinels pass through
// untouched — a 404 from the request queue reaches the caller as
// gmi.ErrModelNotFound, wrapped with which sheet or page it was for.
var (
	// ErrNoImager reports a Config with a nil Imager. Nothing can be
	// rendered without an image client.
	ErrNoImager = errors.New("illustrate: no imager configured")

	// ErrForbiddenModel reports a model id this package refuses to
	// call. See IsForbiddenModel: the refusal happens before any
	// request is sent, because the damage these ids do is silent.
	ErrForbiddenModel = errors.New("illustrate: forbidden image model")

	// ErrInvalidCast reports a cast bible that cannot anchor the
	// character lock: no members at all, a member with an empty name
	// or an empty visual, or two members whose names differ only by
	// case (one character with two spellings, which would get two
	// unrelated reference sheets).
	ErrInvalidCast = errors.New("illustrate: invalid cast")

	// ErrNoPages reports a story with no pages. There is nothing to
	// illustrate, and returning an empty book would look like success.
	ErrNoPages = errors.New("illustrate: story has no pages")

	// ErrNoReference reports a page that has no reference sheet to be
	// rendered against: it names no cast member, names one that is not
	// in the cast bible, or names only members with no sheet. This is
	// the image lock's loud failure. There is deliberately no
	// text-to-image fallback — a t2i page succeeds, looks fine, and
	// has a different cast, which is the whole failure this track
	// exists to prevent (PLAN.md §T6 lock 2).
	ErrNoReference = errors.New("illustrate: no reference sheet for page")

	// ErrNoImage reports a request-queue response that carried no
	// usable image: not image bytes, no data: URI, no image URL, or a
	// URL that did not answer with one.
	ErrNoImage = errors.New("illustrate: response carried no image")

	// ErrUnsupportedImage reports image bytes in a format nothing
	// downstream can store or show. The accepted set is image/png,
	// image/jpeg and image/webp — internal/mediastore's image set, so
	// an image that passes here can always be persisted.
	ErrUnsupportedImage = errors.New("illustrate: unsupported image type")
)

// forbiddenModels are the model ids IsForbiddenModel refuses, compared
// against the normalised id (see normaliseModelID).
//
//   - Qwen-Image-2512 is text-to-image only. An image-to-image call
//     against it succeeds and ignores the reference image, so the
//     character lock is gone and nothing errors (project.md §3, which
//     strikes the id through; PLAN.md §T6's forbidden table;
//     t2-round3.md H1/M2, where it shipped as the default for both
//     image methods and survived two review rounds).
//   - Flux2-Klein and Z-Image never generate: the queue accepts the
//     POST and the job sits in `queued` forever, with no error — a
//     book would hang to its deadline on every page
//     (t6b-live-record.md §1). PLAN.md §T6 told this track to start
//     on them; T6b measured that they do not work, and the forbidden
//     table is where a dead id belongs.
var forbiddenModels = []string{"Qwen-Image-2512", "Flux2-Klein", "Z-Image"}

// forbiddenModelStems are substrings that mark a model id as a video
// model — PLAN.md §T6's forbidden table reads "H3 / any video model",
// and a literal-id match implements neither half: "H3-2" and
// "MiniMax-Hailuo-02" are the same refusals under decorated
// spellings. Matched case-folded against the normalised id. The
// sanctioned ids (DefaultModel, gemini-2.5-flash-image) contain none
// of these stems; IsForbiddenModel's test pins that, so extending
// this list re-checks the allow-list by construction.
var forbiddenModelStems = []string{"h3", "hailuo", "video"}

// IsForbiddenModel reports whether model is one this package refuses
// to call. The comparison is over the normalised id: a vendor prefix,
// a registry tag or a case variant names the same upstream model and
// would do the same silent damage ("MiniMaxAI/Qwen-Image-2512" and
// "qwen-image-2512:latest" are Qwen-Image-2512; "H3-2" and
// "MiniMax-Hailuo-02" are the video models the table refuses).
//
// The check runs in Config.resolve, before any request is built, so a
// forbidden id costs an error rather than a book that looks right and
// has a different cast on every page.
func IsForbiddenModel(model string) bool {
	id := normaliseModelID(model)
	if id == "" {
		return false
	}
	for _, f := range forbiddenModels {
		if strings.EqualFold(id, f) {
			return true
		}
	}
	lower := strings.ToLower(id)
	for _, stem := range forbiddenModelStems {
		if strings.Contains(lower, stem) {
			return true
		}
	}
	return false
}

// normaliseModelID reduces a model id to its bare upstream name:
// surrounding space, a trailing path separator, any vendor/registry
// prefix ("MiniMaxAI/Qwen-Image-2512" — the prefix shape AGENTS.md
// trains callers to use on the text endpoint) and any tag suffix
// ("qwen-image-2512:latest") are stripped.
func normaliseModelID(model string) string {
	id := strings.TrimRight(strings.TrimSpace(model), "/")
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.Index(id, ":"); i >= 0 {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}

// renderer is a resolved Config: every default already substituted,
// so no code below re-reads Config and no default can be applied twice
// or in two ways.
type renderer struct {
	imager   Imager
	model    string
	limit    int
	http     *http.Client
	progress func(Progress)

	mu    sync.Mutex
	done  int
	total int
}

// resolve substitutes Config's defaults and rejects a Config that
// cannot render. It is the single place a default is applied, which
// is why the default-path tests can pin all of them at once.
func (cfg Config) resolve() (*renderer, error) {
	if cfg.Imager == nil {
		return nil, ErrNoImager
	}
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	if IsForbiddenModel(model) {
		return nil, fmt.Errorf("%w: %q is on PLAN.md §T6's forbidden table; use %s", ErrForbiddenModel, model, DefaultModel)
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultFetchTimeout, CheckRedirect: safeRedirectPolicy}
	}
	return &renderer{imager: cfg.Imager, model: model, limit: limit, http: hc, progress: cfg.Progress}, nil
}

// report publishes one Progress event. Calls are serialised under the
// renderer's mutex so a callback written without a lock of its own is
// still safe under the fan-out.
func (r *renderer) report(stage, name string, n int) {
	if r.progress == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.done++
	r.progress(Progress{Stage: stage, Name: name, N: n, Done: r.done, Total: r.total})
}

// Illustrate renders s: a reference sheet for every drawable cast
// member first, then one illustration per page, image-to-image
// against those sheets.
//
// Everything that can fail without spending money fails first — the
// configuration, the cast, and every page's prompt — so a story that
// cannot be illustrated costs nothing. Reference sheets then all
// complete before any page starts, because a page render has nothing
// to lock against until its sheet exists. Each phase fans out through
// errgroup bounded to Config.Limit, and the first error cancels the
// rest of that phase.
//
// The returned Book is zero on any error: a partially illustrated
// book is not a book.
func Illustrate(ctx context.Context, cfg Config, s story.Story) (Book, error) {
	r, err := cfg.resolve()
	if err != nil {
		return Book{}, err
	}
	if err := validateCast(s.Cast); err != nil {
		return Book{}, err
	}
	if len(s.Pages) == 0 {
		return Book{}, fmt.Errorf("%w: %q", ErrNoPages, s.Title)
	}

	plan := PlanReferences(s)
	if len(plan.Sheets) == 0 {
		return Book{}, fmt.Errorf("%w: no cast member has an appearance to draw, so no page can be locked to one", ErrNoReference)
	}

	// Build every page prompt up front, and resolve which reference
	// sheets each page renders against — one per character the page
	// names, so payload.image can carry them all. A page with no sheet
	// is ErrNoReference here, before the first paid call — discovering
	// it after ten images have been generated would cost the whole
	// book's spend to learn the same thing.
	refIndex := sheetIndex(plan)
	prompts := make([]string, len(s.Pages))
	bases := make([][]int, len(s.Pages))
	for i, p := range s.Pages {
		prompt, base, err := buildPage(p, s.Cast, plan, refIndex)
		if err != nil {
			return Book{}, err
		}
		prompts[i] = prompt
		bases[i] = base
	}

	r.total = len(plan.Sheets) + len(s.Pages)

	refs, err := r.renderReferences(ctx, plan.Sheets)
	if err != nil {
		return Book{}, err
	}
	pages, err := r.renderPages(ctx, s.Pages, prompts, bases, refs)
	if err != nil {
		return Book{}, err
	}
	return Book{References: refs, Pages: pages, Skipped: plan.Skipped}, nil
}

// renderReferences generates every reference sheet and returns them in
// cast order. It returns only when all of them are done: PLAN.md §T6
// lock 2 is "one reference image per cast member first, then every
// page as image-to-image against it", and a page that started early
// would have no sheet to use.
func (r *renderer) renderReferences(ctx context.Context, members []story.CastMember) ([]Reference, error) {
	refs := make([]Reference, len(members))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.limit)
	for i, m := range members {
		g.Go(func() error {
			prompt := ReferencePrompt(m)
			raw, err := r.imager.GenerateImage(gctx, prompt, r.model, media.ImageOptions{})
			if err != nil {
				return fmt.Errorf("illustrate: reference sheet for %q: %w", m.Name, err)
			}
			// GenerateImage sends no reference image, so nothing the
			// response could echo is excluded from the walk.
			img, ct, url, err := r.decodeImage(gctx, raw, sentImages{})
			if err != nil {
				return fmt.Errorf("illustrate: reference sheet for %q: %w", m.Name, err)
			}
			if url == "" {
				// Lock 2 chains this sheet's URL into every page
				// render that names the member (payload.image is an
				// array of URLs). Bytes with no URL cannot be
				// chained, so they are not a successful sheet.
				return fmt.Errorf("illustrate: reference sheet for %q decoded to bytes but the response named no media URL, so there is nothing to chain into the page renders", m.Name)
			}
			refs[i] = Reference{Name: m.Name, Visual: m.Visual, Prompt: prompt, ContentType: ct, Image: img, URL: url}
			r.report(StageReference, m.Name, 0)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return refs, nil
}

// renderPages renders every page image-to-image against its reference
// sheets and returns them in page order. prompts and bases are indexed
// alongside pages; bases[i] holds the indexes into refs of the sheets
// page i is locked to — one per character the page names, resolved by
// the same buildPage pass that wrote the prompt — so the attached
// references and the prompt's claim about them cannot disagree.
//
// EditImage is the only call here. There is no GenerateImage fallback
// for a page, by design: image-to-image against the sheets is lock 2,
// and a page that quietly fell back to text-to-image would come back
// looking fine with a different cast. The references travel as the
// sheets' URLs, every named character's sheet among them
// (t6b-live-record.md item 1b: multi-reference is live-verified and
// keeps every entity).
func (r *renderer) renderPages(ctx context.Context, pages []story.Page, prompts []string, bases [][]int, refs []Reference) ([]Illustration, error) {
	out := make([]Illustration, len(pages))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(r.limit)
	for i, p := range pages {
		var (
			sheetURLs  []string
			sheetBytes [][]byte
			lead       Reference
		)
		for j, bi := range bases[i] {
			ref := refs[bi]
			sheetURLs = append(sheetURLs, ref.URL)
			sheetBytes = append(sheetBytes, ref.Image)
			if j == 0 {
				lead = ref
			}
		}
		g.Go(func() error {
			raw, err := r.imager.EditImage(gctx, prompts[i], r.model, sheetURLs, media.ImageOptions{})
			if err != nil {
				return fmt.Errorf("illustrate: page %d: %w", p.N, err)
			}
			// The reference URLs in payload.image come back echoed in
			// the record's payload (t6b-live-record.md §3); what this
			// call sent can never be the result, so the decode excludes
			// it explicitly.
			img, ct, _, err := r.decodeImage(gctx, raw, sentImages{bytes: sheetBytes, urls: sheetURLs})
			if err != nil {
				return fmt.Errorf("illustrate: page %d: %w", p.N, err)
			}
			out[i] = Illustration{N: p.N, Prompt: prompts[i], Reference: lead.Name, ContentType: ct, Image: img}
			r.report(StageIllustration, "", p.N)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// validateCast checks the cast bible can anchor the character lock.
// It deliberately does not call story.Validate: that enforces exactly
// story.PageCount pages, which is Phase B's contract, not a condition
// for drawing a picture — T7 re-renders single pages through this
// same package.
func validateCast(cast []story.CastMember) error {
	if len(cast) == 0 {
		return fmt.Errorf("%w: the cast is empty, so there is nothing to lock characters to", ErrInvalidCast)
	}
	seen := make([]string, 0, len(cast))
	for i, m := range cast {
		if strings.TrimSpace(m.Name) == "" {
			return fmt.Errorf("%w: cast member %d has an empty name", ErrInvalidCast, i)
		}
		if strings.TrimSpace(m.Visual) == "" {
			return fmt.Errorf("%w: cast member %q has an empty visual, so lock 1 has nothing to paste", ErrInvalidCast, m.Name)
		}
		for _, prev := range seen {
			if strings.EqualFold(prev, m.Name) {
				return fmt.Errorf("%w: cast names %q and %q differ only by case; one character with two spellings would get two unrelated reference sheets", ErrInvalidCast, prev, m.Name)
			}
		}
		seen = append(seen, m.Name)
	}
	return nil
}
