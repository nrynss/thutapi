package bookgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"thutapi/internal/audio"
	"thutapi/internal/store"
	"thutapi/internal/story"
	"thutapi/internal/stream"
)

// Completing a book that lost its sound.
//
// A generation run can finish with its art intact and its sound missing:
// narration degrades to captioned-silent rather than binning eight paid-for
// illustrations, and the music bed degrades to a plain film. On 2026-09-06
// a live run did both at once — GMI's per-minute cap refused four of eight
// speech calls and the whole music bed, nothing waited (audio/throttle.go
// now does), and a child was handed a silent film of his own story.
//
// Complete is the repair for a book already in that state. It is
// deliberately NOT a re-run: re-running would call M3 for a new structure
// and the image model for eight new illustrations, which costs about
// twenty-three cents, takes six minutes, and — worse — would give the child
// a DIFFERENT book. The pages, the cast and the art are already in the
// store and are the book; only the last three stages are missing, and those
// are the three the store has everything to redo.

// ErrNoStructuredBook reports a book whose rows cannot be read back as a
// story: no pages, or a page that is not the shape the pipeline persisted.
// Completing it is impossible without re-running the structure stage, which
// would replace the book rather than finish it.
var ErrNoStructuredBook = errors.New("bookgen: book has no structured pages to complete")

// Complete re-runs narration, the PDF and the film for a book whose pages
// and illustrations are already persisted, replacing whatever those three
// stages left behind last time. It reads the story back out of the store
// rather than asking M3 for a new one, so the child gets the same book with
// the sound it should have had.
//
// It publishes the same stage, narration_unavailable, book_ready and failed
// events a run does, on the book's own topic, so anything already watching
// that book sees the repair exactly as it sees a run.
//
// Narration and the music bed degrade here on the same terms as in a run: a
// book that cannot be given a voice today is still re-bound and re-filmed,
// and Complete succeeds. Only a failure that leaves no artifact at all —
// the PDF or the film itself — is an error.
func (h *Handler) Complete(ctx context.Context, bookID string, music bool, voiceID string) (pdfID, videoID string, err error) {
	if bookID == "" {
		return "", "", fmt.Errorf("bookgen: complete: %w: book id must not be empty", store.ErrInvalid)
	}
	st, err := h.storyFromStore(ctx, bookID)
	if err != nil {
		return "", "", err
	}
	opts := runOptions{Music: music}
	if audio.ValidVoiceID(voiceID) {
		opts.VoiceID = voiceID
	}
	h.log.Info("bookgen: completing a book from its persisted pages", "book", bookID, "pages", len(st.Pages), "music", music)
	pdfID, videoID, err = h.finishBook(ctx, bookID, st, opts)
	if err != nil {
		h.log.Error("bookgen: completing the book failed", "book", bookID, "err", err)
		h.cfg.Broker.Publish(Topic(bookID), stream.Event{Name: "failed", Data: failedData})
		return "", "", err
	}
	ready, merr := json.Marshal(bookReadyEvent{PDFURL: "/media/" + pdfID, VideoURL: "/media/" + videoID})
	if merr != nil {
		// A fixed struct of strings cannot fail to marshal; keep the
		// compiler honest without a silent empty payload.
		return "", "", fmt.Errorf("bookgen: marshal book_ready: %w", merr)
	}
	h.cfg.Broker.Publish(Topic(bookID), stream.Event{Name: "book_ready", Data: string(ready)})
	return pdfID, videoID, nil
}

