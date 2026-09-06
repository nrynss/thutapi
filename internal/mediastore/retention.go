package mediastore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"thutapi/internal/store"
)

// The retention sweep — PLAN.md §T11 item 4.
//
// Generated media accumulates on a box with 23 GiB free and no swap, and
// two of its three classes are unreachable by any delete path the product
// has:
//
//   - **Unplaced rows.** media.book_id is nullable and the table's CHECK
//     explicitly permits book_id IS NULL (internal/store/store.go). With
//     no book there is no parent to cascade from, so DeleteBook never
//     reaches one and BookMedia never lists one. Since T8's wire contract
//     2, every spoken interview question persists as exactly such a row —
//     6–10 per interview, plus retries, forever. Sweep A deletes them by
//     age.
//   - **Unreferenced files.** Delete removes the metadata row first, so a
//     crash between the two leaves a file with no row; so does a crashed
//     PersistWithID between blob and row. Nothing lists these either.
//     Sweep B removes them by file age.
//   - **Placed blobs** are anchored to a book and DeleteBook cascades
//     them, so the only question they pose is *when a book goes*. Sweep C
//     answers it with a byte budget: over MaxBytes, whole books are
//     evicted oldest-first.
//
// # How this coexists with T13's voice-sample expiry
//
// internal/audio/clone.go runs its own durable expiry sidecar — a
// JSON map of id → expiry, a one-second sweeper and a startup purge —
// that gives a voice sample a 15-minute lifetime. That mechanism is
// load-bearing for a consent promise shown to a parent, and this sweep
// neither replaces nor duplicates it. Two things keep them apart:
//
//  1. **Age.** A voice sample lives 15 minutes; UnplacedAge is hours. A
//     sample is always deleted by its own owner long before this sweep
//     would look at it.
//  2. **An explicit veto.** RetentionConfig.Retain is asked about every
//     candidate id before anything is deleted, and cmd/thutapi wires it to
//     the voice-sample handler's own tracking set. Even with a
//     misconfigured age, this sweep cannot delete a row or a file that
//     T13 still owns — including the id T13 reserved before its blob
//     existed, which is exactly the crash window its DeleteIfPresent
//     covers.
//
// # What this sweep deliberately does not reach
//
// A metadata row whose file has already vanished. The pass is driven by
// the blob directory, so a row with no file is never visited. That costs
// a hundred bytes of SQLite and no disk, and driving from the directory
// is what lets one pass classify all three cases above without a new
// query on T3's closed store package.

// DefaultMaxBytes is the default byte budget for the blob directory.
// The box has 23 GiB free and no swap (PLAN.md §T11 item 4) and one book
// is roughly 7 MB of images plus narration plus a ~3.5 MB film and a
// ~3.5 MB PDF, so 6 GiB is several hundred books and still leaves the
// box most of its disk.
const DefaultMaxBytes int64 = 6 << 30

// DefaultUnplacedAge is how long an unplaced row may live before the
// sweep takes it. A question clip is worthless the moment its turn is
// answered, and a generation run is six minutes, so two hours is
// generous by more than an order of magnitude while still leaving an
// in-flight persist far outside the window.
const DefaultUnplacedAge = 2 * time.Hour

// DefaultOrphanFileAge is how long a file with no metadata row may live.
// It is longer than DefaultUnplacedAge on purpose: a blob is written
// before its row, so "no row" is briefly the normal state of a healthy
// persist, and the age is what tells a crash leftover apart from a
// write in progress.
const DefaultOrphanFileAge = 6 * time.Hour

// DefaultMinBookAge is the youngest a book may be and still be evicted
// for the byte budget. It keeps a book whose generation is still running
// — or whose reader is still on the page — out of the eviction candidate
// set entirely.
const DefaultMinBookAge = 24 * time.Hour

// DefaultMinBooks is how many of the newest books the byte budget never
// evicts, whatever the arithmetic says. Deleting every book to satisfy a
// budget would leave the shelf empty, which is a worse outcome than
// being over it.
const DefaultMinBooks = 3

// DefaultSweepInterval is how often the background sweeper runs.
const DefaultSweepInterval = 10 * time.Minute

// ErrInvalidRetention is returned by NewSweeper for a configuration that
// cannot be honoured: a negative age, a negative budget, or a negative
// book floor.
var ErrInvalidRetention = errors.New("mediastore: invalid retention config")

