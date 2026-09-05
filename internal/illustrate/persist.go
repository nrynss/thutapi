package illustrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"thutapi/internal/mediastore"
	"thutapi/internal/store"
)

// BookWriter persists one book's finished renders (PLAN.md §T7,
// "Persistence lands here"; the illustration-persistence seam row of
// §Unowned seams, assigned to T7). The ordering is the point: a
// reference sheet is final as soon as it renders and persists
// immediately, while a page illustration persists only after the
// consistency loop approves it — a page becomes final when T7
// approves it, so persisting after the verdict is one write per page
// and regeneration never churns the one-illustration-per-page slot.
//
// A BookWriter is bound to one book (its rows must already exist —
// store.CreateBook, CreatePage and CreateCastMember are the caller's,
// since the anchor foreign keys fire on place). Construct with
// NewBookWriter; the zero value is not usable.
type BookWriter struct {
	db     *store.DB
	blobs  *mediastore.Store
	bookID string
}

// NewBookWriter returns a writer that persists into book bookID: blob
// files through blobs and metadata rows through db. bookID must name
// a book whose pages and cast rows exist, or every place fails with
// store.ErrInvalid.
func NewBookWriter(db *store.DB, blobs *mediastore.Store, bookID string) *BookWriter {
	return &BookWriter{db: db, blobs: blobs, bookID: bookID}
}

// storeReference writes one reference sheet: the bytes go to
// mediastore first (blob file fsynced, then the unplaced row), then
// the row is placed at (book, reference, cast member). The blob's
// content type is already in mediastore's closed image set — decode
// guarantees it — so a Persist cannot fail on the type.
func (w *BookWriter) storeReference(ctx context.Context, ref Reference) error {
	const op = "persist reference sheet"
	id, err := w.blobs.Persist(ctx, bytes.NewReader(ref.Image), ref.ContentType)
	if err != nil {
		return fmt.Errorf("illustrate: %s for %q: %w", op, ref.Name, err)
	}
	place := store.MediaPlace{BookID: w.bookID, Kind: store.MediaReference, CastName: ref.Name}
	return w.place(ctx, id, place, fmt.Sprintf("%s for %q", op, ref.Name))
}

// storePage writes one approved page illustration: the bytes go to
// mediastore, then the row is placed at (book, illustration, page N).
// It is called only after the page's verdict approved it (or, with no
// Judge configured, after its render decoded), so a store write is
// the last thing that happens to a page — never a write of a picture
// the loop is about to discard.
func (w *BookWriter) storePage(ctx context.Context, page *Illustration) error {
	const op = "persist illustration"
	id, err := w.blobs.Persist(ctx, bytes.NewReader(page.Image), page.ContentType)
	if err != nil {
		return fmt.Errorf("illustrate: %s for page %d: %w", op, page.N, err)
	}
	place := store.MediaPlace{BookID: w.bookID, Kind: store.MediaIllustration, PageN: page.N}
	return w.place(ctx, id, place, fmt.Sprintf("%s for page %d", op, page.N))
}

// place attaches the blob id to place, replacing the slot's previous
// occupant when the anchor is already taken. Within one run a slot
// cannot collide — nothing persists before its verdict, so the only
// writer of a slot is the page or member that owns it — but a second
// Illustrate run over the same book rows (a re-render after an edit,
// a re-verify) finds the slot occupied. Replacing is the honest
// semantic for "the current version of this page": the new blob is
// persisted first, so the book keeps serving the old illustration
// until the new one is on disk; the old occupant's row and file are
// then deleted (mediastore.Delete removes both) and the place is
// retried. A crash between the delete and the retried place leaves at
// worst an unplaced orphan blob — exactly what PLAN.md §T11's
// retention sweep owns — and an empty slot the next run fills.
func (w *BookWriter) place(ctx context.Context, id string, place store.MediaPlace, op string) error {
	err := w.db.SetMediaPlace(ctx, id, place)
	if err == nil {
		return nil
	}
	if !errors.Is(err, store.ErrConflict) {
		return fmt.Errorf("illustrate: %s: %w", op, err)
	}
	old, oerr := w.occupant(ctx, place)
	if oerr != nil {
		return fmt.Errorf("illustrate: %s: slot occupied but its occupant could not be read: %w (place error: %v)", op, oerr, err)
	}
	if derr := w.blobs.Delete(ctx, old.ID); derr != nil {
		return fmt.Errorf("illustrate: %s: replacing %s: %w", op, old.ID, derr)
	}
	if err := w.db.SetMediaPlace(ctx, id, place); err != nil {
		return fmt.Errorf("illustrate: %s after replacing %s: %w", op, old.ID, err)
	}
	return nil
}

// occupant returns the blob currently holding the anchor place names,
// so a conflicted place can replace it. The anchor kind picks the
// lookup: a reference anchors on its cast member, an illustration on
// its page (the store's own split between CastMedia and PageMedia).
func (w *BookWriter) occupant(ctx context.Context, place store.MediaPlace) (store.Media, error) {
	switch place.Kind {
	case store.MediaReference:
		return w.db.CastMedia(ctx, place.BookID, place.CastName)
	case store.MediaIllustration:
		return w.db.PageMedia(ctx, place.BookID, place.PageN, store.MediaIllustration)
	default:
		return store.Media{}, fmt.Errorf("illustrate: no occupant lookup for place kind %q", place.Kind)
	}
}
