package illustrate

import (
	"context"
	"net/http"
)

// Imager is illustrate's view of the GMI request-queue image client
// (PLAN.md invariant 3: the consumer declares the interface, as
// narrow as it uses). It is satisfied by *media.Client; tests
// substitute a fake, so nothing below needs HTTP to be exercised.
//
// Both methods take an explicit model. media.EditImage rejects an
// empty one — image-to-image is what carries the character reference,
// and a silent default there is exactly how the forbidden model
// shipped (t2-round3.md H1) — so this package always passes the
// resolved id, never "".
type Imager interface {
	// GenerateImage runs text-to-image and returns the raw
	// request-queue response body.
	GenerateImage(ctx context.Context, prompt, model string) ([]byte, error)
	// EditImage runs image-to-image against refImage and returns the
	// raw request-queue response body.
	EditImage(ctx context.Context, refImage []byte, prompt, model string) ([]byte, error)
}

// Config configures Illustrate. Config flows down from the caller;
// this package reads no environment (PLAN.md invariant 2).
type Config struct {
	// Imager is the image client. Required — a nil Imager is
	// ErrNoImager.
	Imager Imager

	// Model is the GMI image model id for every call, reference
	// sheets and pages alike. Empty means DefaultModel. A forbidden
	// id (IsForbiddenModel) is ErrForbiddenModel, checked before any
	// request is sent.
	Model string

	// Limit bounds how many renders are in flight at once. Zero or
	// negative means DefaultLimit.
	Limit int

	// HTTPClient fetches an image when the request queue answers with
	// a URL instead of inline bytes. Nil means a client with
	// defaultFetchTimeout. It never talks to GMI — that stays behind
	// internal/gmi (PLAN.md invariant 1).
	HTTPClient *http.Client

	// Progress, when non-nil, is called once per completed render.
	// Calls are serialised, so the callback needs no lock of its own;
	// it runs on a render goroutine, so it should not block.
	Progress func(Progress)
}

// Progress reports one completed render.
type Progress struct {
	// Stage is StageReference or StageIllustration.
	Stage string
	// Name is the cast member, for a reference sheet; empty for a page.
	Name string
	// N is the page number, for a page; zero for a reference sheet.
	N int
	// Done counts renders finished so far, Total the renders this run
	// will make. Both count reference sheets and pages together, so
	// Done/Total is the whole book's progress.
	Done, Total int
}

// Reference is one cast member's generated reference sheet: the image
// every page render of that member is locked to (lock 2).
type Reference struct {
	// Name is the cast member the sheet was generated for.
	Name string
	// Visual is that member's visual string, unchanged — the evidence
	// that lock 1 held.
	Visual string
	// Prompt is the full prompt sent, as ReferencePrompt built it.
	Prompt string
	// ContentType is the image type, one of image/png, image/jpeg,
	// image/webp.
	ContentType string
	// Image is the image itself.
	Image []byte
}

// Illustration is one rendered page.
type Illustration struct {
	// N is the page number, copied from the story page.
	N int
	// Prompt is the full prompt sent, as PagePrompt built it.
	Prompt string
	// Reference names the cast member whose sheet this page was
	// rendered against — the image-to-image base.
	Reference string
	// ContentType is the image type, one of image/png, image/jpeg,
	// image/webp.
	ContentType string
	// Image is the image itself.
	Image []byte
}

// Book is everything one Illustrate run produced.
type Book struct {
	// References are the reference sheets, in cast order.
	References []Reference
	// Pages are the illustrations, in story-page order.
	Pages []Illustration
	// Skipped lists every cast member that got no sheet of its own,
	// with the reason — a narrator with no appearance, a second mood
	// of a character already drawn, a member no page names. Nothing is
	// dropped silently; see Plan.
	Skipped []Skip
}
