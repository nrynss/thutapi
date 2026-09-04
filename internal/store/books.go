package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Book is one picture book. ID is unguessable (see NewID) so a book
// link is shareable without being enumerable (PLAN.md §T3).
// CreatedAt is store-assigned and never changes.
type Book struct {
	ID        string    // unguessable (NewID), store-assigned
	Title     string    // the book's name as M3 authored it
	CreatedAt time.Time // store-assigned, UTC, second precision
}

// CreateBook inserts a book with the given title and returns it with
// its assigned ID and CreatedAt. The title must not be empty.
func (d *DB) CreateBook(ctx context.Context, title string) (Book, error) {
	if title == "" {
		return Book{}, fmt.Errorf("store: create book: %w: title must not be empty", ErrInvalid)
	}
	id, err := d.newID()
	if err != nil {
		return Book{}, fmt.Errorf("store: create book: %w", err)
	}
	b := Book{ID: id, Title: title, CreatedAt: time.Now().UTC().Truncate(time.Second)}
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO books (id, title, created_at) VALUES (?, ?, ?)`,
		b.ID, b.Title, b.CreatedAt.Unix())
	if err != nil {
		return Book{}, fmt.Errorf("store: create book: %w", classifyConstraint(err))
	}
	return b, nil
}

// Book returns the book with the given id, or an error wrapping
// ErrNotFound.
func (d *DB) Book(ctx context.Context, id string) (Book, error) {
	var created int64
	b := Book{}
	err := d.db.QueryRowContext(ctx,
		`SELECT id, title, created_at FROM books WHERE id = ?`, id,
	).Scan(&b.ID, &b.Title, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Book{}, fmt.Errorf("store: book %s: %w", id, ErrNotFound)
		}
		return Book{}, fmt.Errorf("store: book %s: %w", id, err)
	}
	b.CreatedAt = scanTime(created)
	return b, nil
}

// Books returns every book, oldest first.
func (d *DB) Books(ctx context.Context) ([]Book, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, title, created_at FROM books ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: books: %w", err)
	}
	defer rows.Close()
	var books []Book
	for rows.Next() {
		var created int64
		var b Book
		if err := rows.Scan(&b.ID, &b.Title, &created); err != nil {
			return nil, fmt.Errorf("store: books: %w", err)
		}
		b.CreatedAt = scanTime(created)
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: books: %w", err)
	}
	return books, nil
}

// UpdateBook replaces the book's title. The book must exist and the
// new title must not be empty; CreatedAt and ID never change.
func (d *DB) UpdateBook(ctx context.Context, b Book) error {
	if b.ID == "" {
		return fmt.Errorf("store: update book: %w: id must not be empty", ErrInvalid)
	}
	if b.Title == "" {
		return fmt.Errorf("store: update book: %w: title must not be empty", ErrInvalid)
	}
	res, err := d.db.ExecContext(ctx, `UPDATE books SET title = ? WHERE id = ?`, b.Title, b.ID)
	if err != nil {
		return fmt.Errorf("store: update book %s: %w", b.ID, classifyConstraint(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update book %s: rows affected: %w", b.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update book %s: %w", b.ID, ErrNotFound)
	}
	return nil
}

// DeleteBook deletes the book and, via foreign-key cascade, its pages,
// cast, interviews and media rows. Unknown ids return ErrNotFound.
func (d *DB) DeleteBook(ctx context.Context, id string) error {
	return d.deleteRow(ctx, `DELETE FROM books WHERE id = ?`, "delete book", id)
}

// deleteRow runs one parameterised DELETE and maps a zero-affected-row
// result to ErrNotFound under the given operation name.
func (d *DB) deleteRow(ctx context.Context, stmt, op, key string) error {
	res, err := d.db.ExecContext(ctx, stmt, key)
	if err != nil {
		return fmt.Errorf("store: %s %s: %w", op, key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s %s: rows affected: %w", op, key, err)
	}
	if n == 0 {
		return fmt.Errorf("store: %s %s: %w", op, key, ErrNotFound)
	}
	return nil
}
