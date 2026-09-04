package store

import (
	"errors"
	"testing"
)

// TestMediaRowLifecycle covers create (both with an assigned and a
// caller-supplied id) → read → place → read back → detach → delete.
// The caller-supplied path is how internal/mediastore names blob
// files and rows identically.
func TestMediaRowLifecycle(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	m, err := db.CreateMedia(ctx, Media{ContentType: "audio/mpeg", SizeBytes: 4242})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(m.ID) != idBytes*2 || m.CreatedAt.IsZero() || m.ContentType != "audio/mpeg" || m.SizeBytes != 4242 {
		t.Fatalf("created media not fully assigned: %+v", m)
	}
	if m.Kind != "" || m.BookID != "" || m.PageN != 0 || m.CastName != "" {
		t.Fatalf("new blob is not unplaced: %+v", m)
	}
	got, err := db.Media(ctx, m.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != m {
		t.Fatalf("read = %+v, want %+v", got, m)
	}

	// Caller-supplied id must be kept, not replaced.
	named, err := db.CreateMedia(ctx, Media{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "image/png", SizeBytes: 1})
	if err != nil {
		t.Fatalf("create named: %v", err)
	}
	if named.ID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("caller-supplied id replaced: %s", named.ID)
	}

	b, err := db.CreateBook(ctx, "attach target")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1, Text: "once"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.SetMediaPlace(ctx, m.ID, MediaPlace{BookID: b.ID, Kind: MediaNarration, PageN: 1}); err != nil {
		t.Fatalf("place: %v", err)
	}
	got, err = db.Media(ctx, m.ID)
	if err != nil {
		t.Fatalf("read placed: %v", err)
	}
	if got.BookID != b.ID || got.Kind != MediaNarration || got.PageN != 1 || got.CastName != "" {
		t.Fatalf("placed media = %+v, want book %s, narration, page 1", got, b.ID)
	}
	if err := db.SetMediaPlace(ctx, m.ID, MediaPlace{}); err != nil {
		t.Fatalf("detach: %v", err)
	}
	got, _ = db.Media(ctx, m.ID)
	if got.BookID != "" || got.Kind != "" || got.PageN != 0 || got.CastName != "" {
		t.Fatalf("detached media = %+v, want fully unplaced", got)
	}

	if err := db.DeleteMedia(ctx, m.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.Media(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err after delete = %v, want ErrNotFound", err)
	}
}

// TestCreateMediaValidations: a metadata row without a content type
// or with a negative size is invalid input, and so is a new row
// arriving pre-placed — the role and anchor are SetMediaPlace's job.
func TestCreateMediaValidations(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	cases := []struct {
		name  string
		media Media
		want  error
	}{
		{"empty content type", Media{SizeBytes: 1}, ErrInvalid},
		{"negative size", Media{ContentType: "image/png", SizeBytes: -1}, ErrInvalid},
		{"place on create", Media{ContentType: "image/png", SizeBytes: 1, Kind: MediaReference, CastName: "Mira"}, ErrInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := db.CreateMedia(ctx, c.media); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// TestCreateMediaInUnknownBookIsInvalid pins the FK classification
// for the media table.
func TestCreateMediaInUnknownBookIsInvalid(t *testing.T) {
	_, err := openTestDB(t).CreateMedia(t.Context(),
		Media{BookID: "00000000000000000000000000000000", ContentType: "image/png", SizeBytes: 1})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestSetMediaPlaceBranches pins the branch set of the place machine:
// ErrNotFound for a missing row, ErrInvalid for an unusable place —
// an unknown book, page or cast member (the foreign keys fire on
// update, not just insert), a kind outside the constrained set, a
// kind without its anchor, an anchor without its kind, and a detach
// carrying fields.
func TestSetMediaPlaceBranches(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	const unknown = "00000000000000000000000000000000"
	if err := db.SetMediaPlace(ctx, unknown, MediaPlace{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown media err = %v, want ErrNotFound", err)
	}
	if err := db.SetMediaPlace(ctx, "", MediaPlace{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty media id err = %v, want ErrInvalid", err)
	}

	b, err := db.CreateBook(ctx, "branch book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1, Text: "once"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira"}); err != nil {
		t.Fatalf("create cast: %v", err)
	}
	cases := []struct {
		name  string
		place MediaPlace
	}{
		{"unknown book", MediaPlace{BookID: unknown}},
		{"unknown kind", MediaPlace{BookID: b.ID, Kind: "trailer", PageN: 1}},
		{"reference without cast member", MediaPlace{BookID: b.ID, Kind: MediaReference}},
		{"reference with page", MediaPlace{BookID: b.ID, Kind: MediaReference, CastName: "Mira", PageN: 1}},
		{"unknown cast member", MediaPlace{BookID: b.ID, Kind: MediaReference, CastName: "Nobody"}},
		{"illustration without page", MediaPlace{BookID: b.ID, Kind: MediaIllustration}},
		{"illustration with cast member", MediaPlace{BookID: b.ID, Kind: MediaIllustration, PageN: 1, CastName: "Mira"}},
		{"page below the 1-based floor", MediaPlace{BookID: b.ID, Kind: MediaNarration, PageN: 0}},
		{"unknown page", MediaPlace{BookID: b.ID, Kind: MediaNarration, PageN: 7}},
		{"detach carrying fields", MediaPlace{Kind: MediaReference, CastName: "Mira"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 1})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if err := db.SetMediaPlace(ctx, m.ID, c.place); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestSetMediaPlaceConflict pins the one-per-slot rule as a database
// rule: a second blob in a taken slot is ErrConflict, while the other
// kind on the same page and a reference slot stay independent — and
// re-placing a blob moves it rather than conflicting with itself.
func TestSetMediaPlaceConflict(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	b, err := db.CreateBook(ctx, "conflict book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1, Text: "once"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira"}); err != nil {
		t.Fatalf("create cast: %v", err)
	}
	first, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 1})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := db.SetMediaPlace(ctx, first.ID, MediaPlace{BookID: b.ID, Kind: MediaIllustration, PageN: 1}); err != nil {
		t.Fatalf("place first: %v", err)
	}
	second, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 2})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := db.SetMediaPlace(ctx, second.ID, MediaPlace{BookID: b.ID, Kind: MediaIllustration, PageN: 1}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second illustration on page 1: err = %v, want ErrConflict", err)
	}
	// The narration slot on the same page is its own slot.
	if err := db.SetMediaPlace(ctx, second.ID, MediaPlace{BookID: b.ID, Kind: MediaNarration, PageN: 1}); err != nil {
		t.Fatalf("narration on the same page: %v", err)
	}
	// A reference slot never collides with page slots, and placing an
	// already-placed blob moves it.
	if err := db.SetMediaPlace(ctx, second.ID, MediaPlace{BookID: b.ID, Kind: MediaReference, CastName: "Mira"}); err != nil {
		t.Fatalf("re-place as reference: %v", err)
	}
	// The narration slot was vacated by the move.
	if _, err := db.PageMedia(ctx, b.ID, 1, MediaNarration); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old narration slot survived the move: %v", err)
	}
}

// TestBookPageAndCastMediaQueries pins the three read shapes the
// downstream tracks need: the book's media listing (sweeps, counts),
// the page-n-asset-by-kind lookup (T6's page illustration, T8's page
// narration, T10's "page n → image + audio") and the cast reference
// lookup (T6's image lock) — each with its not-found and invalid
// branches. Unplaced blobs have no book and are never listed.
func TestBookPageAndCastMediaQueries(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	const unknown = "00000000000000000000000000000000"

	b, err := db.CreateBook(ctx, "query book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(ctx, Page{BookID: b.ID, N: 1, Text: "once"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	if err := db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira"}); err != nil {
		t.Fatalf("create cast: %v", err)
	}
	place := func(contentType string, p MediaPlace) Media {
		t.Helper()
		m, err := db.CreateMedia(ctx, Media{ContentType: contentType, SizeBytes: 3})
		if err != nil {
			t.Fatalf("create %s blob: %v", contentType, err)
		}
		if err := db.SetMediaPlace(ctx, m.ID, p); err != nil {
			t.Fatalf("place %s blob: %v", contentType, err)
		}
		placed, err := db.Media(ctx, m.ID)
		if err != nil {
			t.Fatalf("read placed %s blob: %v", contentType, err)
		}
		return placed
	}
	ref := place("image/png", MediaPlace{BookID: b.ID, Kind: MediaReference, CastName: "Mira"})
	ill := place("image/png", MediaPlace{BookID: b.ID, Kind: MediaIllustration, PageN: 1})
	narr := place("audio/mpeg", MediaPlace{BookID: b.ID, Kind: MediaNarration, PageN: 1})
	unplaced, err := db.CreateMedia(ctx, Media{ContentType: "audio/wav", SizeBytes: 1})
	if err != nil {
		t.Fatalf("create unplaced: %v", err)
	}

	listed, err := db.BookMedia(ctx, b.ID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("book media listed %d rows, want the 3 placed ones", len(listed))
	}
	byID := map[string]Media{}
	for _, m := range listed {
		byID[m.ID] = m
	}
	for _, want := range []Media{ref, ill, narr} {
		if got := byID[want.ID]; got != want {
			t.Fatalf("listed = %+v, want %+v", got, want)
		}
	}
	if _, isListed := byID[unplaced.ID]; isListed {
		t.Fatalf("unplaced blob %s is listed", unplaced.ID)
	}

	gotIll, err := db.PageMedia(ctx, b.ID, 1, MediaIllustration)
	if err != nil {
		t.Fatalf("page illustration: %v", err)
	}
	if gotIll != ill {
		t.Fatalf("page illustration = %+v, want %+v", gotIll, ill)
	}
	gotNarr, err := db.PageMedia(ctx, b.ID, 1, MediaNarration)
	if err != nil {
		t.Fatalf("page narration: %v", err)
	}
	if gotNarr != narr {
		t.Fatalf("page narration = %+v, want %+v", gotNarr, narr)
	}
	gotRef, err := db.CastMedia(ctx, b.ID, "Mira")
	if err != nil {
		t.Fatalf("cast reference: %v", err)
	}
	if gotRef != ref {
		t.Fatalf("cast reference = %+v, want %+v", gotRef, ref)
	}

	notFoundCases := []struct {
		name string
		err  error
	}{
		{"page without that kind", func() error { _, err := db.PageMedia(ctx, b.ID, 2, MediaNarration); return err }()},
		{"unknown book", func() error { _, err := db.PageMedia(ctx, unknown, 1, MediaIllustration); return err }()},
		{"cast member without a reference", func() error { _, err := db.CastMedia(ctx, b.ID, "Nobody"); return err }()},
		{"media after delete", func() error {
			m, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 1})
			if err != nil {
				return err
			}
			if err := db.DeleteMedia(ctx, m.ID); err != nil {
				return err
			}
			_, err = db.Media(ctx, m.ID)
			return err
		}()},
	}
	for _, c := range notFoundCases {
		t.Run("not found: "+c.name, func(t *testing.T) {
			if !errors.Is(c.err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", c.err)
			}
		})
	}

	invalidCases := []struct {
		name string
		err  error
	}{
		{"empty book listing", func() error { _, err := db.BookMedia(ctx, ""); return err }()},
		{"page below the floor", func() error { _, err := db.PageMedia(ctx, b.ID, 0, MediaNarration); return err }()},
		{"reference is not page-anchored", func() error { _, err := db.PageMedia(ctx, b.ID, 1, MediaReference); return err }()},
		{"cast media without book", func() error { _, err := db.CastMedia(ctx, "", "Mira"); return err }()},
		{"cast media without name", func() error { _, err := db.CastMedia(ctx, b.ID, ""); return err }()},
	}
	for _, c := range invalidCases {
		t.Run("invalid: "+c.name, func(t *testing.T) {
			if !errors.Is(c.err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", c.err)
			}
		})
	}
}

