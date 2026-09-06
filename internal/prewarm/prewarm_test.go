package prewarm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"thutapi/internal/mediastore"
	"thutapi/internal/store"
)

// dataDir is one throwaway data dir: a SQLite file and a blob
// directory, the same pair cmd/thutapi opens.
type dataDir struct {
	db       *store.DB
	blobs    *mediastore.Store
	mediaDir string
}

func newDataDir(t *testing.T) dataDir {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(root, "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mediaDir := filepath.Join(root, "media")
	blobs, err := mediastore.Open(t.Context(), mediastore.Config{Dir: mediaDir, DB: db})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	return dataDir{db: db, blobs: blobs, mediaDir: mediaDir}
}

// seedBook writes a small but complete book: two pages, two cast
// members, a reference sheet, one illustration and one narration per
// page — except page 2, which has no narration, because the live
// 2026-09-06 book has exactly that hole on page 4 (the captioned-silent
// tier, PLAN.md §T10g) and a fixture has to carry it faithfully. The
// film and the PDF are attached to the book with no role, which is how
// bookgen persists them.
func seedBook(t *testing.T, d dataDir) string {
	t.Helper()
	ctx := t.Context()
	book, err := d.db.CreateBook(ctx, "Bo and Pip's Moon Mango Dance")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	book.Byline = "monu"
	if err := d.db.UpdateBook(ctx, book); err != nil {
		t.Fatalf("set byline: %v", err)
	}
	for n := 1; n <= 2; n++ {
		page := store.Page{
			BookID:     book.ID,
			N:          n,
			Text:       "page text",
			Prompt:     "a jungle clearing",
			Characters: []string{"Bo", "Pip"},
			Emotion:    "happy",
			Lines:      []store.Line{{Character: "Bo", Text: "hello"}},
		}
		if err := d.db.CreatePage(ctx, page); err != nil {
			t.Fatalf("create page %d: %v", n, err)
		}
	}
	for _, member := range []store.CastMember{
		{BookID: book.ID, Name: "Bo", Visual: "a silly monkey", Pitch: 4},
		{BookID: book.ID, Name: "Pip", Visual: "a tiny blue bird", Pitch: 10, SoundEffects: "bird_chirp"},
	} {
		if err := d.db.CreateCastMember(ctx, member); err != nil {
			t.Fatalf("create cast %s: %v", member.Name, err)
		}
	}

	place := func(contentType string, size int, p store.MediaPlace) {
		t.Helper()
		id, err := d.blobs.Persist(ctx, bytes.NewReader(bytes.Repeat([]byte{byte(size)}, size)), contentType)
		if err != nil {
			t.Fatalf("persist %s: %v", contentType, err)
		}
		p.BookID = book.ID
		if err := d.db.SetMediaPlace(ctx, id, p); err != nil {
			t.Fatalf("place %s: %v", contentType, err)
		}
	}
	place("image/jpeg", 300, store.MediaPlace{Kind: store.MediaReference, CastName: "Bo"})
	place("image/jpeg", 301, store.MediaPlace{Kind: store.MediaIllustration, PageN: 1})
	place("image/jpeg", 302, store.MediaPlace{Kind: store.MediaIllustration, PageN: 2})
	place("audio/mpeg", 120, store.MediaPlace{Kind: store.MediaNarration, PageN: 1})
	place("video/mp4", 400, store.MediaPlace{})
	place("application/pdf", 401, store.MediaPlace{})
	return book.ID
}

func exportTo(t *testing.T, d dataDir, bookID, dir string) Fixture {
	t.Helper()
	f, err := Export(t.Context(), d.db, d.mediaDir, bookID, dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	return f
}

// TestExportImportRoundTrip is the whole point: a finished book leaves
// one data dir and arrives in another with its ids, its rows and its
// bytes unchanged, and without a single model call.
func TestExportImportRoundTrip(t *testing.T) {
	source := newDataDir(t)
	bookID := seedBook(t, source)
	fixtures := t.TempDir()
	fixture := exportTo(t, source, bookID, fixtures)
	if len(fixture.Media) != 6 {
		t.Fatalf("exported %d media entries, want 6", len(fixture.Media))
	}

	target := newDataDir(t)
	restored, err := Import(t.Context(), target.db, target.blobs, fixtures)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(restored) != 1 || restored[0] != bookID {
		t.Fatalf("restored = %v, want [%s]", restored, bookID)
	}

	// The book id survives, which is what keeps /book/{id} working.
	before, err := source.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("source book: %v", err)
	}
	after, err := target.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("restored book: %v", err)
	}
	if after.ID != before.ID || after.Title != before.Title || after.Byline != before.Byline {
		t.Fatalf("restored book = %+v, want %+v", after, before)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("restored CreatedAt = %v, want %v", after.CreatedAt, before.CreatedAt)
	}

	beforePages, err := source.db.Pages(t.Context(), bookID)
	if err != nil {
		t.Fatalf("source pages: %v", err)
	}
	afterPages, err := target.db.Pages(t.Context(), bookID)
	if err != nil {
		t.Fatalf("restored pages: %v", err)
	}
	if len(afterPages) != len(beforePages) {
		t.Fatalf("restored %d pages, want %d", len(afterPages), len(beforePages))
	}
	for i := range beforePages {
		if afterPages[i].Text != beforePages[i].Text ||
			afterPages[i].Prompt != beforePages[i].Prompt ||
			afterPages[i].Emotion != beforePages[i].Emotion ||
			len(afterPages[i].Lines) != len(beforePages[i].Lines) ||
			len(afterPages[i].Characters) != len(beforePages[i].Characters) {
			t.Fatalf("page %d round-tripped as %+v, want %+v", i+1, afterPages[i], beforePages[i])
		}
	}

	beforeCast, err := source.db.Cast(t.Context(), bookID)
	if err != nil {
		t.Fatalf("source cast: %v", err)
	}
	afterCast, err := target.db.Cast(t.Context(), bookID)
	if err != nil {
		t.Fatalf("restored cast: %v", err)
	}
	if len(afterCast) != len(beforeCast) {
		t.Fatalf("restored %d cast members, want %d", len(afterCast), len(beforeCast))
	}
	for i := range beforeCast {
		if afterCast[i] != beforeCast[i] {
			t.Fatalf("cast %d round-tripped as %+v, want %+v", i, afterCast[i], beforeCast[i])
		}
	}

	// Every media id, its place and its bytes.
	beforeMedia, err := source.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("source media: %v", err)
	}
	afterMedia, err := target.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("restored media: %v", err)
	}
	if len(afterMedia) != len(beforeMedia) {
		t.Fatalf("restored %d media rows, want %d", len(afterMedia), len(beforeMedia))
	}
	index := make(map[string]store.Media, len(afterMedia))
	for _, m := range afterMedia {
		index[m.ID] = m
	}
	for _, m := range beforeMedia {
		got, ok := index[m.ID]
		if !ok {
			t.Fatalf("media %s did not survive the round trip", m.ID)
		}
		if got.Kind != m.Kind || got.PageN != m.PageN || got.CastName != m.CastName || got.ContentType != m.ContentType || got.SizeBytes != m.SizeBytes {
			t.Fatalf("media %s round-tripped as %+v, want %+v", m.ID, got, m)
		}
		want, err := os.ReadFile(filepath.Join(source.mediaDir, m.ID))
		if err != nil {
			t.Fatalf("read source blob %s: %v", m.ID, err)
		}
		have, err := os.ReadFile(filepath.Join(target.mediaDir, m.ID))
		if err != nil {
			t.Fatalf("read restored blob %s: %v", m.ID, err)
		}
		if !bytes.Equal(have, want) {
			t.Fatalf("blob %s round-tripped with different bytes", m.ID)
		}
	}

	// A second import is a no-op: the book is already on the shelf.
	again, err := Import(t.Context(), target.db, target.blobs, fixtures)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second import restored %v, want nothing", again)
	}
}

