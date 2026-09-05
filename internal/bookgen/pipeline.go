package bookgen

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"thutapi/internal/audio"
	"thutapi/internal/bookvideo"
	"thutapi/internal/illustrate"
	"thutapi/internal/store"
	"thutapi/internal/story"
	"thutapi/internal/stream"
)

// runBook executes one book's generation pipeline, in the PLAN.md
// §T10c stage order: structure → rows → illustrate (judge + persist)
// → narrate → film → ready. It is the body of the generate job and
// returns the persisted film's media id on success — the value
// book_ready's video_url is built from — and an error on ANY failure.
// A failed run is total: there is no partial book to serve and nothing
// here retries; the caller (generateJob) publishes failed {} and the
// job runner lands the terminal error state.
func (h *Handler) runBook(ctx context.Context, bookID, ivID string) (videoID string, err error) {
	// The run outlives the POST that started it, so everything is read
	// fresh: the interview row carries the transcript to structure and
	// the book row carries the byline question zero wrote.
	iv, err := h.cfg.DB.Interview(ctx, ivID)
	if err != nil {
		return "", fmt.Errorf("bookgen: run %s: %w", bookID, err)
	}
	book, err := h.cfg.DB.Book(ctx, bookID)
	if err != nil {
		return "", fmt.Errorf("bookgen: run %s: %w", bookID, err)
	}

	// Stage 1 — structure, then the store rows the persist stages'
	// place calls anchor on.
	st, err := h.structure(ctx, book, iv)
	if err != nil {
		return "", err
	}

	// Stage 2 — illustrate with T7's judge-and-persist loop. Pages are
	// approved (and persisted) inside Illustrate; each approval reaches
	// the broker as page_approved through the progress bridge.
	bridge := newApprovalBridge(ctx, h, bookID)
	writer := illustrate.NewBookWriter(h.cfg.DB, h.cfg.Blobs, bookID)
	if _, err := illustrate.Illustrate(ctx, illustrate.Config{
		Imager:   h.cfg.Imager,
		Judge:    h.cfg.Judge,
		Persist:  writer,
		Progress: bridge.progress,
	}, st); err != nil {
		return "", fmt.Errorf("bookgen: illustrate: %w", err)
	}
	if err := bridge.err(); err != nil {
		// A page was approved and persisted but its row could not be
		// read back for the event: an internal inconsistency, loud —
		// the race must never silently lose a page.
		return "", err
	}

	// Stage 3 — narrate: one persisted clip per page, in page order.
	clips, err := audio.NarrateBook(ctx, audio.Config{
		TTS:   h.cfg.TTS,
		DB:    h.cfg.DB,
		Blobs: h.cfg.Blobs,
	}, bookID, st.Pages)
	if err != nil {
		return "", fmt.Errorf("bookgen: narrate: %w", err)
	}

	// Stage 4 — the film: persisted pages + narration in, one MP4
	// out, persisted, attached to the book, and ready to serve.
	videoID, err = h.renderFilm(ctx, bookID, st, clips)
	if err != nil {
		return "", err
	}
	return videoID, nil
}

// structure is stage 1: it structures the interview transcript, then
// makes the store ready for the persist stages — the book row gets the
// authored title (byline preserved), and every page and cast member is
// upserted so BookWriter's and NarrateBook's place calls find their
// anchor rows (t6b-live-record.md item 3). Upsert, not create: a re-run
// over the same book replaces the previous attempt's rows.
func (h *Handler) structure(ctx context.Context, book store.Book, iv store.Interview) (story.Story, error) {
	turns := make([]story.Turn, len(iv.Turns))
	for i, t := range iv.Turns {
		turns[i] = story.Turn{Role: t.Role, Text: t.Text}
	}
	st, err := story.Structure(ctx, h.cfg.Chat, turns)
	if err != nil {
		return story.Story{}, fmt.Errorf("bookgen: structure: %w", err)
	}

	// The authored title lands on the book row (created at interview
	// start with WorkingTitle) and the byline — question zero's
	// answer, on the row before generation — is preserved through the
	// update.
	book.Title = st.Title
	if err := h.cfg.DB.UpdateBook(ctx, book); err != nil {
		return story.Story{}, fmt.Errorf("bookgen: update book: %w", err)
	}

	for _, m := range st.Cast {
		member := store.CastMember{
			BookID:       book.ID,
			Name:         m.Name,
			Visual:       m.Visual,
			Pitch:        float64(m.Voice.Pitch),
			SoundEffects: m.Voice.SoundEffects,
		}
		if err := upsertCastMember(ctx, h.cfg.DB, member); err != nil {
			return story.Story{}, err
		}
	}
	for _, p := range st.Pages {
		if err := upsertPage(ctx, h.cfg.DB, book.ID, p); err != nil {
			return story.Story{}, err
		}
	}
	return st, nil
}

