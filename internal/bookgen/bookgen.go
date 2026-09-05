// Package bookgen runs the generation pipeline that turns a closed
// interview into a finished book (PLAN.md §T10c/§T10f): story.Structure
// over the transcript, illustrate.Illustrate with T7's judge-and-persist
// loop, audio.NarrateBook, the printable PDF render (PLAN.md §T10f),
// the bookvideo render, and the store write that makes the book ready.
// Until this package existed every stage was built and APPROVE'd and
// nothing called them (PLAN.md §Unowned seams,
// "Phase B orchestration").
//
// # Trigger and product surface
//
//	POST /interviews/{id}/generate   → 202 {job_id, book_id, topic,
//	                                    events_url, status:"running"}
//	GET  /interviews/{id}/generate/events → SSE on the book's topic
//
// The route lines live in cmd/thutapi's newServer (PLAN.md invariant
// 5); the handlers are here. Generation is triggered, never automatic:
// an automatic fire on the interview's "ended" event would race screen
// 4's voice step (PLAN.md §The flow, decision 20). A second POST while
// a book is generating is refused with 409 (class "busy") — a
// double-fire at ~$0.35 and six minutes a run is the cheapest possible
// expensive bug — and a run that has TERMINATED (success or failure)
// never blocks the next POST: a retry is a tap.
//
// The pipeline runs as one internal/job job, so the short POST returns
// at once (invariant 6) and the job's exactly-once terminal state is
// the run's. The job's own topic (job:<id>) carries the runner's
// terminal event; the PRODUCT surface — what screen 5 subscribes to —
// is the book's topic, not the interview's (which carries turns and
// ends at "ended") and not the job's:
//
//	event: page_approved         → {"n":3,"image_url":"/media/<id>"}
//	event: narration_unavailable → {}
//	event: book_ready            → {"pdf_url":"/media/<id>","video_url":"/media/<id>"}
//	event: failed                → {}
//
// page_approved fires the moment a page's illustration is approved and
// persisted (the count screen 5's race reads into --done — PLAN.md
// §T9a); book_ready fires once the run's finished artifacts are in
// place: the PDF always, and the film whenever narration clips exist (a
// transient narration outage leaves video_url out of the payload —
// see the stage list); failed fires on ANY error, including a panic,
// and means the whole run (T6/T7 return a zero Book on error — there
// is no partial book to serve). The stream is from-now-on: a late
// subscriber catches up from the store (approved pages are
// MediaIllustration rows; the finished film and PDF are the book's
// video/mp4 and application/pdf media rows), the way GET
// /interviews/{id} catches up on turns. The HTTP read that serves that
// state on the book's own route is T10b's surface — recorded as
// contract row C4 of the T10c round-1 record, not invented here.
//
// # The stages, in order (all inside the job)
//
//  1. Read the interview's transcript and its book row back through
//     the store (the book row carries the byline question zero wrote).
//     story.Structure turns the transcript into the validated Story;
//     the book row is then updated to the authored title with the
//     byline preserved, and every page and cast member is upserted as
//     a store row — T7's BookWriter and T8's NarrateBook both require
//     the book/page/cast rows to exist before their place calls fire
//     the anchor foreign keys (t6b-live-record.md item 3). Upsert, not
//     create: a re-run (a retry tap over the same book) finds the rows
//     from the failed attempt and replaces them.
//  2. illustrate.Illustrate with BOTH Config.Judge and Config.Persist
//     set — T7's closing loop: sheets persist on render, pages only
//     after the verdict approves them. Page approvals reach the broker
//     as page_approved events (see the bridge below).
//  3. audio.NarrateBook — one persisted clip per page, in page order.
//     A transient narration failure (gmi.ErrTransient, e.g. a 503
//     capacity outage) is an outage, not an error: narration and the
//     film are skipped, narration_unavailable {} is published once,
//     and the run continues to the PDF stage and succeeds PDF-only.
//     Any other narration failure is total — the run ends before the
//     PDF stage.
//  4. The printable PDF (PLAN.md §T10f): bookpdf renders the cover and
//     pages from the persisted illustrations and text; the PDF is
//     persisted, attached to the book, and supersedes any earlier PDF
//     on regeneration. The PDF stage always runs once reached: its
//     inputs (illustrations, text) never depend on narration, so the
//     outage path reaches it too.
//  5. The film: bookvideo renders title card + page segments + end
//     card + concat from the persisted illustrations and narration;
//     the MP4 is persisted and attached to the book (the row that
//     makes GET /book/{id} serve cold, and the video_url book_ready
//     names). Rendered only when narration clips exist.
//
// A failure anywhere is total: the run returns an error, failed {}
// fires, the job lands its terminal error state, and nothing retries
// automatically. Re-POSTing starts a fresh run that replaces the
// partial rows and blobs (BookWriter, NarrateBook and the film place
// all replace slot occupants).
//
// # The page_approved bridge
//
// illustrate.Config.Progress reports each page AFTER its closing loop
// has approved and persisted it (the loop is the page's last step
// before its Progress event — pinned by t7-round2.md L1). Progress
// carries no media id, so the bridge reads the just-placed row back
// (store.PageMedia) and publishes {"n":N,"image_url":"/media/<id>"}.
// A read-back failure after a successful persist is an internal
// inconsistency and fails the run — the race must never silently stall
// a page behind a store fault.
//
// # Money and fakes
//
// Nothing here calls GMI directly (invariant 1) and nothing spends
// money in tests: the stages are driven through the interfaces their
// packages declare (story.Chatter, illustrate.Imager/Judge,
// audio.TTS) plus the two seams this package declares for the film — a
// videoRenderer over bookvideo.Render and a filmStore over the blob
// store — so the whole ordering is pinned with fakes. The film's content
// type was once a recorded production gap (t10c-round1.md contract row
// C2); T10d closed it — video/mp4 is in mediastore's closed set on main —
// so a production run can land its film.
package bookgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"thutapi/internal/audio"
	"thutapi/internal/bookpdf"
	"thutapi/internal/bookvideo"
	"thutapi/internal/illustrate"
	"thutapi/internal/interview"
	"thutapi/internal/job"
	"thutapi/internal/mediastore"
	"thutapi/internal/store"
	"thutapi/internal/story"
	"thutapi/internal/stream"
)

