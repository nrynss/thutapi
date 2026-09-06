package bookgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"thutapi/internal/audio"
	"thutapi/internal/gmi"
	"thutapi/internal/interview"
	"thutapi/internal/job"
	"thutapi/internal/store"
	"thutapi/internal/story"
)

// TestPipeline_EndToEnd runs one book through the whole pipeline over
// the real HTTP surface: POST /interviews/{id}/generate starts a job;
// the book's topic carries eight page_approved events (one per
// approved+persisted page) and then book_ready with the film's media
// URL; the store ends in the ready shape a cold GET /book/{id} will
// serve from — the authored title, the byline preserved, page/cast
// rows, one reference sheet per drawable member, one illustration and
// one narration per page, and exactly one book-attached film row whose
// bytes serve over the real GET /media/{id} route.
func TestPipeline_EndToEnd(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	st := fullStory()
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	if res.JobID == "" || res.BookID != bookID {
		t.Fatalf("generate response = %+v, want a job id and the book id", res)
	}
	if res.Topic != Topic(bookID) {
		t.Fatalf("topic = %q, want %q", res.Topic, Topic(bookID))
	}
	if res.Events != "/interviews/"+ivID+"/generate/events" {
		t.Fatalf("events_url = %q, want the generate events path", res.Events)
	}
	if res.Status != "running" {
		t.Fatalf("status = %q, want running", res.Status)
	}

	// The events arrive as pages are approved: eight page_approved,
	// one per page, then the terminal book_ready.
	approved := map[string]int{}
	for range st.Pages {
		p := ph.waitEvent(sub, "page_approved")
		img, _ := p["image_url"].(string)
		n := int(p["n"].(float64))
		approved[img] = n
	}
	ready := ph.waitEvent(sub, "book_ready")
	videoURL, _ := ready["video_url"].(string)
	pdfURL, _ := ready["pdf_url"].(string)
	if len(approved) != story.PageCount {
		t.Fatalf("page_approved events = %d, want %d", len(approved), story.PageCount)
	}
	seen := map[int]bool{}
	for img, n := range approved {
		if n < 1 || n > story.PageCount || seen[n] {
			t.Fatalf("page_approved set %v is not one event per page 1..%d", approved, story.PageCount)
		}
		seen[n] = true
		if len(img) < len("/media/x") || img[:len("/media/")] != "/media/" {
			t.Fatalf("page_approved %d image_url = %q, want /media/<id>", n, img)
		}
	}
	if len(videoURL) < len("/media/x") || videoURL[:len("/media/")] != "/media/" {
		t.Fatalf("book_ready video_url = %q, want /media/<id>", videoURL)
	}
	if len(pdfURL) < len("/media/x") || pdfURL[:len("/media/")] != "/media/" {
		t.Fatalf("book_ready pdf_url = %q, want /media/<id>", pdfURL)
	}

	// The job landed its terminal state in the runner's registry.
	runRes := ph.waitJob(res.JobID)
	if runRes.Status != job.StatusDone || runRes.Err != nil {
		t.Fatalf("job result = %+v, want done with no error", runRes)
	}

	// The book row carries the authored title and the preserved byline.
	book, err := ph.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("read book: %v", err)
	}
	if book.Title != st.Title {
		t.Fatalf("book title = %q, want %q (the authored title lands on the row)", book.Title, st.Title)
	}
	if book.Byline != "Mira" {
		t.Fatalf("book byline = %q, want Mira (preserved through the title update)", book.Byline)
	}

	// Pages and cast rows exist with the structured content.
	pages, err := ph.db.Pages(t.Context(), bookID)
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(pages) != story.PageCount {
		t.Fatalf("pages = %d, want %d", len(pages), story.PageCount)
	}
	for i, p := range pages {
		if p.N != st.Pages[i].N || p.Text != st.Pages[i].Text || p.Prompt != st.Pages[i].Prompt {
			t.Fatalf("page %d row = %+v, want the structured content", p.N, p)
		}
	}
	cast, err := ph.db.Cast(t.Context(), bookID)
	if err != nil {
		t.Fatalf("cast: %v", err)
	}
	if len(cast) != len(st.Cast) {
		t.Fatalf("cast = %d, want %d", len(cast), len(st.Cast))
	}

	// Sheets, illustrations and narration landed in their store slots.
	for _, m := range st.Cast {
		if _, err := ph.db.CastMedia(t.Context(), bookID, m.Name); err != nil {
			t.Fatalf("reference sheet for %s: %v", m.Name, err)
		}
	}
	for n := 1; n <= story.PageCount; n++ {
		if _, err := ph.db.PageMedia(t.Context(), bookID, n, store.MediaIllustration); err != nil {
			t.Fatalf("page %d illustration: %v", n, err)
		}
		if _, err := ph.db.PageMedia(t.Context(), bookID, n, store.MediaNarration); err != nil {
			t.Fatalf("page %d narration: %v", n, err)
		}
	}

	// The artifacts: exactly one book-attached video/mp4 row and one application/pdf row,
	// whose bytes serve over the real /media route.
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	var filmRows, pdfRows []store.Media
	for _, m := range all {
		switch m.ContentType {
		case "video/mp4":
			filmRows = append(filmRows, m)
		case "application/pdf":
			pdfRows = append(pdfRows, m)
		}
	}
	if len(filmRows) != 1 {
		t.Fatalf("film rows = %d, want exactly 1", len(filmRows))
	}
	if filmRows[0].ID != videoURL[len("/media/"):] {
		t.Fatalf("book_ready video id %q != the attached film row %q", videoURL, filmRows[0].ID)
	}
	gotFilm := getMedia(t, srv, filmRows[0].ID)
	wantFilm := []byte("film:" + st.Title + ":Mira")
	if string(gotFilm) != string(wantFilm) {
		t.Fatalf("served film bytes = %q, want %q", gotFilm, wantFilm)
	}

	if len(pdfRows) != 1 {
		t.Fatalf("pdf rows = %d, want exactly 1", len(pdfRows))
	}
	if pdfRows[0].ID != pdfURL[len("/media/"):] {
		t.Fatalf("book_ready pdf id %q != the attached pdf row %q", pdfURL, pdfRows[0].ID)
	}
	gotPDF := getMedia(t, srv, pdfRows[0].ID)
	wantPDF := []byte("%PDF-1.4 " + st.Title + ":Mira")
	if string(gotPDF) != string(wantPDF) {
		t.Fatalf("served pdf bytes = %q, want %q", gotPDF, wantPDF)
	}

	// The PDF stage read the real page blobs:
	pdfIns := ph.pdf.inputs()
	if len(pdfIns) != 1 {
		t.Fatalf("pdf render calls = %d, want 1", len(pdfIns))
	}
	if pdfIns[0].Title != st.Title || pdfIns[0].Byline != "Mira" {
		t.Fatalf("pdf input title/byline = %q/%q, want the authored title and byline", pdfIns[0].Title, pdfIns[0].Byline)
	}
	if len(pdfIns[0].Pages) != story.PageCount {
		t.Fatalf("pdf input pages = %d, want %d", len(pdfIns[0].Pages), story.PageCount)
	}
	for i, p := range pdfIns[0].Pages {
		if p.N != i+1 {
			t.Fatalf("pdf input page %d has n=%d, want page order 1..%d", i, p.N, story.PageCount)
		}
		if len(p.ImageBytes) == 0 {
			t.Fatalf("pdf input page %d lacks image bytes", p.N)
		}
	}

	// The film stage read the real page blobs: the renderer's input
	// carries the persisted illustration/narration bytes in page order.
	ins := ph.render.inputs()
	if len(ins) != 1 {
		t.Fatalf("render calls = %d, want 1", len(ins))
	}
	in := ins[0]
	if in.Title != st.Title || in.Byline != "Mira" {
		t.Fatalf("render input title/byline = %q/%q, want the authored title and byline", in.Title, in.Byline)
	}
	if len(in.Pages) != story.PageCount {
		t.Fatalf("render input pages = %d, want %d", len(in.Pages), story.PageCount)
	}
	for i, p := range in.Pages {
		if p.N != i+1 {
			t.Fatalf("render input page %d has n=%d, want page order 1..%d", i, p.N, story.PageCount)
		}
		if len(p.ImageBytes) == 0 || len(p.AudioBytes) == 0 {
			t.Fatalf("render input page %d lacks image/audio bytes", p.N)
		}
	}
}