// upsertCastMember inserts the member or, when a previous run already
// created the row, replaces its content. The cast key (book, name)
// never changes.
func upsertCastMember(ctx context.Context, db *store.DB, m store.CastMember) error {
	cast, err := db.Cast(ctx, m.BookID)
	if err != nil {
		return fmt.Errorf("bookgen: read cast: %w", err)
	}
	for _, existing := range cast {
		if existing.Name == m.Name {
			if err := db.UpdateCastMember(ctx, m); err != nil {
				return fmt.Errorf("bookgen: update cast member %q: %w", m.Name, err)
			}
			return nil
		}
	}
	if err := db.CreateCastMember(ctx, m); err != nil {
		return fmt.Errorf("bookgen: create cast member %q: %w", m.Name, err)
	}
	return nil
}

// upsertPage inserts the page or, when a previous run already created
// the row, replaces its content. The page key (book, n) never changes.
func upsertPage(ctx context.Context, db *store.DB, bookID string, p story.Page) error {
	row := store.Page{
		BookID:     bookID,
		N:          p.N,
		Text:       p.Text,
		Prompt:     p.Prompt,
		Characters: p.Characters,
		Emotion:    p.Emotion,
	}
	for _, l := range p.Lines {
		row.Lines = append(row.Lines, store.Line{Character: l.Character, Text: l.Text})
	}
	if _, err := db.Page(ctx, bookID, p.N); err == nil {
		if err := db.UpdatePage(ctx, row); err != nil {
			return fmt.Errorf("bookgen: update page %d: %w", p.N, err)
		}
		return nil
	}
	if err := db.CreatePage(ctx, row); err != nil {
		return fmt.Errorf("bookgen: create page %d: %w", p.N, err)
	}
	return nil
}

// approvalBridge publishes page_approved events for pages the closing
// loop approves. illustrate.Config.Progress reports each page AFTER
// its verdict and persist (the loop is the page's last step before its
// Progress event — t7-round2.md L1), so the just-placed row is
// readable here. The callbacks are serialised by illustrate and the
// recorded error is read only after Illustrate has returned, so no
// lock is needed between writer and reader.
type approvalBridge struct {
	ctx    context.Context
	h      *Handler
	bookID string
	first  error
}

func newApprovalBridge(ctx context.Context, h *Handler, bookID string) *approvalBridge {
	return &approvalBridge{ctx: ctx, h: h, bookID: bookID}
}

// progress is the illustrate.Config.Progress callback: reference
// sheets carry no page number and publish nothing; an approved page
// publishes page_approved {"n":N,"image_url":"/media/<id>"} with the
// id of the row the closing loop just placed.
func (b *approvalBridge) progress(p illustrate.Progress) {
	if b.first != nil {
		return // the run is already failed; stop publishing
	}
	if p.Stage != illustrate.StageIllustration {
		return
	}
	m, err := b.h.cfg.DB.PageMedia(b.ctx, b.bookID, p.N, store.MediaIllustration)
	if err != nil {
		b.first = fmt.Errorf("bookgen: page %d approved but its media row is unreadable: %w", p.N, err)
		return
	}
	payload, err := json.Marshal(pageApprovedEvent{N: p.N, ImageURL: "/media/" + m.ID})
	if err != nil {
		// A fixed struct cannot fail to marshal; keep the compiler
		// honest without a silent empty event.
		b.first = fmt.Errorf("bookgen: marshal page_approved for page %d: %w", p.N, err)
		return
	}
	b.h.cfg.Broker.Publish(Topic(b.bookID), stream.Event{Name: "page_approved", Data: string(payload)})
}

// err returns the first bridge failure, if any.
func (b *approvalBridge) err() error { return b.first }

