package store

import (
	"errors"
	"testing"
)

// TestCastLifecycle covers create → list (name order) → update →
// delete for a book's cast bible.
func TestCastLifecycle(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "cast book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	mira := CastMember{BookID: b.ID, Name: "Mira", Visual: "a small girl, red raincoat, round glasses", Pitch: 0, SoundEffects: ""}
	dragon := CastMember{BookID: b.ID, Name: "Dragon", Visual: "a green dragon, tiny wings", Pitch: -8, SoundEffects: "spacious_echo"}
	for _, c := range []CastMember{dragon, mira} {
		if err := db.CreateCastMember(ctx, c); err != nil {
			t.Fatalf("create %s: %v", c.Name, err)
		}
	}

	cast, err := db.Cast(ctx, b.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cast) != 2 || cast[0].Name != "Dragon" || cast[1].Name != "Mira" {
		t.Fatalf("list = %+v, want Dragon before Mira", cast)
	}
	if cast[1].Visual != mira.Visual || cast[1].Pitch != mira.Pitch || cast[1].SoundEffects != mira.SoundEffects {
		t.Fatalf("round-trip = %+v, want %+v", cast[1], mira)
	}

	mira.Pitch = 6
	mira.SoundEffects = "robotic"
	if err := db.UpdateCastMember(ctx, mira); err != nil {
		t.Fatalf("update: %v", err)
	}
	cast, err = db.Cast(ctx, b.ID)
	if err != nil {
		t.Fatalf("list after update: %v", err)
	}
	if cast[1].Pitch != 6 || cast[1].SoundEffects != "robotic" {
		t.Fatalf("after update = %+v, want pitch 6 and robotic effects", cast[1])
	}

	if err := db.DeleteCastMember(ctx, b.ID, "Mira"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Cast(ctx, b.ID); err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	cast, _ = db.Cast(ctx, b.ID)
	if len(cast) != 1 || cast[0].Name != "Dragon" {
		t.Fatalf("after delete = %+v, want only Dragon", cast)
	}
}

// TestCreateCastMemberValidations: bad input never reaches the
// database.
func TestCreateCastMemberValidations(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	cases := []struct {
		name   string
		member CastMember
		want   error
	}{
		{"empty book id", CastMember{Name: "Mira"}, ErrInvalid},
		{"empty name", CastMember{BookID: "00000000000000000000000000000000"}, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := db.CreateCastMember(ctx, c.member); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// TestCreateCastMemberTwiceConflicts: one name, one cast entry per
// book — a duplicate is ErrConflict.
func TestCreateCastMemberTwiceConflicts(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "dup cast")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err = db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira", Visual: "a different girl"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// TestCreateCastMemberInUnknownBookIsInvalid pins the FK
// classification for the cast table.
func TestCreateCastMemberInUnknownBookIsInvalid(t *testing.T) {
	err := openTestDB(t).CreateCastMember(t.Context(), CastMember{BookID: "00000000000000000000000000000000", Name: "Mira"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestCastMutationsOnMissingRows pins ErrNotFound across update and
// delete, and ErrInvalid for empty-key calls.
func TestCastMutationsOnMissingRows(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "missing cast")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.UpdateCastMember(ctx, CastMember{BookID: b.ID, Name: "Ghost"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteCastMember(ctx, b.ID, "Ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteCastMember(ctx, "", "Mira"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("delete empty book err = %v, want ErrInvalid", err)
	}
	if err := db.DeleteCastMember(ctx, b.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("delete empty name err = %v, want ErrInvalid", err)
	}
}

// TestCastRejectsEmptyBookID: listing a cast needs a book.
func TestCastRejectsEmptyBookID(t *testing.T) {
	if _, err := openTestDB(t).Cast(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestUpdateCastMemberValidations: the update path validates its key
// like the create path does.
func TestUpdateCastMemberValidations(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := db.UpdateCastMember(ctx, CastMember{Name: "Mira"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty book err = %v, want ErrInvalid", err)
	}
	if err := db.UpdateCastMember(ctx, CastMember{BookID: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name err = %v, want ErrInvalid", err)
	}
}
