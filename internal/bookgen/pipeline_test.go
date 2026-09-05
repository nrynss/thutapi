package bookgen

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
func TestPipeline_FailureIsTotalAndTerminal(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	ph.tts.err = errors.New("fixture: speech pool down")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()
	sub := ph.subscribe(bookID)

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}

	// Every page illustrated and approved before narration failed;
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
	ph.tts.err = nil
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

// TestPipeline_NarrationNonTransientFailureFailsRun pins that a fatal non-transient
// narration error still causes the run to fail and publish failed {}.
func TestPipeline_NarrationNonTransientFailureFailsRun(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("")
	ph.tts.err = errors.New("fatal non-transient error")
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
