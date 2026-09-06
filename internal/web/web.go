// Package web serves Thutapi's server-rendered human pages.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"unicode"

	"thutapi/internal/bookgen"
	"thutapi/internal/store"
)

//go:embed templates/*.html
var templates embed.FS

var pageTemplates = template.Must(template.ParseFS(templates, "templates/*.html"))

// shelfStore is the shelf page's narrow view of the persistent store. The
// shelf needs the book media too: a book row is created the moment an
// interview starts, so "every book" includes every interview anyone ever
// abandoned.
type shelfStore interface {
	Books(ctx context.Context) ([]store.Book, error)
	BookMedia(ctx context.Context, bookID string) ([]store.Media, error)
}

// ShelfBook is a display model for one book card on the landing shelf.
type ShelfBook struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Byline string `json:"byline,omitempty"`
}

// ShelfHandler serves the landing shelf screen.
// Construct it with NewShelfHandler.
type ShelfHandler struct {
	store shelfStore
}

// NewShelfHandler builds the shelf handler from its persistent store.
func NewShelfHandler(db shelfStore) *ShelfHandler {
	return &ShelfHandler{store: db}
}

// Shelf serves the first screen. It lists restored books from the store and
// remains fully functional when JavaScript is off.
func (h *ShelfHandler) Shelf(w http.ResponseWriter, r *http.Request) {
	var books []ShelfBook
	if h.store != nil {
		stored, err := h.store.Books(r.Context())
		if err != nil {
			stored = nil
		}
		for _, b := range stored {
			// Only books there is something to read. A book row is created
			// when an interview STARTS, so an abandoned interview leaves one
			// behind carrying the working title — and the shelf was offering
			// a child five identical "Our story" cards that open on a page
			// with no pictures, no words and nothing to download.
			if !h.readable(r.Context(), b.ID) {
				continue
			}
			books = append(books, ShelfBook{
				ID:     b.ID,
				Title:  b.Title,
				Byline: b.Byline,
			})
		}
	}
	if books == nil {
		books = []ShelfBook{}
	}
	rawJSON, err := json.Marshal(books)
	if err != nil {
		rawJSON = []byte("[]")
	}
	render(w, "shelf", pageData{
		Books:     books,
		BooksJSON: template.JS(rawJSON),
	})
}

// Shelf serves the first screen using an empty shelf store.
// It remains useful when JavaScript is off and preserves backwards compatibility.
func Shelf(w http.ResponseWriter, r *http.Request) {
	NewShelfHandler(nil).Shelf(w, r)
}

// Interview serves the interview and generation screens for one interview.
func Interview(w http.ResponseWriter, r *http.Request) {
	render(w, "interview", pageData{InterviewID: r.PathValue("id")})
}

// bookStore is the book page's narrow view of the persistent store.
type bookStore interface {
	Book(ctx context.Context, id string) (store.Book, error)
	Pages(ctx context.Context, bookID string) ([]store.Page, error)
	BookMedia(ctx context.Context, bookID string) ([]store.Media, error)
}

// generationReader is the book page's narrow view of C4's volatile run state.
type generationReader interface {
	CatchUp(bookID string) bookgen.CatchUp
}

// mediaHandler is the book page's narrow view of the immutable media route.
type mediaHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

// BookHandler serves the book's HTML page and its C4 state read.
// Construct it with NewBookHandler.
type BookHandler struct {
	store      bookStore
	generation generationReader
}

// NewBookHandler builds the book page handler from its persistent book store
// and the generation handler's process-local catch-up state.
func NewBookHandler(db bookStore, generation generationReader) *BookHandler {
	return &BookHandler{store: db, generation: generation}
}

// Book serves the server-rendered, shareable /book/{id} page. Unknown and
// malformed ids are a safe 404; internal store failures do not expose details.
func (h *BookHandler) Book(w http.ResponseWriter, r *http.Request) {
	page, err := h.load(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writePageError(w, err)
		return
	}
	render(w, "book", pageData{Book: page})
}

// State serves C4's JSON catch-up at /book/{id}/state. It is a read, not a
// polling protocol: clients subscribe to their book SSE stream and use this
// once to recover events that happened before that subscription.
func (h *BookHandler) State(w http.ResponseWriter, r *http.Request) {
	page, err := h.load(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeStateError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(page) // the client may close after headers are sent
}

// readable reports whether a book has a finished artifact behind it: the
// printable PDF or the film. Either one means a run reached its last stages
// and there is a book to open; neither means the row is an interview that
// never became one.
func (h *ShelfHandler) readable(ctx context.Context, bookID string) bool {
	media, err := h.store.BookMedia(ctx, bookID)
	if err != nil {
		// A store fault is not evidence that a book is empty. Show it and
		// let the book page say what it can — hiding a real book because
		// one read failed is the worse of the two mistakes.
		return true
	}
	for _, m := range media {
		if m.ContentType == pdfMediaType || m.ContentType == filmMediaType {
			return true
		}
	}
	return false
}

// The two finished-artifact content types the shelf and the download route
// recognise. They mirror bookgen's own constants; this package does not
// import them because bookgen keeps them unexported.
const (
	pdfMediaType  = "application/pdf"
	filmMediaType = "video/mp4"
)

// DownloadHandler serves a book artifact with a filename derived from the
// story title. The media handler still owns byte serving, ranges and errors.
type DownloadHandler struct {
	store      bookStore
	media      mediaHandler
	generation generationReader
}

// NewDownloadHandler builds the named artifact download handler.
func NewDownloadHandler(db bookStore, media mediaHandler, generation generationReader) *DownloadHandler {
	return &DownloadHandler{store: db, media: media, generation: generation}
}

// Download serves the requested book PDF or film as an attachment whose
// filename is a safe slug of the story title.
func (h *DownloadHandler) Download(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	book, err := h.store.Book(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "download unavailable", http.StatusInternalServerError)
		return
	}
	contentType, extension := artifactType(r.PathValue("kind"))
	if contentType == "" {
		http.NotFound(w, r)
		return
	}
	if state := h.generation.CatchUp(id).Status; state == bookgen.GenerationRunning || state == bookgen.GenerationFailed || state == bookgen.GenerationUnknown {
		http.NotFound(w, r)
		return
	}
	mediaRows, err := h.store.BookMedia(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "download unavailable", http.StatusInternalServerError)
		return
	}
	mediaID := ""
	for _, media := range mediaRows {
		if media.ContentType == contentType {
			mediaID = media.ID
			break
		}
	}
	if mediaID == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s%s"`, storySlug(book.Title), extension))
	mediaRequest := r.Clone(r.Context())
	mediaRequest.SetPathValue("id", mediaID)
	h.media.ServeHTTP(w, mediaRequest)
}