// BookTopicPrefix namespaces the book SSE topics, the same convention
// interview.TopicPrefix and job.TopicPrefix use. A book's events live
// on Topic(bookID): every run over the same book (a retry) streams to
// the same topic, so a screen-5 client that stayed subscribed through
// a failure sees the next run's page_approved events arrive after
// failed {}.
const BookTopicPrefix = "book:"

// Topic is the broker topic a book's generation events publish on:
// BookTopicPrefix plus the book id.
func Topic(bookID string) string { return BookTopicPrefix + bookID }

// filmContentType is the MIME type the finished film is persisted as.
// mediastore's closed content-type set carries it (T10d closed the
// t10c-round1.md C2 gap), so a production run persists the film and
// book_ready names its URL; a persist failure still fails the run loudly,
// never a book_ready naming a URL that cannot be stored.
const filmContentType = "video/mp4"

// pdfContentType is the MIME type the finished printable PDF is persisted as.
const pdfContentType = "application/pdf"

// narrationUnavailableData is the narration_unavailable event's payload: {}
const narrationUnavailableData = "{}"

// Wire event payloads. These are the exact SSE data lines screen 5
// (and the T9a race's --done counter) consume; the JSON tags are the
// PLAN.md §T10c wire shape, pinned by the raw-wire tests.
type pageApprovedEvent struct {
	N        int    `json:"n"`
	ImageURL string `json:"image_url"`
}

type bookReadyEvent struct {
	PDFURL   string `json:"pdf_url"`
	VideoURL string `json:"video_url,omitempty"`
}

// failedData is the failed event's payload: {} — no code, no prose
// (PLAN.md §T10c; decision 17: the child gets a sentence, the operator
// gets the log).
const failedData = "{}"

// generateResponse is the body of a 202 answer to POST
// /interviews/{id}/generate. Topic is the book's topic and Events its
// SSE stream — the subscribe-then-watch pair screen 5 needs, in the
// same shape the interview start response carries.
type generateResponse struct {
	JobID  string `json:"job_id"`
	BookID string `json:"book_id"`
	Topic  string `json:"topic"`
	Events string `json:"events_url"`
	Status string `json:"status"`
}

