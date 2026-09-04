package store

import (
	"context"
	"errors"
	"testing"
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