// getMedia GETs one /media/{id} through the server and returns the
// body.
func getMedia(t *testing.T, srv *httptest.Server, id string) []byte {
	t.Helper()
	resp, err := http.Get(srv.URL + "/media/" + id)
	if err != nil {
		t.Fatalf("GET media: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /media/%s status = %d, want 200", id, resp.StatusCode)
	}
	var body []byte
	buf := make([]byte, 512)
	for {
		n, rerr := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if rerr != nil {
			break
		}
	}
	return body
}

// TestPipeline_StageOrder pins the PLAN.md §T10c stage order by the
// fake call log: structure before any image work, every reference
// sheet before any page, every page before any narration, narration
// before the render, the render before the film persist.
func TestPipeline_StageOrder(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, _ := ph.makeEndedInterview("")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("job = %+v, want done", runRes)
	}

	order := ph.order.snapshot()
	stages := []string{"imager:gen", "imager:edit", "tts", "pdf", "render"}
	prev := -1
	for _, s := range stages {
		at := firstIndex(order, s)
		if at < 0 {
			t.Fatalf("stage %q never ran (order: %v)", s, order)
		}
		if at <= prev {
			t.Fatalf("stage %q at %d is not after the previous stage at %d (order: %v)", s, at, prev, order)
		}
		prev = at
	}
	if lastIndexOf(order, "film") <= firstIndex(order, "render") {
		t.Fatalf("film persist did not happen after video render (order: %v)", order)
	}

	gen, edit := ph.imager.kinds()
	if gen != 2 {
		t.Fatalf("reference renders = %d, want 2 (one per drawable member)", gen)
	}
	if edit != story.PageCount {
		t.Fatalf("page renders = %d, want %d (every verdict matched, so no regeneration)", edit, story.PageCount)
	}
	if len(ph.tts.recorded()) != story.PageCount {
		t.Fatalf("narration calls = %d, want %d", len(ph.tts.recorded()), story.PageCount)
	}

	// All page renders happen between the last sheet and the first
	// narration: no sheet renders after a page starts, and no
	// narration before every page is approved.
	if firstIndex(order, "imager:gen") > firstIndex(order, "imager:edit") {
		t.Fatalf("a page render started before the sheets finished (order: %v)", order)
	}
	if firstIndex(order, "tts") < lastIndexOf(order, "imager:edit") {
		t.Fatalf("narration started before every page render finished (order: %v)", order)
	}
}

// lastIndexOf returns the last position of want in entries, or -1.
func lastIndexOf(entries []string, want string) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i] == want {
			return i
		}
	}
	return -1
}

// TestPipeline_ApprovalEventsCarryRealIds pins the page_approved
// payloads to rows that exist and serve: each event's image_url names
// the MediaIllustration slot of its page.
func TestPipeline_ApprovalEventsCarryRealIds(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()
	sub := ph.subscribe(bookID)

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	got := map[int]string{}
	for range fullStory().Pages {
		p := ph.waitEvent(sub, "page_approved")
		got[int(p["n"].(float64))] = p["image_url"].(string)
	}
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("job = %+v, want done", runRes)
	}
	for n := 1; n <= story.PageCount; n++ {
		row, err := ph.db.PageMedia(t.Context(), bookID, n, store.MediaIllustration)
		if err != nil {
			t.Fatalf("page %d illustration: %v", n, err)
		}
		if got[n] != "/media/"+row.ID {
			t.Fatalf("page_approved %d = %q, want the persisted illustration row %q", n, got[n], "/media/"+row.ID)
		}
	}
}

// TestPipeline_FailureIsTotalAndTerminal pins the failure shape: a
// stage failure fails the whole run (no book_ready, no film row), the
// job lands its terminal error, the book topic carries failed {} (and
// nothing else terminal), and a re-POST after the terminal starts a
// fresh run that completes.
//
// The vehicle is the PDF stage. It used to be narration, which is no
// longer a fatal stage at all (§T10f/§T10g, H3): narration degrades
// per page and the book still lands with both URLs, so it can no
// longer stand in for "a stage failed". The PDF is a genuine hard
// stage — book_ready must never name a pdf_url that was not rendered.
func TestPipeline_FailureIsTotalAndTerminal(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	ph.pdf.err = errors.New("fixture: pdf renderer down")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()
	sub := ph.subscribe(bookID)

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}

	// Every page illustrated and approved before the PDF stage failed;
	// then the run died with failed {}, never book_ready.
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	failed := ph.waitEvent(sub, "failed")
	if len(failed) != 0 {
		t.Fatalf("failed payload = %v, want {} (no code, no prose)", failed)
	}

	runRes := ph.waitJob(res.JobID)
	if runRes.Status != job.StatusError || runRes.Err == nil {
		t.Fatalf("job result = %+v, want terminal error", runRes)
	}
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	for _, m := range all {
		if m.ContentType == "video/mp4" {
			t.Fatalf("failed run left a film row: %+v", m)
		}
	}

	// The failed run is terminal: a re-POST starts a fresh run (new
	// job id) and this one completes. Drain events until the terminal
	// book_ready — the fresh run re-approves every page.
	ph.pdf.err = nil
	code2, res2, eerr := ph.postGenerate(srv, ivID)
	if code2 != http.StatusAccepted {
		t.Fatalf("re-POST after failure status = %d (%+v), want 202", code2, eerr)
	}
	if res2.JobID == res.JobID {
		t.Fatalf("re-POST reused job id %q; a failed run must not block a fresh one", res2.JobID)
	}
	sawPage := false
	for {
		select {
		case ev := <-sub.Events:
			switch ev.Name {
			case "page_approved":
				sawPage = true
			case "book_ready":
				if runRes := ph.waitJob(res2.JobID); runRes.Status != job.StatusDone {
					t.Fatalf("second job = %+v, want done", runRes)
				}
				if !sawPage {
					t.Fatalf("second run published book_ready without any page_approved")
				}
				return
			case "failed":
				t.Fatalf("second run failed: %s", ev.Data)
			case "stage":
				// The second run announces its stages like any other.
			default:
				t.Fatalf("unexpected event %q on the book topic", ev.Name)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("second run never reached book_ready")
		}
	}
}