// RetentionConfig configures NewSweeper. The zero value is usable and
// means every default above.
type RetentionConfig struct {
	// UnplacedAge is how old an unplaced media row must be before it is
	// deleted. Zero means DefaultUnplacedAge.
	UnplacedAge time.Duration
	// OrphanFileAge is how old a file with no metadata row must be
	// before it is removed. Zero means DefaultOrphanFileAge.
	OrphanFileAge time.Duration
	// MaxBytes is the byte budget for the blob directory. Zero means
	// DefaultMaxBytes; a negative value is ErrInvalidRetention. Set it
	// to Unbounded to sweep orphans only and never evict a book.
	MaxBytes int64
	// MinBookAge is the youngest a book may be and still be evicted for
	// the byte budget. Zero means DefaultMinBookAge.
	MinBookAge time.Duration
	// MinBooks is how many of the newest books are never evicted. Zero
	// means DefaultMinBooks.
	MinBooks int
	// Protected lists book ids the byte budget must never evict — the
	// prewarmed books a judge lands on (PLAN.md §T11 item 2).
	Protected []string
	// Retain reports whether a media id belongs to another lifetime
	// owner and must be left alone. Nil retains nothing. cmd/thutapi
	// wires this to T13's voice-sample tracking set; see the package
	// comment above for why.
	Retain func(id string) bool
	// Interval is how often Start's loop sweeps. Zero means
	// DefaultSweepInterval.
	Interval time.Duration
	// Now is the clock, injected for tests. Nil means time.Now.
	Now func() time.Time
}

// Unbounded is the MaxBytes value that disables book eviction, leaving
// the orphan sweeps running. It is spelled as a named constant rather
// than as the zero value because zero has to mean "I did not configure
// this" — a struct field that defaults to "no disk cap" is the bug
// PLAN.md §T11 item 4 is about.
const Unbounded int64 = -1

// SweepResult is what one pass did. It is returned for logging and so a
// test can assert on the specific class of thing that went, rather than
// on a total that several causes could produce.
type SweepResult struct {
	// UnplacedDeleted and UnplacedBytes count sweep A: aged-out rows
	// with no book, row and blob.
	UnplacedDeleted int
	UnplacedBytes   int64
	// OrphanFilesDeleted and OrphanFileBytes count sweep B: files with
	// no metadata row at all.
	OrphanFilesDeleted int
	OrphanFileBytes    int64
	// BooksEvicted and BookBytes count sweep C: whole books dropped to
	// get back under the byte budget.
	BooksEvicted int
	BookBytes    int64
	// Retained counts candidates another owner vetoed — T13's live
	// voice samples. A non-zero value here is the two mechanisms
	// staying out of each other's way, not a failure.
	Retained int
	// BytesBefore and BytesAfter are the blob directory's measured size
	// at the start and end of the pass.
	BytesBefore int64
	BytesAfter  int64
}

// Sweeper enforces the retention policy over one Store. Create it with
// NewSweeper; the zero value is not usable. Sweep is safe to call
// concurrently with the store's own traffic.
type Sweeper struct {
	store         *Store
	unplacedAge   time.Duration
	orphanFileAge time.Duration
	maxBytes      int64
	minBookAge    time.Duration
	minBooks      int
	protected     map[string]bool
	retain        func(id string) bool
	interval      time.Duration
	now           func() time.Time

	startOnce sync.Once
	closeOnce sync.Once
	started   atomic.Bool
	stop      chan struct{}
	done      chan struct{}
}

