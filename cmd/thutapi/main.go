// Command thutapi runs the Thutapi HTTP server.
//
// Thutapi interviews a child via short typed questions, then authors an
// illustrated picture book read aloud. See dev-diary/project.md for the
// product spec and dev-diary/PLAN.md for the build plan.
//
// This file is the process entry only. T0 owns it. It must remain free of
// business logic so every later track can build on top without rewriting the
// bootstrap.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"thutapi/internal/audio"
	"thutapi/internal/bookgen"
	"thutapi/internal/bookpdf"
	"thutapi/internal/bookvideo"
	"thutapi/internal/gate"
	"thutapi/internal/gmi/media"
	"thutapi/internal/gmi/text"
	"thutapi/internal/interview"
	"thutapi/internal/job"
	"thutapi/internal/mediastore"
	"thutapi/internal/prewarm"
	"thutapi/internal/store"
	"thutapi/internal/stream"
	"thutapi/internal/web"
)

// version is stamped at link time via -X main.version=<v>. Default "dev"
// keeps a clean `go build .` working when no -X flag is supplied. The
// build to ops dashboards and the README's curl smoke is meaningful.
var version = "dev"

// errShutdownTimeout is returned by parseFlags when the caller asks for a
// non-positive graceful-shutdown deadline (H3). It is typed so callers and
// tests can errors.Is against it.
var errShutdownTimeout = errors.New("shutdown-timeout must be greater than 0")

// errUnexpectedOperand is returned by parseFlags when the caller passes a
// positional argument after the flag set is parsed (M2). flag.NewFlagSet
// stops parsing at the first non-flag token, which silently drops every
// later flag.
var errUnexpectedOperand = errors.New("unexpected positional argument")

// config holds the runtime configuration. T0 reads ADDR / PORT, the
// shutdown-timeout flag, and the IdleTimeout knob; T3 adds the data
// dir (DATA_DIR or -data-dir) the SQLite file and the media blobs
// live under, and T13 adds PUBLIC_ORIGIN for public voice-sample URLs.
// Later tracks extend this struct and the parseFlags surface, they do not
// replace it.
//
// ReadTimeout and WriteTimeout are deliberately not part of config — they
// are derived in newHTTPServer from the graceful-shutdown deadline so a
// slow request body cannot outlive the deadline (H1, re-opened in
// round 2). WriteTimeout is fixed at 0 because project.md §Pipeline
// folds SSE into the architecture (interview turns stream text, TTS
// audio lands asynchronously); a non-zero WriteTimeout would force-close
// every long-lived stream.
type config struct {
	addr         string        // bind address, e.g. "0.0.0.0:8080"
	timeout      time.Duration // graceful shutdown deadline
	idleTimeout  time.Duration // http.Server.IdleTimeout
	dataDir      string        // SQLite file + media blobs live here (T3)
	publicOrigin string        // public HTTPS origin used for GMI source_audio (T13)
	uploadToken  string        // bearer for the public T13 voice-sample route
	gatePasscode string        // optional shared passcode for T11's gated routes
	mediaMax     int64         // T11 retention: byte budget for generated media, 0 = unbounded
	prewarmDir   string        // T11 prewarm: fixture directory, "" = <data dir>/prewarm
	exportBook   string        // T11 prewarm: export this book as a fixture and exit
	completeBook string        // finish this book's narration, PDF and film, then exit ("all" for every filmless book)
	completeMute bool          // complete without a music bed
	pruneEmpty   bool          // delete book rows abandoned interviews left behind, then exit
}

type gmiVoiceCloner struct {
	client *media.Client
}

func (c gmiVoiceCloner) CloneVoice(ctx context.Context, request audio.VoiceCloneRequest) (audio.VoiceCloneResult, error) {
	raw, err := c.client.CloneVoice(ctx, request.SourceAudio, request.Text, request.VoiceID)
	if err != nil {
		return audio.VoiceCloneResult{}, err
	}
	return audio.DecodeVoiceCloneResponse(raw)
}

// completeThrottle is the retry budget for this process: the audio package
// default when this process is serving, and a deliberately patient one when
// it was started to repair books. The patient budget spends up to about
// seven minutes on one page before giving it up as silent.
func completeThrottle(cfg config) audio.ThrottleConfig {
	if cfg.completeBook == "" {
		return audio.ThrottleConfig{}
	}
	return audio.ThrottleConfig{Attempts: 8, Backoff: 30 * time.Second}
}