// TestPipeline_StructureFailurePublishesOnlyFailed pins that a failure
// before any page work produces no page_approved at all — the whole
// run is one failed {} and nothing spent money.
func TestPipeline_StructureFailurePublishesOnlyFailed(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	ph.chat.raw = "this is not a story at all" // Structure's corrective retry fails too
	srv := httptest.NewServer(ph.mux())
	sub := ph.subscribe(bookID)
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	ph.waitEvent(sub, "failed")
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusError {
		t.Fatalf("job = %+v, want terminal error", runRes)
	}
	if gen, edit := ph.imager.kinds(); gen != 0 || edit != 0 {
		t.Fatalf("imager calls = %d gen / %d edit after a structure failure, want none", gen, edit)
	}
	if len(ph.tts.recorded()) != 0 {
		t.Fatalf("tts calls = %d after a structure failure, want none", len(ph.tts.recorded()))
	}
}

// TestPipeline_PanicStillPublishesFailed pins the panic boundary: a
// panicking stage must not swallow the book topic's terminal event —
// failed {} fires once, and the job lands its ErrPanic terminal.
func TestPipeline_PanicStillPublishesFailed(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	ph.chat.panics = true
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()
	sub := ph.subscribe(bookID)

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	ph.waitEvent(sub, "failed")
	runRes := ph.waitJob(res.JobID)
	if runRes.Status != job.StatusError || !errors.Is(runRes.Err, job.ErrPanic) {
		t.Fatalf("job = %+v, want a terminal error wrapping ErrPanic", runRes)
	}
}

// TestPipeline_RenderFailureAndFilmFailureAreTotal pins the two film
// stage failures: a render error and a persist error both fail the
// run, publish failed {} and leave no book-ready film row.
func TestPipeline_RenderFailureAndFilmFailureAreTotal(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*pipelineHarness)
	}{
		{"pdf render", func(ph *pipelineHarness) { ph.pdf.err = errors.New("fixture: pdf render failed") }},
		{"render", func(ph *pipelineHarness) { ph.render.err = errors.New("fixture: ffmpeg failed") }},
		{"film persist", func(ph *pipelineHarness) { ph.film.err = errors.New("fixture: blob store down") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ph := newPipelineHarness(t)
			ivID, bookID := ph.makeEndedInterview("")
			tc.fail(ph)
			srv := httptest.NewServer(ph.mux())
			defer srv.Close()
			sub := ph.subscribe(bookID)

			code, res, _ := ph.postGenerate(srv, ivID)
			if code != http.StatusAccepted {
				t.Fatalf("POST generate status = %d, want 202", code)
			}
			for range fullStory().Pages {
				ph.waitEvent(sub, "page_approved")
			}
			ph.waitEvent(sub, "failed")
			runRes := ph.waitJob(res.JobID)
			if runRes.Status != job.StatusError {
				t.Fatalf("job = %+v, want terminal error", runRes)
			}
			all, err := ph.db.BookMedia(t.Context(), bookID)
			if err != nil {
				t.Fatalf("book media: %v", err)
			}
			for _, m := range all {
				if m.ContentType == "video/mp4" {
					t.Fatalf("failed run left a film row: %+v", m)
				}
			}
		})
	}
}

// TestPipeline_NarrationTransientOutageProducesCaptionedSilentFilm pins
// §T10g's outage path, which supersedes T10f's pre-activation contract: when
// MiniMax TTS fails with gmi.ErrTransient (e.g. 503 capacity outage),
// narration is skipped, narration_unavailable {} is published once, the PDF
// is still rendered and attached, and the film is STILL rendered — captioned
// and silent, with the page words reaching the renderer and no audio bytes —
// so book_ready carries both pdf_url and video_url. The run completes with
// StatusDone (not failed).
func TestPipeline_NarrationTransientOutageProducesCaptionedSilentFilm(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("Leo")
	st := fullStory()
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	// Inject gmi.ErrTransient into TTS to simulate upstream 503 capacity exhaustion:
	ph.tts.err = gmi.ErrTransient

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}

	// Eight page_approved events still arrive:
	for range st.Pages {
		ph.waitEvent(sub, "page_approved")
	}

	// narration_unavailable arrives with empty {}:
	nu := ph.waitEvent(sub, "narration_unavailable")
	if len(nu) != 0 {
		t.Fatalf("narration_unavailable event data = %+v, want empty {}", nu)
	}

	// book_ready arrives with pdf_url AND video_url (the captioned-silent film):
	ready := ph.waitEvent(sub, "book_ready")
	pdfURL, ok := ready["pdf_url"].(string)
	if !ok || len(pdfURL) < len("/media/x") || pdfURL[:len("/media/")] != "/media/" {
		t.Fatalf("book_ready pdf_url = %q, want /media/<id>", pdfURL)
	}
	videoURL, ok := ready["video_url"].(string)
	if !ok || len(videoURL) < len("/media/x") || videoURL[:len("/media/")] != "/media/" {
		t.Fatalf("book_ready video_url = %v, want /media/<id> (the film is captioned-silent, never absent)", videoURL)
	}

	// Job finishes successfully!
	runRes := ph.waitJob(res.JobID)
	if runRes.Status != job.StatusDone || runRes.Err != nil {
		t.Fatalf("job result = %+v, want done with no error", runRes)
	}

	// Film renderer WAS called once — with the page words and no audio bytes
	// (bookvideo derives each silent page's hold from its words).
	ins := ph.render.inputs()
	if len(ins) != 1 {
		t.Fatalf("film renderer calls = %d, want 1 (captioned-silent film)", len(ins))
	}
	if len(ins[0].Pages) != story.PageCount {
		t.Fatalf("render input pages = %d, want %d", len(ins[0].Pages), story.PageCount)
	}
	for i, p := range ins[0].Pages {
		if p.Text != st.Pages[i].Text {
			t.Fatalf("render input page %d text = %q, want the page's own words %q", p.N, p.Text, st.Pages[i].Text)
		}
		if len(p.AudioBytes) != 0 {
			t.Fatalf("render input page %d carries audio bytes, want silent on outage", p.N)
		}
		if len(p.ImageBytes) == 0 {
			t.Fatalf("render input page %d lacks image bytes", p.N)
		}
	}

	// PDF renderer WAS called with the story:
	pdfIns := ph.pdf.inputs()
	if len(pdfIns) != 1 {
		t.Fatalf("pdf renderer calls = %d, want 1", len(pdfIns))
	}
	if pdfIns[0].Title != st.Title || pdfIns[0].Byline != "Leo" {
		t.Fatalf("pdf input title/byline = %q/%q, want %q/Leo", pdfIns[0].Title, pdfIns[0].Byline, st.Title)
	}

	// Exactly one application/pdf row AND one video/mp4 row attached to the book:
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	var pdfRows, filmRows []store.Media
	for _, m := range all {
		switch m.ContentType {
		case "application/pdf":
			pdfRows = append(pdfRows, m)
		case "video/mp4":
			filmRows = append(filmRows, m)
		}
	}
	if len(pdfRows) != 1 {
		t.Fatalf("pdf rows = %d, want exactly 1", len(pdfRows))
	}
	if len(filmRows) != 1 {
		t.Fatalf("film rows = %d, want exactly 1 (captioned-silent film)", len(filmRows))
	}
	if pdfRows[0].ID != pdfURL[len("/media/"):] {
		t.Fatalf("book_ready pdf id %q != attached pdf row %q", pdfURL, pdfRows[0].ID)
	}
	if filmRows[0].ID != videoURL[len("/media/"):] {
		t.Fatalf("book_ready video id %q != attached film row %q", videoURL, filmRows[0].ID)
	}

	// Served PDF matches:
	got := getMedia(t, srv, pdfRows[0].ID)
	want := []byte("%PDF-1.4 " + st.Title + ":Leo")
	if string(got) != string(want) {
		t.Fatalf("served pdf bytes = %q, want %q", got, want)
	}
}