func artifactType(kind string) (contentType, extension string) {
	switch kind {
	case "pdf":
		return "application/pdf", ".pdf"
	case "video":
		return "video/mp4", ".mp4"
	default:
		return "", ""
	}
}

func storySlug(title string) string {
	var slug strings.Builder
	pendingSeparator := false
	word := false
	for _, r := range title {
		if r == '&' {
			if word {
				slug.WriteByte('-')
			}
			slug.WriteString("and")
			word = true
			pendingSeparator = true
			continue
		}
		if r == '\'' || r == '\u2019' {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if pendingSeparator && word {
				slug.WriteByte('-')
			}
			pendingSeparator = false
			if r >= 'A' && r <= 'Z' {
				r += 'a' - 'A'
			}
			slug.WriteRune(r)
			word = true
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			pendingSeparator = true
			continue
		}
		if word {
			pendingSeparator = true
		}
	}
	result := strings.Trim(slug.String(), "-")
	if result == "" {
		return "book"
	}
	return result
}

type pageData struct {
	InterviewID string
	Book        bookPage
	Books       []ShelfBook
	BooksJSON   template.JS
}

type bookPage struct {
	ID       string                   `json:"id"`
	Title    string                   `json:"title"`
	Byline   string                   `json:"byline,omitempty"`
	Status   bookgen.GenerationStatus `json:"status"`
	Pages    []bookPageLeaf           `json:"pages"`
	PDFURL   string                   `json:"pdf_url"`
	VideoURL string                   `json:"video_url"`
}

type bookPageLeaf struct {
	N        int    `json:"n"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
}

func (h *BookHandler) load(ctx context.Context, id string) (bookPage, error) {
	book, err := h.store.Book(ctx, id)
	if err != nil {
		return bookPage{}, err
	}
	pages, err := h.store.Pages(ctx, id)
	if err != nil {
		return bookPage{}, err
	}
	media, err := h.store.BookMedia(ctx, id)
	if err != nil {
		return bookPage{}, err
	}

	catchup := h.generation.CatchUp(id)
	images := make(map[int]string)
	pdfURL, videoURL := "", ""
	for _, m := range media {
		switch {
		case m.Kind == store.MediaIllustration:
			images[m.PageN] = "/media/" + m.ID
		case m.ContentType == "application/pdf":
			pdfURL = "/media/" + m.ID
		case m.ContentType == "video/mp4":
			videoURL = "/media/" + m.ID
		}
	}

	// A retry can retain the previous run's media rows while it works. Its
	// in-process approval set is therefore the only honest progress source.
	if catchup.Status == bookgen.GenerationRunning || catchup.Status == bookgen.GenerationFailed {
		images = make(map[int]string, len(catchup.Approved))
		pdfURL, videoURL = "", ""
		for _, approved := range catchup.Approved {
			images[approved.N] = approved.ImageURL
		}
	}
	if catchup.Status == bookgen.GenerationNotStarted && pdfURL != "" && videoURL != "" {
		catchup.Status = bookgen.GenerationReady
	}
	if catchup.Status == bookgen.GenerationReady && (pdfURL == "" || videoURL == "") {
		catchup.Status = bookgen.GenerationUnknown
	}
	if catchup.Status == bookgen.GenerationUnknown {
		images = make(map[int]string, len(catchup.Approved))
		pdfURL, videoURL = "", ""
		for _, approved := range catchup.Approved {
			images[approved.N] = approved.ImageURL
		}
	}

	state := bookPage{
		ID:       book.ID,
		Title:    book.Title,
		Byline:   book.Byline,
		Status:   catchup.Status,
		PDFURL:   pdfURL,
		VideoURL: videoURL,
	}
	for _, page := range pages {
		if imageURL := images[page.N]; imageURL != "" {
			state.Pages = append(state.Pages, bookPageLeaf{N: page.N, Text: page.Text, ImageURL: imageURL})
		}
	}
	return state, nil
}

func (h *BookHandler) writePageError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
		http.NotFound(w, nil)
		return
	}
	http.Error(w, "page unavailable", http.StatusInternalServerError)
}

func (h *BookHandler) writeStateError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	class := "internal"
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalid) {
		status, class = http.StatusNotFound, "not_found"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: class}) // the client may close after headers are sent
}

func render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "page unavailable", http.StatusInternalServerError)
	}
}