// NewSweeper returns the retention sweeper for s.
func (s *Store) NewSweeper(cfg RetentionConfig) (*Sweeper, error) {
	if cfg.UnplacedAge < 0 || cfg.OrphanFileAge < 0 || cfg.MinBookAge < 0 || cfg.Interval < 0 {
		return nil, fmt.Errorf("mediastore: new sweeper: %w: durations must not be negative", ErrInvalidRetention)
	}
	if cfg.MinBooks < 0 {
		return nil, fmt.Errorf("mediastore: new sweeper: %w: MinBooks must not be negative", ErrInvalidRetention)
	}
	if cfg.MaxBytes < 0 && cfg.MaxBytes != Unbounded {
		return nil, fmt.Errorf("mediastore: new sweeper: %w: MaxBytes must not be negative", ErrInvalidRetention)
	}
	w := &Sweeper{
		store:         s,
		unplacedAge:   orDuration(cfg.UnplacedAge, DefaultUnplacedAge),
		orphanFileAge: orDuration(cfg.OrphanFileAge, DefaultOrphanFileAge),
		maxBytes:      cfg.MaxBytes,
		minBookAge:    orDuration(cfg.MinBookAge, DefaultMinBookAge),
		minBooks:      cfg.MinBooks,
		protected:     make(map[string]bool, len(cfg.Protected)),
		retain:        cfg.Retain,
		interval:      orDuration(cfg.Interval, DefaultSweepInterval),
		now:           cfg.Now,
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	if cfg.MaxBytes == 0 {
		w.maxBytes = DefaultMaxBytes
	}
	if cfg.MinBooks == 0 {
		w.minBooks = DefaultMinBooks
	}
	if w.now == nil {
		w.now = time.Now
	}
	if w.retain == nil {
		w.retain = func(string) bool { return false }
	}
	for _, id := range cfg.Protected {
		w.protected[id] = true
	}
	return w, nil
}

// orDuration is the "zero means the default" resolution the config doc
// promises for every duration field.
func orDuration(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

// Start runs Sweep on a ticker until Close. It sweeps once immediately,
// so a process that has been down long enough to accumulate orphans
// does not wait a full interval to clean them. Calling Start twice is a
// no-op after the first.
func (w *Sweeper) Start() {
	w.startOnce.Do(func() {
		w.started.Store(true)
		go w.loop()
	})
}

// loop is Start's goroutine. Its only exit is Close.
func (w *Sweeper) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		// A failed sweep is retried on the next tick: nothing about a
		// retention pass is worth stopping the server for, and the
		// blobs it did not reach are still there to reach.
		if _, err := w.Sweep(context.Background()); err != nil {
			w.store.log.Error("mediastore: retention sweep failed", "err", err.Error())
		}
		select {
		case <-ticker.C:
		case <-w.stop:
			return
		}
	}
}

// Close stops Start's loop and waits for it to exit. It is safe on a
// Sweeper that was never started — there is no goroutine to wait for —
// and safe to call more than once.
func (w *Sweeper) Close() {
	w.closeOnce.Do(func() {
		close(w.stop)
	})
	if w.started.Load() {
		<-w.done
	}
}

// blobEntry is one file in the blob directory, classified against its
// metadata row.
type blobEntry struct {
	id      string
	size    int64
	modTime time.Time
	bookID  string
	created time.Time
	// orphan is true when the file has no metadata row at all.
	orphan bool
}

// Sweep runs one retention pass: aged-out unplaced rows, then
// unreferenced files, then — if the directory is still over budget —
// whole books, oldest first.
//
// It returns what it did. A failure to remove one blob's file is logged
// and the pass continues: a sweep that stops at the first stubborn file
// leaves the rest of the disk uncollected, which is the failure it
// exists to prevent. A failure to read the directory or the database is
// returned, because nothing after it can be trusted.
func (w *Sweeper) Sweep(ctx context.Context) (SweepResult, error) {
	now := w.now()
	blobs, err := w.scan(ctx)
	if err != nil {
		return SweepResult{}, err
	}

	var result SweepResult
	live := make([]blobEntry, 0, len(blobs))
	for _, b := range blobs {
		result.BytesBefore += b.size
		if w.retain(b.id) {
			// T13 (or any other lifetime owner) still holds this id.
			result.Retained++
			live = append(live, b)
			continue
		}
		switch {
		case b.orphan:
			if now.Sub(b.modTime) < w.orphanFileAge {
				live = append(live, b)
				continue
			}
			if err := w.store.root.Remove(b.id); err != nil && !errors.Is(err, fs.ErrNotExist) {
				w.store.log.Error("mediastore: unreferenced blob not removed", "id", b.id, "err", err.Error())
				live = append(live, b)
				continue
			}
			result.OrphanFilesDeleted++
			result.OrphanFileBytes += b.size
		case b.bookID == "":
			if now.Sub(b.created) < w.unplacedAge {
				live = append(live, b)
				continue
			}
			if err := w.store.Delete(ctx, b.id); err != nil && !errors.Is(err, ErrNotFound) {
				w.store.log.Error("mediastore: unplaced blob not deleted", "id", b.id, "err", err.Error())
				live = append(live, b)
				continue
			}
			result.UnplacedDeleted++
			result.UnplacedBytes += b.size
		default:
			live = append(live, b)
		}
	}

	evicted, bytes, err := w.enforceBudget(ctx, now, live)
	if err != nil {
		return result, err
	}
	result.BooksEvicted = evicted
	result.BookBytes = bytes
	result.BytesAfter = result.BytesBefore - result.UnplacedBytes - result.OrphanFileBytes - result.BookBytes
	return result, nil
}