// TestPipeline_NarrationFailureStillCompletesTheBook is H3's
// regression test, and it INVERTS the pin that used to live here
// (TestPipeline_NarrationNonTransientFailureFailsRun, which asserted a
// non-transient narration error published failed {} and landed a
// terminal job error). That pin contradicted §T10f/§T10g and was the
// live defect: on 2026-09-06 an eight-page book was structured and
// fully illustrated, seven of eight pages narrated, and page 4's TTS
// came back "request-queue poll deadline exceeded (last status:
// processing)" — not a 503, so not gmi.ErrTransient — and the whole
// run failed. No pdf_url, no video_url, every illustration wasted.
//
// The contract this now pins: a per-page narration failure of ANY
// error class degrades THAT PAGE to the captioned-silent tier and the
// book completes with both URLs. The subtests cover one page failing
// and every page failing, both with a deliberately non-transient
// error, because no error class may be special-cased.
func TestPipeline_NarrationFailureStillCompletesTheBook(t *testing.T) {
	// The exact shape of the live failure: not a 503, not transient.
	fatal := errors.New("media: request-queue poll deadline exceeded (last status: processing)")

	tests := []struct {
		name string
		// silent reports whether the page with this text must come out
		// silent; arrange installs the failure on the harness.
		arrange   func(ph *pipelineHarness, st story.Story)
		wantVoice func(text string) bool
	}{
		{
			name: "one page fails",
			arrange: func(ph *pipelineHarness, st story.Story) {
				ph.tts.textErrs = map[string]error{st.Pages[3].Text: fatal}
			},
			wantVoice: func(text string) bool { return text != fullStory().Pages[3].Text },
		},
		{
			name: "every page fails",
			arrange: func(ph *pipelineHarness, st story.Story) {
				ph.tts.err = fatal
			},
			wantVoice: func(string) bool { return false },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPipelineHarness(t)
			ivID, bookID := ph.makeEndedInterview("")
			st := fullStory()
			tt.arrange(ph, st)
			srv := httptest.NewServer(ph.mux())
			defer srv.Close()
			sub := ph.subscribe(bookID)

			code, res, _ := ph.postGenerate(srv, ivID)
			if code != http.StatusAccepted {
				t.Fatalf("POST generate status = %d, want 202", code)
			}
			for range st.Pages {
				ph.waitEvent(sub, "page_approved")
			}

			// narration_unavailable is published once, and then the run
			// carries on: book_ready, never failed.
			ph.waitEvent(sub, "narration_unavailable")
			ready := ph.waitEvent(sub, "book_ready")

			// BOTH URLs on every run — the §T10g contract T10g closed on.
			pdfURL, ok := ready["pdf_url"].(string)
			if !ok || !strings.HasPrefix(pdfURL, "/media/") || len(pdfURL) <= len("/media/") {
				t.Fatalf("book_ready pdf_url = %v, want /media/<id>", ready["pdf_url"])
			}
			videoURL, ok := ready["video_url"].(string)
			if !ok || !strings.HasPrefix(videoURL, "/media/") || len(videoURL) <= len("/media/") {
				t.Fatalf("book_ready video_url = %v, want /media/<id>", ready["video_url"])
			}

			if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone || runRes.Err != nil {
				t.Fatalf("job = %+v, want done with no error", runRes)
			}

			// The film was rendered once, mixing tiers: the pages that
			// spoke carry audio and a Duration, the pages that failed
			// carry neither and hold on their words alone.
			ins := ph.render.inputs()
			if len(ins) != 1 {
				t.Fatalf("film renderer calls = %d, want 1", len(ins))
			}
			if len(ins[0].Pages) != story.PageCount {
				t.Fatalf("render input pages = %d, want %d", len(ins[0].Pages), story.PageCount)
			}
			for i, p := range ins[0].Pages {
				if p.Text != st.Pages[i].Text {
					t.Fatalf("render page %d text = %q, want %q", p.N, p.Text, st.Pages[i].Text)
				}
				if len(p.ImageBytes) == 0 {
					t.Fatalf("render page %d lacks image bytes", p.N)
				}
				voiced := tt.wantVoice(p.Text)
				if got := len(p.AudioBytes) > 0; got != voiced {
					t.Errorf("render page %d has audio = %v, want %v", p.N, got, voiced)
				}
				// bookvideo refuses a Duration without audio and audio
				// without a Duration; the two must agree per page.
				if got := p.Duration > 0; got != voiced {
					t.Errorf("render page %d has a Duration = %v, want %v", p.N, got, voiced)
				}
			}

			// One PDF row and one film row are really attached.
			all, err := ph.db.BookMedia(t.Context(), bookID)
			if err != nil {
				t.Fatalf("book media: %v", err)
			}
			var pdfRows, filmRows []store.Media
			for _, m := range all {
				switch m.ContentType {
				case "application/pdf":
					pdfRows = append(pdfRows, m)
				case "video/mp4":
					filmRows = append(filmRows, m)
				}
			}
			if len(pdfRows) != 1 || pdfRows[0].ID != pdfURL[len("/media/"):] {
				t.Fatalf("pdf rows = %+v, want exactly the one book_ready names", pdfRows)
			}
			if len(filmRows) != 1 || filmRows[0].ID != videoURL[len("/media/"):] {
				t.Fatalf("film rows = %+v, want exactly the one book_ready names", filmRows)
			}
		})
	}
}

// TestDoubleFireRefused pins the cheapest-expensive-bug guard: a
// second POST while the first run is generating is refused with 409
// busy and starts nothing; once the run terminates, the next POST
// starts a fresh run and supersedes the first film.
func TestDoubleFireRefused(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	// Gate the structure chat so the first run provably stays running
	// while the second POST arrives.
	gate := make(chan struct{})
	ph.chat.mu.Lock()
	ph.chat.gate = gate
	ph.chat.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()
	sub := ph.subscribe(bookID)

	code, first, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("first POST status = %d, want 202", code)
	}

	// The first run is gated inside structure — still running.
	code2, _, eerr := ph.postGenerate(srv, ivID)
	if code2 != http.StatusConflict || eerr.Error != classBusy {
		t.Fatalf("second POST = %d (%+v), want 409 busy", code2, eerr)
	}

	// Release the gate and let the first run finish.
	close(gate)
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ready1 := ph.waitEvent(sub, "book_ready")
	if runRes := ph.waitJob(first.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("first job = %+v, want done", runRes)
	}

	// Terminal run: the next POST starts a fresh run over the same
	// book and topic.
	code3, second, _ := ph.postGenerate(srv, ivID)
	if code3 != http.StatusAccepted {
		t.Fatalf("POST after terminal status = %d, want 202", code3)
	}
	if second.JobID == first.JobID {
		t.Fatalf("second run reused the first job id")
	}
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ready2 := ph.waitEvent(sub, "book_ready")
	if runRes := ph.waitJob(second.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("second job = %+v, want done", runRes)
	}

	// The second film superseded the first: exactly one book-attached
	// video row, the second run's.
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	var films []store.Media
	for _, m := range all {
		if m.ContentType == "video/mp4" {
			films = append(films, m)
		}
	}
	if len(films) != 1 {
		t.Fatalf("film rows after two runs = %d, want 1 (the old film is superseded)", len(films))
	}
	secondVideo := ready2["video_url"].(string)
	if films[0].ID != secondVideo[len("/media/"):] {
		t.Fatalf("surviving film %q is not the second run's %q", films[0].ID, secondVideo)
	}
	if ready1["video_url"] == secondVideo {
		t.Fatalf("both runs produced the same film URL %q", secondVideo)
	}
	// The second PDF superseded the first: exactly one book-attached
	// application/pdf row, the second run's, and the two runs produced
	// different pdf ids.
	var pdfs []store.Media
	for _, m := range all {
		if m.ContentType == "application/pdf" {
			pdfs = append(pdfs, m)
		}
	}
	if len(pdfs) != 1 {
		t.Fatalf("pdf rows after two runs = %d, want 1 (the old PDF is superseded)", len(pdfs))
	}
	secondPDF := ready2["pdf_url"].(string)
	if pdfs[0].ID != secondPDF[len("/media/"):] {
		t.Fatalf("surviving pdf %q is not the second run's %q", pdfs[0].ID, secondPDF)
	}
	if ready1["pdf_url"] == secondPDF {
		t.Fatalf("both runs produced the same PDF URL %q", secondPDF)
	}
}

