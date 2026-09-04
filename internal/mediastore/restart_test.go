package mediastore

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"thutapi/internal/store"
)

// TestBookSurvivesRestartWithMedia is PLAN.md §T3's Done when,
// pinned: a book — its pages, cast bible, interview transcript and a
// persisted media blob — is written to a data dir, everything is
// closed as if the container stopped, and a fresh process reopens the
// same directory and reads it all back, including serving the blob
// through a Range request. Any state held only in memory fails this
// through a Range request. The blobs are also placed — a page's
// narration and illustration, a cast member's reference sheet — and
// the placements answer the T6/T8/T10 queries after the restart.
func TestBookSurvivesRestartWithMedia(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()

	// --- first life: create everything, then "stop the container" ---
	db, err := store.Open(ctx, store.Config{Path: filepath.Join(dir, "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	media, err := Open(ctx, Config{Dir: filepath.Join(dir, "media"), DB: db})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}

	book, err := db.CreateBook(ctx, "Mira and the Rain Dragon")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	page := store.Page{
		BookID:     book.ID,
		N:          1,
		Text:       "Mira counted the raindrops on the window.",
		Prompt:     "a small girl in a red raincoat watching a green dragon dance in the rain",
		Characters: []string{"Mira", "Dragon"},
		Emotion:    "happy",
		Lines: []store.Line{
			{Character: "Mira", Text: "Look, the dragon is dancing!"},
			{Character: "Dragon", Text: "Splish, splash."},
		},
	}
	if err := db.CreatePage(ctx, page); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.CreateCastMember(ctx, store.CastMember{
		BookID: book.ID, Name: "Dragon",
		Visual: "a small green dragon with tiny wings", Pitch: -8, SoundEffects: "spacious_echo",
	}); err != nil {
		t.Fatalf("create cast: %v", err)
	}
	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("create interview: %v", err)
	}
	iv.Turns = []store.Turn{
		{Role: "interviewer", Text: "What is your hero called?"},
		{Role: "child", Text: "mira! she has a red coat"},
	}
	iv.BookID = book.ID
	if err := db.UpdateInterview(ctx, iv); err != nil {
		t.Fatalf("update interview: %v", err)
	}
	narration := blob(8192) // deterministic audio-shaped bytes
	mediaID, err := media.Persist(ctx, bytes.NewReader(narration), "audio/mpeg")
	if err != nil {
		t.Fatalf("persist narration: %v", err)
	}
	illustration := blob(4096)
	illustrationID, err := media.Persist(ctx, bytes.NewReader(illustration), "image/png")
	if err != nil {
		t.Fatalf("persist illustration: %v", err)
	}
	reference := blob(2048)
	referenceID, err := media.Persist(ctx, bytes.NewReader(reference), "image/png")
	if err != nil {
		t.Fatalf("persist reference: %v", err)
	}
	// Place each blob where the pipeline puts it: the page's
	// narration and its illustration, and the Dragon's reference
	// sheet — the image lock T6 generates every page against.
	if err := db.SetMediaPlace(ctx, mediaID,
		store.MediaPlace{BookID: book.ID, Kind: store.MediaNarration, PageN: 1}); err != nil {
		t.Fatalf("place narration: %v", err)
	}
	if err := db.SetMediaPlace(ctx, illustrationID,
		store.MediaPlace{BookID: book.ID, Kind: store.MediaIllustration, PageN: 1}); err != nil {
		t.Fatalf("place illustration: %v", err)
	}
	if err := db.SetMediaPlace(ctx, referenceID,
		store.MediaPlace{BookID: book.ID, Kind: store.MediaReference, CastName: "Dragon"}); err != nil {
		t.Fatalf("place reference: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// --- second life: a fresh process reopens the same data dir ---
	db2, err := store.Open(ctx, store.Config{Path: filepath.Join(dir, "thutapi.db")})
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer db2.Close()
	media2, err := Open(ctx, Config{Dir: filepath.Join(dir, "media"), DB: db2})
	if err != nil {
		t.Fatalf("reopen media store: %v", err)
	}

	gotBook, err := db2.Book(ctx, book.ID)
	if err != nil {
		t.Fatalf("book after restart: %v", err)
	}
	if gotBook.Title != book.Title || !gotBook.CreatedAt.Equal(book.CreatedAt) {
		t.Fatalf("book after restart = %+v, want %+v", gotBook, book)
	}

	pages, err := db2.Pages(ctx, book.ID)
	if err != nil {
		t.Fatalf("pages after restart: %v", err)
	}
	if len(pages) != 1 || pages[0].Text != page.Text || pages[0].Prompt != page.Prompt ||
		pages[0].Emotion != page.Emotion || len(pages[0].Characters) != 2 ||
		len(pages[0].Lines) != 2 || pages[0].Lines[1].Text != "Splish, splash." {
		t.Fatalf("page after restart = %+v, want %+v", pages, page)
	}

	cast, err := db2.Cast(ctx, book.ID)
	if err != nil {
		t.Fatalf("cast after restart: %v", err)
	}
	if len(cast) != 1 || cast[0].Name != "Dragon" || cast[0].Pitch != -8 ||
		cast[0].SoundEffects != "spacious_echo" || cast[0].Visual != "a small green dragon with tiny wings" {
		t.Fatalf("cast after restart = %+v, want the Dragon entry", cast)
	}

	gotIV, err := db2.Interview(ctx, iv.ID)
	if err != nil {
		t.Fatalf("interview after restart: %v", err)
	}
	if gotIV.BookID != book.ID || len(gotIV.Turns) != 2 || gotIV.Turns[1].Text != "mira! she has a red coat" {
		t.Fatalf("interview after restart = %+v, want transcript and book link", gotIV)
	}

	row, err := db2.Media(ctx, mediaID)
	if err != nil {
		t.Fatalf("media row after restart: %v", err)
	}
	if row.ContentType != "audio/mpeg" || row.SizeBytes != int64(len(narration)) || row.BookID != book.ID {
		t.Fatalf("media row after restart = %+v, want audio/mpeg, %d bytes, book %s",
			row, len(narration), book.ID)
	}

	// The placements answer the T6/T8/T10 queries after the restart:
	// the book's listing, "page 1 → image + audio", and the Dragon's
	// reference sheet — with the not-found and bad-kind sentinels
	// still honest.
	listed, err := db2.BookMedia(ctx, book.ID)
	if err != nil {
		t.Fatalf("book media after restart: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("book media after restart = %d rows, want 3", len(listed))
	}
	gotIll, err := db2.PageMedia(ctx, book.ID, 1, store.MediaIllustration)
	if err != nil {
		t.Fatalf("page illustration after restart: %v", err)
	}
	if gotIll.ID != illustrationID || gotIll.ContentType != "image/png" || gotIll.PageN != 1 || gotIll.BookID != book.ID {
		t.Fatalf("page illustration after restart = %+v", gotIll)
	}
	gotNarr, err := db2.PageMedia(ctx, book.ID, 1, store.MediaNarration)
	if err != nil {
		t.Fatalf("page narration after restart: %v", err)
	}
	if gotNarr.ID != mediaID || gotNarr.SizeBytes != int64(len(narration)) {
		t.Fatalf("page narration after restart = %+v", gotNarr)
	}
	gotRef, err := db2.CastMedia(ctx, book.ID, "Dragon")
	if err != nil {
		t.Fatalf("cast reference after restart: %v", err)
	}
	if gotRef.ID != referenceID || gotRef.CastName != "Dragon" {
		t.Fatalf("cast reference after restart = %+v", gotRef)
	}
	if _, err := db2.PageMedia(ctx, book.ID, 2, store.MediaNarration); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty page slot err = %v, want ErrNotFound", err)
	}
	if err := db2.SetMediaPlace(ctx, illustrationID,
		store.MediaPlace{BookID: book.ID, Kind: "trailer", PageN: 1}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad kind err = %v, want ErrInvalid", err)
	}

	// The blob still serves — and scrubs: a Range request against the
	// reopened store returns 206 with exactly the requested bytes.
	const start, end = 100, 299
	req := httptest.NewRequest(http.MethodGet, "/media/"+mediaID, nil)
	req.Header.Set("Range", "bytes=100-299")
	req.SetPathValue("id", mediaID)
	rr := httptest.NewRecorder()
	media2.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusPartialContent; got != want {
		t.Fatalf("range status = %d, want %d", got, want)
	}
	if got, want := rr.Header().Get("Content-Range"), "bytes 100-299/8192"; got != want {
		t.Fatalf("content-range = %q, want %q", got, want)
	}
	if got := rr.Body.Bytes(); !bytes.Equal(got, narration[start:end+1]) {
		t.Fatalf("range body = %d bytes, want the %d requested bytes", len(got), end-start+1)
	}
	if got := rr.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("content-type = %q, want audio/mpeg", got)
	}
}
