package illustrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"thutapi/internal/gmi/media"
	"thutapi/internal/mediastore"
	"thutapi/internal/store"
)

// persistHarness is a real store + mediastore over temp dirs whose
// book rows match the story under test — pages 1..pages and the given
// cast members — following internal/store's test conventions
// (media_test.go builds books, pages and cast rows the same way
// before placing media). Persistence tests run against the real
// sqlite + blob store, never a fake: the anchors, the partial unique
// slot indexes and the blob lifecycle are exactly what the writer
// must survive.
type persistHarness struct {
	db       *store.DB
	blobs    *mediastore.Store
	mediaDir string
	bookID   string
}

func newPersistHarness(t *testing.T, pages int, cast []string) *persistHarness {
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
	book, err := db.CreateBook(ctx, "persist book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	for n := 1; n <= pages; n++ {
		if err := db.CreatePage(ctx, store.Page{BookID: book.ID, N: n, Text: "once"}); err != nil {
			t.Fatalf("create page %d: %v", n, err)
		}
	}
	for _, name := range cast {
		if err := db.CreateCastMember(ctx, store.CastMember{BookID: book.ID, Name: name}); err != nil {
			t.Fatalf("create cast member %q: %v", name, err)
		}
	}
	return &persistHarness{db: db, blobs: blobs, mediaDir: mediaDir, bookID: book.ID}
}

// blobBytes reads the blob file behind a media id back from disk —
// the byte-level check that what the store row describes is what the
// writer actually persisted.
func (h *persistHarness) blobBytes(t *testing.T, id string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.mediaDir, id))
	if err != nil {
		t.Fatalf("read blob %s: %v", id, err)
	}
	return b
}

// TestPersist_SheetsImmediatePagesOnlyAfterApproval pins the ordering
// that is T7's whole reason for existing (PLAN.md §T7 "Persistence
// lands here"): reference sheets land in the store as soon as they
// render — even when the run then fails — and a page that never
// earns a verdict writes nothing. The judge rejects every page, so
// the run dies on ErrConsistency with a zero Book, yet both sheets
// are placed and zero illustrations exist.
func TestPersist_SheetsImmediatePagesOnlyAfterApproval(t *testing.T) {
	h := newPersistHarness(t, 2, []string{"Mira", "Bramble"})
	writer := NewBookWriter(h.db, h.blobs, h.bookID)
	judge := &scriptedJudge{out: []bool{false, false, false}}
	book, err := Illustrate(t.Context(), Config{Imager: scriptedImager(), Judge: judge, Persist: writer}, twoCastStory())
	if !errors.Is(err, ErrConsistency) {
		t.Fatalf("err = %v, want errors.Is(.., ErrConsistency)", err)
	}
	assertZeroBook(t, book)
	// Both sheets persisted despite the page failure. Which member
	// won which generate slot is fan-out order, so assert the two
	// blobs hold the two sheet renders between them.
	mira, err := h.db.CastMedia(t.Context(), h.bookID, "Mira")
	if err != nil {
		t.Fatalf("Mira sheet missing: %v", err)
	}
	if mira.Kind != store.MediaReference || mira.ContentType != "image/png" {
		t.Fatalf("Mira row = %+v, want a placed reference", mira)
	}
	bramble, err := h.db.CastMedia(t.Context(), h.bookID, "Bramble")
	if err != nil {
		t.Fatalf("Bramble sheet missing: %v", err)
	}
	if bramble.ID == mira.ID {
		t.Fatalf("both members point at the same blob %s", mira.ID)
	}
	got := [][]byte{h.blobBytes(t, mira.ID), h.blobBytes(t, bramble.ID)}
	want := [][]byte{pngBytes("t7sheet-1"), pngBytes("t7sheet-2")}
	for _, w := range want {
		if !bytes.Equal(got[0], w) && !bytes.Equal(got[1], w) {
			t.Errorf("neither member's blob holds %q", w)
		}
	}

	// No page ever earned a verdict, so no illustration row exists —
	// the drifted renders were never written to the store.
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("placed rows = %d, want exactly the 2 sheets", len(all))
	}
	for _, m := range all {
		if m.Kind != store.MediaReference {
			t.Errorf("row %s has kind %q, want reference", m.ID, m.Kind)
		}
	}
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaIllustration); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("page 1 err = %v, want ErrNotFound (nothing approved, nothing persisted)", err)
	}
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 2, store.MediaIllustration); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("page 2 err = %v, want ErrNotFound (nothing approved, nothing persisted)", err)
	}
}