// TestGenerateRefusals pins the route's refusal classes: an unknown
// interview is 404 not_found, an un-ended interview is 409 not_ended,
// and an interview with no book row is 500 internal.
func TestGenerateRefusals(t *testing.T) {
	ph := newPipelineHarness(t)
	_, bookID := ph.makeEndedInterview("")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	// Unknown interview.
	code, _, eerr := ph.postGenerate(srv, "00000000000000000000000000000000")
	if code != http.StatusNotFound || eerr.Error != classNotFound {
		t.Fatalf("unknown interview = %d (%+v), want 404 not_found", code, eerr)
	}

	// An open interview: a transcript with no closing turn.
	book, err := ph.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("read book: %v", err)
	}
	iv, err := ph.db.CreateInterview(t.Context())
	if err != nil {
		t.Fatalf("create interview: %v", err)
	}
	iv.BookID = book.ID
	iv.Turns = []store.Turn{
		{Role: interview.RoleInterviewer, Text: "Who is your hero?"},
		{Role: interview.RoleChild, Text: "Mira"},
	}
	if err := ph.db.UpdateInterview(t.Context(), iv); err != nil {
		t.Fatalf("update interview: %v", err)
	}
	code, _, eerr = ph.postGenerate(srv, iv.ID)
	if code != http.StatusConflict || eerr.Error != classNotEnded {
		t.Fatalf("open interview = %d (%+v), want 409 not_ended", code, eerr)
	}

	// An interview with no book row (cannot arise through the normal
	// start path, but the route must refuse it loudly, not panic).
	orphan, err := ph.db.CreateInterview(t.Context())
	if err != nil {
		t.Fatalf("create orphan interview: %v", err)
	}
	orphan.Turns = []store.Turn{{Role: interview.RoleClosing, Text: "bye"}}
	if err := ph.db.UpdateInterview(t.Context(), orphan); err != nil {
		t.Fatalf("update orphan interview: %v", err)
	}
	code, _, eerr = ph.postGenerate(srv, orphan.ID)
	if code != http.StatusInternalServerError || eerr.Error != classInternal {
		t.Fatalf("bookless interview = %d (%+v), want 500 internal", code, eerr)
	}
}

// TestPipeline_CatchUpStateIsInTheStore pins the late-subscriber
// catch-up surface that exists today: the stream is from-now-on, so a
// subscriber joining after the run cannot replay events — but the
// book's current state is fully readable from the store rows the
// events described (illustration rows are the approved-page count; the
// attached film row is the ready marker; the book row carries the
// title and byline). The HTTP read of that state is contract row C4.
func TestPipeline_CatchUpStateIsInTheStore(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	ph.waitJob(res.JobID)

	// A subscriber joining now sees nothing replayed: broker
	// Subscribe has no replay.
	late := ph.subscribe(bookID)
	select {
	case ev := <-late.Events:
		t.Fatalf("late subscriber received a replayed event %q", ev.Name)
	case <-time.After(100 * time.Millisecond):
	}

	// The catch-up read (what GET /book/{id} will serve — contract row
	// C4): every page's illustration is a row, the film is attached,
	// the title is authored.
	pages, err := ph.db.Pages(t.Context(), bookID)
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(pages) != story.PageCount {
		t.Fatalf("pages = %d, want %d", len(pages), story.PageCount)
	}
	book, err := ph.db.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book: %v", err)
	}
	if book.Title == interview.WorkingTitle {
		t.Fatalf("book title = %q, want the authored title (state is in the store)", book.Title)
	}
}

// TestUpsert_RowsAreReplacedNotDuplicated pins the re-run semantics of
// the store-row stage: upserting the same pages/cast twice keeps one
// row per slot with the newest content.
func TestUpsert_RowsAreReplacedNotDuplicated(t *testing.T) {
	ph := newPipelineHarness(t)
	ctx := t.Context()
	book, err := ph.db.CreateBook(ctx, "upsert book")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	st := fullStory()

	for attempt := 0; attempt < 2; attempt++ {
		for _, m := range st.Cast {
			if err := upsertCastMember(ctx, ph.db, store.CastMember{BookID: book.ID, Name: m.Name, Visual: m.Visual}); err != nil {
				t.Fatalf("upsert cast %s (attempt %d): %v", m.Name, attempt, err)
			}
		}
		for _, p := range st.Pages {
			if err := upsertPage(ctx, ph.db, book.ID, p); err != nil {
				t.Fatalf("upsert page %d (attempt %d): %v", p.N, attempt, err)
			}
		}
	}
	cast, err := ph.db.Cast(ctx, book.ID)
	if err != nil {
		t.Fatalf("cast: %v", err)
	}
	if len(cast) != len(st.Cast) {
		t.Fatalf("cast rows = %d after two upserts, want %d", len(cast), len(st.Cast))
	}
	pages, err := ph.db.Pages(ctx, book.ID)
	if err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(pages) != len(st.Pages) {
		t.Fatalf("page rows = %d after two upserts, want %d", len(pages), len(st.Pages))
	}
}

// musicHarness turns a pipeline harness's music step on: the film
// stage generates a bed (fakeMusic) and mixes it (fakeMixRunner whose
// fake ffmpeg writes a distinguishable mixed film).
func (ph *pipelineHarness) enableMusic(t *testing.T) (*fakeMusic, *fakeMixRunner) {
	t.Helper()
	bed := newBedServer()
	t.Cleanup(bed.Close)
	music := &fakeMusic{bedBase: bed.URL}
	mix := &fakeMixRunner{}
	ph.h.cfg.Music = music
	ph.h.cfg.MusicMix = audio.MixConfig{Runner: mix}
	ph.render.dur = 20 * time.Second
	return music, mix
}