// scan lists the blob directory and classifies every file against its
// metadata row. Files whose names are not media ids are ignored
// entirely: this package named every blob it wrote, so anything else
// belongs to somebody the sweep has no business deleting.
func (w *Sweeper) scan(ctx context.Context) ([]blobEntry, error) {
	entries, err := os.ReadDir(w.store.dir)
	if err != nil {
		return nil, fmt.Errorf("mediastore: sweep: read blob dir: %w", err)
	}
	blobs := make([]blobEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // removed under us; the next pass will not see it
			}
			return nil, fmt.Errorf("mediastore: sweep: stat %s: %w", entry.Name(), err)
		}
		b := blobEntry{id: entry.Name(), size: info.Size(), modTime: info.ModTime()}
		m, err := w.store.media(ctx, b.id)
		switch {
		case errors.Is(err, ErrNotFound):
			b.orphan = true
		case err != nil:
			return nil, fmt.Errorf("mediastore: sweep: %w", err)
		default:
			b.bookID, b.created = m.BookID, m.CreatedAt
		}
		blobs = append(blobs, b)
	}
	return blobs, nil
}

// enforceBudget evicts whole books, oldest first, until the surviving
// blobs fit the byte budget. Books are the unit because a book missing
// half its pages is worse than a book that is gone: DeleteBook cascades
// its pages, cast and media rows, and this removes their files.
//
// Three things are never evicted, whatever the arithmetic says: a book
// in Protected (the prewarmed landing books), a book younger than
// MinBookAge (its generation may still be running), and the newest
// MinBooks books (an empty shelf is worse than an over-budget one).
func (w *Sweeper) enforceBudget(ctx context.Context, now time.Time, live []blobEntry) (int, int64, error) {
	if w.maxBytes < 0 {
		return 0, 0, nil
	}
	var total int64
	byBook := make(map[string]int64)
	for _, b := range live {
		total += b.size
		if b.bookID != "" {
			byBook[b.bookID] += b.size
		}
	}
	if total <= w.maxBytes {
		return 0, 0, nil
	}

	books, err := w.store.db.Books(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("mediastore: sweep: list books: %w", err)
	}
	// Books arrive oldest first; keep the newest MinBooks by trimming
	// the tail of the candidate list.
	sort.SliceStable(books, func(i, j int) bool { return books[i].CreatedAt.Before(books[j].CreatedAt) })
	candidates := books
	if len(candidates) > w.minBooks {
		candidates = candidates[:len(candidates)-w.minBooks]
	} else {
		candidates = nil
	}

	evicted, freed := 0, int64(0)
	for _, book := range candidates {
		if total <= w.maxBytes {
			break
		}
		if w.protected[book.ID] || now.Sub(book.CreatedAt) < w.minBookAge {
			continue
		}
		removed, err := w.evictBook(ctx, book.ID)
		if err != nil {
			return evicted, freed, err
		}
		// byBook is what this pass actually measured on disk; the rows'
		// own sizes are the fallback when a blob was written between the
		// directory scan and the eviction.
		size := byBook[book.ID]
		if removed > size {
			size = removed
		}
		total -= size
		freed += size
		evicted++
	}
	return evicted, freed, nil
}

// evictBook deletes one book and every file its media rows named. The
// rows are read before DeleteBook, because the cascade takes them and
// nothing could name the files afterwards.
func (w *Sweeper) evictBook(ctx context.Context, bookID string) (int64, error) {
	rows, err := w.store.db.BookMedia(ctx, bookID)
	if err != nil {
		return 0, fmt.Errorf("mediastore: sweep: book media %s: %w", bookID, err)
	}
	if err := w.store.db.DeleteBook(ctx, bookID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return 0, nil // already gone; nothing to charge to the budget
		}
		return 0, fmt.Errorf("mediastore: sweep: delete book %s: %w", bookID, err)
	}
	var freed int64
	for _, row := range rows {
		if w.retain(row.ID) {
			// Should not happen — a retained id is a voice sample and a
			// voice sample is never placed in a book — but the veto is
			// absolute, so the file stays and its owner removes it.
			continue
		}
		if err := w.store.root.Remove(row.ID); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				w.store.log.Error("mediastore: evicted blob not removed", "id", row.ID, "book", bookID, "err", err.Error())
			}
			continue
		}
		freed += row.SizeBytes
	}
	w.store.log.Info("mediastore: evicted book for retention budget", "book", bookID, "blobs", len(rows), "bytes", freed)
	return freed, nil
}
