package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"thutapi/internal/bookgen"
	"thutapi/internal/mediastore"
	"thutapi/internal/prewarm"
	"thutapi/internal/store"
)

type fixedGeneration struct {
	state bookgen.CatchUp
}

type recordingMedia struct {
	id string
}

func (m recordingMedia) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != m.id {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Write([]byte("media"))
}

func (g fixedGeneration) CatchUp(string) bookgen.CatchUp {
	if g.state.Status == "" {
		return bookgen.CatchUp{Status: bookgen.GenerationNotStarted}
	}
	return g.state
}

func openBookStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(t.TempDir(), "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func placeMedia(t *testing.T, db *store.DB, bookID, id, contentType string, place store.MediaPlace) {
	t.Helper()
	if _, err := db.CreateMedia(t.Context(), store.Media{ID: id, ContentType: contentType, SizeBytes: 1}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	if err := db.SetMediaPlace(t.Context(), id, place); err != nil {
		t.Fatalf("place %s: %v", id, err)
	}
}

func TestPagesRenderColdShells(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "A cold book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	bookHandler := NewBookHandler(db, fixedGeneration{})
	tests := []struct {
		name string
		h    http.HandlerFunc
		path string
		want string
	}{
		{"shelf", Shelf, "/", "Make your own book"},
		{"interview", Interview, "/interview/abc", "data-interview-id=\"abc\""},
		{"book", bookHandler.Book, "/book/" + book.ID, "data-book-id=\"" + book.ID + "\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.name == "interview" {
				r.SetPathValue("id", "abc")
			}
			if tt.name == "book" {
				r.SetPathValue("id", book.ID)
			}
			w := httptest.NewRecorder()
			tt.h(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			if !strings.Contains(w.Body.String(), tt.want) {
				t.Fatalf("body lacks %q", tt.want)
			}
			for _, required := range []string{"/static/app.css", "width=device-width,initial-scale=1"} {
				if !strings.Contains(w.Body.String(), required) {
					t.Fatalf("body lacks %q", required)
				}
			}
			if tt.name != "book" && !strings.Contains(w.Body.String(), "/static/app.js") {
				t.Fatal("interactive shell lacks its module entry")
			}
		})
	}
}

func TestBookStateAndColdPageExposeOnlyApprovedArtifacts(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "Mira & the <brambles>")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	book.Byline = "Mira"
	if err := db.UpdateBook(t.Context(), book); err != nil {
		t.Fatalf("byline: %v", err)
	}
	for n, text := range []string{"First <page> words", "Second page words"} {
		if err := db.CreatePage(t.Context(), store.Page{BookID: book.ID, N: n + 1, Text: text}); err != nil {
			t.Fatalf("create page %d: %v", n+1, err)
		}
	}
	placeMedia(t, db, book.ID, "image-1", "image/jpeg", store.MediaPlace{BookID: book.ID, Kind: store.MediaIllustration, PageN: 1})
	placeMedia(t, db, book.ID, "image-2", "image/jpeg", store.MediaPlace{BookID: book.ID, Kind: store.MediaIllustration, PageN: 2})
	placeMedia(t, db, book.ID, "pdf-1", "application/pdf", store.MediaPlace{BookID: book.ID})
	placeMedia(t, db, book.ID, "film-1", "video/mp4", store.MediaPlace{BookID: book.ID})
	h := NewBookHandler(db, fixedGeneration{state: bookgen.CatchUp{Status: bookgen.GenerationReady}})

	stateReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/state", nil)
	stateReq.SetPathValue("id", book.ID)
	stateRes := httptest.NewRecorder()
	h.State(stateRes, stateReq)
	if stateRes.Code != http.StatusOK {
		t.Fatalf("state status = %d, body %s", stateRes.Code, stateRes.Body.String())
	}
	if got := stateRes.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
	}
	var state bookPage
	if err := json.Unmarshal(stateRes.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Status != bookgen.GenerationReady || state.PDFURL != "/media/pdf-1" || state.VideoURL != "/media/film-1" {
		t.Fatalf("state = %+v, want ready with both artifact URLs", state)
	}
	if len(state.Pages) != 2 || state.Pages[0].ImageURL != "/media/image-1" || state.Pages[0].Text != "First <page> words" {
		t.Fatalf("state pages = %+v, want placed image rows with their text", state.Pages)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID, nil)
	pageReq.SetPathValue("id", book.ID)
	pageRes := httptest.NewRecorder()
	h.Book(pageRes, pageReq)
	body := pageRes.Body.String()
	for _, required := range []string{"Mira &amp; the &lt;brambles&gt;", "A book by Mira", "/media/image-1", "First &lt;page&gt; words", "<video controls", "/media/film-1", "Download the film", "/book/" + book.ID + "/download/video", "Download the book (PDF)", "/book/" + book.ID + "/download/pdf", "/static/book/book.css", "/book/" + book.ID} {
		if !strings.Contains(body, required) {
			t.Fatalf("cold page lacks %q", required)
		}
	}
}