// errorEvent is the body of every refused generate request: the error
// field is one machine class ("not_found", "not_ended", "busy",
// "capacity", "internal") — never prose; the full error chain goes to
// the log.
type errorEvent struct {
	Error string `json:"error"`
}

// Machine classes the generate route answers with (the interview
// package's vocabulary, extended by the states only this route has).
const (
	classNotFound = "not_found"
	classBusy     = "busy"
	classCapacity = "capacity"
	classInternal = "internal"
	// classNotEnded refuses generation for an interview whose closing
	// turn has not been persisted: generation belongs after screen 4,
	// which belongs after an ended interview.
	classNotEnded = "not_ended"
)

// Sentinel errors. Every refusal wraps one of these; branch with
// errors.Is (PLAN.md invariant 8).
var (
	// ErrNotConfigured is returned by New when a required dependency
	// is nil or MediaDir is empty. The message names the Config.
	ErrNotConfigured = errors.New("bookgen: handler not configured")
	// ErrBusy is returned when a generate POST arrives for a book
	// whose run has not terminated. HTTP 409, class "busy".
	ErrBusy = errors.New("bookgen: a generation is already running for this book")
	// ErrOpenInterview is returned when a generate POST arrives for an
	// interview whose transcript has no closing turn — the interview
	// is not ended, so there is nothing to structure. HTTP 409, class
	// "not_ended".
	ErrOpenInterview = errors.New("bookgen: interview has not ended")
)

// jobRunner is bookgen's view of the job Runner: it starts the
// pipeline off the request path and remembers each run's terminal
// state, which is what the double-fire refusal consults. Satisfied by
// *job.Runner.
type jobRunner interface {
	Start(ctx context.Context, fn job.Func) (string, error)
	Result(id string) (job.Result, error)
}

// broadcaster is bookgen's view of the SSE broker: publish generation
// events on the book's topic and serve it as a stream. Satisfied by
// *stream.Broker.
type broadcaster interface {
	Publish(topic string, ev stream.Event)
	ServeTopic(w http.ResponseWriter, r *http.Request, topic string)
}

// pdfRenderer is bookgen's view of the printable PDF renderer: story and
// illustrations in, PDF bytes out. Satisfied by *bookpdf.Renderer.
type pdfRenderer interface {
	Render(ctx context.Context, in bookpdf.Input) ([]byte, error)
}

// videoRenderer is bookgen's view of the film renderer (PLAN.md
// invariant 3): page images and narration in, one MP4 at
// in.OutputPath. Satisfied by *FFmpegRenderer, which adapts
// bookvideo.Render.
type videoRenderer interface {
	Render(ctx context.Context, in bookvideo.Input) error
}

// filmStore is bookgen's view of the media store for finished
// films: write one MP4 blob, remove older ones on retry.
type filmStore interface {
	Persist(ctx context.Context, src io.Reader, contentType string) (string, error)
	Delete(ctx context.Context, id string) error
}