// renderFilm is stage 4: it reads the persisted illustrations and
// narration clips, renders the film through the video renderer, and
// persists the MP4 — blob first, then a store row attached to the book
// (kind empty: the store's kinds name a blob's role in its book and a
// film has no page or cast anchor, so it is book media with no role —
// BookMedia lists it, the §T11 retention sweep never touches it, and
// GET /media/{id} serves it cold with Range). A film already attached
// to the book (a regeneration) is superseded: the new blob is placed
// first and the old rows are removed afterwards, so the book keeps
// serving the previous film until the new one is on disk.
func (h *Handler) renderFilm(ctx context.Context, bookID string, st story.Story, clips []audio.Clip) (string, error) {
	if len(clips) != len(st.Pages) {
		return "", fmt.Errorf("bookgen: render: narrate returned %d clips for %d pages", len(clips), len(st.Pages))
	}
	inputs := make([]bookvideo.PageInput, len(st.Pages))
	for i, p := range st.Pages {
		if clips[i].N != p.N {
			return "", fmt.Errorf("bookgen: render: clip %d is for page %d, want page %d in order", i, clips[i].N, p.N)
		}
		ill, err := h.cfg.DB.PageMedia(ctx, bookID, p.N, store.MediaIllustration)
		if err != nil {
			return "", fmt.Errorf("bookgen: render: page %d illustration: %w", p.N, err)
		}
		img, err := os.ReadFile(filepath.Join(h.cfg.MediaDir, ill.ID))
		if err != nil {
			return "", fmt.Errorf("bookgen: render: read page %d illustration blob: %w", p.N, err)
		}
		aud, err := os.ReadFile(filepath.Join(h.cfg.MediaDir, clips[i].Media.ID))
		if err != nil {
			return "", fmt.Errorf("bookgen: render: read page %d narration blob: %w", p.N, err)
		}
		inputs[i] = bookvideo.PageInput{N: p.N, ImageBytes: img, AudioBytes: aud}
	}

	// The book row now carries the authored title and the byline; the
	// title card is drawn from them.
	book, err := h.cfg.DB.Book(ctx, bookID)
	if err != nil {
		return "", fmt.Errorf("bookgen: render: %w", err)
	}
	tmp, err := os.CreateTemp("", "thutapi-book-*.mp4")
	if err != nil {
		return "", fmt.Errorf("bookgen: render: create output file: %w", err)
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name) // best effort cleanup on close error
		return "", fmt.Errorf("bookgen: render: close output file: %w", err)
	}
	defer os.Remove(name) // best effort cleanup: the film lives in the blob store now

	if err := h.cfg.Video.Render(ctx, bookvideo.Input{
		Title:      book.Title,
		Byline:     book.Byline,
		Pages:      inputs,
		OutputPath: name,
	}); err != nil {
		return "", fmt.Errorf("bookgen: render: %w", err)
	}

	f, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("bookgen: render: open film: %w", err)
	}
	videoID, err := h.cfg.Film.Persist(ctx, f, filmContentType)
	closeErr := f.Close()
	if err != nil {
		return "", fmt.Errorf("bookgen: persist film: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("bookgen: persist film: close source: %w", closeErr)
	}
	if err := h.cfg.DB.SetMediaPlace(ctx, videoID, store.MediaPlace{BookID: bookID}); err != nil {
		// The film is persisted but unattached: no row makes it
		// servable as the book's, so the run is not ready. The
		// unplaced blob is the crash-window orphan class §T11's
		// retention sweep owns.
		return "", fmt.Errorf("bookgen: attach film: %w", err)
	}
	h.supersedeFilms(ctx, bookID, videoID)
	return videoID, nil
}

// supersedeFilms removes a book's older films once the new one is
// placed. Failure here does not fail the run: the new film is already
// the book's media (BookMedia lists oldest first, so a stale row would
// only confuse T10b's video lookup); a leftover row is logged and left
// for the operator rather than reported as a failed run whose book is
// actually ready.
func (h *Handler) supersedeFilms(ctx context.Context, bookID, keepID string) {
	rows, err := h.cfg.DB.BookMedia(ctx, bookID)
	if err != nil {
		h.log.Warn("bookgen: read book media to supersede old films", "book", bookID, "err", err)
		return
	}
	for _, m := range rows {
		if m.ID == keepID || m.ContentType != filmContentType {
			continue
		}
		if err := h.cfg.Film.Delete(ctx, m.ID); err != nil {
			h.log.Warn("bookgen: superseded film not removed", "book", bookID, "media", m.ID, "err", err)
		}
	}
}
