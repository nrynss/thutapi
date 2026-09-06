package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBookLifecycle covers create → read → list → update → delete for
// one book, the CRUD spine PLAN.md §T3 asks for.
func TestBookLifecycle(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	b, err := db.CreateBook(ctx, "Mira and the Rain Dragon")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.ID == "" || b.Title != "Mira and the Rain Dragon" || b.CreatedAt.IsZero() {
		t.Fatalf("created book not fully assigned: %+v", b)
	}

	got, err := db.Book(ctx, b.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != b.ID || got.Title != b.Title || !got.CreatedAt.Equal(b.CreatedAt) {
		t.Fatalf("read = %+v, want %+v", got, b)
	}

	b2, err := db.CreateBook(ctx, "The Robot Who Ate Lunch")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	books, err := db.Books(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("list has %d books, want 2", len(books))
	}
	byID := map[string]string{}
	for _, bk := range books {
		byID[bk.ID] = bk.Title
	}
	if byID[b.ID] != b.Title || byID[b2.ID] != b2.Title {
		t.Fatalf("list titles wrong: %v", byID)
	}

	b.Title = "Mira and the Thunder Dragon"
	if err := db.UpdateBook(ctx, b); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = db.Book(ctx, b.ID)
	if err != nil {
		t.Fatalf("read after update: %v", err)
	}
	if got.Title != b.Title || !got.CreatedAt.Equal(b.CreatedAt) {
		t.Fatalf("after update = %+v: title changed, CreatedAt must not", got)
	}

	if err := db.DeleteBook(ctx, b.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Book(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err after delete = %v, want ErrNotFound", err)
	}
}

// TestCreateBookRejectsEmptyTitle: a book without a title is invalid
// input, not a row with an empty field.
func TestCreateBookRejectsEmptyTitle(t *testing.T) {
	if _, err := openTestDB(t).CreateBook(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestBookUpdatesOnUnknownBook pins the ErrNotFound branches: neither
// an update nor a delete may silently no-op.
func TestBookUpdatesOnUnknownBook(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := db.UpdateBook(ctx, Book{ID: "00000000000000000000000000000000", Title: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteBook(ctx, "00000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete err = %v, want ErrNotFound", err)
	}
}

// TestUpdateBookRejectsEmptyFields: the update path validates the same
// fields the create path does.
func TestUpdateBookRejectsEmptyFields(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := db.UpdateBook(ctx, Book{Title: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id: err = %v, want ErrInvalid", err)
	}
	b, err := db.CreateBook(ctx, "t")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.UpdateBook(ctx, Book{ID: b.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty title: err = %v, want ErrInvalid", err)
	}
}

// TestDeleteBookCascades pins the foreign-key cascade: a book's
// pages, cast, interviews and media rows are meaningless once the
// book is gone, so DeleteBook removes them all. (The blob FILES are
// mediastore's concern — PLAN.md §T11 owns the sweep.)
func TestDeleteBookCascades(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	b, err := db.CreateBook(ctx, "cascade me")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1, Text: "once"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira"}); err != nil {
		t.Fatalf("create cast: %v", err)
	}
	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("create interview: %v", err)
	}
	if err := db.UpdateInterview(ctx, Interview{ID: iv.ID, BookID: b.ID}); err != nil {
		t.Fatalf("link interview: %v", err)
	}
	m, err := db.CreateMedia(ctx, Media{BookID: b.ID, ContentType: "image/png", SizeBytes: 1})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}

	if err := db.DeleteBook(ctx, b.ID); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	if _, err := db.Page(ctx, b.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("page survived cascade: %v", err)
	}
	if cast, err := db.Cast(ctx, b.ID); err != nil || len(cast) != 0 {
		t.Fatalf("cast survived cascade: %v, %v", cast, err)
	}
	if _, err := db.Interview(ctx, iv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("interview survived cascade: %v", err)
	}
	if _, err := db.Media(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("media survived cascade: %v", err)
	}
}

// TestBookByline: question zero's answer round-trips through create →
// update → read → list, and clearing it is a legal state rather than a
// validation error (PLAN.md §The flow, screen 3 — a skipped question
// zero drops the title card's byline line, so "" must survive).
//
// Mutation: drop `byline` from either SELECT, or drop it from the
// UPDATE, and the round-trip assertions below fail.
func TestBookByline(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	b, err := db.CreateBook(ctx, "Mira and Bramble's Long Day")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if b.Byline != "" {
		t.Errorf("new book byline = %q, want empty", b.Byline)
	}

	b.Byline = "Mira"
	if err := db.UpdateBook(ctx, b); err != nil {
		t.Fatalf("update book: %v", err)
	}
	got, err := db.Book(ctx, b.ID)
	if err != nil {
		t.Fatalf("read book: %v", err)
	}
	if got.Byline != "Mira" {
		t.Errorf("byline = %q, want %q", got.Byline, "Mira")
	}
	if got.Title != b.Title {
		t.Errorf("title = %q, want %q", got.Title, b.Title)
	}

	// Listing carries it too — the shelf (screen 1) reads books, not one book.
	list, err := db.Books(ctx)
	if err != nil {
		t.Fatalf("books: %v", err)
	}
	if len(list) != 1 || list[0].Byline != "Mira" {
		t.Errorf("Books() = %+v, want one book with byline Mira", list)
	}

	// Skipping question zero is legal: an empty byline is not ErrInvalid.
	got.Byline = ""
	if err := db.UpdateBook(ctx, got); err != nil {
		t.Fatalf("clearing byline must be legal, got: %v", err)
	}
	cleared, err := db.Book(ctx, b.ID)
	if err != nil {
		t.Fatalf("read book: %v", err)
	}
	if cleared.Byline != "" {
		t.Errorf("byline after clear = %q, want empty", cleared.Byline)
	}
}

// TestCreateBookWithIDPreservesEverythingItIsGiven is §T11's prewarm
// requirement: a fixture restores a book under the id it was published
// at, with its byline and its original creation time.
func TestCreateBookWithIDPreservesEverythingItIsGiven(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	want := Book{
		ID:        "d625fd608be48227f08c33cf860e5de8",
		Title:     "Bo and Pip's Moon Mango Dance",
		Byline:    "monu",
		CreatedAt: time.Date(2026, 9, 6, 3, 54, 9, 0, time.UTC),
	}
	created, err := db.CreateBookWithID(ctx, want)
	if err != nil {
		t.Fatalf("create with id: %v", err)
	}
	if created != want {
		t.Fatalf("create returned %+v, want %+v", created, want)
	}
	got, err := db.Book(ctx, want.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ID != want.ID || got.Title != want.Title || got.Byline != want.Byline || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("read back %+v, want %+v", got, want)
	}
}

// TestCreateBookWithIDStampsAZeroCreatedAt exercises the one default in
// CreateBookWithID through its own default path.
func TestCreateBookWithIDStampsAZeroCreatedAt(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	before := time.Now().UTC().Truncate(time.Second)
	created, err := db.CreateBookWithID(ctx, Book{ID: "00000000000000000000000000000001", Title: "t"})
	if err != nil {
		t.Fatalf("create with id: %v", err)
	}
	if created.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want now (at or after %v)", created.CreatedAt, before)
	}
	if created.CreatedAt.Truncate(time.Second) != created.CreatedAt {
		t.Fatalf("CreatedAt = %v, want second precision", created.CreatedAt)
	}
}

func TestCreateBookWithIDRejectsBadInput(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if _, err := db.CreateBookWithID(ctx, Book{Title: "t"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id error = %v, want ErrInvalid", err)
	}
	if _, err := db.CreateBookWithID(ctx, Book{ID: "00000000000000000000000000000002"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty title error = %v, want ErrInvalid", err)
	}
	if _, err := db.CreateBookWithID(ctx, Book{ID: "00000000000000000000000000000003", Title: "t"}); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.CreateBookWithID(ctx, Book{ID: "00000000000000000000000000000003", Title: "t"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate id error = %v, want ErrConflict", err)
	}
}
