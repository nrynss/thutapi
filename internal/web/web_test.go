package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"thutapi/internal/bookgen"
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
