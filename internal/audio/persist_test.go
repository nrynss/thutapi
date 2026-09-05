package audio

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"thutapi/internal/store"
	"thutapi/internal/story"
)

// newNotFoundServer answers every request with 404 — an audio_url
// that has already expired.
func newNotFoundServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// waitForDownloads blocks until the clip server has handled want
// requests. A handler that never arrives fails the test instead of
// hanging it.
func waitForDownloads(t *testing.T, s *clipServer, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.totalHits() < want {
		if time.Now().After(deadline) {
			t.Fatalf("clip server handled %d requests, want %d", s.totalHits(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNarrate_PersistsOneClipPerPage pins the deliverable T8 owes
// T10 (PLAN.md §T8): one persisted clip per page, anchored
// store.MediaNarration at (book, page N), for every page of the book
// and nothing else — no extra rows, no stray blobs, the right bytes
// behind every row.
func TestNarrate_PersistsOneClipPerPage(t *testing.T) {
	h := newNarrationHarness(t, 3)
	fake := &fakeTTS{}
	clips, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, threePages())
	if err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
	if len(clips) != 3 {
		t.Fatalf("clips = %d, want 3", len(clips))
	}
	for _, p := range threePages() {
		row, err := h.db.PageMedia(t.Context(), h.bookID, p.N, store.MediaNarration)
		if err != nil {
			t.Fatalf("page %d narration missing: %v", p.N, err)
		}
		if row.BookID != h.bookID || row.Kind != store.MediaNarration || row.PageN != p.N {
			t.Errorf("row = %+v, want the narration anchored at (book, page %d)", row, p.N)
		}
		if got := h.blobBytes(t, row.ID); string(got) != string(clipBytes(p.Text)) {
			t.Errorf("page %d blob = %q, want the downloaded clip for its text", p.N, got)
		}
	}
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("placed rows = %d, want exactly 3", len(all))
	}
	for _, m := range all {
		if m.Kind != store.MediaNarration {
			t.Errorf("row %s kind = %q, want narration", m.ID, m.Kind)
		}
	}
}

// TestNarrate_DownloadCompletesBeforeTheRowExists pins the ordering
// that download-on-receipt and blob-first-then-place promise: while a
// page's clip is still downloading, that page's narration row must
// not exist yet. With Limit 1 the run is serialised, so page 2's
// download starts only after page 1 is fully placed — and the gate
// proves the row follows the download, not the other way around.
func TestNarrate_DownloadCompletesBeforeTheRowExists(t *testing.T) {
	h := newNarrationHarness(t, 2)
	release := make(chan struct{})
	cleanupClose(t, release)
	h.clips.Server.Close() // drop the ungated harness server
	blocked := map[string]chan struct{}{threePages()[1].Text: release}
	h.clips = newClipServer(t, blocked)

	fake := &fakeTTS{}
	cfg := h.cfg(fake)
	cfg.Limit = 1 // serialise: page 1 is fully placed before page 2 starts
	done := make(chan error, 1)
	go func() {
		_, err := NarrateBook(t.Context(), cfg, h.bookID, threePages()[:2])
		done <- err
	}()

	// Page 1's download and page 2's download have both entered the
	// server; page 2's is gated, so its row cannot exist yet if the
	// ordering holds (a persist-before-download implementation would
	// have placed page 2 while the audio was still in flight).
	waitForDownloads(t, h.clips, 2)
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaNarration); err != nil {
		t.Errorf("page 1 should be fully placed before page 2's download starts (Limit 1): %v", err)
	}
	if _, err := h.db.PageMedia(t.Context(), h.bookID, 2, store.MediaNarration); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("page 2 row exists while its download is gated (err = %v) — the row was placed before the audio arrived", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("NarrateBook: %v", err)
	}
}

// TestNarrate_ReRunReplacesTheOccupant pins the replace semantics of
// a re-narrate over the same book rows (the ErrConflict path): the
// slot keeps exactly one row, the old occupant's row and file are
// deleted, and — the ordering half — the old clip keeps serving until
// the replacement is on disk: while the new download is gated, the
// old row and blob are still intact.
func TestNarrate_ReRunReplacesTheOccupant(t *testing.T) {
	h := newNarrationHarness(t, 2)
	pages := threePages()[:2]
	fake := &fakeTTS{}
	if _, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, pages); err != nil {
		t.Fatalf("first run: %v", err)
	}
	old1, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaNarration)
	if err != nil {
		t.Fatalf("first run page 1: %v", err)
	}
	old2, err := h.db.PageMedia(t.Context(), h.bookID, 2, store.MediaNarration)
	if err != nil {
		t.Fatalf("first run page 2: %v", err)
	}

	// Second run over the same pages. Page 1's new download is gated
	// so the test can observe the moment between "new clip fetched"
	// and "old clip deleted".
	release := make(chan struct{})
	cleanupClose(t, release)
	h.clips.Server.Close()
	h.clips = newClipServer(t, map[string]chan struct{}{pages[0].Text: release})

	done := make(chan error, 1)
	go func() {
		_, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, pages)
		done <- err
	}()
	// Both new downloads have started; page 1's is gated mid-flight.
	waitForDownloads(t, h.clips, 2)

	// While the replacement download is in flight the old occupant
	// must still be the one the book serves.
	cur, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaNarration)
	if err != nil || cur.ID != old1.ID {
		t.Fatalf("page 1 row during replacement = %+v (err %v), want the old occupant %s still in place until the new blob is persisted", cur, err, old1.ID)
	}
	if h.blobGone(t, old1.ID) {
		t.Fatalf("old page-1 blob %s deleted while the replacement is still downloading", old1.ID)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("second run: %v", err)
	}

	// Every slot now points at a fresh blob; the old rows and files
	// are gone; the book still holds exactly two narration rows.
	new1, err := h.db.PageMedia(t.Context(), h.bookID, 1, store.MediaNarration)
	if err != nil {
		t.Fatalf("second run page 1: %v", err)
	}
	if new1.ID == old1.ID {
		t.Errorf("page 1 kept the old blob %s — the slot was not replaced", old1.ID)
	}
	new2, err := h.db.PageMedia(t.Context(), h.bookID, 2, store.MediaNarration)
	if err != nil {
		t.Fatalf("second run page 2: %v", err)
	}
	if new2.ID == old2.ID {
		t.Errorf("page 2 kept the old blob %s — the slot was not replaced", old2.ID)
	}
	if _, err := h.db.Media(t.Context(), old1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old page-1 row still present: %v", err)
	}
	if _, err := h.db.Media(t.Context(), old2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old page-2 row still present: %v", err)
	}
	if !h.blobGone(t, old1.ID) || !h.blobGone(t, old2.ID) {
		t.Errorf("old blobs still on disk after replacement")
	}
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("placed rows after re-run = %d, want 2", len(all))
	}
}

