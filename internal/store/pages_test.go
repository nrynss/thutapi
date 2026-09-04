package store

import (
	"errors"
	"testing"
)

// pagesFixture returns a valid page for bookID.
func pagesFixture(bookID string, n int) Page {
	return Page{
		BookID:     bookID,
		N:          n,
		Text:       "Mira counted the raindrops.",
		Prompt:     "a small girl in a red raincoat under a grey sky",
		Characters: []string{"Mira", "Dragon"},
		Emotion:    "happy",
		Lines: []Line{
			{Character: "Mira", Text: "Look, the dragon is dancing!"},
			{Character: "Dragon", Text: "Splish, splash."},
		},
	}
}

// TestPageLifecycle covers create → read → ordered list → update →
// delete, including the JSON columns round-tripping.
func TestPageLifecycle(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "lifecycle")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	p := pagesFixture(b.ID, 1)
	if err := db.CreatePage(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.Page(ctx, b.ID, 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.BookID != p.BookID || got.Text != p.Text || got.Prompt != p.Prompt ||
		got.Emotion != p.Emotion || len(got.Characters) != 2 || len(got.Lines) != 2 ||
		got.Lines[1].Character != "Dragon" {
		t.Fatalf("read = %+v, want %+v", got, p)
	}

	// Insert out of order; the list must come back in page order.
	if err := db.CreatePage(ctx, pagesFixture(b.ID, 3)); err != nil {
		t.Fatalf("create page 3: %v", err)
	}
	if err := db.CreatePage(ctx, pagesFixture(b.ID, 2)); err != nil {
		t.Fatalf("create page 2: %v", err)
	}
	pages, err := db.Pages(ctx, b.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pages) != 3 || pages[0].N != 1 || pages[1].N != 2 || pages[2].N != 3 {
		t.Fatalf("list = %v, want pages ordered 1,2,3", pageNumbers(pages))
	}

	p2 := pages[1]
	p2.Text = "rewritten by Phase B"
	p2.Lines = nil
	if err := db.UpdatePage(ctx, p2); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = db.Page(ctx, b.ID, 2)
	if err != nil {
		t.Fatalf("read after update: %v", err)
	}
	if got.Text != "rewritten by Phase B" || got.Lines != nil {
		t.Fatalf("after update = %+v, want rewritten text and no lines", got)
	}

	if err := db.DeletePage(ctx, b.ID, 2); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Page(ctx, b.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err after delete = %v, want ErrNotFound", err)
	}
}

func pageNumbers(pages []Page) []int {
	ns := make([]int, len(pages))
	for i, p := range pages {
		ns[i] = p.N
	}
	return ns
}

// TestCreatePageValidations: bad input never reaches the database.
func TestCreatePageValidations(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	cases := []struct {
		name string
		page Page
		want error
	}{
		{"empty book id", Page{N: 1}, ErrInvalid},
		{"zero page number", Page{BookID: "00000000000000000000000000000000", N: 0}, ErrInvalid},
		{"negative page number", Page{BookID: "00000000000000000000000000000000", N: -3}, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := db.CreatePage(ctx, c.page)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// TestCreatePageTwiceConflicts pins the natural key: page n exists
// once per book, so a duplicate create is ErrConflict, not a silent
// overwrite.
func TestCreatePageTwiceConflicts(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "dup")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// TestCreatePageInUnknownBookIsInvalid pins the FK classification: a
// dangling book reference is the caller's bad input (ErrInvalid).
func TestCreatePageInUnknownBookIsInvalid(t *testing.T) {
	err := openTestDB(t).CreatePage(t.Context(), Page{BookID: "00000000000000000000000000000000", N: 1})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestPageNotFoundBranches pins ErrNotFound for every read/update/
// delete on a missing page.
func TestPageNotFoundBranches(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "missing")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if _, err := db.Page(ctx, b.ID, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read err = %v, want ErrNotFound", err)
	}
	if err := db.UpdatePage(ctx, pagesFixture(b.ID, 9)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update err = %v, want ErrNotFound", err)
	}
	if err := db.DeletePage(ctx, b.ID, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete err = %v, want ErrNotFound", err)
	}
}

// TestPagesRejectsEmptyBookID: listing without a book is invalid
// input, not "every page in the world".
func TestPagesRejectsEmptyBookID(t *testing.T) {
	if _, err := openTestDB(t).Pages(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestPageWithCorruptColumnIsInternal injects bytes this package
// never wrote into a JSON column and pins the ErrInternal wrap —
// undecodable columns are corruption, not caller input.
func TestPageWithCorruptColumnIsInternal(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "corrupt")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE pages SET characters = '{not json' WHERE book_id = ?`, b.ID); err != nil {
		t.Fatalf("corrupt column: %v", err)
	}
	if _, err := db.Page(ctx, b.ID, 1); !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal", err)
	}
}

// TestUpdatePageValidations: the update path validates its key like
// the create path does.
func TestUpdatePageValidations(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := db.UpdatePage(ctx, Page{N: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty book err = %v, want ErrInvalid", err)
	}
	if err := db.UpdatePage(ctx, Page{BookID: "x", N: 0}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero n err = %v, want ErrInvalid", err)
	}
}

// TestDeletePageValidations: deletes validate their key too.
func TestDeletePageValidations(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := db.DeletePage(ctx, "", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty book err = %v, want ErrInvalid", err)
	}
	if err := db.DeletePage(ctx, "x", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero n err = %v, want ErrInvalid", err)
	}
}
