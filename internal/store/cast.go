package store

import (
	"context"
	"fmt"
)

// CastMember is one entry of a book's cast bible (project.md §1 Phase
// B): how to draw the character (Visual, pasted verbatim into every
// page prompt — the text half of the character lock, project.md §2)
// and how T8 pitches their cloned voice (Pitch, SoundEffects).
type CastMember struct {
	BookID string // owning book
	Name   string // the character's name, unique within the book
	// Visual is the verbatim appearance string ("a small girl, red
	// raincoat, ..."). Never paraphrased downstream.
	Visual string
	// Pitch is the voice-clone pitch offset (0, -8, +6 per the cast
	// table in project.md).
	Pitch float64
	// SoundEffects is the clone's sound_effects value ("" for the
	// narrator, "spacious_echo" for the dragon, ...).
	SoundEffects string
}

// CreateCastMember inserts the member. BookID must be non-empty and
// name an existing book; Name must be non-empty and must not already
// exist in the book's cast (ErrConflict).
func (d *DB) CreateCastMember(ctx context.Context, c CastMember) error {
	if c.BookID == "" {
		return fmt.Errorf("store: create cast member: %w: book id must not be empty", ErrInvalid)
	}
	if c.Name == "" {
		return fmt.Errorf("store: create cast member: %w: name must not be empty", ErrInvalid)
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO "cast" (book_id, name, visual, pitch, sound_effects)
		 VALUES (?, ?, ?, ?, ?)`,
		c.BookID, c.Name, c.Visual, c.Pitch, c.SoundEffects)
	if err != nil {
		return fmt.Errorf("store: create cast member %q in book %s: %w",
			c.Name, c.BookID, classifyConstraint(err))
	}
	return nil
}

// Cast returns the book's cast in name order. An empty book id is
// invalid input.
func (d *DB) Cast(ctx context.Context, bookID string) ([]CastMember, error) {
	if bookID == "" {
		return nil, fmt.Errorf("store: cast: %w: book id must not be empty", ErrInvalid)
	}
	rows, err := d.db.QueryContext(ctx,
		`SELECT book_id, name, visual, pitch, sound_effects
		 FROM "cast" WHERE book_id = ? ORDER BY name`, bookID)
	if err != nil {
		return nil, fmt.Errorf("store: cast of book %s: %w", bookID, err)
	}
	defer rows.Close()
	var members []CastMember
	for rows.Next() {
		var c CastMember
		if err := rows.Scan(&c.BookID, &c.Name, &c.Visual, &c.Pitch, &c.SoundEffects); err != nil {
			return nil, fmt.Errorf("store: cast of book %s: %w", bookID, err)
		}
		members = append(members, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: cast of book %s: %w", bookID, err)
	}
	return members, nil
}

// UpdateCastMember replaces the member's visual, pitch and sound
// effects. The member must exist; its key (book and name) never
// changes.
func (d *DB) UpdateCastMember(ctx context.Context, c CastMember) error {
	if c.BookID == "" {
		return fmt.Errorf("store: update cast member: %w: book id must not be empty", ErrInvalid)
	}
	if c.Name == "" {
		return fmt.Errorf("store: update cast member: %w: name must not be empty", ErrInvalid)
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE "cast" SET visual = ?, pitch = ?, sound_effects = ?
		 WHERE book_id = ? AND name = ?`,
		c.Visual, c.Pitch, c.SoundEffects, c.BookID, c.Name)
	if err != nil {
		return fmt.Errorf("store: update cast member %q in book %s: %w",
			c.Name, c.BookID, classifyConstraint(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update cast member %q in book %s: rows affected: %w",
			c.Name, c.BookID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update cast member %q in book %s: %w",
			c.Name, c.BookID, ErrNotFound)
	}
	return nil
}

// DeleteCastMember removes one member from a book's cast. Unknown
// members return ErrNotFound.
func (d *DB) DeleteCastMember(ctx context.Context, bookID, name string) error {
	if bookID == "" {
		return fmt.Errorf("store: delete cast member: %w: book id must not be empty", ErrInvalid)
	}
	if name == "" {
		return fmt.Errorf("store: delete cast member: %w: name must not be empty", ErrInvalid)
	}
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM "cast" WHERE book_id = ? AND name = ?`, bookID, name)
	if err != nil {
		return fmt.Errorf("store: delete cast member %q in book %s: %w", name, bookID, err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete cast member %q in book %s: rows affected: %w",
			name, bookID, err)
	}
	if deleted == 0 {
		return fmt.Errorf("store: delete cast member %q in book %s: %w",
			name, bookID, ErrNotFound)
	}
	return nil
}
