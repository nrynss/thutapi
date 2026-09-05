// Package store persists Thutapi's books on SQLite.
//
// The driver is modernc.org/sqlite — the one third-party module
// PLAN.md §T0 sanctions, pure Go so CGO_ENABLED=0 and the distroless
// image keep working (PLAN.md §T3).
//
// Schema per PLAN.md §T3, with columns taken from project.md §1's
// Phase-B JSON (title; per page: text, prompt, characters, emotion,
// lines; per cast member: visual and voice) rather than invented:
//
//	books      one picture book: id, title, created_at
//	pages      one spread: text, illustration prompt, characters,
//	           emotion, spoken lines — keyed (book_id, n)
//	cast       the cast bible: name, visual, voice pitch and sound
//	           effects — keyed (book_id, name)
//	interviews the Phase-A transcript and its book link
//	media      metadata for one stored blob: the book it belongs
//	           to and — once placed — its role (cast reference
//	           sheet, page illustration, page narration) with the
//	           page or cast member it anchors to; the bytes live on
//	           disk under the data dir and the rows are written here
//	           by internal/mediastore
//
// Configuration arrives through Config — this package never reads the
// environment (PLAN.md invariant 2). Open returns the concrete *DB;
// consumers declare their own interfaces over it (invariant 3). Every
// operation takes a context and respects its cancellation. Errors
// cross the package boundary as the sentinels below, matched with
// errors.Is (invariant 8). Writes survive a process restart: the
// SQLite file lives under the configured data dir, next to the media
// blobs.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// idBytes is the entropy per id: 128 bits, hex-encoded to 32
// characters. PLAN.md §T3 requires unguessable media paths, so the
// ids come from crypto/rand, never a counter.
const idBytes = 16

// ErrNotFound is returned when a row does not exist: an unknown book,
// page, cast member, interview or media id. Match with errors.Is
// (PLAN.md invariant 8).
var ErrNotFound = errors.New("not found")

// ErrInvalid is returned when caller input is unusable: an empty
// required field, a page number below 1, or a reference to a book
// that does not exist. The error message names the field.
var ErrInvalid = errors.New("invalid input")

// ErrConflict is returned when a create would violate a natural key
// that must stay unique: a page number twice in one book, a cast name
// twice in one book.
var ErrConflict = errors.New("conflict")

// ErrInternal is returned for failures that are the store's, not the
// caller's: id generation, a column holding undecodable JSON. These
// are bugs or corruption, never bad input.
var ErrInternal = errors.New("internal error")

// Config configures Open.
type Config struct {
	// Path is the filesystem path of the SQLite database file, e.g.
	// "<data-dir>/thutapi.db". The file is created if absent and its
	// parent directory is created if needed. Must not be empty.
	Path string
}

// DB is the SQLite-backed store. Create it with Open; the zero value
// is not usable. It is safe for concurrent use: the pool holds a
// single connection, which serialises access and keeps every statement
// on a connection carrying the DSN pragmas (foreign keys on, busy
// timeout, WAL).
type DB struct {
	db *sql.DB

	// rand is the source of unguessable ids. Open sets it to
	// crypto/rand's Reader; tests substitute a deterministic or
	// failing source.
	rand io.Reader
}

// Open opens (creating if absent) the SQLite database at cfg.Path and
// applies the schema. The returned DB must be closed with Close.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("store: open: %w: Path must not be empty", ErrInvalid)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("store: open: create data dir: %w", err)
	}
	// The DSN carries the pragmas so every pooled connection gets them
	// at open, not just the first: foreign keys make DeleteBook cascade
	// to pages, cast, interviews and media; the busy timeout rides out
	// lock contention; WAL keeps a crash mid-write from corrupting the
	// last committed transaction. The path goes in absolute: SQLite's
	// URI parser rejects a relative path, and pinning it now also
	// guards against a later chdir moving the database out from under
	// an open handle.
	abs, err := filepath.Abs(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("store: open: resolve path: %w", err)
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     abs,
		RawQuery: "_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL",
	}).String()
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// One connection: SQLite takes one writer at a time, and a second
	// pooled connection could only ever produce SQLITE_BUSY. Serialise
	// instead of retry.
	sqldb.SetMaxOpenConns(1)
	db := &DB{db: sqldb, rand: rand.Reader}
	if err := sqldb.PingContext(ctx); err != nil {
		sqldb.Close() // nothing to drain; the pool never opened a live session
		return nil, fmt.Errorf("store: open: ping: %w", err)
	}
	if err := db.migrate(ctx); err != nil {
		sqldb.Close() // ditto — a failed migration leaves nothing to drain
		return nil, err
	}
	return db, nil
}

// Close closes the database. Operations after Close fail.
func (d *DB) Close() error {
	return d.db.Close()
}

