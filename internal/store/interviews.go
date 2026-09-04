package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Turn is one exchange of the Phase-A interview: what the interviewer
// asked or what the child answered. Role vocabulary belongs to T4;
// the store treats it as an opaque non-empty string.
type Turn struct {
	Role string `json:"role"` // who spoke (e.g. "interviewer", "child")
	Text string `json:"text"` // what was said
}

// Interview is one child's session: the ordered transcript so a
// refresh or a restart resumes mid-conversation (PLAN.md §T3 — a book
// survives a container restart), and BookID once Phase B has authored
// the book from it. ID and CreatedAt are store-assigned.
type Interview struct {
	ID string
	// BookID is empty until the interview is linked to its book.
	BookID string
	Turns  []Turn
	// CreatedAt is store-assigned and never changes.
	CreatedAt time.Time
}

// CreateInterview starts an empty interview and returns it with its
// assigned ID and CreatedAt. Turns and the book link arrive later via
// UpdateInterview.
func (d *DB) CreateInterview(ctx context.Context) (Interview, error) {
	id, err := d.newID()
	if err != nil {
		return Interview{}, fmt.Errorf("store: create interview: %w", err)
	}
	iv := Interview{ID: id, Turns: []Turn{}, CreatedAt: time.Now().UTC().Truncate(time.Second)}
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO interviews (id, book_id, turns, created_at) VALUES (?, NULL, ?, ?)`,
		iv.ID, "[]", iv.CreatedAt.Unix())
	if err != nil {
		return Interview{}, fmt.Errorf("store: create interview: %w", classifyConstraint(err))
	}
	return iv, nil
}

// Interview returns the interview with the given id, or an error
// wrapping ErrNotFound.
func (d *DB) Interview(ctx context.Context, id string) (Interview, error) {
	var (
		iv      Interview
		book    sql.NullString
		turns   string
		created int64
	)
	err := d.db.QueryRowContext(ctx,
		`SELECT id, book_id, turns, created_at FROM interviews WHERE id = ?`, id,
	).Scan(&iv.ID, &book, &turns, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Interview{}, fmt.Errorf("store: interview %s: %w", id, ErrNotFound)
		}
		return Interview{}, fmt.Errorf("store: interview %s: %w", id, err)
	}
	iv.BookID = book.String
	if err := json.Unmarshal([]byte(turns), &iv.Turns); err != nil {
		return Interview{}, fmt.Errorf("store: interview %s: decode turns: %w: %w", id, ErrInternal, err)
	}
	iv.CreatedAt = scanTime(created)
	return iv, nil
}

// UpdateInterview stores the interview's transcript and its book
// link. The interview must exist; ID and CreatedAt never change. An
// empty BookID unlinks; a non-empty one must name an existing book
// (ErrInvalid otherwise).
func (d *DB) UpdateInterview(ctx context.Context, iv Interview) error {
	if iv.ID == "" {
		return fmt.Errorf("store: update interview: %w: id must not be empty", ErrInvalid)
	}
	for i, t := range iv.Turns {
		if t.Role == "" || t.Text == "" {
			return fmt.Errorf("store: update interview: %w: turn %d needs a role and text", ErrInvalid, i)
		}
	}
	turns, err := json.Marshal(iv.Turns)
	if err != nil {
		return fmt.Errorf("store: update interview: encode turns: %w: %w", ErrInternal, err)
	}
	var book any // NULL when empty — FK-less unlinked interview
	if iv.BookID != "" {
		book = iv.BookID
	}
	res, err := d.db.ExecContext(ctx,
		`UPDATE interviews SET book_id = ?, turns = ? WHERE id = ?`,
		book, string(turns), iv.ID)
	if err != nil {
		return fmt.Errorf("store: update interview %s: %w", iv.ID, classifyConstraint(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update interview %s: rows affected: %w", iv.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update interview %s: %w", iv.ID, ErrNotFound)
	}
	return nil
}

// DeleteInterview removes the interview. Unknown ids return
// ErrNotFound.
func (d *DB) DeleteInterview(ctx context.Context, id string) error {
	return d.deleteRow(ctx, `DELETE FROM interviews WHERE id = ?`, "delete interview", id)
}
