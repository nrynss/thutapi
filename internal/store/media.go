package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MediaKind names the role a blob plays in its book (project.md
// §Pipeline): a cast member's reference sheet, a page's illustration
// or a page's narration. The zero value means the blob is not placed
// yet — mediastore persists bytes before their book exists.
type MediaKind string

const (
	// MediaReference is one cast member's reference sheet: the image
	// every page render of that member is generated against (the
	// image lock, project.md §2).
	MediaReference MediaKind = "reference"
	// MediaIllustration is one page's picture, generated image-to-
	// image against the cast reference sheets.
	MediaIllustration MediaKind = "illustration"
	// MediaNarration is one page's read-aloud audio.
	MediaNarration MediaKind = "narration"
)

// MediaPlace is a blob's place in its book: the book, the role it
// plays there (Kind) and the anchor that role implies — a cast
// member for a reference sheet, a page number for an illustration or
// a narration. The zero place (empty BookID) detaches.
type MediaPlace struct {
	BookID string
	Kind   MediaKind
	// PageN anchors an illustration or a narration to its 1-based
	// page. It must be zero for every other kind.
	PageN int
	// CastName anchors a reference sheet to its cast member. It must
	// be empty for every other kind.
	CastName string
}

// Media is the metadata row of one stored blob. The bytes live on
// disk under the data dir — internal/mediastore writes them and calls
// this package for the row — so SizeBytes and ContentType are
// functions of the file and never change after CreateMedia. ID is
// unguessable (see NewID), which is what makes a /media/ URL
// shareable without being enumerable (PLAN.md §T3).
type Media struct {
	ID string
	// BookID is empty until the blob is attached to a book.
	BookID string
	// Kind is the blob's role in its book; empty while the blob is
	// unplaced — freshly persisted, or attached to a book but not
	// yet given a role.
	Kind MediaKind
	// PageN is the page an illustration or a narration belongs to;
	// 0 for every other kind.
	PageN int
	// CastName is the cast member a reference sheet belongs to;
	// empty for every other kind.
	CastName string
	// ContentType is the served MIME type (e.g. audio/mpeg,
	// image/png).
	ContentType string
	// SizeBytes is the blob's length in bytes.
	SizeBytes int64
	// CreatedAt is store-assigned and never changes.
	CreatedAt time.Time
}

// CreateMedia inserts the metadata row and returns it with its ID and
// CreatedAt assigned. The caller may pre-set ID — internal/mediastore
// pre-generates it so the blob file and the row share the name;
// otherwise a fresh unguessable id is assigned. ContentType must not
// be empty; SizeBytes must not be negative; a non-empty BookID must
// name an existing book (ErrInvalid otherwise). A new row is always
// unplaced: the role and anchor are set afterwards with
// SetMediaPlace, so a Kind, PageN or CastName here is ErrInvalid.
func (d *DB) CreateMedia(ctx context.Context, m Media) (Media, error) {
	if m.ContentType == "" {
		return Media{}, fmt.Errorf("store: create media: %w: content type must not be empty", ErrInvalid)
	}
	if m.SizeBytes < 0 {
		return Media{}, fmt.Errorf("store: create media: %w: size %d must not be negative", ErrInvalid, m.SizeBytes)
	}
	if m.Kind != "" || m.PageN != 0 || m.CastName != "" {
		return Media{}, fmt.Errorf("store: create media: %w: a new blob is unplaced; set its place with SetMediaPlace", ErrInvalid)
	}
	id := m.ID
	if id == "" {
		assigned, err := d.newID()
		if err != nil {
			return Media{}, fmt.Errorf("store: create media: %w", err)
		}
		id = assigned
	}
	m.ID = id
	m.CreatedAt = time.Now().UTC().Truncate(time.Second)
	var book any // NULL when unattached
	if m.BookID != "" {
		book = m.BookID
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO media (id, book_id, kind, page_n, cast_name, content_type, size_bytes, created_at)
		 VALUES (?, ?, '', NULL, NULL, ?, ?, ?)`,
		m.ID, book, m.ContentType, m.SizeBytes, m.CreatedAt.Unix())
	if err != nil {
		return Media{}, fmt.Errorf("store: create media: %w", classifyConstraint(err))
	}
	return m, nil
}

// Media returns the metadata row for the given id, or an error
// wrapping ErrNotFound.
func (d *DB) Media(ctx context.Context, id string) (Media, error) {
	return scanMedia(d.db.QueryRowContext(ctx,
		`SELECT id, book_id, kind, page_n, cast_name, content_type, size_bytes, created_at
		 FROM media WHERE id = ?`, id), "media "+id)
}

// SetMediaPlace attaches the blob to its place in a book, replacing
// any previous place: the book, the role it plays there (Kind) and
// the anchor that role implies. The zero place detaches. The media
// row must exist (ErrNotFound) and the anchored book, page or cast
// member must exist (ErrInvalid — the foreign key fires on update,
// not just insert). The blob's id, content type and size never
// change. A slot that is already taken — a second illustration for
// the same page, a second reference for the same cast member — is
// ErrConflict: one illustration and one narration per page, one
// reference per cast member.
func (d *DB) SetMediaPlace(ctx context.Context, id string, p MediaPlace) error {
	if id == "" {
		return fmt.Errorf("store: set media place: %w: media id must not be empty", ErrInvalid)
	}
	// The columns to write. The nil-valued anys are NULLs; the shape
	// of the anchor is decided by the kind, so a reference can never
	// carry a page and an illustration can never carry a cast member.
	var book, page, cast any
	kind := ""
	switch {
	case p.BookID == "":
		if p.Kind != "" || p.PageN != 0 || p.CastName != "" {
			return fmt.Errorf("store: set media place: %w: an empty book detaches; no other field may be set", ErrInvalid)
		}
	case p.Kind == "":
		if p.PageN != 0 || p.CastName != "" {
			return fmt.Errorf("store: set media place: %w: a blob attached without a kind has no anchor", ErrInvalid)
		}
		book = p.BookID
	case p.Kind == MediaReference:
		if p.CastName == "" || p.PageN != 0 {
			return fmt.Errorf("store: set media place: %w: a reference names a cast member and no page", ErrInvalid)
		}
		book, cast = p.BookID, p.CastName
		kind = string(p.Kind)
	case p.Kind == MediaIllustration || p.Kind == MediaNarration:
		if p.PageN < 1 || p.CastName != "" {
			return fmt.Errorf("store: set media place: %w: a %s names a page of at least 1 and no cast member", ErrInvalid, p.Kind)
		}
		book, page = p.BookID, p.PageN
		kind = string(p.Kind)
	default:
		return fmt.Errorf("store: set media place: %w: unknown kind %q (want reference, illustration or narration)", ErrInvalid, p.Kind)
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE media SET book_id = ?, kind = ?, page_n = ?, cast_name = ? WHERE id = ?`,
		book, kind, page, cast, id)
	if err != nil {
		return fmt.Errorf("store: set media place %s: %w", id, classifyConstraint(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set media place %s: rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: set media place %s: %w", id, ErrNotFound)
	}
	return nil
}

// BookMedia returns everything attached to the book, oldest first —
// the book's media as a whole, whatever each blob's role. Unplaced
// blobs have no book and are never listed. An empty book id is
// invalid input.
func (d *DB) BookMedia(ctx context.Context, bookID string) ([]Media, error) {
	if bookID == "" {
		return nil, fmt.Errorf("store: book media: %w: book id must not be empty", ErrInvalid)
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, book_id, kind, page_n, cast_name, content_type, size_bytes, created_at
		 FROM media WHERE book_id = ? ORDER BY created_at, id`, bookID)
	if err != nil {
		return nil, fmt.Errorf("store: book media in book %s: %w", bookID, err)
	}
	defer rows.Close()
	var media []Media
	for rows.Next() {
		m, err := scanMedia(rows, "book media in book "+bookID)
		if err != nil {
			return nil, err
		}
		media = append(media, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: book media in book %s: %w", bookID, err)
	}
	return media, nil
}

// PageMedia returns the blob bound to page n as kind — the
// illustration or the narration T10's flipbook asks for when it
// renders "page n → image URL + audio URL". Kind must be
// MediaIllustration or MediaNarration (a reference is anchored to a
// cast member — see CastMedia). No blob in that slot, or an unknown
// book or page, is ErrNotFound.
func (d *DB) PageMedia(ctx context.Context, bookID string, n int, kind MediaKind) (Media, error) {
	if bookID == "" {
		return Media{}, fmt.Errorf("store: page media: %w: book id must not be empty", ErrInvalid)
	}
	if n < 1 {
		return Media{}, fmt.Errorf("store: page media: %w: page number %d must be at least 1", ErrInvalid, n)
	}
	if kind != MediaIllustration && kind != MediaNarration {
		return Media{}, fmt.Errorf("store: page media: %w: kind %q is not page-anchored", ErrInvalid, kind)
	}
	return scanMedia(d.db.QueryRowContext(ctx,
		`SELECT id, book_id, kind, page_n, cast_name, content_type, size_bytes, created_at
		 FROM media WHERE book_id = ? AND page_n = ? AND kind = ?`,
		bookID, n, string(kind)),
		fmt.Sprintf("page %d %s in book %s", n, kind, bookID))
}

// CastMedia returns the cast member's reference sheet — the image
// T6 locks every page render of that member against. An empty book
// id or member name is invalid input; a member without a reference
// sheet (or an unknown member) is ErrNotFound.
func (d *DB) CastMedia(ctx context.Context, bookID, name string) (Media, error) {
	if bookID == "" {
		return Media{}, fmt.Errorf("store: cast media: %w: book id must not be empty", ErrInvalid)
	}
	if name == "" {
		return Media{}, fmt.Errorf("store: cast media: %w: cast member name must not be empty", ErrInvalid)
	}
	return scanMedia(d.db.QueryRowContext(ctx,
		`SELECT id, book_id, kind, page_n, cast_name, content_type, size_bytes, created_at
		 FROM media WHERE book_id = ? AND cast_name = ? AND kind = ?`,
		bookID, name, string(MediaReference)),
		fmt.Sprintf("reference for cast member %s in book %s", name, bookID))
}

// DeleteMedia removes the metadata row. The blob file on disk is
// internal/mediastore's concern. Unknown ids return ErrNotFound.
func (d *DB) DeleteMedia(ctx context.Context, id string) error {
	return d.deleteRow(ctx, `DELETE FROM media WHERE id = ?`, "delete media", id)
}

// scanMedia decodes one media row, mapping a missing row to
// ErrNotFound under the given description ("media <id>", "page 2
// illustration in book <id>", ...). Any other scan failure is
// returned wrapped: this package wrote the columns, so one that
// fails to decode is corruption.
func scanMedia(r rowScanner, what string) (Media, error) {
	var (
		m          Media
		book, cast sql.NullString
		kind       string
		page       sql.NullInt64
		created    int64
	)
	if err := r.Scan(&m.ID, &book, &kind, &page, &cast, &m.ContentType, &m.SizeBytes, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Media{}, fmt.Errorf("store: %s: %w", what, ErrNotFound)
		}
		return Media{}, fmt.Errorf("store: %s: %w", what, err)
	}
	m.BookID = book.String
	m.Kind = MediaKind(kind)
	m.PageN = int(page.Int64)
	m.CastName = cast.String
	m.CreatedAt = scanTime(created)
	return m, nil
}