// TestPipeline_MusicOnMixesTheBedUnderTheFilm pins contract row C4 end
// to end: when Config.Music is set, stage 5 generates a wordless bed
// through the settled default shape, mixes it under the RENDERED film
// with the renderer's computed total as the fade anchor, and persists
// the MIXED film as the book's one video row — the render output never
// reaches the store. The music-off path is every other test in this
// file: no music client, no mix, plain film persisted.
func TestPipeline_MusicOnMixesTheBedUnderTheFilm(t *testing.T) {
	ph := newPipelineHarness(t)
	music, mix := ph.enableMusic(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	st := fullStory()
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	for range st.Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ready := ph.waitEvent(sub, "book_ready")
	videoURL, _ := ready["video_url"].(string)
	if !strings.HasPrefix(videoURL, "/media/") {
		t.Fatalf("book_ready video_url = %q, want /media/<id>", videoURL)
	}
	runRes := ph.waitJob(res.JobID)
	if runRes.Status != job.StatusDone || runRes.Err != nil {
		t.Fatalf("job = %+v, want done", runRes)
	}

	// The bed was generated once with the settled default shape.
	calls := music.recorded()
	if len(calls) != 1 {
		t.Fatalf("music calls = %d, want 1", len(calls))
	}
	if calls[0].lyrics != audio.DefaultMusicLyrics || calls[0].prompt != audio.DefaultMusicPrompt {
		t.Errorf("music call = (%q, %q), want the gibberish-vocalise default shape", calls[0].lyrics, calls[0].prompt)
	}
	if calls[0].model != audio.DefaultMusicModel {
		t.Errorf("music model = %q, want %q", calls[0].model, audio.DefaultMusicModel)
	}

	// The mix ran exactly once, over the renderer's own output, with
	// the renderer's computed total as the film duration.
	if mix.count() != 1 {
		t.Fatalf("mix calls = %d, want 1", mix.count())
	}
	got := mix.last()
	filmIdx, bedIdx, outIdx := -1, -1, -1
	for i, a := range got {
		switch a {
		case "-i":
			if filmIdx == -1 {
				filmIdx = i + 1
			} else if bedIdx == -1 {
				bedIdx = i + 1
			}
		case "-y":
			outIdx = i + 1
		}
	}
	if filmIdx == -1 || bedIdx == -1 || outIdx == -1 {
		t.Fatalf("mix command missing inputs/output: %v", got)
	}
	// The mix's film input is the RENDERER'S OWN output file — the
	// render happens before the mix and its path rides straight over
	// (the temp files are cleaned up by the time this test reads the
	// recorded command, so the identity is asserted by path).
	ins := ph.render.inputs()
	if len(ins) != 1 {
		t.Fatalf("render calls = %d, want 1", len(ins))
	}
	if got[filmIdx] != ins[0].OutputPath {
		t.Errorf("mix film input = %q, want the renderer's output %q", got[filmIdx], ins[0].OutputPath)
	}
	if got[bedIdx] == "" {
		t.Error("mix bed input is empty")
	}
	// The fade anchor: the mix received the renderer's computed total
	// (the fake renderer returned 20s), and its filter ends the fade at
	// exactly that total.
	filter := ""
	for i, a := range got {
		if a == "-filter_complex" {
			filter = got[i+1]
		}
	}
	if !strings.Contains(filter, "afade=t=out:st=18.000:d=2.000") {
		t.Errorf("mix filter = %q, want the end fade anchored at the renderer's 20s total (2 s wind-down)", filter)
	}

	// The persisted video row holds the MIXED bytes, never the plain
	// render — the music film replaced the plain one in the single
	// video slot.
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	var films []store.Media
	for _, m := range all {
		if m.ContentType == "video/mp4" {
			films = append(films, m)
		}
	}
	if len(films) != 1 {
		t.Fatalf("video rows = %d, want exactly 1 (the mixed film replaced the plain render)", len(films))
	}
	if films[0].ID != videoURL[len("/media/"):] {
		t.Fatalf("video row %q != book_ready's %q", films[0].ID, videoURL)
	}
	served := getMedia(t, srv, films[0].ID)
	if !strings.HasPrefix(string(served), "mixed-film:") {
		t.Errorf("persisted film = %q, want the MIXED film bytes", served)
	}
}

// TestPipeline_MusicTransientFailureDegradesToThePlainFilm pins the
// degradation path: a bed generation failure wrapped in gmi.ErrTransient
// is an outage, not an error — the film plays without music and the run
// still lands book_ready (exactly how the narration outage degrades).
func TestPipeline_MusicTransientFailureDegradesToThePlainFilm(t *testing.T) {
	ph := newPipelineHarness(t)
	_, mix := ph.enableMusic(t)
	ph.h.cfg.Music.(*fakeMusic).err = fmt.Errorf("%w: music queue down", gmi.ErrTransient)
	ivID, bookID := ph.makeEndedInterview("Mira")
	st := fullStory()
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	for range st.Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ready := ph.waitEvent(sub, "book_ready")
	videoURL, _ := ready["video_url"].(string)
	runRes := ph.waitJob(res.JobID)
	if runRes.Status != job.StatusDone || runRes.Err != nil {
		t.Fatalf("job = %+v, want done despite the music outage", runRes)
	}
	if mix.count() != 0 {
		t.Errorf("mix calls = %d, want 0 (no bed, no mix)", mix.count())
	}
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	for _, m := range all {
		if m.ContentType == "video/mp4" {
			if m.ID != videoURL[len("/media/"):] {
				t.Fatalf("video row %q != book_ready's %q", m.ID, videoURL)
			}
			if !strings.HasPrefix(string(getMedia(t, srv, m.ID)), "film:") {
				t.Errorf("persisted film = %q, want the PLAIN film (no music)", getMedia(t, srv, m.ID))
			}
		}
	}
}

// TestPipeline_FailedRunLogsTheError pins H4: a generation run that
// fails must log an ERROR naming the book and the underlying error.
// The child is shown a bare failed {} — no code, no prose (§T9) — so
// this line is the only record that will ever exist. When the live
// failure of 2026-09-06 happened the server logged NOTHING at all,
// which made it undiagnosable in production.
func TestPipeline_FailedRunLogsTheError(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	ph.pdf.err = errors.New("fixture: pdf renderer down")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()
	sub := ph.subscribe(bookID)

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ph.waitEvent(sub, "failed")
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusError {
		t.Fatalf("job = %+v, want terminal error", runRes)
	}

	logs := ph.logs.String()
	if !strings.Contains(logs, "level=ERROR") {
		t.Fatalf("a failed run logged no ERROR line; logs:\n%s", logs)
	}
	for _, want := range []string{"generation failed", bookID, "fixture: pdf renderer down"} {
		if !strings.Contains(logs, want) {
			t.Errorf("failure log does not mention %q; logs:\n%s", want, logs)
		}
	}
}

// TestPipeline_AnyMusicFailureDegradesToThePlainFilm INVERTS two pins
// that used to live here — TestPipeline_MusicHardFailureFailsRun and
// TestPipeline_MixFailureFailsRun, which asserted that a non-transient
// bed failure and a failed ffmpeg mix each failed the whole run and
// left no film row. Those pins encoded H2: by the time the music step
// runs, the PDF is persisted and the film is RENDERED, so failing the
// run there threw away roughly $0.35 and six minutes of finished book
// and showed the child a failure screen — over background music.
//
// The contract now: ANY music failure degrades to the plain film, the
// same way a transient one always did. The subtests cover the two
// concrete non-transient triggers the review found (a rejected music
// request, and a bed whose storage URL answers non-2xx) plus a broken
// mix pass.
func TestPipeline_AnyMusicFailureDegradesToThePlainFilm(t *testing.T) {
	tests := []struct {
		name string
		// arrange installs the failure; wantMix reports whether the mix
		// step should still have been reached.
		arrange func(t *testing.T, ph *pipelineHarness, mix *fakeMixRunner)
		wantMix int
	}{
		{
			name: "non-transient bed failure",
			arrange: func(_ *testing.T, ph *pipelineHarness, _ *fakeMixRunner) {
				ph.h.cfg.Music.(*fakeMusic).err = errors.New("media: payload rejected")
			},
		},
		{
			name: "bed storage URL answers non-2xx",
			arrange: func(_ *testing.T, ph *pipelineHarness, _ *fakeMixRunner) {
				// The bed request succeeds and names a URL that 404s —
				// the fetchBed leg of the failure, not the queue leg.
				gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "gone", http.StatusNotFound)
				}))
				t.Cleanup(gone.Close)
				ph.h.cfg.Music.(*fakeMusic).bedBase = gone.URL
			},
		},
		{
			name: "mix pass fails",
			arrange: func(_ *testing.T, ph *pipelineHarness, _ *fakeMixRunner) {
				ph.h.cfg.MusicMix = audio.MixConfig{Runner: &fakeMixRunner{err: errors.New("ffmpeg died")}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ph := newPipelineHarness(t)
			_, mix := ph.enableMusic(t)
			tt.arrange(t, ph, mix)
			ivID, bookID := ph.makeEndedInterview("Mira")
			sub := ph.subscribe(bookID)
			srv := httptest.NewServer(ph.mux())
			defer srv.Close()

			code, res, _ := ph.postGenerate(srv, ivID)
			if code != http.StatusAccepted {
				t.Fatalf("POST generate status = %d, want 202", code)
			}
			for range fullStory().Pages {
				ph.waitEvent(sub, "page_approved")
			}
			ready := ph.waitEvent(sub, "book_ready")
			videoURL, _ := ready["video_url"].(string)
			pdfURL, _ := ready["pdf_url"].(string)
			if !strings.HasPrefix(pdfURL, "/media/") || !strings.HasPrefix(videoURL, "/media/") {
				t.Fatalf("book_ready = %v, want both a pdf_url and a video_url", ready)
			}
			if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone || runRes.Err != nil {
				t.Fatalf("job = %+v, want done despite the music failure", runRes)
			}
			if got := mix.count(); got != tt.wantMix {
				t.Errorf("mix calls = %d, want %d", got, tt.wantMix)
			}

			// The film that landed is the PLAIN one the renderer wrote,
			// not a mixed file — and it is the one book_ready names.
			all, err := ph.db.BookMedia(t.Context(), bookID)
			if err != nil {
				t.Fatalf("book media: %v", err)
			}
			films := 0
			for _, m := range all {
				if m.ContentType != "video/mp4" {
					continue
				}
				films++
				if m.ID != videoURL[len("/media/"):] {
					t.Fatalf("video row %q != book_ready's %q", m.ID, videoURL)
				}
				if got := string(getMedia(t, srv, m.ID)); !strings.HasPrefix(got, "film:") {
					t.Errorf("persisted film = %q, want the PLAIN film (no music)", got)
				}
			}
			if films != 1 {
				t.Fatalf("film rows = %d, want exactly 1", films)
			}
		})
	}
}