// TestPersist_PageStoresTheApprovedRender pins what "persist after
// the verdict" means byte for byte: the first render is judged false
// and regenerated, and the row + blob that land in the store are the
// APPROVED regeneration's bytes — the drifted first attempt never
// touches the store, and the Book returns the same approved bytes.
func TestPersist_PageStoresTheApprovedRender(t *testing.T) {
	h := newPersistHarness(t, 1, []string{"Mira"})
	writer := NewBookWriter(h.db, h.blobs, h.bookID)
	judge := &scriptedJudge{out: []bool{false, true}}
	book, err := Illustrate(t.Context(), Config{Imager: scriptedImager(), Judge: judge, Persist: writer}, oneCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	row, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaIllustration)
	if err != nil {
		t.Fatalf("page 1 illustration missing after approval: %v", err)
	}
	if row.Kind != store.MediaIllustration || row.PageN != 1 || row.ContentType != "image/png" {
		t.Fatalf("page row = %+v, want the placed illustration of page 1", row)
	}
	if got := h.blobBytes(t, row.ID); !bytes.Equal(got, pngBytes("t7page-2")) {
		t.Errorf("stored page blob is not the approved regeneration's bytes (page-2)")
	}
	if got := book.Pages[0].Image; !bytes.Equal(got, pngBytes("t7page-2")) {
		t.Errorf("Book page and stored page disagree — the store has bytes the run did not approve")
	}
	// The reference sheet persisted immediately alongside.
	ref, err := h.db.CastMedia(t.Context(), h.bookID, "Mira")
	if err != nil {
		t.Fatalf("Mira sheet missing: %v", err)
	}
	if got := h.blobBytes(t, ref.ID); !bytes.Equal(got, pngBytes("t7sheet-1")) {
		t.Errorf("Mira blob is not the rendered sheet bytes")
	}
}

// TestPersist_ReRunReplacesTheSlotOccupant pins the ErrConflict
// handling (requirement 3): the unique illustration slot is per page,
// so a second Illustrate run over the same book rows — a re-render
// after an edit — must replace the earlier version, deleting the old
// row and blob, not fail the run or pile a second row into the slot.
func TestPersist_ReRunReplacesTheSlotOccupant(t *testing.T) {
	h := newPersistHarness(t, 2, []string{"Mira", "Bramble"})
	writer := NewBookWriter(h.db, h.blobs, h.bookID)
	cfg := func() Config {
		return Config{Imager: scriptedImager(), Judge: &scriptedJudge{out: []bool{true}}, Persist: writer}
	}
	if _, err := Illustrate(t.Context(), cfg(), twoCastStory()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	oldMira, err := h.db.CastMedia(t.Context(), h.bookID, "Mira")
	if err != nil {
		t.Fatalf("first run Mira sheet: %v", err)
	}
	oldPage1, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaIllustration)
	if err != nil {
		t.Fatalf("first run page 1: %v", err)
	}

	// The second run re-renders the same book and must replace, not
	// collide: every slot keeps exactly one row, pointing at new blobs.
	if _, err := Illustrate(t.Context(), cfg(), twoCastStory()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	newMira, err := h.db.CastMedia(t.Context(), h.bookID, "Mira")
	if err != nil {
		t.Fatalf("second run Mira sheet: %v", err)
	}
	if newMira.ID == oldMira.ID {
		t.Errorf("re-run kept the old Mira sheet id %s — the slot was not replaced", oldMira.ID)
	}
	newPage1, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaIllustration)
	if err != nil {
		t.Fatalf("second run page 1: %v", err)
	}
	if newPage1.ID == oldPage1.ID {
		t.Errorf("re-run kept the old page-1 illustration id %s — the slot was not replaced", oldPage1.ID)
	}
	// The old occupant's row and blob are gone.
	if _, err := h.db.Media(t.Context(), oldPage1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old page-1 row still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.mediaDir, oldPage1.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old page-1 blob still on disk: %v", err)
	}
	// The book still holds exactly four placed rows: two sheets, two pages.
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("placed rows after re-run = %d, want 4", len(all))
	}
}

// TestPersist_AnchorValidationFailsLoudly pins that a persist target
// the book cannot anchor — a cast member with no row in the store's
// cast table — surfaces as store.ErrInvalid and fails the run with a
// zero Book: the anchor foreign keys fire on place, and a book whose
// sheets cannot be stored is not a book. One member only, so the
// failure is deterministic (no sibling sheet to race the cancel).
func TestPersist_AnchorValidationFailsLoudly(t *testing.T) {
	// The book has pages but no cast rows; the story draws Mira.
	h := newPersistHarness(t, 1, nil)
	writer := NewBookWriter(h.db, h.blobs, h.bookID)
	judge := &scriptedJudge{out: []bool{true}}
	book, err := Illustrate(t.Context(), Config{Imager: scriptedImager(), Judge: judge, Persist: writer}, oneCastStory())
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v, want errors.Is(.., store.ErrInvalid)", err)
	}
	assertZeroBook(t, book)
	if _, err := h.db.CastMedia(t.Context(), h.bookID, "Mira"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Mira err = %v, want ErrNotFound (never placed)", err)
	}
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaIllustration); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("page 1 err = %v, want ErrNotFound (pages never ran)", err)
	}
}

