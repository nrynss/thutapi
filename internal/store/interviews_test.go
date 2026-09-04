package store

import (
	"errors"
	"testing"
)

// TestInterviewLifecycle covers create → transcript round-trip →
// book link → delete. The transcript surviving is what lets T4 resume
// a conversation after a restart.
func TestInterviewLifecycle(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if iv.ID == "" || iv.CreatedAt.IsZero() {
		t.Fatalf("created interview not fully assigned: %+v", iv)
	}

	// Fresh interview: unlinked, empty transcript.
	got, err := db.Interview(ctx, iv.ID)
	if err != nil {
		t.Fatalf("read fresh: %v", err)
	}
	if got.BookID != "" || len(got.Turns) != 0 || !got.CreatedAt.Equal(iv.CreatedAt) {
		t.Fatalf("fresh interview = %+v, want unlinked and empty", got)
	}

	iv.Turns = []Turn{
		{Role: "interviewer", Text: "What is your hero called?"},
		{Role: "child", Text: "mira! she has a red coat"},
		{Role: "interviewer", Text: "What colour is the dragon?"},
		{Role: "child", Text: "green like the park"},
	}
	b, err := db.CreateBook(ctx, "Mira and the Rain Dragon")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	iv.BookID = b.ID
	if err := db.UpdateInterview(ctx, iv); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err = db.Interview(ctx, iv.ID)
	if err != nil {
		t.Fatalf("read after update: %v", err)
	}
	if got.BookID != b.ID || !got.CreatedAt.Equal(iv.CreatedAt) || len(got.Turns) != 4 ||
		got.Turns[1].Role != "child" || got.Turns[1].Text != "mira! she has a red coat" {
		t.Fatalf("after update = %+v, want transcript and book link round-tripped", got)
	}

	if err := db.DeleteInterview(ctx, iv.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Interview(ctx, iv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err after delete = %v, want ErrNotFound", err)
	}
}

// TestUpdateInterviewRejectsEmptyTurns: a turn without a role or text
// is invalid input — the transcript must stay decodable on both ends.
func TestUpdateInterviewRejectsEmptyTurns(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	err = db.UpdateInterview(ctx, Interview{ID: iv.ID, Turns: []Turn{{Role: "", Text: "hello"}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty role: err = %v, want ErrInvalid", err)
	}
	err = db.UpdateInterview(ctx, Interview{ID: iv.ID, Turns: []Turn{{Role: "child", Text: ""}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty text: err = %v, want ErrInvalid", err)
	}
}

// TestUpdateInterviewToUnknownBookIsInvalid pins the FK
// classification: linking to a book that does not exist is bad input.
func TestUpdateInterviewToUnknownBookIsInvalid(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	err = db.UpdateInterview(ctx, Interview{ID: iv.ID, BookID: "00000000000000000000000000000000"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestInterviewNotFoundBranches pins ErrNotFound for read, update and
// delete of a missing interview, and ErrInvalid for the empty id.
func TestInterviewNotFoundBranches(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if _, err := db.Interview(ctx, "00000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateInterview(ctx, Interview{ID: "00000000000000000000000000000000"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteInterview(ctx, "00000000000000000000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete err = %v, want ErrNotFound", err)
	}
	if err := db.UpdateInterview(ctx, Interview{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id err = %v, want ErrInvalid", err)
	}
}

// TestInterviewWithCorruptTurnsIsInternal pins the ErrInternal wrap on
// an undecodable transcript.
func TestInterviewWithCorruptTurnsIsInternal(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE interviews SET turns = '[broken' WHERE id = ?`, iv.ID); err != nil {
		t.Fatalf("corrupt column: %v", err)
	}
	if _, err := db.Interview(ctx, iv.ID); !errors.Is(err, ErrInternal) {
		t.Fatalf("err = %v, want ErrInternal", err)
	}
}