// Config carries the pipeline's dependencies. Config flows down from
// cmd/thutapi's run(); nothing here reads the environment (PLAN.md
// invariant 2). Every field is required except Log; New refuses a nil
// dependency.
type Config struct {
	// DB is the book store. Concrete because T7's BookWriter and T8's
	// NarrateBook take *store.DB; bookgen also reads interviews, books
	// and media rows and writes pages, cast and titles through it.
	DB *store.DB
	// Blobs is the blob store pages and narration persist through
	// (concrete: T7/T8 require *mediastore.Store). The film's own
	// persist goes through Film.
	Blobs *mediastore.Store
	// MediaDir is the directory Blobs was opened on — where blob files
	// live, one file named by its media id (the layout mediastore
	// documents). The film stage reads the persisted illustrations and
	// narration from here.
	MediaDir string
	// Chat is the M3 text client story.Structure calls. Satisfied by
	// *text.Client; tests substitute a scripted fake.
	Chat story.Chatter
	// Judge is the M3 text client T7's consistency verdicts call.
	// Satisfied by *text.Client — one client satisfies both
	// story.Chatter and illustrate.Judge, so production passes a
	// single instance (the judge-adapter decision, t10c-round1.md D3).
	// Kept separate from Chat so tests can script the two roles
	// independently.
	Judge illustrate.Judge
	// Imager is the GMI image client illustrate renders with.
	// Satisfied by *gmi/media.Client (invariant 3).
	Imager illustrate.Imager
	// TTS is the GMI speech client audio.NarrateBook speaks with.
	// Satisfied by *gmi/media.Client.
	TTS audio.TTS
	// Broker carries the book-topic events and serves the SSE route.
	Broker broadcaster
	// Jobs runs the pipeline off the request path.
	Jobs jobRunner
	// PDF renders the printable PDF book. Production wires a
	// *bookpdf.Renderer; tests substitute a fake.
	PDF pdfRenderer
	// Video renders the finished film. Production wires an
	// *FFmpegRenderer; tests substitute a fake, so no test needs
	// ffmpeg.
	Video videoRenderer
	// VideoCfg is the ffmpeg configuration passed to every render —
	// paths, durations, the Fredoka font once T9 vendors it.
	VideoCfg bookvideo.Config
	// Film persists the finished MP4 and removes superseded films.
	// Production wires the same *mediastore.Store as Blobs (see the
	// filmStore doc for the recorded content-type gap).
	Film filmStore
	// Log receives run failures and cleanup warnings. Nil discards.
	Log *slog.Logger
}

// Handler serves the generate route and its events stream and owns the
// book-gen jobs. Build it with New; the zero value is not usable.
type Handler struct {
	cfg Config
	log *slog.Logger

	// mu guards runs: bookID → the id of its current-or-last job. The
	// entry is never deleted — job.Result says whether that job is
	// still running, which is the exactly-once read the double-fire
	// refusal runs on. A terminal job's entry simply lets the next
	// POST start a fresh run and overwrite it.
	mu   sync.Mutex
	runs map[string]string
}

// New returns a Handler with the given dependencies. It returns an
// error wrapping ErrNotConfigured when a required dependency is nil
// (or MediaDir is empty). A nil here is a wiring bug in run(): the
// Handler cannot serve a generate request without any of them.
func New(cfg Config) (*Handler, error) {
	if cfg.DB == nil || cfg.Blobs == nil || cfg.MediaDir == "" ||
		cfg.Chat == nil || cfg.Judge == nil || cfg.Imager == nil ||
		cfg.TTS == nil || cfg.Broker == nil || cfg.Jobs == nil ||
		cfg.PDF == nil || cfg.Video == nil || cfg.Film == nil {
		return nil, ErrNotConfigured
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Handler{
		cfg:  cfg,
		log:  cfg.Log,
		runs: make(map[string]string),
	}, nil
}

// Generate handles POST /interviews/{id}/generate: it validates the
// interview (exists, ended, linked to a book), refuses a second run
// while one is generating, starts the pipeline as an internal/job job,
// and returns the job id plus the book topic and events URL at once.
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobID, bookID, err := h.startRun(r.Context(), id)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, generateResponse{
		JobID:  jobID,
		BookID: bookID,
		Topic:  Topic(bookID),
		Events: eventsPath(id),
		Status: "running",
	})
}

// Events handles GET /interviews/{id}/generate/events: the book's SSE
// stream for the interview's book, served by internal/stream (headers,
// flushing and heartbeats are its contract). Unknown interviews, and
// interviews with no book row, are 404s before the stream opens.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iv, err := h.cfg.DB.Interview(r.Context(), id)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if iv.BookID == "" {
		h.writeError(w, fmt.Errorf("bookgen: events %s: %w", id, store.ErrNotFound))
		return
	}
	h.cfg.Broker.ServeTopic(w, r, Topic(iv.BookID))
}