func TestBookStateRunningUsesLatestRunNotStaleMedia(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "Retry book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	for n := 1; n <= 2; n++ {
		if err := db.CreatePage(t.Context(), store.Page{BookID: book.ID, N: n, Text: "page"}); err != nil {
			t.Fatalf("create page %d: %v", n, err)
		}
		placeMedia(t, db, book.ID, "old-image-"+string(rune('0'+n)), "image/jpeg", store.MediaPlace{BookID: book.ID, Kind: store.MediaIllustration, PageN: n})
	}
	placeMedia(t, db, book.ID, "old-pdf", "application/pdf", store.MediaPlace{BookID: book.ID})
	placeMedia(t, db, book.ID, "old-film", "video/mp4", store.MediaPlace{BookID: book.ID})
	h := NewBookHandler(db, fixedGeneration{state: bookgen.CatchUp{
		Status:   bookgen.GenerationRunning,
		Approved: []bookgen.ApprovedPage{{N: 2, ImageURL: "/media/new-image-2"}},
	}})
	req := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/state", nil)
	req.SetPathValue("id", book.ID)
	res := httptest.NewRecorder()
	h.State(res, req)
	var state bookPage
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Status != bookgen.GenerationRunning || len(state.Pages) != 1 || state.Pages[0].N != 2 || state.Pages[0].ImageURL != "/media/new-image-2" {
		t.Fatalf("running state = %+v, want only current-run page 2", state)
	}
	if state.PDFURL != "" || state.VideoURL != "" {
		t.Fatalf("running state exposes stale artifacts: %+v", state)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID, nil)
	pageReq.SetPathValue("id", book.ID)
	pageRes := httptest.NewRecorder()
	h.Book(pageRes, pageReq)
	body := pageRes.Body.String()
	if strings.Contains(body, "download/pdf") || strings.Contains(body, "download/video") || strings.Contains(body, "<video") {
		t.Fatalf("running page exposes stale artifact controls: %s", body)
	}
}

func TestBookStateDefaultsToNotStartedWithoutArtifacts(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "Fresh book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	h := NewBookHandler(db, fixedGeneration{})
	req := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/state", nil)
	req.SetPathValue("id", book.ID)
	res := httptest.NewRecorder()
	h.State(res, req)
	var state bookPage
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Status != bookgen.GenerationNotStarted || len(state.Pages) != 0 || state.PDFURL != "" || state.VideoURL != "" {
		t.Fatalf("default state = %+v, want an empty not_started book", state)
	}
}

func TestBookStateReportsFailedLatestRunWithoutInventingArtifacts(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "A paused book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(t.Context(), store.Page{BookID: book.ID, N: 1, Text: "A real approved page."}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	placeMedia(t, db, book.ID, "old-pdf", "application/pdf", store.MediaPlace{BookID: book.ID})
	placeMedia(t, db, book.ID, "old-film", "video/mp4", store.MediaPlace{BookID: book.ID})
	h := NewBookHandler(db, fixedGeneration{state: bookgen.CatchUp{
		Status:   bookgen.GenerationFailed,
		Approved: []bookgen.ApprovedPage{{N: 1, ImageURL: "/media/approved-1"}},
	}})
	req := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/state", nil)
	req.SetPathValue("id", book.ID)
	res := httptest.NewRecorder()
	h.State(res, req)
	var state bookPage
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Status != bookgen.GenerationFailed || len(state.Pages) != 1 || state.Pages[0].ImageURL != "/media/approved-1" || state.PDFURL != "" || state.VideoURL != "" {
		t.Fatalf("failed state = %+v, want only the known approved page and no invented artifacts", state)
	}
	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID, nil)
	pageReq.SetPathValue("id", book.ID)
	pageRes := httptest.NewRecorder()
	h.Book(pageRes, pageReq)
	body := pageRes.Body.String()
	if strings.Contains(body, "download/pdf") || strings.Contains(body, "download/video") || strings.Contains(body, "<video") {
		t.Fatalf("failed page exposes stale artifact controls: %s", body)
	}
}