// completeBooks runs bookgen.Complete over one book id, or over every book
// with no film when the id is "all", and reports what each one ended with.
// One book's failure does not stop the rest: the point of the command is to
// repair a set, and a book that cannot be repaired today is still named in
// the error at the end.
func completeBooks(ctx context.Context, log *slog.Logger, generate *bookgen.Handler, which string, music bool) error {
	ids := []string{which}
	if which == "all" {
		found, err := generate.IncompleteBooks(ctx)
		if err != nil {
			return err
		}
		if len(found) == 0 {
			log.Info("no books need completing; every book has a film")
			return nil
		}
		ids = found
	}
	log.Info("completing books", "books", ids, "music", music)
	var failures []error
	for _, id := range ids {
		pdfID, videoID, err := generate.Complete(ctx, id, music, "")
		if err != nil {
			log.Error("could not complete book", "book", id, "err", err)
			failures = append(failures, fmt.Errorf("book %s: %w", id, err))
			continue
		}
		log.Info("book completed", "book", id, "pdf", "/media/"+pdfID, "video", "/media/"+videoID)
	}
	return errors.Join(failures...)
}

// resolveAddr is the ADDR/PORT resolution: ADDR wins if set, else
// "0.0.0.0:$PORT" if PORT is set, else "0.0.0.0:8080".
func resolveAddr() string {
	if v := os.Getenv("ADDR"); v != "" {
		return v
	}
	if p := os.Getenv("PORT"); p != "" {
		return "0.0.0.0:" + p
	}
	return "0.0.0.0:8080"
}

// resolveDataDir is the DATA_DIR resolution, mirroring resolveAddr:
// DATA_DIR wins if set, else "data" — the gitignored directory at the
// working-directory root (T3).
func resolveDataDir() string {
	if v := os.Getenv("DATA_DIR"); v != "" {
		return v
	}
	return "data"
}

// resolveMediaMaxBytes is the MEDIA_MAX_BYTES resolution for T11's
// retention sweep: an explicit byte budget wins, an unset or unparseable
// value falls back to the package default, and an explicit 0 means
// "no byte budget, sweep orphans only". The box has 23 GiB free and no
// swap (PLAN.md §T11 item 4), so the default is deliberately far below it.
func resolveMediaMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("MEDIA_MAX_BYTES"))
	if raw == "" {
		return mediastore.DefaultMaxBytes
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return mediastore.DefaultMaxBytes
	}
	if n == 0 {
		return mediastore.Unbounded
	}
	return n
}

// parseConfig reads environment only — no flags. It is the seam tests use so
// they don't re-register flag entries across invocations.
func parseConfig() config {
	return config{
		addr:         resolveAddr(),
		timeout:      10 * time.Second,
		idleTimeout:  120 * time.Second,
		dataDir:      resolveDataDir(),
		publicOrigin: os.Getenv("PUBLIC_ORIGIN"),
		uploadToken:  os.Getenv("UPLOAD_TOKEN"),
		gatePasscode: os.Getenv("GATE_PASSCODE"),
		mediaMax:     resolveMediaMaxBytes(),
		prewarmDir:   os.Getenv("PREWARM_DIR"),
	}
}