// startRun validates and starts one run. It holds h.mu across the
// check-and-start, so two concurrent POSTs cannot both pass the
// double-fire refusal.
func (h *Handler) startRun(ctx context.Context, id string) (jobID, bookID string, err error) {
	iv, err := h.cfg.DB.Interview(ctx, id)
	if err != nil {
		return "", "", fmt.Errorf("bookgen: generate %s: %w", id, err)
	}
	if iv.BookID == "" {
		return "", "", fmt.Errorf("bookgen: generate %s: interview has no book row", id)
	}
	if !ended(iv) {
		return "", "", fmt.Errorf("bookgen: generate %s: %w", id, ErrOpenInterview)
	}
	bookID = iv.BookID

	h.mu.Lock()
	defer h.mu.Unlock()
	if prev, ok := h.runs[bookID]; ok {
		res, rerr := h.cfg.Jobs.Result(prev)
		if rerr == nil && res.Status == job.StatusRunning {
			return "", "", fmt.Errorf("bookgen: generate %s: %w", id, ErrBusy)
		}
	}
	jobID, err = h.cfg.Jobs.Start(ctx, h.generateJob(bookID, id))
	if err != nil {
		return "", "", fmt.Errorf("bookgen: generate %s: %w", id, err)
	}
	h.runs[bookID] = jobID
	return jobID, bookID, nil
}

// ended reports whether the interview's persisted transcript carries a
// closing turn as its last turn — the restart-stable end marker every
// end path persists (internal/interview package doc, "Restart
// semantics"). The role vocabulary is the interview package's.
func ended(iv store.Interview) bool {
	return len(iv.Turns) > 0 && iv.Turns[len(iv.Turns)-1].Role == interview.RoleClosing
}

// eventsPath is the SSE stream URL for one interview's book, served by
// Events. A screen-5 reload derives it from the interview id alone —
// the same id its own URL carries — so catching up never needs a prior
// generate response.
func eventsPath(id string) string {
	return "/interviews/" + id + "/generate/events"
}

// generateJob wraps one pipeline run as a job.Func. It is the only
// place the book topic's terminal event (book_ready or failed) is
// published: exactly once per run, on every path — a plain failure, a
// cancelled context, and a panic (which is re-raised for job.call's
// panic boundary to convert into the ErrPanic terminal).
func (h *Handler) generateJob(bookID, ivID string) job.Func {
	return func(ctx context.Context, _ func(string)) (data []byte, err error) {
		topic := Topic(bookID)
		var once sync.Once
		publish := func(name, payload string) {
			once.Do(func() {
				h.cfg.Broker.Publish(topic, stream.Event{Name: name, Data: payload})
			})
		}
		defer func() {
			if p := recover(); p != nil {
				publish("failed", failedData)
				panic(p) // job.call recovers and lands the ErrPanic terminal
			}
		}()
		defer func() {
			if err != nil {
				publish("failed", failedData)
			}
		}()
		pdfID, videoID, err := h.runBook(ctx, bookID, ivID)
		if err != nil {
			return nil, err
		}
		ready := bookReadyEvent{
			PDFURL: "/media/" + pdfID,
		}
		if videoID != "" {
			ready.VideoURL = "/media/" + videoID
		}
		b, merr := json.Marshal(ready)
		if merr != nil {
			// A fixed struct of strings cannot fail to marshal; keep
			// the compiler honest without a silent empty payload.
			return nil, fmt.Errorf("bookgen: marshal book_ready: %w", merr)
		}
		publish("book_ready", string(b))
		return nil, nil
	}
}

// writeError maps an error to its HTTP status by machine class and
// writes the class as the body's "error" field. The full prose goes to
// the log only (PLAN.md §T9: no failure text on the child's screen).
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	class, status := classify(err)
	h.log.Error("bookgen: request failed", "class", class, "err", err)
	writeJSON(w, status, errorEvent{Error: class})
}

// classify maps an error to its machine class and HTTP status: a
// not-found interview is 404; an open interview or a busy book is 409;
// a saturated runner is 503; everything else is 500.
func classify(err error) (class string, status int) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return classNotFound, http.StatusNotFound
	case errors.Is(err, ErrOpenInterview):
		return classNotEnded, http.StatusConflict
	case errors.Is(err, ErrBusy):
		return classBusy, http.StatusConflict
	case errors.Is(err, job.ErrLimit):
		return classCapacity, http.StatusServiceUnavailable
	default:
		return classInternal, http.StatusInternalServerError
	}
}

// writeJSON writes one JSON body. An encode failure after the headers
// are gone is a dropped connection, not an application error.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