// TestPersist_WithoutJudgeTheRenderIsTheApproval pins the seam's
// fourth combination: Config.Persist with no Judge persists sheets
// and pages without a model call — a successful decode is the
// approval — so an operator can store renders with the human as the
// judge.
func TestPersist_WithoutJudgeTheRenderIsTheApproval(t *testing.T) {
	h := newPersistHarness(t, 1, []string{"Mira"})
	writer := NewBookWriter(h.db, h.blobs, h.bookID)
	book, err := Illustrate(t.Context(), Config{Imager: scriptedImager(), Persist: writer}, oneCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.Pages) != 1 {
		t.Fatalf("Pages = %d, want 1", len(book.Pages))
	}
	row, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaIllustration)
	if err != nil {
		t.Fatalf("page 1 illustration missing: %v", err)
	}
	if got := h.blobBytes(t, row.ID); !bytes.Equal(got, pngBytes("t7page-1")) {
		t.Errorf("stored page blob is not the single render's bytes (page-1)")
	}
	ref, err := h.db.CastMedia(t.Context(), h.bookID, "Mira")
	if err != nil {
		t.Fatalf("Mira sheet missing: %v", err)
	}
	if got := h.blobBytes(t, ref.ID); !bytes.Equal(got, pngBytes("t7sheet-1")) {
		t.Errorf("Mira blob is not the rendered sheet bytes")
	}
}

// TestPersist_ReferenceSheetSurvivesSiblingStoreFailure pins the
// immediacy half of the persist ordering (round-1 M2): a reference
// sheet is final and paid for the moment it renders, so it persists
// inside the member's own goroutine — before the phase can fail.
// Config.Limit 1 serialises the phase, so the order is deterministic:
// member 1 (Mira) renders and persists, then member 2 (Bramble)
// renders and its storeReference fails loudly — the harness has no
// Bramble cast row, so the place dies with store.ErrInvalid and the
// run returns a zero Book.
//
// The END state cannot tell immediate from deferred persist — member
// 1's row is present either way once a deferred loop reaches it — so
// the pin observes the store from inside member 2's render: under
// immediacy her sheet must already be placed at that moment, because
// member 1's goroutine (render AND persist) fully completed before
// member 2's began. Under a deferred persist (round-1 P-A: hooks
// moved out of the goroutines into a post-Wait loop) nothing has been
// written when member 2 renders, and the assertion goes red.
func TestPersist_ReferenceSheetSurvivesSiblingStoreFailure(t *testing.T) {
	h := newPersistHarness(t, 2, []string{"Mira"}) // member 2, Bramble, has no cast row
	writer := NewBookWriter(h.db, h.blobs, h.bookID)
	fake := &fakeImager{}
	var (
		mu                     sync.Mutex
		generates              int
		sawMiraAtSiblingRender int
	)
	fake.generate = func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
		mu.Lock()
		generates++
		n := generates
		mu.Unlock()
		if n == 2 {
			// Member 2's sheet render. Member 1's goroutine has fully
			// completed under Limit 1, so immediacy means her row
			// exists in the store right now — before this render and
			// before member 2's storeReference can fail the phase.
			if _, err := h.db.CastMedia(ctx, h.bookID, "Mira"); err == nil {
				mu.Lock()
				sawMiraAtSiblingRender++
				mu.Unlock()
			}
		}
		return queueEnvelope(fixtureMedia.url(fmt.Sprintf("immediacy-%d", n))), nil
	}
	book, err := Illustrate(t.Context(), Config{Imager: fake, Limit: 1, Persist: writer}, twoCastStory())
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v, want errors.Is(.., store.ErrInvalid)", err)
	}
	assertZeroBook(t, book)
	mu.Lock()
	defer mu.Unlock()
	if generates != 2 {
		t.Fatalf("generate renders = %d, want 2 (both members' sheets rendered before the phase died)", generates)
	}
	if sawMiraAtSiblingRender != 1 {
		t.Errorf("member 2's render saw member 1's sheet placed %d times, want 1 — the sheet must persist inside member 1's goroutine, before the phase can fail", sawMiraAtSiblingRender)
	}
	// The run died on member 2's storeReference; member 1's sheet is
	// the only placed row.
	mira, err := h.db.CastMedia(t.Context(), h.bookID, "Mira")
	if err != nil {
		t.Fatalf("Mira sheet missing after the run: %v", err)
	}
	if mira.Kind != store.MediaReference || mira.ContentType != "image/png" {
		t.Fatalf("Mira row = %+v, want a placed reference", mira)
	}
	if got := h.blobBytes(t, mira.ID); !bytes.Equal(got, pngBytes("immediacy-1")) {
		t.Errorf("Mira blob is not the first sheet render's bytes")
	}
	if _, err := h.db.CastMedia(t.Context(), h.bookID, "Bramble"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Bramble err = %v, want ErrNotFound (its sheet was never placed)", err)
	}
}