// parseFlags merges CLI flags onto parseConfig. It uses a private FlagSet so
// the test seam (parseConfig) is independent of os.Args.
func parseFlags(args []string) (config, error) {
	cfg := parseConfig()
	fs := flag.NewFlagSet("thutapi", flag.ContinueOnError)
	fs.DurationVar(&cfg.timeout, "shutdown-timeout", cfg.timeout, "graceful shutdown deadline")
	fs.StringVar(&cfg.dataDir, "data-dir", cfg.dataDir, "directory for the SQLite database and media blobs")
	fs.StringVar(&cfg.prewarmDir, "prewarm-dir", cfg.prewarmDir, "directory of prewarm fixtures (default <data-dir>/prewarm)")
	fs.StringVar(&cfg.exportBook, "prewarm-export", "", "export this book id as a prewarm fixture and exit")
	fs.StringVar(&cfg.completeBook, "complete", "", `finish this book id's narration, PDF and film from its persisted pages, then exit ("all" completes every book with no film)`)
	fs.BoolVar(&cfg.completeMute, "complete-no-music", false, "complete without a background music bed")
	fs.BoolVar(&cfg.pruneEmpty, "prune-abandoned", false, "delete the empty book rows abandoned interviews left behind (no pages, no media), then exit")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	// H3: reject non-positive shutdown deadlines. flag.DurationVar happily
	// parses "0s" and negative durations, which would make Shutdown's
	// context fire immediately and turn every SIGTERM into exit 1.
	if cfg.timeout <= 0 {
		return cfg, errShutdownTimeout
	}
	// M2: flag stops parsing at the first non-flag token, so anything
	// after the first positional operand is silently dropped. Reject it
	// explicitly so a caller cannot think `-shutdown-timeout=1s` after a
	// positional has taken effect.
	if fs.NArg() != 0 {
		return cfg, errUnexpectedOperand
	}
	return cfg, nil
}

// server is the application's HTTP root. It owns the routes and the
// dependencies they need: GET /healthz (T0), the media handler (T3),
// the interview handler (T4) and the generation handler (T10c).
type server struct {
	mux          *http.ServeMux
	log          *slog.Logger
	start        time.Time
	media        *mediastore.Store
	voiceSamples *audio.VoiceSampleHandler
	interviews   *interview.Handler
	generate     *bookgen.Handler
	shelf        *web.ShelfHandler
	book         *web.BookHandler
	download     *web.DownloadHandler
}

// gateRules are T11's per-route budgets. They are package-level so the
// route wrap and any test read exactly one set of numbers.
//
// The arithmetic is spend, not traffic. A full generation is ~$0.35 and
// six minutes (PLAN.md §T11 item 1), so the generate rule's per-client
// burst of 3 is about $1.05 for one caller from cold, and its global
// bucket holds the whole route to ~$2.10 at once and ~$2.10 an hour
// after that. The failure screen's *try again* re-POSTs this same route
// and legitimately spends a token — that is the intent recorded in
// PLAN.md §T11 item 1, and it is why the burst is 3 rather than 1.
//
// POST /interviews and the per-turn answer route each cost one M3 call
// plus a TTS synthesis, so they are not free either; their limits are
// set to sit well above one honest interview (6–10 questions,
// internal/interview/prompt.go) and well below a script.
//
// POST /voice-sample already carries its own UPLOAD_TOKEN bearer auth
// (internal/audio/clone.go). This is the rate limit that route's
// TODO(§T11) asks for and not a replacement for that auth: one token
// holder could otherwise drive unbounded 5 MiB transcodes.
var (
	generateRule = gate.Rule{
		Name:      "generate",
		PerClient: gate.Limit{Burst: 3, Every: 20 * time.Minute},
		Global:    gate.Limit{Burst: 6, Every: 10 * time.Minute},
	}
	interviewStartRule = gate.Rule{
		Name:      "interview-start",
		PerClient: gate.Limit{Burst: 5, Every: 2 * time.Minute},
		Global:    gate.Limit{Burst: 30, Every: 10 * time.Second},
	}
	interviewAnswerRule = gate.Rule{
		Name:      "interview-answer",
		PerClient: gate.Limit{Burst: 20, Every: 15 * time.Second},
		Global:    gate.Limit{Burst: 120, Every: 2 * time.Second},
	}
	voiceSampleRule = gate.Rule{
		Name:      "voice-sample",
		PerClient: gate.Limit{Burst: 4, Every: 5 * time.Minute},
		Global:    gate.Limit{Burst: 12, Every: time.Minute},
	}
)

