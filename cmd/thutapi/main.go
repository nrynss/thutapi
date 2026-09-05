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
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"thutapi/internal/bookgen"
	"thutapi/internal/bookvideo"
	"thutapi/internal/gmi/media"
	"thutapi/internal/gmi/text"
	"thutapi/internal/interview"
	"thutapi/internal/job"
	"thutapi/internal/mediastore"
	"thutapi/internal/store"
	"thutapi/internal/stream"
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
// live under. Later tracks extend this struct and the parseFlags
// surface, they do not replace it.
//
// ReadTimeout and WriteTimeout are deliberately not part of config — they
// are derived in newHTTPServer from the graceful-shutdown deadline so a
// slow request body cannot outlive the deadline (H1, re-opened in
// round 2). WriteTimeout is fixed at 0 because project.md §Pipeline
// folds SSE into the architecture (interview turns stream text, TTS
// audio lands asynchronously); a non-zero WriteTimeout would force-close
// every long-lived stream.
type config struct {
	addr        string        // bind address, e.g. "0.0.0.0:8080"
	timeout     time.Duration // graceful shutdown deadline
	idleTimeout time.Duration // http.Server.IdleTimeout
	dataDir     string        // SQLite file + media blobs live here (T3)
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

// parseConfig reads environment only — no flags. It is the seam tests use so
// they don't re-register flag entries across invocations.
func parseConfig() config {
	return config{
		addr:        resolveAddr(),
		timeout:     10 * time.Second,
		idleTimeout: 120 * time.Second,
		dataDir:     resolveDataDir(),
	}
}

// parseFlags merges CLI flags onto parseConfig. It uses a private FlagSet so
// the test seam (parseConfig) is independent of os.Args.
func parseFlags(args []string) (config, error) {
	cfg := parseConfig()
	fs := flag.NewFlagSet("thutapi", flag.ContinueOnError)
	fs.DurationVar(&cfg.timeout, "shutdown-timeout", cfg.timeout, "graceful shutdown deadline")
	fs.StringVar(&cfg.dataDir, "data-dir", cfg.dataDir, "directory for the SQLite database and media blobs")
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
	mux        *http.ServeMux
	log        *slog.Logger
	start      time.Time
	media      *mediastore.Store
	interviews *interview.Handler
	generate   *bookgen.Handler
}

// newServer wires the routes. media, interviews and generate must be
// non-nil: they are live handlers, not optional dependencies.
func newServer(log *slog.Logger, media *mediastore.Store, interviews *interview.Handler, generate *bookgen.Handler) *server {
	s := &server{mux: http.NewServeMux(), log: log, start: time.Now(), media: media, interviews: interviews, generate: generate}
	// /healthz is the one route T0 ships. Liveness only — no dependency
	// checks, no probes. That distinction belongs to a later track.
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// T3's one sanctioned route line (PLAN.md invariant 5): media
	// blobs serve through the mediastore handler, which answers Range
	// requests so narration can be scrubbed (PLAN.md §T3).
	s.mux.Handle("GET /media/{id}", s.media)
	// T4's sanctioned route lines (PLAN.md invariant 5): the interview
	// loop — start, catch-up transcript, SSE events, answers. The loop
	// itself lives in internal/interview.
	s.mux.HandleFunc("POST /interviews", s.interviews.Start)
	s.mux.HandleFunc("GET /interviews/{id}", s.interviews.Transcript)
	s.mux.HandleFunc("GET /interviews/{id}/events", s.interviews.Events)
	s.mux.HandleFunc("POST /interviews/{id}/answers", s.interviews.Answer)
	// T10c's sanctioned route lines (PLAN.md invariant 5): start a
	// book's generation and stream its events on the book's topic.
	// The pipeline lives in internal/bookgen.
	s.mux.HandleFunc("POST /interviews/{id}/generate", s.generate.Generate)
	s.mux.HandleFunc("GET /interviews/{id}/generate/events", s.generate.Events)
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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

	// The two GMI clients are process-wide: one text client serves the
	// interview turns, Phase-B structuring and T7's consistency judge
	// (a *text.Client satisfies every seam); one request-queue client
	// serves the image renders and the speech synthesis (invariant 1:
	// nothing outside internal/gmi talks to GMI).
	textCli := text.New()
	mediaCli := media.New()

	// T4/T10c: the interview loop and the generation pipeline stream
	// over SSE and run off the request path (PLAN.md invariant 6 —
	// nothing blocks on M3), so main owns the broker and the job
	// runner and hands them to both handlers. One runner bounds the
	// whole process's long work.
	broker := stream.New(stream.Config{})
	runner := job.New(broker)
	interviews, err := interview.New(interview.Config{
		Chat:   textCli,
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
		Broker:   broker,
		Jobs:     runner,
		Video:    bookgen.NewFFmpegRenderer(bookvideo.Config{}),
		Film:     blobs,
		Log:      log,
	})
	if err != nil {
		return fmt.Errorf("build generation handler: %w", err)
	}

	srvHTTP := newHTTPServer(cfg, newServer(log, blobs, interviews, generate))

	errCh := make(chan error, 1)
	go func() {
		log.Info("thutapi listening", "addr", cfg.addr)
		errCh <- srvHTTP.ListenAndServe()
	}()

	select {
	case sig := <-sigs:
		// L1: log the actual operator signal (e.g. "terminated") rather
		// than the context-cancel message.
		log.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
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