// schema is applied by migrate in order. CREATE TABLE IF NOT EXISTS
// makes it idempotent, so reopening an existing data dir — the
// container restart PLAN.md §T3's Done when is about — is a no-op.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS books (
		id         TEXT PRIMARY KEY,
		title      TEXT NOT NULL,
		byline     TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS pages (
		book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
		n          INTEGER NOT NULL CHECK (n >= 1),
		text       TEXT NOT NULL DEFAULT '',
		prompt     TEXT NOT NULL DEFAULT '',
		characters TEXT NOT NULL DEFAULT '[]',
		emotion    TEXT NOT NULL DEFAULT '',
		lines      TEXT NOT NULL DEFAULT '[]',
		PRIMARY KEY (book_id, n)
	)`,
	// "cast" is a SQL keyword and quoted everywhere it appears.
	`CREATE TABLE IF NOT EXISTS "cast" (
		book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
		name          TEXT NOT NULL,
		visual        TEXT NOT NULL DEFAULT '',
		pitch         REAL NOT NULL DEFAULT 0,
		sound_effects TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (book_id, name)
	)`,
	`CREATE TABLE IF NOT EXISTS interviews (
		id         TEXT PRIMARY KEY,
		book_id    TEXT REFERENCES books(id) ON DELETE CASCADE,
		turns      TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL
	)`,
	// A media row is either unplaced — book_id NULL, kind '' — or
	// attached to a book with a role and exactly the anchor that
	// role implies: a reference sheet names its cast member, an
	// illustration or a narration names its page. The composite
	// foreign keys make a dangling anchor impossible and cascade to
	// the book cascade's rule: rows go, files are §T11's sweep. The
	// partial unique indexes make one blob per slot (one reference
	// per cast member, one illustration and one narration per page)
	// the database's rule, not a convention. SetMediaPlace writes
	// kind/page_n/cast_name.
	`CREATE TABLE IF NOT EXISTS media (
		id           TEXT PRIMARY KEY,
		book_id      TEXT REFERENCES books(id) ON DELETE CASCADE,
		kind         TEXT NOT NULL DEFAULT '',
		page_n       INTEGER,
		cast_name    TEXT,
		content_type TEXT NOT NULL,
		size_bytes   INTEGER NOT NULL,
		created_at   INTEGER NOT NULL,
		FOREIGN KEY (book_id, page_n) REFERENCES pages (book_id, n) ON DELETE CASCADE,
		FOREIGN KEY (book_id, cast_name) REFERENCES "cast" (book_id, name) ON DELETE CASCADE,
		CHECK (
			(book_id IS NULL AND kind = '' AND page_n IS NULL AND cast_name IS NULL)
			OR (book_id IS NOT NULL AND (
				(kind = '' AND page_n IS NULL AND cast_name IS NULL)
				OR (kind = 'reference' AND page_n IS NULL AND cast_name IS NOT NULL)
				OR (kind IN ('illustration', 'narration') AND page_n IS NOT NULL AND cast_name IS NULL)
			))
		)
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS media_page_illustration ON media (book_id, page_n) WHERE kind = 'illustration'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS media_page_narration ON media (book_id, page_n) WHERE kind = 'narration'`,
	`CREATE UNIQUE INDEX IF NOT EXISTS media_cast_reference ON media (book_id, cast_name) WHERE kind = 'reference'`,
}

func (d *DB) migrate(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := d.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}

// NewID returns a fresh unguessable id: 128 bits of crypto/rand,
// hex-encoded to 32 lowercase characters. Media paths and book links
// are only safe to share because these ids cannot be enumerated
// (PLAN.md §T3). internal/mediastore uses it to name blob files and
// rows identically.
func NewID() (string, error) {
	return randomID(rand.Reader)
}

// randomID draws idBytes of entropy from r.
func randomID(r io.Reader) (string, error) {
	var b [idBytes]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("store: new id: %w: %w", ErrInternal, err)
	}
	return hex.EncodeToString(b[:]), nil
}

// newID is randomID bound to the DB's id source.
func (d *DB) newID() (string, error) {
	return randomID(d.rand)
}

// classifyConstraint maps a SQLite constraint violation to its
// sentinel: a duplicate natural key is ErrConflict, a dangling parent
// reference is ErrInvalid. The driver runs with extended result codes,
// so the code names the specific violation. Any other error comes back
// unchanged.
func classifyConstraint(err error) error {
	var serr *sqlite.Error
	if !errors.As(err, &serr) {
		return err
	}
	switch serr.Code() {
	case sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY, sqlite3.SQLITE_CONSTRAINT_UNIQUE:
		return ErrConflict
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return ErrInvalid
	}
	return err
}

// rowScanner is the Scan subset shared by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTime converts a unix-seconds column value to the UTC time the
// store hands out. Times round-trip exactly because writes truncate to
// the second before storing.
func scanTime(unix int64) time.Time {
	return time.Unix(unix, 0).UTC()
}