// TestPipeline_MusicOffSkipsMusicBedAndMix asserts that when POST /interviews/{id}/generate
// specifies {"music": false}, GenerateMusicBed and MixBed are skipped entirely,
// persisting the plain film directly without touching the music provider.
func TestPipeline_MusicOffSkipsMusicBedAndMix(t *testing.T) {
	ph := newPipelineHarness(t)
	music, mix := ph.enableMusic(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/interviews/"+ivID+"/generate", strings.NewReader(`{"music":false}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do generate: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("generate status = %d, want 202", resp.StatusCode)
	}

	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ready := ph.waitEvent(sub, "book_ready")
	videoURL, _ := ready["video_url"].(string)

	// Assert NO music calls were made and mix was never invoked:
	if calls := len(music.recorded()); calls != 0 {
		t.Fatalf("music bed calls = %d, want 0 when music is false", calls)
	}
	if mixes := mix.count(); mixes != 0 {
		t.Fatalf("mix calls = %d, want 0 when music is false", mixes)
	}

	// Persisted film is the plain film:
	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	var filmID string
	for _, m := range all {
		if m.ContentType == "video/mp4" {
			filmID = m.ID
			break
		}
	}
	if filmID == "" {
		t.Fatal("no video/mp4 row found")
	}
	if filmID != videoURL[len("/media/"):] {
		t.Fatalf("film id %q != video_url %q", filmID, videoURL)
	}
	served := getMedia(t, srv, filmID)
	if !strings.HasPrefix(string(served), "film:") {
		t.Errorf("persisted film = %q, want PLAIN film (film: prefix)", served)
	}
}

// TestPipeline_MusicExplicitTrueInvokesMusicBedAndMix asserts that when POST /interviews/{id}/generate
// explicitly specifies {"music": true}, GenerateMusicBed and MixBed are invoked as expected.
func TestPipeline_MusicExplicitTrueInvokesMusicBedAndMix(t *testing.T) {
	ph := newPipelineHarness(t)
	music, mix := ph.enableMusic(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/interviews/"+ivID+"/generate", strings.NewReader(`{"music":true}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do generate: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("generate status = %d, want 202", resp.StatusCode)
	}

	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ready := ph.waitEvent(sub, "book_ready")
	videoURL, _ := ready["video_url"].(string)

	if calls := len(music.recorded()); calls != 1 {
		t.Fatalf("music bed calls = %d, want 1 when music is true", calls)
	}
	if mixes := mix.count(); mixes != 1 {
		t.Fatalf("mix calls = %d, want 1 when music is true", mixes)
	}

	all, err := ph.db.BookMedia(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book media: %v", err)
	}
	var filmID string
	for _, m := range all {
		if m.ContentType == "video/mp4" {
			filmID = m.ID
			break
		}
	}
	if filmID != videoURL[len("/media/"):] {
		t.Fatalf("film id %q != video_url %q", filmID, videoURL)
	}
	served := getMedia(t, srv, filmID)
	if !strings.HasPrefix(string(served), "mixed-film:") {
		t.Errorf("persisted film = %q, want MIXED film", served)
	}
}

// postGenerateJSON sends one generate request with an explicit body, the
// shape the browser's voice step posts.
func postGenerateJSON(t *testing.T, srv *httptest.Server, ivID, body string) (int, generateResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/interviews/"+ivID+"/generate", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do generate: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out generateResponse
	if resp.StatusCode == http.StatusAccepted {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode generate response %q: %v", raw, err)
		}
	}
	return resp.StatusCode, out
}

// TestPipeline_StagesAnnounceEveryStepInOrder pins the signal screen 5's
// progress reading is built on. Before it existed, the eight page_approved
// events were the only thing on the wire, so the four minutes of narration,
// PDF and film after the last page carried nothing at all and the wait
// screen sat frozen at 8/8 (Live bug report 2).
func TestPipeline_StagesAnnounceEveryStepInOrder(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	sub := ph.subscribe(bookID)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ph.waitEvent(sub, "book_ready")
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("job = %+v, want done", runRes)
	}

	var got []Stage
	for _, raw := range ph.stages {
		var ev stageEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("decode stage event %q: %v", raw, err)
		}
		got = append(got, ev.Stage)
	}
	if !slices.Equal(got, Stages) {
		t.Fatalf("stage events = %v, want every stage in pipeline order %v", got, Stages)
	}
	// The last stage entered is also what a reload recovers, so a client
	// that missed the events still reads the run's real position.
	if state := ph.h.CatchUp(bookID); state.Stage != StageFilming {
		t.Fatalf("CatchUp stage = %q, want the last stage entered", state.Stage)
	}
}

