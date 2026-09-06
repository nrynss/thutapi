// Package prewarm captures a finished book as a portable fixture and
// restores it into any data dir — without one model call.
//
// PLAN.md §T11 item 2 asks for prewarmed books as the landing
// experience, and names the reason plainly: the free window closed on
// 2026-09-06 and judging runs to the 11th, so every generation after
// that bills at ~$0.35 a book out of pocket. A finished book already
// exists on disk; what was missing is a way to keep it. A fixture is
// that way. It survives a redeploy, a lost SQLite file and a fresh
// machine, and restoring one costs a file copy.
//
// # What a fixture is
//
//	<dir>/<book id>/book.json    the manifest: book, pages, cast, media
//	<dir>/<book id>/media/<id>   one file per blob, named by its media id
//
// Ids are preserved end to end — the book's and every blob's — so
// /book/{id} and every /media/{id} inside it keep working across a
// restore. A prewarmed book's URL is the thing a judge is handed; a
// restore that renamed it would break the link it exists to serve.
//
// # What it deliberately does not do
//
// It never calls a model, and it never re-runs a pipeline stage. Export
// reads rows and blobs; Import writes them back. A book that is
// incomplete on disk is exported incomplete and restored incomplete —
// page 4 of the live 2026-09-06 book has no narration, which is the
// captioned-silent tier working as designed (PLAN.md §T10g), and a
// fixture that quietly filled that gap would be a fixture that lies.
//
// # Retention
//
// Restored books are the ones §T11 item 4's byte budget must never
// evict. Books returns their ids for exactly that, and cmd/thutapi
// hands them to mediastore.RetentionConfig.Protected.
package prewarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"thutapi/internal/mediastore"
	"thutapi/internal/store"
)

// manifestName is the file every fixture directory is identified by.
const manifestName = "book.json"

// mediaDirName is the subdirectory the blob files live in.
const mediaDirName = "media"

// ErrInvalidFixture is returned for a fixture that cannot be trusted:
// a manifest that does not parse, an id that is not the 32-character
// lowercase hex shape the store issues, a media entry with no blob, or
// a blob whose length does not match the manifest.
var ErrInvalidFixture = errors.New("prewarm: invalid fixture")

// Media is one blob inside a fixture, with the place it holds in the
// book. The zero Kind is an unplaced blob — the book film and the
// printable PDF are attached to the book with no role, which is how
// bookgen persists them.
type Media struct {
	ID          string          `json:"id"`
	Kind        store.MediaKind `json:"kind,omitempty"`
	PageN       int             `json:"page_n,omitempty"`
	CastName    string          `json:"cast_name,omitempty"`
	ContentType string          `json:"content_type"`
	SizeBytes   int64           `json:"size_bytes"`
}

