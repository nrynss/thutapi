package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openTestDB opens a store on a throwaway file. Most tests want the
// same ceremony; the store lives or dies with t.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "thutapi.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestOpenRejectsEmptyPath pins Config validation: a store without a
// file path has nowhere to live.
func TestOpenRejectsEmptyPath(t *testing.T) {
	_, err := Open(t.Context(), Config{})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestOpenCreatesDataDir pins that Open brings its own directory up —
// the configured data dir may not exist yet on first boot.
func TestOpenCreatesDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	db, err := Open(t.Context(), Config{Path: filepath.Join(dir, "thutapi.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
}

// TestNewIDIsUnguessable pins the id shape PLAN.md §T3's shareability
// rides on: 32 lowercase hex characters — 128 bits of entropy — and
// never a repeat. A counter or a timestamp would make /media/ URLs
// enumerable.
func TestNewIDIsUnguessable(t *testing.T) {
	seen := make(map[string]bool, 256)
	for i := 0; i < 256; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if len(id) != idBytes*2 {
			t.Fatalf("id %q has length %d, want %d", id, len(id), idBytes*2)
		}
		if strings.ToLower(id) != id || strings.TrimSpace(id) != id {
			t.Fatalf("id %q is not canonical lowercase hex", id)
		}
		for _, c := range id {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Fatalf("id %q contains non-hex character %q", id, c)
			}
		}
		if seen[id] {
			t.Fatalf("NewID repeated %q across 256 draws", id)
		}
		seen[id] = true
	}
}

// failingReader simulates crypto/rand failure: a dead entropy source
// must fail every id-generating create with ErrInternal.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestCreateWithDeadRandSourceIsInternal(t *testing.T) {
	db := openTestDB(t)
	db.rand = failingReader{}
	ctx := t.Context()
	cases := []struct {
		name string
		call func() error
	}{
		{"create book", func() error { _, err := db.CreateBook(ctx, "Mira"); return err }},
		{"create interview", func() error { _, err := db.CreateInterview(ctx); return err }},
		{"create media", func() error { _, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 1}); return err }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, ErrInternal) {
				t.Fatalf("err = %v, want ErrInternal", err)
			}
		})
	}
}

// TestOpenFailsOnGarbageFile pins the honest failure when the path
// exists but is not a database — e.g. the data dir was clobbered.
// Open surfaces the driver's error rather than pretending to succeed.
func TestOpenFailsOnGarbageFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-db")
	if err := os.WriteFile(path, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), Config{Path: path}); err == nil {
		t.Fatal("open on a non-database file succeeded")
	}
}

// TestOpenFailsWhenPathIsUnderAFile pins the MkdirAll failure branch:
// a path whose parent is a regular file cannot host a database.
func TestOpenFailsWhenPathIsUnderAFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), Config{Path: filepath.Join(blocker, "thutapi.db")}); err == nil {
		t.Fatal("open under a regular-file parent succeeded")
	}
}

// TestReopenReadsBackSchema pins the restart half of PLAN.md §T3's
// Done when at the schema level: reopening an existing database is a
// no-op migration, and rows written before the restart read back
// after it. The full end-to-end survival test (blob + Range serving)
// lives in internal/mediastore.
func TestReopenReadsBackSchema(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "thutapi.db")

	db, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	b, err := db.CreateBook(ctx, "Mira and the Rain Dragon")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := Open(ctx, Config{Path: path})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	got, err := db2.Book(ctx, b.ID)
	if err != nil {
		t.Fatalf("book after reopen: %v", err)
	}
	if got.Title != b.Title || !got.CreatedAt.Equal(b.CreatedAt) {
		t.Fatalf("book = %+v, want title %q created %v", got, b.Title, b.CreatedAt)
	}
}