// TestImportNeverOverwritesAnExistingBook: a reader may be holding a
// link to the book already there.
func TestImportNeverOverwritesAnExistingBook(t *testing.T) {
	d := newDataDir(t)
	bookID := seedBook(t, d)
	fixtures := t.TempDir()
	exportTo(t, d, bookID, fixtures)

	book, err := d.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	book.Title = "A title an operator changed by hand"
	if err := d.db.UpdateBook(t.Context(), book); err != nil {
		t.Fatalf("update book: %v", err)
	}
	restored, err := Import(t.Context(), d.db, d.blobs, fixtures)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(restored) != 0 {
		t.Fatalf("import restored %v over a book that was already there", restored)
	}
	after, err := d.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book after import: %v", err)
	}
	if after.Title != book.Title {
		t.Fatalf("title = %q, want the operator's %q", after.Title, book.Title)
	}
}

func TestBooksListsEveryFixtureID(t *testing.T) {
	d := newDataDir(t)
	first := seedBook(t, d)
	second := seedBook(t, d)
	fixtures := t.TempDir()
	exportTo(t, d, first, fixtures)
	exportTo(t, d, second, fixtures)

	ids, err := Books(fixtures)
	if err != nil {
		t.Fatalf("Books: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("Books returned %v, want two ids", ids)
	}
	want := map[string]bool{first: true, second: true}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("Books returned unexpected id %s", id)
		}
	}
	if ids[0] > ids[1] {
		t.Fatalf("Books returned %v, want sorted ids", ids)
	}
}

