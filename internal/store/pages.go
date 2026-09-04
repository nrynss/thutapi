package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Line is one spoken line on a page (project.md §1 Phase B:
// lines[].character/text).
type Line struct {
	Character string `json:"character"` // cast member who speaks
	Text      string `json:"text"`      // what they say
}

// Page is one spread of a book. All content comes from M3's Phase-B
// JSON (project.md §1).
type Page struct {
	BookID     string   // owning book
	N          int      // 1-based page number within the book
	Text       string   // the page's narration
	Prompt     string   // illustration prompt T6 sends to the image model
	Characters []string // cast members appearing on the page
	Emotion    string   // narration emotion T8 speaks the page with
	Lines      []Line   // spoken character lines, in order
}

// CreatePage inserts the page. BookID must be non-empty and name an
// existing book; N must be at least 1 and must not already exist in
// the book (ErrConflict).
func (d *DB) CreatePage(ctx context.Context, p Page) error {
	if p.BookID == "" {
		return fmt.Errorf("store: create page: %w: book id must not be empty", ErrInvalid)
	}
	if p.N < 1 {
		return fmt.Errorf("store: create page: %w: page number %d must be at least 1", ErrInvalid, p.N)
	}
	characters, lines, err := marshalPage(p)
	if err != nil {
		return fmt.Errorf("store: create page: %w", err)
	}
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO pages (book_id, n, text, prompt, characters, emotion, lines)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.BookID, p.N, p.Text, p.Prompt, characters, p.Emotion, lines)
	if err != nil {
		return fmt.Errorf("store: create page %d in book %s: %w", p.N, p.BookID, classifyConstraint(err))
	}
	return nil
}

// marshalPage encodes the page's JSON columns. marshalling fixed
// shapes ([]string, []Line) cannot fail, so an error here is
// ErrInternal by construction.
func marshalPage(p Page) (characters, lines string, err error) {
	charJSON, err := json.Marshal(p.Characters)
	if err != nil {
		return "", "", fmt.Errorf("encode characters: %w: %w", ErrInternal, err)
	}
	lineJSON, err := json.Marshal(p.Lines)
	if err != nil {
		return "", "", fmt.Errorf("encode lines: %w: %w", ErrInternal, err)
	}
	return string(charJSON), string(lineJSON), nil
}

// Page returns one page of a book, or an error wrapping ErrNotFound.
func (d *DB) Page(ctx context.Context, bookID string, n int) (Page, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT book_id, n, text, prompt, characters, emotion, lines
		 FROM pages WHERE book_id = ? AND n = ?`, bookID, n)
	p, err := scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Page{}, fmt.Errorf("store: page %d in book %s: %w", n, bookID, ErrNotFound)
		}
		return Page{}, err
	}
	return p, nil
}

// Pages returns the book's pages in page order. An empty book id is
// invalid input.
func (d *DB) Pages(ctx context.Context, bookID string) ([]Page, error) {
	if bookID == "" {
		return nil, fmt.Errorf("store: pages: %w: book id must not be empty", ErrInvalid)
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT book_id, n, text, prompt, characters, emotion, lines
		 FROM pages WHERE book_id = ? ORDER BY n`, bookID)
	if err != nil {
		return nil, fmt.Errorf("store: pages in book %s: %w", bookID, err)
	}
	defer rows.Close()
	var pages []Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: pages in book %s: %w", bookID, err)
	}
	return pages, nil
}

// scanPage decodes one pages row. Decoding failures — a column
// holding bytes this package did not write — are ErrInternal.
func scanPage(r rowScanner) (Page, error) {
	var (
		p                Page
		characters, line string
	)
	if err := r.Scan(&p.BookID, &p.N, &p.Text, &p.Prompt, &characters, &p.Emotion, &line); err != nil {
		return Page{}, err
	}
	if err := json.Unmarshal([]byte(characters), &p.Characters); err != nil {
		return Page{}, fmt.Errorf("store: page %d in book %s: decode characters: %w: %w", p.N, p.BookID, ErrInternal, err)
	}
	if err := json.Unmarshal([]byte(line), &p.Lines); err != nil {
		return Page{}, fmt.Errorf("store: page %d in book %s: decode lines: %w: %w", p.N, p.BookID, ErrInternal, err)
	}
	return p, nil
}

// UpdatePage replaces the page's content: text, prompt, characters,
// emotion and lines. The page must exist; its key (book and number)
// never changes.
func (d *DB) UpdatePage(ctx context.Context, p Page) error {
	if p.BookID == "" {
		return fmt.Errorf("store: update page: %w: book id must not be empty", ErrInvalid)
	}
	if p.N < 1 {
		return fmt.Errorf("store: update page: %w: page number %d must be at least 1", ErrInvalid, p.N)
	}
	characters, lines, err := marshalPage(p)
	if err != nil {
		return fmt.Errorf("store: update page: %w", err)
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE pages SET text = ?, prompt = ?, characters = ?, emotion = ?, lines = ?
		 WHERE book_id = ? AND n = ?`,
		p.Text, p.Prompt, characters, p.Emotion, lines, p.BookID, p.N)
	if err != nil {
		return fmt.Errorf("store: update page %d in book %s: %w", p.N, p.BookID, classifyConstraint(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update page %d in book %s: rows affected: %w", p.N, p.BookID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update page %d in book %s: %w", p.N, p.BookID, ErrNotFound)
	}
	return nil
}

// DeletePage removes one page of a book. Unknown pages return
// ErrNotFound.
func (d *DB) DeletePage(ctx context.Context, bookID string, n int) error {
	if bookID == "" {
		return fmt.Errorf("store: delete page: %w: book id must not be empty", ErrInvalid)
	}
	if n < 1 {
		return fmt.Errorf("store: delete page: %w: page number %d must be at least 1", ErrInvalid, n)
	}
	res, err := d.db.ExecContext(ctx, `DELETE FROM pages WHERE book_id = ? AND n = ?`, bookID, n)
	if err != nil {
		return fmt.Errorf("store: delete page %d in book %s: %w", n, bookID, err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete page %d in book %s: rows affected: %w", n, bookID, err)
	}
	if deleted == 0 {
		return fmt.Errorf("store: delete page %d in book %s: %w", n, bookID, ErrNotFound)
	}
	return nil
}