// IncompleteBooks lists the books whose last three stages did not all land,
// oldest first. A book is incomplete when it is missing its film, its PDF,
// or a narration clip on any page it has.
//
// The narration check is what makes this useful rather than merely correct:
// the run that lost four of eight voices to a per-minute cap still produced
// a film, so "has a film" would have called that book finished and left the
// child with the silent one. A page with no narration row is a page nobody
// can hear, and it is visible in the store without asking the provider
// anything.
//
// A book with no pages at all is never listed: it was never structured, and
// Complete refuses it rather than inventing a story (ErrNoStructuredBook).
func (h *Handler) IncompleteBooks(ctx context.Context) ([]string, error) {
	books, err := h.cfg.DB.Books(ctx)
	if err != nil {
		return nil, fmt.Errorf("bookgen: list books to complete: %w", err)
	}
	var incomplete []string
	for _, b := range books {
		missing, err := h.missingWork(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		if missing {
			incomplete = append(incomplete, b.ID)
		}
	}
	return incomplete, nil
}

// missingWork reports whether book bookID is missing an artifact the last
// three stages produce.
func (h *Handler) missingWork(ctx context.Context, bookID string) (bool, error) {
	pages, err := h.cfg.DB.Pages(ctx, bookID)
	if err != nil {
		return false, fmt.Errorf("bookgen: read pages for book %s: %w", bookID, err)
	}
	if len(pages) == 0 {
		return false, nil
	}
	media, err := h.cfg.DB.BookMedia(ctx, bookID)
	if err != nil {
		return false, fmt.Errorf("bookgen: read media for book %s: %w", bookID, err)
	}
	film, pdf := false, false
	for _, m := range media {
		switch m.ContentType {
		case filmContentType:
			film = true
		case pdfContentType:
			pdf = true
		}
	}
	if !film || !pdf {
		return true, nil
	}
	for _, p := range pages {
		_, err := h.cfg.DB.PageMedia(ctx, bookID, p.N, store.MediaNarration)
		if errors.Is(err, store.ErrNotFound) {
			return true, nil
		}
		if err != nil {
			return false, fmt.Errorf("bookgen: read narration for book %s page %d: %w", bookID, p.N, err)
		}
	}
	return false, nil
}

// storyFromStore rebuilds the structured story out of the rows the run
// persisted. Every field the last three stages read is on those rows — the
// page text and emotion narration speaks, the lines and text the film
// captions, the title the PDF and the film put on their cover — so nothing
// here needs the model that wrote them.
func (h *Handler) storyFromStore(ctx context.Context, bookID string) (story.Story, error) {
	book, err := h.cfg.DB.Book(ctx, bookID)
	if err != nil {
		return story.Story{}, fmt.Errorf("bookgen: complete %s: %w", bookID, err)
	}
	pages, err := h.cfg.DB.Pages(ctx, bookID)
	if err != nil {
		return story.Story{}, fmt.Errorf("bookgen: complete %s: %w", bookID, err)
	}
	if len(pages) == 0 {
		return story.Story{}, fmt.Errorf("bookgen: complete %s: %w", bookID, ErrNoStructuredBook)
	}
	cast, err := h.cfg.DB.Cast(ctx, bookID)
	if err != nil {
		return story.Story{}, fmt.Errorf("bookgen: complete %s: %w", bookID, err)
	}

	st := story.Story{Title: book.Title}
	for _, m := range cast {
		st.Cast = append(st.Cast, story.CastMember{
			Name:   m.Name,
			Visual: m.Visual,
			Voice:  story.Voice{Pitch: int(m.Pitch), SoundEffects: m.SoundEffects},
		})
	}
	for _, p := range pages {
		lines := make([]story.Line, len(p.Lines))
		for i, l := range p.Lines {
			lines[i] = story.Line{Character: l.Character, Text: l.Text}
		}
		st.Pages = append(st.Pages, story.Page{
			N:          p.N,
			Text:       p.Text,
			Prompt:     p.Prompt,
			Characters: p.Characters,
			Emotion:    p.Emotion,
			Lines:      lines,
		})
	}
	// Page order is the film's segment order and the PDF's page order.
	// Pages already come back ordered; sorting is the cheap guarantee that
	// a future store change cannot silently shuffle a book.
	sort.Slice(st.Pages, func(i, j int) bool { return st.Pages[i].N < st.Pages[j].N })
	if st.Pages[0].N != 1 {
		return story.Story{}, fmt.Errorf("bookgen: complete %s: %w: pages start at %d", bookID, ErrNoStructuredBook, st.Pages[0].N)
	}
	return st, nil
}