// TestMissingAndEmptyDirectoriesAreNotErrors: prewarm is optional, and a
// deployment with no fixtures must still boot.
func TestMissingAndEmptyDirectoriesAreNotErrors(t *testing.T) {
	d := newDataDir(t)
	cases := []struct {
		name string
		dir  string
	}{
		{"unset", ""},
		{"missing", filepath.Join(t.TempDir(), "no-such-dir")},
		{"empty", t.TempDir()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids, err := Books(tc.dir)
			if err != nil {
				t.Fatalf("Books: %v", err)
			}
			if len(ids) != 0 {
				t.Fatalf("Books = %v, want none", ids)
			}
			restored, err := Import(t.Context(), d.db, d.blobs, tc.dir)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if len(restored) != 0 {
				t.Fatalf("Import restored %v, want nothing", restored)
			}
		})
	}
}

// TestNonFixtureEntriesAreSkipped: an operator will keep notes in the
// fixture tree, and a stray file must not stop a boot.
func TestNonFixtureEntriesAreSkipped(t *testing.T) {
	fixtures := t.TempDir()
	if err := os.WriteFile(filepath.Join(fixtures, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.Mkdir(filepath.Join(fixtures, "scratch"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ids, err := Books(fixtures)
	if err != nil {
		t.Fatalf("Books: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("Books = %v, want none", ids)
	}
}

// writeFixture writes a hand-built fixture so the validator can be
// driven at each of its refusals.
func writeFixture(t *testing.T, root, dirName string, f Fixture, blobs map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(filepath.Join(dir, mediaDirName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for id, body := range blobs {
		if err := os.WriteFile(filepath.Join(dir, mediaDirName, id), body, 0o600); err != nil {
			t.Fatalf("write blob: %v", err)
		}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), raw, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

const goodID = "d625fd608be48227f08c33cf860e5de8"
const goodMediaID = "fbe841abbcc038394af5b6ebec4875ca"

// TestValidatorRefusesABrokenFixture: nothing here is relaxed to make a
// fixture pass. A fixture that fails is a fixture that is wrong.
func TestValidatorRefusesABrokenFixture(t *testing.T) {
	cases := []struct {
		name    string
		dirName string
		fixture Fixture
		blobs   map[string][]byte
	}{
		{
			name:    "manifest names a different book than its directory",
			dirName: goodID,
			fixture: Fixture{ID: "0000000000000000000000000000dead", Title: "t"},
		},
		{
			name:    "book id is not the store's shape",
			dirName: "not-a-book-id",
			fixture: Fixture{ID: "not-a-book-id", Title: "t"},
		},
		{
			name:    "book id is uppercase hex",
			dirName: strings.ToUpper(goodID),
			fixture: Fixture{ID: strings.ToUpper(goodID), Title: "t"},
		},
		{
			name:    "no title",
			dirName: goodID,
			fixture: Fixture{ID: goodID},
		},
		{
			name:    "media id is not the store's shape",
			dirName: goodID,
			fixture: Fixture{ID: goodID, Title: "t", Media: []Media{{ID: "short", ContentType: "image/png"}}},
		},
		{
			name:    "media id appears twice",
			dirName: goodID,
			fixture: Fixture{ID: goodID, Title: "t", Media: []Media{
				{ID: goodMediaID, ContentType: "image/png", SizeBytes: 3},
				{ID: goodMediaID, ContentType: "image/png", SizeBytes: 3},
			}},
			blobs: map[string][]byte{goodMediaID: []byte("abc")},
		},
		{
			name:    "media entry with no blob behind it",
			dirName: goodID,
			fixture: Fixture{ID: goodID, Title: "t", Media: []Media{{ID: goodMediaID, ContentType: "image/png", SizeBytes: 3}}},
		},
		{
			name:    "blob length disagrees with the manifest",
			dirName: goodID,
			fixture: Fixture{ID: goodID, Title: "t", Media: []Media{{ID: goodMediaID, ContentType: "image/png", SizeBytes: 99}}},
			blobs:   map[string][]byte{goodMediaID: []byte("abc")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, tc.dirName, tc.fixture, tc.blobs)
			if _, err := Books(root); !errors.Is(err, ErrInvalidFixture) {
				t.Fatalf("Books error = %v, want ErrInvalidFixture", err)
			}
			d := newDataDir(t)
			if _, err := Import(t.Context(), d.db, d.blobs, root); !errors.Is(err, ErrInvalidFixture) {
				t.Fatalf("Import error = %v, want ErrInvalidFixture", err)
			}
		})
	}
}

func TestUnparseableManifestIsAnInvalidFixture(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, goodID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if _, err := Books(root); !errors.Is(err, ErrInvalidFixture) {
		t.Fatalf("Books error = %v, want ErrInvalidFixture", err)
	}
}

// TestImportRollsBackAPartialRestore: a fixture whose content type the
// media store refuses must leave the shelf exactly as it was, not half
// a book.
func TestImportRollsBackAPartialRestore(t *testing.T) {
	source := newDataDir(t)
	bookID := seedBook(t, source)
	fixtures := t.TempDir()
	fixture := exportTo(t, source, bookID, fixtures)

	// Corrupt one media entry's content type. mediastore's closed type
	// set refuses it, which is exactly the mid-restore failure the
	// rollback exists for.
	fixture.Media[2].ContentType = "text/plain"
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, bookID, manifestName), raw, 0o600); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	target := newDataDir(t)
	restored, err := Import(t.Context(), target.db, target.blobs, fixtures)
	if !errors.Is(err, mediastore.ErrInvalidContentType) {
		t.Fatalf("Import error = %v, want ErrInvalidContentType", err)
	}
	if len(restored) != 0 {
		t.Fatalf("Import reported %v restored on a failure", restored)
	}
	if _, err := target.db.Book(t.Context(), bookID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a half-restored book was left on the shelf: %v", err)
	}
	entries, err := os.ReadDir(target.mediaDir)
	if err != nil {
		t.Fatalf("read media dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("rollback left %d blobs behind", len(entries))
	}
}

func TestExportUnknownBook(t *testing.T) {
	d := newDataDir(t)
	if _, err := Export(t.Context(), d.db, d.mediaDir, "0000000000000000000000000000dead", t.TempDir()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Export error = %v, want store.ErrNotFound", err)
	}
}

func TestExportMissingBlobIsAnError(t *testing.T) {
	d := newDataDir(t)
	bookID := seedBook(t, d)
	rows, err := d.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if err := os.Remove(filepath.Join(d.mediaDir, rows[0].ID)); err != nil {
		t.Fatalf("remove blob: %v", err)
	}
	if _, err := Export(t.Context(), d.db, d.mediaDir, bookID, t.TempDir()); err == nil {
		t.Fatal("Export succeeded with a missing blob")
	}
}

// TestExportReplacesAnExistingFixture: re-exporting after a regeneration
// must not leave the previous run's blobs beside the new ones.
func TestExportReplacesAnExistingFixture(t *testing.T) {
	d := newDataDir(t)
	bookID := seedBook(t, d)
	fixtures := t.TempDir()
	exportTo(t, d, bookID, fixtures)
	stale := filepath.Join(fixtures, bookID, mediaDirName, "00000000000000000000000000000000")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale blob: %v", err)
	}
	exportTo(t, d, bookID, fixtures)
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a stale blob survived the re-export: %v", err)
	}
}

// TestImportSurvivesAFixtureWithNoMedia: a book with rows and no blobs
// is degenerate but not invalid, and it must not panic a boot.
func TestImportSurvivesAFixtureWithNoMedia(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, goodID, Fixture{ID: goodID, Title: "empty"}, nil)
	d := newDataDir(t)
	restored, err := Import(t.Context(), d.db, d.blobs, root)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(restored) != 1 {
		t.Fatalf("restored = %v, want one book", restored)
	}
	if _, err := d.db.Book(t.Context(), goodID); err != nil {
		t.Fatalf("book was not restored: %v", err)
	}
}

// stubBookWriter fails on demand so Import's error paths can be reached
// without corrupting a real store.
type stubBookWriter struct {
	inner   bookWriter
	failOn  string
	failErr error
}

func (s stubBookWriter) Book(ctx context.Context, id string) (store.Book, error) {
	if s.failOn == "Book" {
		return store.Book{}, s.failErr
	}
	return s.inner.Book(ctx, id)
}

func (s stubBookWriter) CreateBookWithID(ctx context.Context, b store.Book) (store.Book, error) {
	if s.failOn == "CreateBookWithID" {
		return store.Book{}, s.failErr
	}
	return s.inner.CreateBookWithID(ctx, b)
}

func (s stubBookWriter) CreatePage(ctx context.Context, p store.Page) error {
	if s.failOn == "CreatePage" {
		return s.failErr
	}
	return s.inner.CreatePage(ctx, p)
}

func (s stubBookWriter) CreateCastMember(ctx context.Context, c store.CastMember) error {
	if s.failOn == "CreateCastMember" {
		return s.failErr
	}
	return s.inner.CreateCastMember(ctx, c)
}

func (s stubBookWriter) SetMediaPlace(ctx context.Context, id string, p store.MediaPlace) error {
	if s.failOn == "SetMediaPlace" {
		return s.failErr
	}
	return s.inner.SetMediaPlace(ctx, id, p)
}

func (s stubBookWriter) DeleteBook(ctx context.Context, id string) error {
	return s.inner.DeleteBook(ctx, id)
}

// TestImportFailurePathsRollBack drives each write that can fail
// mid-restore and asserts the shelf is left as it was.
func TestImportFailurePathsRollBack(t *testing.T) {
	source := newDataDir(t)
	bookID := seedBook(t, source)
	fixtures := t.TempDir()
	exportTo(t, source, bookID, fixtures)

	boom := errors.New("boom")
	for _, stage := range []string{"Book", "CreateBookWithID", "CreatePage", "CreateCastMember", "SetMediaPlace"} {
		t.Run(stage, func(t *testing.T) {
			target := newDataDir(t)
			db := stubBookWriter{inner: target.db, failOn: stage, failErr: boom}
			restored, err := Import(t.Context(), db, target.blobs, fixtures)
			if !errors.Is(err, boom) {
				t.Fatalf("Import error = %v, want boom", err)
			}
			if len(restored) != 0 {
				t.Fatalf("Import reported %v restored", restored)
			}
			if _, err := target.db.Book(t.Context(), bookID); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("a half-restored book survived %s: %v", stage, err)
			}
			entries, err := os.ReadDir(target.mediaDir)
			if err != nil {
				t.Fatalf("read media dir: %v", err)
			}
			if len(entries) != 0 {
				t.Fatalf("%s left %d blobs behind", stage, len(entries))
			}
		})
	}
}