func TestBookStateUnknownDoesNotExposePriorRunArtifacts(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "An unreadable run")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if err := db.CreatePage(t.Context(), store.Page{BookID: book.ID, N: 1, Text: "A known page."}); err != nil {
		t.Fatalf("create page: %v", err)
	}
	placeMedia(t, db, book.ID, "old-pdf", "application/pdf", store.MediaPlace{BookID: book.ID})
	placeMedia(t, db, book.ID, "old-film", "video/mp4", store.MediaPlace{BookID: book.ID})
	h := NewBookHandler(db, fixedGeneration{state: bookgen.CatchUp{
		Status:   bookgen.GenerationUnknown,
		Approved: []bookgen.ApprovedPage{{N: 1, ImageURL: "/media/current-1"}},
	}})

	req := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/state", nil)
	req.SetPathValue("id", book.ID)
	res := httptest.NewRecorder()
	h.State(res, req)
	var state bookPage
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Status != bookgen.GenerationUnknown || len(state.Pages) != 1 || state.Pages[0].ImageURL != "/media/current-1" || state.PDFURL != "" || state.VideoURL != "" {
		t.Fatalf("unknown state = %+v, want known pages and no prior artifacts", state)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID, nil)
	pageReq.SetPathValue("id", book.ID)
	pageRes := httptest.NewRecorder()
	h.Book(pageRes, pageReq)
	body := pageRes.Body.String()
	if strings.Contains(body, "old-pdf") || strings.Contains(body, "old-film") || strings.Contains(body, "download/pdf") || strings.Contains(body, "download/video") || strings.Contains(body, "<video") {
		t.Fatalf("unknown page exposes prior artifacts: %s", body)
	}

	for _, kind := range []string{"pdf", "video"} {
		download := NewDownloadHandler(db, recordingMedia{id: "old-" + map[string]string{"pdf": "pdf", "video": "film"}[kind]}, fixedGeneration{state: bookgen.CatchUp{Status: bookgen.GenerationUnknown}})
		downloadReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/download/"+kind, nil)
		downloadReq.SetPathValue("id", book.ID)
		downloadReq.SetPathValue("kind", kind)
		downloadRes := httptest.NewRecorder()
		download.Download(downloadRes, downloadReq)
		if downloadRes.Code != http.StatusNotFound {
			t.Fatalf("unknown %s download status = %d, want 404", kind, downloadRes.Code)
		}
	}
}

func TestDownloadUsesStorySlugAndArtifactExtension(t *testing.T) {
	db := openBookStore(t)
	book, err := db.CreateBook(t.Context(), "Mira & Bramble’s Long Day")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	placeMedia(t, db, book.ID, "film-1", "video/mp4", store.MediaPlace{BookID: book.ID})
	placeMedia(t, db, book.ID, "pdf-1", "application/pdf", store.MediaPlace{BookID: book.ID})
	h := NewDownloadHandler(db, recordingMedia{id: "film-1"}, fixedGeneration{state: bookgen.CatchUp{Status: bookgen.GenerationReady}})
	req := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/download/video", nil)
	req.SetPathValue("id", book.ID)
	req.SetPathValue("kind", "video")
	res := httptest.NewRecorder()
	h.Download(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("download status = %d, want 200", res.Code)
	}
	if got, want := res.Header().Get("Content-Disposition"), `attachment; filename="mira-and-brambles-long-day.mp4"`; got != want {
		t.Fatalf("content-disposition = %q, want %q", got, want)
	}
	if got := res.Body.String(); got != "media" {
		t.Fatalf("download body = %q, want delegated media", got)
	}

	pdfHandler := NewDownloadHandler(db, recordingMedia{id: "pdf-1"}, fixedGeneration{state: bookgen.CatchUp{Status: bookgen.GenerationReady}})
	pdfReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/download/pdf", nil)
	pdfReq.SetPathValue("id", book.ID)
	pdfReq.SetPathValue("kind", "pdf")
	pdfRes := httptest.NewRecorder()
	pdfHandler.Download(pdfRes, pdfReq)
	if got, want := pdfRes.Header().Get("Content-Disposition"), `attachment; filename="mira-and-brambles-long-day.pdf"`; got != want {
		t.Fatalf("pdf content-disposition = %q, want %q", got, want)
	}
	running := NewDownloadHandler(db, recordingMedia{id: "film-1"}, fixedGeneration{state: bookgen.CatchUp{Status: bookgen.GenerationRunning}})
	runningReq := httptest.NewRequest(http.MethodGet, "/book/"+book.ID+"/download/video", nil)
	runningReq.SetPathValue("id", book.ID)
	runningReq.SetPathValue("kind", "video")
	runningRes := httptest.NewRecorder()
	running.Download(runningRes, runningReq)
	if runningRes.Code != http.StatusNotFound {
		t.Fatalf("running download status = %d, want 404 without a current-run artifact", runningRes.Code)
	}
}