// newServer wires the routes. media, voiceSamples, interviews, generate
// and guard must be non-nil: they are live handlers, not optional
// dependencies. It returns an error rather than an unenforced mux when a
// gate rule cannot be honoured — an ungated generate route is the open
// wallet §T11 exists to close, so it must never be the fallback.
func newServer(log *slog.Logger, media *mediastore.Store, voiceSamples *audio.VoiceSampleHandler, interviews *interview.Handler, generate *bookgen.Handler, db *store.DB, guard *gate.Gate) (*server, error) {
	if guard == nil {
		return nil, errors.New("newServer: gate must not be nil")
	}
	s := &server{
		mux:          http.NewServeMux(),
		log:          log,
		start:        time.Now(),
		media:        media,
		voiceSamples: voiceSamples,
		interviews:   interviews,
		generate:     generate,
		shelf:        web.NewShelfHandler(db),
		book:         web.NewBookHandler(db, generate),
		download:     web.NewDownloadHandler(db, media, generate),
	}
	// /healthz is the one route T0 ships. Liveness only — no dependency
	// checks, no probes. That distinction belongs to a later track.
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// T9's human page and static-asset routes. API routes remain plural.
	s.mux.HandleFunc("GET /{$}", s.shelf.Shelf)
	s.mux.HandleFunc("GET /interview/{id}", web.Interview)
	s.mux.HandleFunc("GET /book/{id}", s.book.Book)
	s.mux.HandleFunc("GET /book/{id}/state", s.book.State)
	s.mux.HandleFunc("GET /book/{id}/download/{kind}", s.download.Download)
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", staticAssets()))
	// T3's one sanctioned route line (PLAN.md invariant 5): media
	// blobs serve through the mediastore handler, which answers Range
	// requests so narration can be scrubbed (PLAN.md §T3).
	s.mux.HandleFunc("GET /media/{id}", s.handleMedia)
	// T4's sanctioned route lines (PLAN.md invariant 5): the interview
	// loop — start, catch-up transcript, SSE events, answers. The loop
	// itself lives in internal/interview.
	s.mux.HandleFunc("GET /interviews/{id}", s.interviews.Transcript)
	s.mux.HandleFunc("GET /interviews/{id}/events", s.interviews.Events)
	// T10c's sanctioned route line (PLAN.md invariant 5): stream a
	// book's generation events on the book's topic. The pipeline lives
	// in internal/bookgen.
	s.mux.HandleFunc("GET /interviews/{id}/generate/events", s.generate.Events)

	// T11's gate wraps the routes that spend money, and only those.
	// Everything registered above stays ungated by construction: the
	// shelf, the shareable book page and its state read, the download
	// links, /static, /healthz for the box's monitoring, and
	// GET /media/{id} — a gated media route would break the <video>
	// element and every shared link (PLAN.md §T11 item 1).
	//
	// GET /interviews/{id}/events and the generation event stream are
	// reads of work already paid for, so they are ungated too; gating
	// them would refuse a reconnecting client its own running book.
	gated := []struct {
		pattern string
		rule    gate.Rule
		handler http.Handler
	}{
		// T13's two adult capture modes converge here. The handler bounds
		// upload bytes, invokes the shipping static ffmpeg without a
		// shell, then persists only audio/mpeg for the existing
		// unguessable /media/{id} route.
		{"POST /voice-sample", voiceSampleRule, s.voiceSamples},
		{"POST /interviews", interviewStartRule, http.HandlerFunc(s.interviews.Start)},
		{"POST /interviews/{id}/answers", interviewAnswerRule, http.HandlerFunc(s.interviews.Answer)},
		{"POST /interviews/{id}/generate", generateRule, http.HandlerFunc(s.generate.Generate)},
	}
	for _, route := range gated {
		handler, err := guard.Protect(route.rule, route.handler)
		if err != nil {
			return nil, fmt.Errorf("newServer: %s: %w", route.pattern, err)
		}
		s.mux.Handle(route.pattern, handler)
	}
	return s, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if s.voiceSamples.ServeMedia(w, r, s.media) {
		return
	}
	s.media.ServeHTTP(w, r)
}

// staticAssets serves the static/ tree but refuses the developer-only files
// that live inside it: Go sources (static/race/race_test.go) and the
// browser-test harness are not shippable assets, so publishing them over
// HTTP would leak source. They 404 like any other missing path; every real
// asset (app.css, app.js, book/**, race/*.html, vendor/**) still serves.
//
// Static assets set Cache-Control: no-cache, must-revalidate so intermediary
// caches (such as Cloudflare) and browsers do not serve stale scripts or
// stylesheets across deployments.
//
// noDirFS additionally suppresses http.FileServer's generated directory
// listings, which would otherwise enumerate the whole asset tree — including
// the names of the very files the basename rule refuses to serve.
//
// Directory indexes are off entirely, so "index.html" is refused here too:
// http.FileServer would otherwise 301 any */index.html to its directory,
// which now 404s anyway. No asset in static/ is named index.html.
func staticAssets() http.Handler {
	files := http.FileServer(noDirFS{http.Dir("static")})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.ToLower(path.Base(r.URL.Path))
		if strings.HasSuffix(name, ".go") || strings.HasPrefix(name, "browser-test.") || name == "index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		files.ServeHTTP(w, r)
	})
}