// TestNarrate_MissingPageRowFailsLoudly pins the anchor contract:
// narrating a page whose store row does not exist fails with
// store.ErrInvalid at the place — the foreign key fires on the
// update, not on a check this package can do cheaper. store.CreateBook
// and store.CreatePage are the caller's (the T7 precedent); narration
// never creates rows.
func TestNarrate_MissingPageRowFailsLoudly(t *testing.T) {
	h := newNarrationHarness(t, 2)
	fake := &fakeTTS{}
	pages := []story.Page{{N: 5, Text: "a page the book does not have", Emotion: "happy"}}
	clips, err := NarrateBook(t.Context(), h.cfg(fake), h.bookID, pages)
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("err = %v, want errors.Is(.., store.ErrInvalid)", err)
	}
	if clips != nil {
		t.Fatalf("clips = %v, want nil on failure", clips)
	}
	all, err := h.db.BookMedia(t.Context(), h.bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("placed rows = %d, want 0 — nothing anchors without a page row", len(all))
	}
}

// TestNarrate_DownloadFailureFailsThePage pins that a clip whose URL
// answers badly fails that page loudly (ErrNoAudio) and the run with
// it: an expiring URL that is already gone is a real failure, never a
// silent hole in the book.
func TestNarrate_DownloadFailureFailsThePage(t *testing.T) {
	h := newNarrationHarness(t, 1)
	fake := &fakeTTS{}
	cfg := h.cfg(fake)
	// Point the fake at a server that answers every clip with 404,
	// after cfg has wired the harness server.
	fake.audioBase = newNotFoundServer(t).URL
	clips, err := NarrateBook(t.Context(), cfg, h.bookID, threePages()[:1])
	if !errors.Is(err, ErrNoAudio) {
		t.Fatalf("err = %v, want errors.Is(.., ErrNoAudio)", err)
	}
	if clips != nil {
		t.Fatalf("clips = %v, want nil on failure", clips)
	}
}