// TestAnchorCascadesRemovesRows pins the composite foreign keys'
// cascade: deleting a page takes its illustration and narration rows,
// deleting a cast member takes the reference row, and neighbouring
// slots survive. The blob FILES stay unreferenced on disk —
// mediastore's and PLAN.md §T11's sweep, not the store's.
func TestAnchorCascadesRemovesRows(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)

	b, err := db.CreateBook(ctx, "cascade book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	for _, n := range []int{1, 2} {
		if err := db.CreatePage(ctx, Page{BookID: b.ID, N: n, Text: "once"}); err != nil {
			t.Fatalf("create page %d: %v", n, err)
		}
	}
	if err := db.CreateCastMember(ctx, CastMember{BookID: b.ID, Name: "Mira"}); err != nil {
		t.Fatalf("create cast: %v", err)
	}
	ill, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 1})
	if err != nil {
		t.Fatalf("create illustration: %v", err)
	}
	if err := db.SetMediaPlace(ctx, ill.ID, MediaPlace{BookID: b.ID, Kind: MediaIllustration, PageN: 1}); err != nil {
		t.Fatalf("place illustration: %v", err)
	}
	narr, err := db.CreateMedia(ctx, Media{ContentType: "audio/mpeg", SizeBytes: 2})
	if err != nil {
		t.Fatalf("create narration: %v", err)
	}
	if err := db.SetMediaPlace(ctx, narr.ID, MediaPlace{BookID: b.ID, Kind: MediaNarration, PageN: 2}); err != nil {
		t.Fatalf("place narration: %v", err)
	}
	ref, err := db.CreateMedia(ctx, Media{ContentType: "image/png", SizeBytes: 3})
	if err != nil {
		t.Fatalf("create reference: %v", err)
	}
	if err := db.SetMediaPlace(ctx, ref.ID, MediaPlace{BookID: b.ID, Kind: MediaReference, CastName: "Mira"}); err != nil {
		t.Fatalf("place reference: %v", err)
	}

	if err := db.DeletePage(ctx, b.ID, 1); err != nil {
		t.Fatalf("delete page 1: %v", err)
	}
	if _, err := db.Media(ctx, ill.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("illustration survived page cascade: %v", err)
	}
	if err := db.DeleteCastMember(ctx, b.ID, "Mira"); err != nil {
		t.Fatalf("delete cast member: %v", err)
	}
	if _, err := db.Media(ctx, ref.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reference survived cast cascade: %v", err)
	}
	// A slot anchored elsewhere is nobody's cascade casualty.
	if _, err := db.PageMedia(ctx, b.ID, 2, MediaNarration); err != nil {
		t.Fatalf("page 2 narration lost to its neighbours' cascades: %v", err)
	}
	if _, err := db.Media(ctx, narr.ID); err != nil {
		t.Fatalf("narration row lost: %v", err)
	}
}

// TestCreateMediaDuplicateIDConflicts pins the UNIQUE half of the
// constraint classification: two metadata rows with the same
// caller-supplied id are ErrConflict — mediastore would otherwise be
// able to serve one id as two different blobs.
func TestCreateMediaDuplicateIDConflicts(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	m := Media{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ContentType: "image/png", SizeBytes: 1}
	if _, err := db.CreateMedia(ctx, m); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := db.CreateMedia(ctx, m); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}