// TestPipeline_VoiceIDNarratesTheBook is the other half of the voice step:
// a cloned voice the adult earned has to actually reach the speech calls,
// or the whole clone path is decoration. A value that is not shaped like a
// voice identity is dropped rather than forwarded — it comes from an
// untrusted client and ends up in a provider request body.
func TestPipeline_VoiceIDNarratesTheBook(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "cloned voice", body: `{"music":false,"voice_id":"cloned-voice-abc"}`, want: "cloned-voice-abc"},
		{name: "no voice", body: `{"music":false}`, want: audio.DefaultVoice},
		{name: "empty voice", body: `{"music":false,"voice_id":""}`, want: audio.DefaultVoice},
		{name: "malformed voice is dropped", body: `{"music":false,"voice_id":"../../etc/passwd"}`, want: audio.DefaultVoice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ph := newPipelineHarness(t)
			ivID, bookID := ph.makeEndedInterview("Mira")
			sub := ph.subscribe(bookID)
			srv := httptest.NewServer(ph.mux())
			defer srv.Close()

			code, res := postGenerateJSON(t, srv, ivID, tc.body)
			if code != http.StatusAccepted {
				t.Fatalf("POST generate status = %d, want 202", code)
			}
			for range fullStory().Pages {
				ph.waitEvent(sub, "page_approved")
			}
			ph.waitEvent(sub, "book_ready")
			if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone {
				t.Fatalf("job = %+v, want done", runRes)
			}

			calls := ph.tts.recorded()
			if len(calls) == 0 {
				t.Fatalf("no narration calls were made")
			}
			for _, c := range calls {
				if c.voice != tc.want {
					t.Fatalf("narration voice = %q, want %q", c.voice, tc.want)
				}
			}
		})
	}
}

// TestComplete_GivesASilentBookItsSoundBack is the repair for the book of
// 2026-09-06: art intact, no narration, no music, and a child holding a
// silent film of his own story. Complete must give it a voice and a bed
// WITHOUT calling the structure or image models — a re-run would hand the
// child a different book.
func TestComplete_GivesASilentBookItsSoundBack(t *testing.T) {
	ph := newPipelineHarness(t)
	music, mix := ph.enableMusic(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	// A first run whose narration is refused end to end, exactly as a
	// per-minute cap refused it live: the book lands captioned-silent.
	ph.tts.err = fmt.Errorf("%w: rate limit exceeded(RPM). Please try again", gmi.ErrRateLimited)
	sub := ph.subscribe(bookID)
	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ph.waitEvent(sub, "narration_unavailable")
	ph.waitEvent(sub, "book_ready")
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("first job = %+v, want done", runRes)
	}
	for n := 1; n <= story.PageCount; n++ {
		if _, err := ph.db.PageMedia(t.Context(), bookID, n, store.MediaNarration); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("page %d has narration after a refused run: %v", n, err)
		}
	}
	structureCalls := ph.chat.calls
	imageCalls := ph.imager.count()
	musicCalls := len(music.recorded())
	mixPasses := mix.count()

	// The cap clears. Complete finishes the book the child already has.
	ph.tts.err = nil
	repair := ph.subscribe(bookID)
	pdfID, videoID, err := ph.h.Complete(t.Context(), bookID, true, "")
	if err != nil {
		t.Fatalf("Complete = %v, want the book finished", err)
	}
	if pdfID == "" || videoID == "" {
		t.Fatalf("Complete returned pdf %q video %q, want both", pdfID, videoID)
	}

	// Every page now speaks.
	for n := 1; n <= story.PageCount; n++ {
		if _, err := ph.db.PageMedia(t.Context(), bookID, n, store.MediaNarration); err != nil {
			t.Errorf("page %d still silent after Complete: %v", n, err)
		}
	}
	// And the film has a bed under it.
	if got := len(music.recorded()) - musicCalls; got != 1 {
		t.Errorf("music bed calls during the repair = %d, want 1", got)
	}
	if got := mix.count() - mixPasses; got != 1 {
		t.Errorf("mix passes during the repair = %d, want the bed mixed under the film", got)
	}
	// Nothing was re-structured and nothing was re-drawn: this is the same
	// book, not a new one.
	if ph.chat.calls != structureCalls {
		t.Errorf("structure calls = %d, want the original %d — Complete must not re-write the story", ph.chat.calls, structureCalls)
	}
	if got := ph.imager.count(); got != imageCalls {
		t.Errorf("image calls = %d, want the original %d — Complete must not re-draw the book", got, imageCalls)
	}
	// The repair announces itself on the book's own topic like a run does.
	ready := ph.waitEvent(repair, "book_ready")
	if u, _ := ready["video_url"].(string); u != "/media/"+videoID {
		t.Errorf("book_ready video_url = %q, want /media/%s", u, videoID)
	}
}

func TestComplete_RefusesABookItWouldHaveToInvent(t *testing.T) {
	ph := newPipelineHarness(t)
	if _, _, err := ph.h.Complete(t.Context(), "", true, ""); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("Complete(\"\") = %v, want ErrInvalid", err)
	}
	if _, _, err := ph.h.Complete(t.Context(), "no-such-book", true, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Complete(unknown) = %v, want ErrNotFound", err)
	}
	// A book row with no pages was never structured; completing it would
	// mean inventing the story, which is a re-run, not a repair.
	bare, err := ph.db.CreateBook(t.Context(), "A Book With No Pages")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	if _, _, err := ph.h.Complete(t.Context(), bare.ID, true, ""); !errors.Is(err, ErrNoStructuredBook) {
		t.Errorf("Complete(unstructured) = %v, want ErrNoStructuredBook", err)
	}
}

// TestIncompleteBooks_NamesEveryBookMissingSound pins what `-complete all`
// picks up. The narration half is the part that matters: the run of
// 2026-09-06 produced a film with four of eight pages silent, so a
// film-only check would have called that book finished and left the child
// with the silent one.
func TestIncompleteBooks_NamesEveryBookMissingSound(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	neverStructured, err := ph.db.CreateBook(t.Context(), "Never Generated")
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	sub := ph.subscribe(bookID)
	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	for range fullStory().Pages {
		ph.waitEvent(sub, "page_approved")
	}
	ph.waitEvent(sub, "book_ready")
	if runRes := ph.waitJob(res.JobID); runRes.Status != job.StatusDone {
		t.Fatalf("job = %+v, want done", runRes)
	}

	// A run that landed everything is finished, and a book that was never
	// structured is not Complete's to fix — it has no story to re-render,
	// and naming it would only produce noise an operator cannot act on.
	finished, err := ph.h.IncompleteBooks(t.Context())
	if err != nil {
		t.Fatalf("IncompleteBooks: %v", err)
	}
	if slices.Contains(finished, bookID) {
		t.Errorf("IncompleteBooks = %v, want the finished book dropped", finished)
	}
	if slices.Contains(finished, neverStructured.ID) {
		t.Errorf("IncompleteBooks = %v, want the never-structured book left out", finished)
	}

	// Take one page's voice away: the book still has its film, its PDF and
	// seven of eight voices, and it is exactly the captioned-silent book a
	// film-only check misses.
	clip, err := ph.db.PageMedia(t.Context(), bookID, 3, store.MediaNarration)
	if err != nil {
		t.Fatalf("read page 3 narration: %v", err)
	}
	if err := ph.db.DeleteMedia(t.Context(), clip.ID); err != nil {
		t.Fatalf("delete page 3 narration: %v", err)
	}
	silent, err := ph.h.IncompleteBooks(t.Context())
	if err != nil {
		t.Fatalf("IncompleteBooks: %v", err)
	}
	if !slices.Contains(silent, bookID) {
		t.Errorf("IncompleteBooks = %v, want a book with a silent page named", silent)
	}
}
