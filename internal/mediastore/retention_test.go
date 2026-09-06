package mediastore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"thutapi/internal/store"
)

// sweepClock is how these tests age things: the sweeper's Now is pushed
// forward rather than the rows being backdated, because store stamps
// created_at itself and a test that reaches around it would stop
// testing the real row.
func sweepClock(offset time.Duration) func() time.Time {
	return func() time.Time { return time.Now().Add(offset) }
}

// newSweeper builds a sweeper over s, defaulting the clock to three
// hours from now — past DefaultUnplacedAge, short of DefaultMinBookAge.
func newSweeper(t *testing.T, s *Store, cfg RetentionConfig) *Sweeper {
	t.Helper()
	if cfg.Now == nil {
		cfg.Now = sweepClock(3 * time.Hour)
	}
	w, err := s.NewSweeper(cfg)
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}
	return w
}

// persistUnplaced writes one blob with no book — the shape every spoken
// interview question takes (PLAN.md §T11 item 4).
func persistUnplaced(t *testing.T, s *Store, size int) string {
	t.Helper()
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(size)), "audio/mpeg")
	if err != nil {
		t.Fatalf("persist unplaced: %v", err)
	}
	return id
}

// persistBook creates a book with one page and one placed illustration
// of the given size, and returns the book id and the media id.
func persistBook(t *testing.T, s *Store, title string, size int) (string, string) {
	t.Helper()
	book, err := s.db.CreateBook(t.Context(), title)
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := s.db.CreatePage(t.Context(), store.Page{BookID: book.ID, N: 1, Text: "once"}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(size)), "image/png")
	if err != nil {
		t.Fatalf("persist illustration: %v", err)
	}
	place := store.MediaPlace{BookID: book.ID, Kind: store.MediaIllustration, PageN: 1}
	if err := s.db.SetMediaPlace(t.Context(), id, place); err != nil {
		t.Fatalf("place illustration: %v", err)
	}
	return book.ID, id
}

func fileExists(t *testing.T, s *Store, id string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(s.dir, id))
	if err == nil {
		return true
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	t.Fatalf("stat blob %s: %v", id, err)
	return false
}

func rowExists(t *testing.T, s *Store, id string) bool {
	t.Helper()
	_, err := s.media(t.Context(), id)
	if err == nil {
		return true
	}
	if errors.Is(err, ErrNotFound) {
		return false
	}
	t.Fatalf("look up media %s: %v", id, err)
	return false
}

// TestSweepDeletesAgedUnplacedAndKeepsPlaced is PLAN.md §T11 item 4's
// core claim: the class nothing else can reach goes, and the class a
// book owns stays.
func TestSweepDeletesAgedUnplacedAndKeepsPlaced(t *testing.T) {
	s := openTestStore(t)
	question := persistUnplaced(t, s, 2048)
	_, illustration := persistBook(t, s, "Bo and Pip", 4096)

	result, err := newSweeper(t, s, RetentionConfig{}).Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.UnplacedDeleted != 1 {
		t.Fatalf("UnplacedDeleted = %d, want 1", result.UnplacedDeleted)
	}
	if result.UnplacedBytes != 2048 {
		t.Fatalf("UnplacedBytes = %d, want 2048", result.UnplacedBytes)
	}
	if result.BooksEvicted != 0 || result.OrphanFilesDeleted != 0 {
		t.Fatalf("sweep removed more than the unplaced row: %+v", result)
	}
	if rowExists(t, s, question) || fileExists(t, s, question) {
		t.Fatal("the aged unplaced blob survived: row or file still present")
	}
	if !rowExists(t, s, illustration) || !fileExists(t, s, illustration) {
		t.Fatal("a placed illustration was swept")
	}
	if result.BytesBefore != 2048+4096 {
		t.Fatalf("BytesBefore = %d, want %d", result.BytesBefore, 2048+4096)
	}
	if result.BytesAfter != 4096 {
		t.Fatalf("BytesAfter = %d, want 4096", result.BytesAfter)
	}
}