// Fixture is one finished book, complete enough to serve from a data
// dir that has never seen it.
type Fixture struct {
	ID        string             `json:"id"`
	Title     string             `json:"title"`
	Byline    string             `json:"byline,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	Pages     []store.Page       `json:"pages"`
	Cast      []store.CastMember `json:"cast"`
	Media     []Media            `json:"media"`
	Note      string             `json:"note,omitempty"`
	Source    map[string]string  `json:"source,omitempty"`
	_         struct{}           // keep future fields additive
}

// bookReader is the export side's view of the store: rows out, nothing
// written. The consumer declares it (PLAN.md §Architectural
// invariants 3).
type bookReader interface {
	Book(ctx context.Context, id string) (store.Book, error)
	Pages(ctx context.Context, bookID string) ([]store.Page, error)
	Cast(ctx context.Context, bookID string) ([]store.CastMember, error)
	BookMedia(ctx context.Context, bookID string) ([]store.Media, error)
}

// bookWriter is the import side's view of the store.
type bookWriter interface {
	Book(ctx context.Context, id string) (store.Book, error)
	CreateBookWithID(ctx context.Context, b store.Book) (store.Book, error)
	CreatePage(ctx context.Context, p store.Page) error
	CreateCastMember(ctx context.Context, c store.CastMember) error
	SetMediaPlace(ctx context.Context, id string, p store.MediaPlace) error
	DeleteBook(ctx context.Context, id string) error
}

// blobWriter is the import side's view of mediastore: it restores a
// blob under the id the fixture recorded, which is what keeps every
// /media/{id} URL inside the book working.
type blobWriter interface {
	PersistWithID(ctx context.Context, id string, src io.Reader, contentType string) error
	DeleteIfPresent(ctx context.Context, id string) error
}

// Export writes bookID from db and mediaDir into dir/<book id>/, and
// returns the manifest it wrote. mediaDir is the directory
// internal/mediastore names its blobs in — one file per media id.
//
// An existing fixture for the same book is replaced.
func Export(ctx context.Context, db bookReader, mediaDir, bookID, dir string) (Fixture, error) {
	book, err := db.Book(ctx, bookID)
	if err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: %w", bookID, err)
	}
	pages, err := db.Pages(ctx, bookID)
	if err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: pages: %w", bookID, err)
	}
	cast, err := db.Cast(ctx, bookID)
	if err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: cast: %w", bookID, err)
	}
	rows, err := db.BookMedia(ctx, bookID)
	if err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: media: %w", bookID, err)
	}

	fixture := Fixture{
		ID:        book.ID,
		Title:     book.Title,
		Byline:    book.Byline,
		CreatedAt: book.CreatedAt,
		Pages:     pages,
		Cast:      cast,
	}
	target := filepath.Join(dir, bookID)
	if err := os.RemoveAll(target); err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: clear target: %w", bookID, err)
	}
	blobDir := filepath.Join(target, mediaDirName)
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: create fixture dir: %w", bookID, err)
	}
	for _, row := range rows {
		size, err := copyFile(filepath.Join(mediaDir, row.ID), filepath.Join(blobDir, row.ID))
		if err != nil {
			return Fixture{}, fmt.Errorf("prewarm: export %s: blob %s: %w", bookID, row.ID, err)
		}
		fixture.Media = append(fixture.Media, Media{
			ID:          row.ID,
			Kind:        row.Kind,
			PageN:       row.PageN,
			CastName:    row.CastName,
			ContentType: row.ContentType,
			SizeBytes:   size,
		})
	}
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: encode manifest: %w", bookID, err)
	}
	if err := os.WriteFile(filepath.Join(target, manifestName), append(raw, '\n'), 0o644); err != nil {
		return Fixture{}, fmt.Errorf("prewarm: export %s: write manifest: %w", bookID, err)
	}
	return fixture, nil
}

// Books lists the book ids a fixture directory holds, sorted, whether
// or not they are already in the database. It is what cmd/thutapi hands
// to the retention sweep's Protected list, so a prewarmed book is
// protected even on a run that imported nothing.
//
// A missing directory is not an error: prewarm is optional and a
// deployment without fixtures must still start.
func Books(dir string) ([]string, error) {
	fixtures, err := list(dir)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(fixtures))
	for _, f := range fixtures {
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

// Import restores every fixture in dir that the database does not
// already have, and returns the ids it restored. Books already present
// are left exactly as they are: a restore must never overwrite a book a
// reader is holding a link to.
//
// A partially restored book is rolled back — its rows deleted and its
// blobs removed — so a failure leaves the shelf as it was rather than
// half a book. A missing directory restores nothing and is not an
// error.
func Import(ctx context.Context, db bookWriter, blobs blobWriter, dir string) ([]string, error) {
	fixtures, err := list(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].ID < fixtures[j].ID })
	var restored []string
	for _, f := range fixtures {
		_, err := db.Book(ctx, f.ID)
		if err == nil {
			continue // already on the shelf
		}
		if !errors.Is(err, store.ErrNotFound) {
			return restored, fmt.Errorf("prewarm: import %s: %w", f.ID, err)
		}
		if err := restore(ctx, db, blobs, filepath.Join(dir, f.ID), f); err != nil {
			return restored, err
		}
		restored = append(restored, f.ID)
	}
	return restored, nil
}

// restore writes one fixture into the store, undoing its own work on
// any failure.
func restore(ctx context.Context, db bookWriter, blobs blobWriter, dir string, f Fixture) error {
	if _, err := db.CreateBookWithID(ctx, store.Book{ID: f.ID, Title: f.Title, Byline: f.Byline, CreatedAt: f.CreatedAt}); err != nil {
		return fmt.Errorf("prewarm: import %s: book: %w", f.ID, err)
	}
	// rollback drops everything this call created. The book cascade
	// takes the pages, cast and media rows; the blobs are ours to
	// remove. Its own failures are appended to the error the caller
	// already has, because the original failure is the useful one.
	rollback := func(cause error) error {
		for _, m := range f.Media {
			if err := blobs.DeleteIfPresent(ctx, m.ID); err != nil {
				cause = fmt.Errorf("%w (rollback blob %s: %v)", cause, m.ID, err)
			}
		}
		if err := db.DeleteBook(ctx, f.ID); err != nil {
			cause = fmt.Errorf("%w (rollback book %s: %v)", cause, f.ID, err)
		}
		return cause
	}
	for _, page := range f.Pages {
		page.BookID = f.ID
		if err := db.CreatePage(ctx, page); err != nil {
			return rollback(fmt.Errorf("prewarm: import %s: page %d: %w", f.ID, page.N, err))
		}
	}
	for _, member := range f.Cast {
		member.BookID = f.ID
		if err := db.CreateCastMember(ctx, member); err != nil {
			return rollback(fmt.Errorf("prewarm: import %s: cast %s: %w", f.ID, member.Name, err))
		}
	}
	for _, m := range f.Media {
		path := filepath.Join(dir, mediaDirName, m.ID)
		file, err := os.Open(path)
		if err != nil {
			return rollback(fmt.Errorf("prewarm: import %s: %w: blob %s: %v", f.ID, ErrInvalidFixture, m.ID, err))
		}
		err = blobs.PersistWithID(ctx, m.ID, file, m.ContentType)
		file.Close()
		if err != nil {
			return rollback(fmt.Errorf("prewarm: import %s: blob %s: %w", f.ID, m.ID, err))
		}
		place := store.MediaPlace{BookID: f.ID, Kind: m.Kind, PageN: m.PageN, CastName: m.CastName}
		if err := db.SetMediaPlace(ctx, m.ID, place); err != nil {
			return rollback(fmt.Errorf("prewarm: import %s: place %s: %w", f.ID, m.ID, err))
		}
	}
	return nil
}

// list reads and validates every manifest under dir. A missing dir is
// an empty list. A directory without a manifest is skipped — a fixture
// tree is a place an operator will also leave notes.
func list(dir string) ([]Fixture, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("prewarm: read fixture dir: %w", err)
	}
	var fixtures []Fixture
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name(), manifestName)
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("prewarm: read manifest %s: %w", path, err)
		}
		var f Fixture
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("prewarm: %w: manifest %s: %v", ErrInvalidFixture, path, err)
		}
		if err := validate(f, entry.Name(), filepath.Join(dir, entry.Name())); err != nil {
			return nil, err
		}
		fixtures = append(fixtures, f)
	}
	return fixtures, nil
}

// validate refuses a fixture that would produce a broken book. It
// checks what the store cannot: that the manifest's id matches the
// directory it was found in, that every id has the shape the store
// issues, and that every media entry has a blob of the recorded length
// behind it.
//
// Nothing here relaxes a validator to make the fixture pass. A fixture
// that fails is a fixture that is wrong.
func validate(f Fixture, dirName, dir string) error {
	if f.ID != dirName {
		return fmt.Errorf("prewarm: %w: %s holds a manifest for book %q", ErrInvalidFixture, dirName, f.ID)
	}
	if !validID(f.ID) {
		return fmt.Errorf("prewarm: %w: book id %q is not a 32-character hex id", ErrInvalidFixture, f.ID)
	}
	if f.Title == "" {
		return fmt.Errorf("prewarm: %w: book %s has no title", ErrInvalidFixture, f.ID)
	}
	seen := make(map[string]bool, len(f.Media))
	for _, m := range f.Media {
		if !validID(m.ID) {
			return fmt.Errorf("prewarm: %w: book %s: media id %q is not a 32-character hex id", ErrInvalidFixture, f.ID, m.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("prewarm: %w: book %s: media id %s appears twice", ErrInvalidFixture, f.ID, m.ID)
		}
		seen[m.ID] = true
		info, err := os.Stat(filepath.Join(dir, mediaDirName, m.ID))
		if err != nil {
			return fmt.Errorf("prewarm: %w: book %s: blob %s: %v", ErrInvalidFixture, f.ID, m.ID, err)
		}
		if info.Size() != m.SizeBytes {
			return fmt.Errorf("prewarm: %w: book %s: blob %s is %d bytes, manifest says %d",
				ErrInvalidFixture, f.ID, m.ID, info.Size(), m.SizeBytes)
		}
	}
	return nil
}

// validID is the store's id shape: 32 lowercase hex characters, 128
// bits of crypto/rand (store.NewID). A fixture carrying anything else
// did not come from this product.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// copyFile copies src to dst and returns the number of bytes written.
func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err != nil {
		out.Close()
		return 0, err
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	return n, nil
}

// Ensure a *mediastore.Store satisfies the blob seam this package
// declares. The check is free and it fails at compile time rather than
// at the first restore on a cold box.
var _ blobWriter = (*mediastore.Store)(nil)