func TestBookStateMissingIsJSONNotFound(t *testing.T) {
	h := NewBookHandler(openBookStore(t), fixedGeneration{})
	req := httptest.NewRequest(http.MethodGet, "/book/missing/state", nil)
	req.SetPathValue("id", "missing")
	res := httptest.NewRecorder()
	h.State(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("state status = %d, want 404", res.Code)
	}
	if got := res.Body.String(); strings.TrimSpace(got) != `{"error":"not_found"}` {
		t.Fatalf("state error body = %q, want not_found JSON", got)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/book/missing", nil)
	pageReq.SetPathValue("id", "missing")
	pageRes := httptest.NewRecorder()
	h.Book(pageRes, pageReq)
	if pageRes.Code != http.StatusNotFound {
		t.Fatalf("page status = %d, want 404", pageRes.Code)
	}
}

type errStore struct{}

func (errStore) Books(context.Context) ([]store.Book, error) {
	return nil, errors.New("db down")
}

func TestShelfHandler_EmptyStore(t *testing.T) {
	h := NewShelfHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	h.Shelf(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "Make your own book") {
		t.Fatalf("body lacks CTA: %s", body)
	}
	if !strings.Contains(body, `<script type="application/json" id="shelf-books">[]</script>`) {
		t.Fatalf("body lacks empty json script: %s", body)
	}
	if !strings.Contains(body, `data-books="[]"`) {
		t.Fatalf("body lacks empty data-books: %s", body)
	}
	if strings.Contains(body, "class=\"book-card\"") {
		t.Fatalf("empty shelf should not render book cards: %s", body)
	}
}

func TestShelfHandler_StoreError(t *testing.T) {
	h := NewShelfHandler(errStore{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	h.Shelf(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "Make your own book") {
		t.Fatalf("body lacks CTA: %s", body)
	}
	if !strings.Contains(body, `<script type="application/json" id="shelf-books">[]</script>`) {
		t.Fatalf("body lacks empty json script on store error: %s", body)
	}
}

func TestShelf_PackageLevelFunction(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	Shelf(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body := res.Body.String()
	if !strings.Contains(body, "Make your own book") {
		t.Fatalf("body lacks CTA: %s", body)
	}
	if !strings.Contains(body, `data-interview-id="shelf"`) {
		t.Fatalf("body lacks shelf interview-id: %s", body)
	}
	if !strings.Contains(body, `<script type="application/json" id="shelf-books">[]</script>`) {
		t.Fatalf("body lacks empty json script: %s", body)
	}
	if !strings.Contains(body, `<script type="module" src="/static/app.js?v=2"></script>`) {
		t.Fatalf("body lacks cache-busted module script: %s", body)
	}
}

func TestShelfHandler_WithBooks(t *testing.T) {
	db := openBookStore(t)
	b1, err := db.CreateBook(t.Context(), "Moon Bear's Honey")
	if err != nil {
		t.Fatalf("create book 1: %v", err)
	}
	if err := db.UpdateBook(t.Context(), store.Book{ID: b1.ID, Title: b1.Title, Byline: "Little Bear"}); err != nil {
		t.Fatalf("update book 1 byline: %v", err)
	}
	b2, err := db.CreateBook(t.Context(), "Sun River")
	if err != nil {
		t.Fatalf("create book 2: %v", err)
	}

	h := NewShelfHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	h.Shelf(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "Moon Bear&#39;s Honey") && !strings.Contains(body, "Moon Bear's Honey") {
		t.Fatalf("body lacks book 1 title: %s", body)
	}
	if !strings.Contains(body, "By Little Bear") {
		t.Fatalf("body lacks book 1 byline: %s", body)
	}
	if !strings.Contains(body, "Sun River") {
		t.Fatalf("body lacks book 2 title: %s", body)
	}
	if !strings.Contains(body, "/book/"+b1.ID) || !strings.Contains(body, "/book/"+b2.ID) {
		t.Fatalf("body lacks book links: %s", body)
	}
	// Verify JSON script
	if !strings.Contains(body, `<script type="application/json" id="shelf-books">`) {
		t.Fatalf("body lacks shelf-books script: %s", body)
	}
	var parsed []ShelfBook
	startIdx := strings.Index(body, `<script type="application/json" id="shelf-books">`) + len(`<script type="application/json" id="shelf-books">`)
	endIdx := strings.Index(body[startIdx:], `</script>`)
	if err := json.Unmarshal([]byte(body[startIdx:startIdx+endIdx]), &parsed); err != nil {
		t.Fatalf("unmarshal shelf-books script: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("len(parsed) = %d, want 2", len(parsed))
	}
	byID := make(map[string]ShelfBook)
	for _, b := range parsed {
		byID[b.ID] = b
	}
	if b, ok := byID[b1.ID]; !ok || b.Title != "Moon Bear's Honey" || b.Byline != "Little Bear" {
		t.Fatalf("book 1 = %+v, want title 'Moon Bear's Honey' and byline 'Little Bear'", byID[b1.ID])
	}
	if b, ok := byID[b2.ID]; !ok || b.Title != "Sun River" || b.Byline != "" {
		t.Fatalf("book 2 = %+v, want title 'Sun River' and empty byline", byID[b2.ID])
	}
}

func TestPrewarmFixtureCompletedAndRestored(t *testing.T) {
	fixtureDir, err := filepath.Abs(filepath.Join("..", "..", "data", "prewarm", "d625fd608be48227f08c33cf860e5de8"))
	if err != nil {
		t.Fatalf("resolve fixture dir: %v", err)
	}
	manifestPath := filepath.Join(fixtureDir, "book.json")
	rawManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	type manifestMedia struct {
		ID          string          `json:"id"`
		Kind        store.MediaKind `json:"kind,omitempty"`
		PageN       int             `json:"page_n,omitempty"`
		CastName    string          `json:"cast_name,omitempty"`
		ContentType string          `json:"content_type"`
		SizeBytes   int64           `json:"size_bytes"`
	}
	type manifestDoc struct {
		ID        string            `json:"id"`
		Title     string            `json:"title"`
		Byline    string            `json:"byline,omitempty"`
		CreatedAt string            `json:"created_at"`
		Pages     []json.RawMessage `json:"pages"`
		Cast      []json.RawMessage `json:"cast"`
		Media     []manifestMedia   `json:"media"`
	}
	var manifest manifestDoc
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if len(manifest.Media) != 20 {
		t.Fatalf("manifest has %d media items, want 20", len(manifest.Media))
	}

	var hasVideo, hasPDF bool
	for _, m := range manifest.Media {
		if m.ContentType == "video/mp4" {
			hasVideo = true
		}
		if m.ContentType == "application/pdf" {
			hasPDF = true
		}
		blobPath := filepath.Join(fixtureDir, "media", m.ID)
		info, err := os.Stat(blobPath)
		if err != nil {
			t.Fatalf("media blob %s missing on disk: %v", m.ID, err)
		}
		if info.Size() == 0 {
			t.Fatalf("media blob %s on disk is empty", m.ID)
		}
	}
	if !hasVideo {
		t.Fatalf("manifest missing video/mp4 media item")
	}
	if !hasPDF {
		t.Fatalf("manifest missing application/pdf media item")
	}

	// Now verify restore of the fixture
	dataDir := t.TempDir()
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(dataDir, "thutapi.db")})
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer db.Close()

	mediaDir := filepath.Join(dataDir, "media")
	blobs, err := mediastore.Open(t.Context(), mediastore.Config{Dir: mediaDir, DB: db})
	if err != nil {
		t.Fatalf("open mediastore: %v", err)
	}

	fixturesRoot := filepath.Dir(fixtureDir) // data/prewarm
	restored, err := prewarm.Import(t.Context(), db, blobs, fixturesRoot)
	if err != nil {
		t.Fatalf("prewarm.Import: %v", err)
	}
	if len(restored) != 1 || restored[0] != "d625fd608be48227f08c33cf860e5de8" {
		t.Fatalf("restored = %v, want [d625fd608be48227f08c33cf860e5de8]", restored)
	}

	// Assert 20 media items
	mediaRows, err := db.BookMedia(t.Context(), "d625fd608be48227f08c33cf860e5de8")
	if err != nil {
		t.Fatalf("BookMedia: %v", err)
	}
	if len(mediaRows) != 20 {
		t.Fatalf("len(BookMedia) = %d, want 20", len(mediaRows))
	}

	// Verify shelf lists it
	shelfH := NewShelfHandler(db)
	sReq := httptest.NewRequest(http.MethodGet, "/", nil)
	sRes := httptest.NewRecorder()
	shelfH.Shelf(sRes, sReq)
	if sRes.Code != http.StatusOK {
		t.Fatalf("shelf status = %d", sRes.Code)
	}
	if !strings.Contains(sRes.Body.String(), "Bo and Pip&#39;s Moon Mango Dance") &&
		!strings.Contains(sRes.Body.String(), "Bo and Pip's Moon Mango Dance") {
		t.Fatalf("shelf body lacks title: %s", sRes.Body.String())
	}
	if !strings.Contains(sRes.Body.String(), "/book/d625fd608be48227f08c33cf860e5de8") {
		t.Fatalf("shelf body lacks book link: %s", sRes.Body.String())
	}

	// Verify book page and state
	bookH := NewBookHandler(db, fixedGeneration{})
	bReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8", nil)
	bReq.SetPathValue("id", "d625fd608be48227f08c33cf860e5de8")
	bRes := httptest.NewRecorder()
	bookH.Book(bRes, bReq)
	if bRes.Code != http.StatusOK {
		t.Fatalf("book page status = %d", bRes.Code)
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8/state", nil)
	stateReq.SetPathValue("id", "d625fd608be48227f08c33cf860e5de8")
	stateRes := httptest.NewRecorder()
	bookH.State(stateRes, stateReq)
	if stateRes.Code != http.StatusOK {
		t.Fatalf("state status = %d", stateRes.Code)
	}
	var state bookPage
	if err := json.Unmarshal(stateRes.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if state.Status != bookgen.GenerationReady {
		t.Fatalf("state.Status = %v, want ready", state.Status)
	}
	if state.PDFURL == "" || state.VideoURL == "" {
		t.Fatalf("state lacks urls: PDFURL=%q, VideoURL=%q", state.PDFURL, state.VideoURL)
	}

	// Verify downloads
	dlH := NewDownloadHandler(db, blobs, fixedGeneration{})
	dlPDFReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8/download/pdf", nil)
	dlPDFReq.SetPathValue("id", "d625fd608be48227f08c33cf860e5de8")
	dlPDFReq.SetPathValue("kind", "pdf")
	dlPDFRes := httptest.NewRecorder()
	dlH.Download(dlPDFRes, dlPDFReq)
	if dlPDFRes.Code != http.StatusOK {
		t.Fatalf("pdf download status = %d", dlPDFRes.Code)
	}
	if ct := dlPDFRes.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("pdf download content-type = %q, want application/pdf", ct)
	}

	dlVidReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8/download/video", nil)
	dlVidReq.SetPathValue("id", "d625fd608be48227f08c33cf860e5de8")
	dlVidReq.SetPathValue("kind", "video")
	dlVidRes := httptest.NewRecorder()
	dlH.Download(dlVidRes, dlVidReq)
	if dlVidRes.Code != http.StatusOK {
		t.Fatalf("video download status = %d", dlVidRes.Code)
	}
	if ct := dlVidRes.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("video download content-type = %q, want video/mp4", ct)
	}
}