// TestSweepKeepsYoungUnplacedRows: an illustration is persisted before
// it is placed, so a sweep that took fresh unplaced rows would delete
// pages out of a running generation.
func TestSweepKeepsYoungUnplacedRows(t *testing.T) {
	s := openTestStore(t)
	fresh := persistUnplaced(t, s, 1024)
	result, err := newSweeper(t, s, RetentionConfig{Now: time.Now}).Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.UnplacedDeleted != 0 {
		t.Fatalf("UnplacedDeleted = %d, want 0", result.UnplacedDeleted)
	}
	if !rowExists(t, s, fresh) || !fileExists(t, s, fresh) {
		t.Fatal("a freshly persisted unplaced blob was swept")
	}
}

// TestSweepUnplacedAgeBoundary walks the threshold itself rather than a
// point comfortably either side of it.
func TestSweepUnplacedAgeBoundary(t *testing.T) {
	cases := []struct {
		name   string
		offset time.Duration
		gone   bool
	}{
		{"well inside the window", time.Minute, false},
		{"just inside the window", DefaultUnplacedAge - time.Minute, false},
		{"past the window", DefaultUnplacedAge + time.Minute, true},
		{"long past the window", 72 * time.Hour, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			id := persistUnplaced(t, s, 512)
			if _, err := newSweeper(t, s, RetentionConfig{Now: sweepClock(tc.offset)}).Sweep(t.Context()); err != nil {
				t.Fatalf("sweep: %v", err)
			}
			if got := !rowExists(t, s, id); got != tc.gone {
				t.Fatalf("deleted = %v, want %v", got, tc.gone)
			}
		})
	}
}

// TestSweepRetainVetoIsAbsolute is the T13 boundary: a media id another
// lifetime owner still holds is never touched, whatever its age.
func TestSweepRetainVetoIsAbsolute(t *testing.T) {
	s := openTestStore(t)
	sample := persistUnplaced(t, s, 4096)
	question := persistUnplaced(t, s, 1024)

	w := newSweeper(t, s, RetentionConfig{
		Now:    sweepClock(72 * time.Hour),
		Retain: func(id string) bool { return id == sample },
	})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Retained != 1 {
		t.Fatalf("Retained = %d, want 1", result.Retained)
	}
	if !rowExists(t, s, sample) || !fileExists(t, s, sample) {
		t.Fatal("the retained voice sample was swept out from under its owner")
	}
	if rowExists(t, s, question) {
		t.Fatal("the un-retained question clip survived")
	}
}

// TestSweepRetainVetoCoversAReservedIDWithNoRow is T13's crash window:
// PersistWithID reserves an id and records its expiry before the row
// exists, so a blob with no row can still be owned.
func TestSweepRetainVetoCoversAReservedIDWithNoRow(t *testing.T) {
	s := openTestStore(t)
	reserved := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(s.dir, reserved), blob(256), 0o600); err != nil {
		t.Fatalf("write reserved blob: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(s.dir, reserved), old, old); err != nil {
		t.Fatalf("age reserved blob: %v", err)
	}
	w := newSweeper(t, s, RetentionConfig{Retain: func(id string) bool { return id == reserved }})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.OrphanFilesDeleted != 0 {
		t.Fatalf("OrphanFilesDeleted = %d, want 0", result.OrphanFilesDeleted)
	}
	if !fileExists(t, s, reserved) {
		t.Fatal("a reserved but not yet inserted blob was removed")
	}
}

// TestSweepRemovesAgedUnreferencedFiles covers Delete's row-first
// ordering: a crash between the row delete and the file remove leaves a
// file nothing lists.
func TestSweepRemovesAgedUnreferencedFiles(t *testing.T) {
	s := openTestStore(t)
	aged := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	young := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, id := range []string{aged, young} {
		if err := os.WriteFile(filepath.Join(s.dir, id), blob(1500), 0o600); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}
	old := time.Now().Add(-(DefaultOrphanFileAge + time.Hour))
	if err := os.Chtimes(filepath.Join(s.dir, aged), old, old); err != nil {
		t.Fatalf("age blob: %v", err)
	}

	result, err := newSweeper(t, s, RetentionConfig{Now: time.Now}).Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.OrphanFilesDeleted != 1 || result.OrphanFileBytes != 1500 {
		t.Fatalf("orphan file sweep = %d files / %d bytes, want 1 / 1500", result.OrphanFilesDeleted, result.OrphanFileBytes)
	}
	if fileExists(t, s, aged) {
		t.Fatal("the aged unreferenced file survived")
	}
	if !fileExists(t, s, young) {
		t.Fatal("a young unreferenced file was removed; a healthy persist writes the blob before the row")
	}
}