// noDirFS is an http.FileSystem that opens files only. Refusing to open a
// directory makes http.FileServer answer 404 instead of rendering an
// auto-generated index of it.
type noDirFS struct{ fsys http.FileSystem }

func (n noDirFS) Open(name string) (http.File, error) {
	file, err := n.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if stat.IsDir() {
		file.Close()
		return nil, fs.ErrNotExist
	}
	return file, nil
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","uptime_seconds":%d,"version":%q}`, int(time.Since(s.start).Seconds()), version)
}

// newHTTPServer builds the http.Server with the timeouts that protect
// graceful shutdown (H1).
//
// The slow-body failure mode that H1 fixes: if a request is accepted just
// before SIGTERM and its body stays open past the shutdown deadline, the
// graceful Shutdown call returns context.DeadlineExceeded and the process
// exits 1 — which the Hetzner Traefik orchestrator treats as unhealthy and
// restart-loops on SIGTERM during deploy.
//
// ReadTimeout therefore must expire strictly before the graceful-shutdown
// deadline (cfg.timeout), so the slow body is force-closed in time. We set
// ReadTimeout = cfg.timeout - headroom, where headroom is the smaller of
// 2s and cfg.timeout/2. Two properties matter:
//
//   - Strict ordering. ReadTimeout < cfg.timeout always, so Shutdown has
//     a deterministic window to observe the drained connection before its
//     own deadline fires. One second alone left a 30% flake rate under our
//     local scheduler; production still benefits from the wider margin.
//   - Positive by construction. ReadTimeout > 0 for every legal
//     cfg.timeout (including 1s and 2s), because headroom is bounded by
//     cfg.timeout/2. Go's http.Server treats ReadTimeout: 0 as "no
//     timeout", so a naive cfg.timeout - 2s that clamped to 0 would
//     silently re-open H1 for legal flag values like -shutdown-timeout=1s.
//     The min() floor is what prevents that re-introduction.
//
// ReadHeaderTimeout (5s) is independent and protects the header parse
// separately.
//
// WriteTimeout is 0 by design. project.md §Pipeline folds SSE into the
// architecture for the interview turns and TTS streams (text streams while
// audio lands); a non-zero WriteTimeout would force-close every long-lived
// SSE stream and is the wrong default for this product. The slow-body case
// H1 is actually about is fully bounded by ReadTimeout, so leaving
// WriteTimeout at 0 does not re-open H1.
//
// IdleTimeout > cfg.timeout is fine — idle connections are not blocking
// Shutdown and they need to live longer than the shutdown budget to
// remain reusable across the deadline.
func newHTTPServer(cfg config, h http.Handler) *http.Server {
	headroom := 2 * time.Second
	if cfg.timeout/2 < headroom {
		headroom = cfg.timeout / 2
	}
	readTimeout := cfg.timeout - headroom

	return &http.Server{
		Addr:              cfg.addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       readTimeout,
		WriteTimeout:      0,
		IdleTimeout:       cfg.idleTimeout,
	}
}