// TestSweepIgnoresFilesItDidNotName: the blob directory is this
// package's, but a sweep that deleted anything it found there would be
// one operator mistake away from deleting the database.
func TestSweepIgnoresFilesItDidNotName(t *testing.T) {
	s := openTestStore(t)
	for _, name := range []string{"thutapi.db", "notes.txt", "SHORT", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if err := os.WriteFile(filepath.Join(s.dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		old := time.Now().Add(-72 * time.Hour)
		if err := os.Chtimes(filepath.Join(s.dir, name), old, old); err != nil {
			t.Fatalf("age %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(s.dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	result, err := newSweeper(t, s, RetentionConfig{}).Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.OrphanFilesDeleted != 0 {
		t.Fatalf("OrphanFilesDeleted = %d, want 0", result.OrphanFilesDeleted)
	}
	for _, name := range []string{"thutapi.db", "notes.txt", "SHORT", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := os.Stat(filepath.Join(s.dir, name)); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
	}
}

// TestSweepEvictsOldestBooksOverBudget is the disk cap. The books are
// aged past MinBookAge by the clock, and only enough of them go to get
// back under the budget.
func TestSweepEvictsOldestBooksOverBudget(t *testing.T) {
	s := openTestStore(t)
	var books, media []string
	for _, title := range []string{"oldest", "middle", "newest"} {
		bookID, mediaID := persistBook(t, s, title, 4096)
		books = append(books, bookID)
		media = append(media, mediaID)
		// store stamps created_at to the second, so space the books out
		// or "oldest first" is a coin toss.
		time.Sleep(1100 * time.Millisecond)
	}

	// 12 KiB on disk, budget 9 KiB: exactly one book has to go.
	w := newSweeper(t, s, RetentionConfig{
		Now:      sweepClock(48 * time.Hour),
		MaxBytes: 9000,
		MinBooks: 1,
	})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.BooksEvicted != 1 {
		t.Fatalf("BooksEvicted = %d, want 1", result.BooksEvicted)
	}
	if result.BookBytes != 4096 {
		t.Fatalf("BookBytes = %d, want 4096", result.BookBytes)
	}
	if _, err := s.db.Book(t.Context(), books[0]); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the oldest book survived: %v", err)
	}
	if fileExists(t, s, media[0]) {
		t.Fatal("the evicted book's blob file was left on disk")
	}
	for i := 1; i < len(books); i++ {
		if _, err := s.db.Book(t.Context(), books[i]); err != nil {
			t.Fatalf("book %d was evicted unnecessarily: %v", i, err)
		}
		if !fileExists(t, s, media[i]) {
			t.Fatalf("book %d lost its blob", i)
		}
	}
}

// TestSweepBudgetProtectsPrewarmedBooks: the landing books a judge sees
// are never the ones the arithmetic takes (PLAN.md §T11 item 2).
func TestSweepBudgetProtectsPrewarmedBooks(t *testing.T) {
	s := openTestStore(t)
	prewarmed, prewarmedMedia := persistBook(t, s, "prewarmed", 4096)
	time.Sleep(1100 * time.Millisecond)
	ordinary, ordinaryMedia := persistBook(t, s, "ordinary", 4096)
	time.Sleep(1100 * time.Millisecond)
	persistBook(t, s, "newest", 4096)

	// 12 KiB on disk against a 9 KiB budget: one book must go, and the
	// oldest one — the prewarmed book — is the one it must not be.
	w := newSweeper(t, s, RetentionConfig{
		Now:       sweepClock(48 * time.Hour),
		MaxBytes:  9000,
		MinBooks:  1,
		Protected: []string{prewarmed},
	})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.BooksEvicted != 1 {
		t.Fatalf("BooksEvicted = %d, want 1", result.BooksEvicted)
	}
	if _, err := s.db.Book(t.Context(), prewarmed); err != nil {
		t.Fatalf("the protected book was evicted: %v", err)
	}
	if !fileExists(t, s, prewarmedMedia) {
		t.Fatal("the protected book's blob was removed")
	}
	if _, err := s.db.Book(t.Context(), ordinary); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the unprotected book survived: %v", err)
	}
	if fileExists(t, s, ordinaryMedia) {
		t.Fatal("the evicted book's blob survived")
	}
}

// TestSweepBudgetKeepsTheNewestBooks and does not empty the shelf to
// satisfy an impossible budget.
func TestSweepBudgetKeepsTheNewestBooks(t *testing.T) {
	s := openTestStore(t)
	var books []string
	for _, title := range []string{"one", "two", "three"} {
		bookID, _ := persistBook(t, s, title, 4096)
		books = append(books, bookID)
		time.Sleep(1100 * time.Millisecond)
	}
	w := newSweeper(t, s, RetentionConfig{
		Now:      sweepClock(48 * time.Hour),
		MaxBytes: 1, // unsatisfiable on purpose
	})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.BooksEvicted != 0 {
		t.Fatalf("BooksEvicted = %d, want 0 — MinBooks defaults to %d", result.BooksEvicted, DefaultMinBooks)
	}
	for i, id := range books {
		if _, err := s.db.Book(t.Context(), id); err != nil {
			t.Fatalf("book %d went to satisfy an unsatisfiable budget: %v", i, err)
		}
	}
}

// TestSweepBudgetSparesYoungBooks: a book younger than MinBookAge may
// still have a generation running against it, so it is not a candidate
// even when the budget is blown and MinBooks would allow it.
func TestSweepBudgetSparesYoungBooks(t *testing.T) {
	s := openTestStore(t)
	var books []string
	for _, title := range []string{"one", "two", "three"} {
		bookID, _ := persistBook(t, s, title, 8192)
		books = append(books, bookID)
		time.Sleep(1100 * time.Millisecond)
	}
	w := newSweeper(t, s, RetentionConfig{
		Now:      sweepClock(3 * time.Hour), // inside DefaultMinBookAge
		MaxBytes: 1,
		MinBooks: 1,
	})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.BooksEvicted != 0 {
		t.Fatalf("BooksEvicted = %d, want 0", result.BooksEvicted)
	}
	for i, id := range books {
		if _, err := s.db.Book(t.Context(), id); err != nil {
			t.Fatalf("book %d, younger than MinBookAge, was evicted: %v", i, err)
		}
	}
}

// TestSweepUnboundedNeverEvictsABook exercises the Unbounded value
// through its own path: orphans still go, books never do.
func TestSweepUnboundedNeverEvictsABook(t *testing.T) {
	s := openTestStore(t)
	bookID, _ := persistBook(t, s, "kept", 8192)
	question := persistUnplaced(t, s, 1024)
	w := newSweeper(t, s, RetentionConfig{
		Now:      sweepClock(72 * time.Hour),
		MaxBytes: Unbounded,
		MinBooks: 1,
	})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.BooksEvicted != 0 {
		t.Fatalf("BooksEvicted = %d, want 0 under Unbounded", result.BooksEvicted)
	}
	if result.UnplacedDeleted != 1 {
		t.Fatalf("UnplacedDeleted = %d, want 1 — Unbounded disables the budget, not the orphan sweep", result.UnplacedDeleted)
	}
	if _, err := s.db.Book(t.Context(), bookID); err != nil {
		t.Fatalf("a book was evicted under Unbounded: %v", err)
	}
	if rowExists(t, s, question) {
		t.Fatal("the orphan sweep did not run under Unbounded")
	}
}

// TestSweepUnderBudgetEvictsNothing pins the early return.
func TestSweepUnderBudgetEvictsNothing(t *testing.T) {
	s := openTestStore(t)
	bookID, _ := persistBook(t, s, "small", 512)
	w := newSweeper(t, s, RetentionConfig{Now: sweepClock(48 * time.Hour), MinBooks: 1})
	result, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.BooksEvicted != 0 {
		t.Fatalf("BooksEvicted = %d, want 0", result.BooksEvicted)
	}
	if _, err := s.db.Book(t.Context(), bookID); err != nil {
		t.Fatalf("book evicted while under budget: %v", err)
	}
}

// TestNewSweeperDefaults exercises every zero-value field through its
// documented default (AGENTS.md §Testing rule 2).
func TestNewSweeperDefaults(t *testing.T) {
	s := openTestStore(t)
	w, err := s.NewSweeper(RetentionConfig{})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}
	if w.unplacedAge != DefaultUnplacedAge {
		t.Fatalf("UnplacedAge default = %v, want %v", w.unplacedAge, DefaultUnplacedAge)
	}
	if w.orphanFileAge != DefaultOrphanFileAge {
		t.Fatalf("OrphanFileAge default = %v, want %v", w.orphanFileAge, DefaultOrphanFileAge)
	}
	if w.maxBytes != DefaultMaxBytes {
		t.Fatalf("MaxBytes default = %d, want %d", w.maxBytes, DefaultMaxBytes)
	}
	if w.minBookAge != DefaultMinBookAge {
		t.Fatalf("MinBookAge default = %v, want %v", w.minBookAge, DefaultMinBookAge)
	}
	if w.minBooks != DefaultMinBooks {
		t.Fatalf("MinBooks default = %d, want %d", w.minBooks, DefaultMinBooks)
	}
	if w.interval != DefaultSweepInterval {
		t.Fatalf("Interval default = %v, want %v", w.interval, DefaultSweepInterval)
	}
	if w.now == nil || w.now().IsZero() {
		t.Fatal("Now default did not resolve to a working clock")
	}
	if w.retain == nil {
		t.Fatal("Retain default is nil; it must be a func that retains nothing")
	}
	if w.retain("0123456789abcdef0123456789abcdef") {
		t.Fatal("the default Retain retained something")
	}
}

func TestNewSweeperRejectsInvalidConfig(t *testing.T) {
	s := openTestStore(t)
	cases := []struct {
		name string
		cfg  RetentionConfig
	}{
		{"negative unplaced age", RetentionConfig{UnplacedAge: -time.Second}},
		{"negative orphan file age", RetentionConfig{OrphanFileAge: -time.Second}},
		{"negative min book age", RetentionConfig{MinBookAge: -time.Second}},
		{"negative interval", RetentionConfig{Interval: -time.Second}},
		{"negative min books", RetentionConfig{MinBooks: -1}},
		{"negative max bytes", RetentionConfig{MaxBytes: -2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := s.NewSweeper(tc.cfg)
			if !errors.Is(err, ErrInvalidRetention) {
				t.Fatalf("NewSweeper error = %v, want ErrInvalidRetention", err)
			}
			if w != nil {
				t.Fatal("NewSweeper returned a sweeper alongside an error")
			}
		})
	}
}

// TestSweeperStartSweepsImmediatelyAndClosesCleanly pins the goroutine's
// exit path (AGENTS.md §Go style, Concurrency).
func TestSweeperStartSweepsImmediatelyAndClosesCleanly(t *testing.T) {
	s := openTestStore(t)
	question := persistUnplaced(t, s, 1024)
	w := newSweeper(t, s, RetentionConfig{Now: sweepClock(72 * time.Hour), Interval: time.Hour})
	w.Start()
	w.Start() // idempotent
	deadline := time.Now().Add(5 * time.Second)
	for rowExists(t, s, question) {
		if time.Now().After(deadline) {
			t.Fatal("Start did not sweep within 5s; it must sweep once immediately")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.Close()
	w.Close() // idempotent
}

func TestSweeperCloseWithoutStartDoesNotBlock(t *testing.T) {
	s := openTestStore(t)
	w := newSweeper(t, s, RetentionConfig{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Close()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on a sweeper that was never started")
	}
}

// TestSweepOnAMissingDirectoryIsAnError: a sweep that cannot read the
// directory has classified nothing, so it must say so rather than
// report a clean pass.
func TestSweepOnAMissingDirectoryIsAnError(t *testing.T) {
	s := openTestStore(t)
	if err := os.RemoveAll(s.dir); err != nil {
		t.Fatalf("remove blob dir: %v", err)
	}
	if _, err := newSweeper(t, s, RetentionConfig{}).Sweep(t.Context()); err == nil {
		t.Fatal("Sweep on a missing blob directory returned no error")
	}
}

// TestSweepIsIdempotent: a second pass over a swept directory finds
// nothing left to do, which is what a ten-minute ticker will mostly be
// doing.
func TestSweepIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	persistUnplaced(t, s, 1024)
	persistBook(t, s, "kept", 2048)
	w := newSweeper(t, s, RetentionConfig{Now: sweepClock(72 * time.Hour)})
	if _, err := w.Sweep(t.Context()); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	second, err := w.Sweep(t.Context())
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.UnplacedDeleted != 0 || second.OrphanFilesDeleted != 0 || second.BooksEvicted != 0 {
		t.Fatalf("second sweep was not a no-op: %+v", second)
	}
}