// run is the testable body of main(). It wires the parsed config to an
// http.Server, blocks until either the server errors out or a shutdown
// signal arrives on sigs, then performs graceful Shutdown. Returning nil
// means a clean shutdown; any non-nil error is logged and turned into exit
// 1 by main(). The sigs channel is injected so L1 can pin the signal-name
// log line without spawning the real process.
func run(log *slog.Logger, args []string, sigs <-chan os.Signal) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

	// T3: the book store and the media blobs both live under the data
	// dir, so a container restart — same volume, fresh process —
	// reopens everything where it was left (PLAN.md §T3 Done when).
	// mediaDir is handed to the generation handler too: blob files are
	// named by their media id inside it, which is how the film stage
	// reads the persisted pages and narration back.
	db, err := store.Open(context.Background(), store.Config{
		Path: filepath.Join(cfg.dataDir, "thutapi.db"),
	})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer db.Close() // run returns only at shutdown; nothing outlives it
	mediaDir := filepath.Join(cfg.dataDir, "media")
	blobs, err := mediastore.Open(context.Background(), mediastore.Config{
		Dir: mediaDir,
		DB:  db,
	})
	if err != nil {
		return fmt.Errorf("open media store: %w", err)
	}
	// T11 prewarm. The fixture directory holds finished books as rows
	// plus blobs, so a redeploy or a lost SQLite file does not cost
	// ~$0.35 and six minutes per landing book to rebuild (PLAN.md §T11
	// item 2). -prewarm-export writes one and exits; a normal boot
	// restores anything the database does not already have.
	prewarmDir := cfg.prewarmDir
	if prewarmDir == "" {
		prewarmDir = filepath.Join(cfg.dataDir, "prewarm")
	}
	if cfg.exportBook != "" {
		fixture, err := prewarm.Export(context.Background(), db, mediaDir, cfg.exportBook, prewarmDir)
		if err != nil {
			return fmt.Errorf("export prewarm fixture: %w", err)
		}
		log.Info("prewarm fixture written", "book", fixture.ID, "title", fixture.Title, "blobs", len(fixture.Media), "dir", filepath.Join(prewarmDir, fixture.ID))
		return nil
	}
	restored, err := prewarm.Import(context.Background(), db, blobs, prewarmDir)
	if err != nil {
		return fmt.Errorf("import prewarm fixtures: %w", err)
	}
	if len(restored) > 0 {
		log.Info("prewarmed books restored", "books", restored)
	}
	// Every book the fixture directory names is protected from the
	// retention budget, whether this boot restored it or found it
	// already there.
	protected, err := prewarm.Books(prewarmDir)
	if err != nil {
		return fmt.Errorf("list prewarm fixtures: %w", err)
	}

	// The two GMI clients are process-wide: one text client serves the
	// interview turns, Phase-B structuring and T7's consistency judge
	// (a *text.Client satisfies every seam); one request-queue client
	// serves the image renders and the speech synthesis (invariant 1:
	// nothing outside internal/gmi talks to GMI).
	textCli := text.New()
	// Narration polls for up to 10 minutes, matching the budget §T10e's live
	// harness verified the joined pipeline under. The package default is 120s,
	// and observed queue latency for one TTS call exceeds that (2026-09-06:
	// 33s on one call, >120s on another), which would strand narration and
	// emit narration_unavailable on a book the provider was still working on.
	mediaCli := media.NewWithPoll(media.PollConfig{Timeout: 10 * time.Minute})
	voiceSamples, err := audio.NewVoiceSampleHandler(audio.VoiceSampleConfig{
		Store:        blobs,
		PublicOrigin: cfg.publicOrigin,
		UploadToken:  cfg.uploadToken,
		Cloner:       gmiVoiceCloner{client: mediaCli},
		ExpiryFile:   filepath.Join(cfg.dataDir, "voice-sample-expiry.json"),
	})
	if err != nil {
		return fmt.Errorf("build voice sample handler: %w", err)
	}
	defer voiceSamples.Close()

	// T11's retention sweep. Unplaced rows — every spoken interview
	// question is one — are unreachable by DeleteBook and immortal
	// without this (PLAN.md §T11 item 4), and the blob directory is
	// otherwise uncapped on a box with 23 GiB free and no swap.
	//
	// Retain is the veto that keeps this out of T13's way: a live voice
	// sample is an unplaced row like any other, and only its own handler
	// may decide when its 15 minutes are up.
	sweeper, err := blobs.NewSweeper(mediastore.RetentionConfig{
		MaxBytes:  cfg.mediaMax,
		Protected: protected,
		Retain:    voiceSamples.Tracked,
	})
	if err != nil {
		return fmt.Errorf("build retention sweeper: %w", err)
	}
	// A -complete run shares /data with the container that is still serving,
	// and that container is already sweeping. A second sweeper against the
	// same media directory would be two processes deciding independently what
	// is an orphan, so this one only watches when it is the one serving.
	if cfg.completeBook == "" && !cfg.pruneEmpty {
		sweeper.Start()
		defer sweeper.Close() // run returns only at shutdown; nothing outlives it
	}

	// T4/T10c: the interview loop and the generation pipeline stream
	// over SSE and run off the request path (PLAN.md invariant 6 —
	// nothing blocks on M3), so main owns the broker and the job
	// runner and hands them to both handlers. One runner bounds the
	// whole process's long work.
	broker := stream.New(stream.Config{})
	runner := job.New(broker)
	interviews, err := interview.New(interview.Config{
		Chat: textCli,
		Speaker: interview.QuestionSpeakerFunc(func(ctx context.Context, text string) (string, error) {
			_, id, err := audio.SynthesizeQuestion(ctx, audio.Config{TTS: mediaCli, DB: db, Blobs: blobs}, text)
			return id, err
		}),
		Store:  db,
		Broker: broker,
		Jobs:   runner,
		Log:    log,
	})
	if err != nil {
		return fmt.Errorf("build interview handler: %w", err)
	}
	generate, err := bookgen.New(bookgen.Config{
		DB:       db,
		Blobs:    blobs,
		MediaDir: mediaDir,
		Chat:     textCli,
		Judge:    textCli,
		Imager:   mediaCli,
		TTS:      mediaCli,
		Music:    mediaCli,
		Broker:   broker,
		Jobs:     runner,
		PDF:      bookpdf.NewRenderer(),
		Video:    bookgen.NewFFmpegRenderer(bookvideo.Config{}),
		Film:     blobs,
		Log:      log,
		// A repair waits far longer on a throttled or capacity-exhausted
		// provider than a live run does, because nobody is watching it. A
		// child on the wait screen is: the live default keeps that wait
		// bounded and degrades to a captioned-silent page rather than
		// stretching six minutes into fifteen. On 2026-09-06 the speech
		// model answered "Upstream capacity temporarily exhausted" for
		// every page for twenty minutes straight, which the live budget
		// cannot ride out and an unattended repair can.
		Throttle: completeThrottle(cfg),
	})
	if err != nil {
		return fmt.Errorf("build generation handler: %w", err)
	}

	// -complete finishes a book whose art landed but whose sound did not:
	// it re-runs narration, the PDF and the film from the pages already in
	// the store and exits, without serving. It never calls M3 or the image
	// model, so it cannot give the child a different book than the one they
	// already have. See internal/bookgen/complete.go.
	// -prune-abandoned removes the book rows an abandoned interview leaves
	// behind. A row is only touched when it has no pages AND no media, so a
	// story somebody told is never in reach of it — that is -complete's to
	// repair.
	if cfg.pruneEmpty {
		removed, err := generate.PruneAbandoned(context.Background())
		if err != nil {
			return fmt.Errorf("prune abandoned books: %w", err)
		}
		log.Info("abandoned book rows removed", "count", len(removed), "books", removed)
		return nil
	}

	if cfg.completeBook != "" {
		return completeBooks(context.Background(), log, generate, cfg.completeBook, !cfg.completeMute)
	}

	// T11's gate. Rate limits are always on; the passcode is enforced
	// only when GATE_PASSCODE is set (internal/gate's package doc records
	// why that is the default posture and settles PLAN.md decision 9).
	guard, err := gate.New(gate.Config{Passcode: cfg.gatePasscode, Log: log})
	if err != nil {
		return fmt.Errorf("build gate: %w", err)
	}
	handler, err := newServer(log, blobs, voiceSamples, interviews, generate, db, guard)
	if err != nil {
		return err
	}
	srvHTTP := newHTTPServer(cfg, handler)

	errCh := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		log.Info("thutapi listening", "addr", cfg.addr)
		errCh <- srvHTTP.ListenAndServe()
	}()

	select {
	case sig := <-sigs:
		// L1: log the actual operator signal (e.g. "terminated") rather
		// than the context-cancel message.
		log.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		<-serveDone
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	if err := srvHTTP.Shutdown(shutdownCtx); err != nil {
		return err
	}
	<-serveDone
	log.Info("thutapi stopped cleanly")
	return nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	if err := run(log, os.Args[1:], sigs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		log.Error("server exited", "err", err.Error())
		os.Exit(1)
	}
}
