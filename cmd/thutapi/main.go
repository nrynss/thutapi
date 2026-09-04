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
	"syscall"
	"time"
)

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
// shutdown-timeout flag, and the http.Server timeout knobs; later tracks will
// extend this struct and the parseFlags surface, not replace it.
type config struct {
	addr         string        // bind address, e.g. "0.0.0.0:8080"
	timeout      time.Duration // graceful shutdown deadline
	readTimeout  time.Duration // http.Server.ReadTimeout
	writeTimeout time.Duration // http.Server.WriteTimeout
	idleTimeout  time.Duration // http.Server.IdleTimeout
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

// parseConfig reads environment only — no flags. It is the seam tests use so
// they don't re-register flag entries across invocations.
func parseConfig() config {
	return config{
		addr:         resolveAddr(),
		timeout:      10 * time.Second,
		readTimeout:  30 * time.Second,
		writeTimeout: 30 * time.Second,
		idleTimeout:  120 * time.Second,
	}
}

// parseFlags merges CLI flags onto parseConfig. It uses a private FlagSet so
// the test seam (parseConfig) is independent of os.Args.
func parseFlags(args []string) (config, error) {
	cfg := parseConfig()
	fs := flag.NewFlagSet("thutapi", flag.ContinueOnError)
	fs.DurationVar(&cfg.timeout, "shutdown-timeout", cfg.timeout, "graceful shutdown deadline")
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
// dependencies they need. T0 wires a single handler: GET /healthz.
type server struct {
	mux   *http.ServeMux
	log   *slog.Logger
	start time.Time
}

func newServer(log *slog.Logger) *server {
	s := &server{mux: http.NewServeMux(), log: log, start: time.Now()}
	// /healthz is the one route T0 ships. Liveness only — no dependency
	// checks, no probes. That distinction belongs to a later track.
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"ok","uptime_seconds":%d}`, int(time.Since(s.start).Seconds()))
}

// newHTTPServer builds the http.Server with the timeouts that protect
// graceful shutdown (H1). ReadTimeout, WriteTimeout and IdleTimeout were all
// zero before this fix; a slow request body could then hang past the
// shutdown deadline, force srv.Shutdown to return context.DeadlineExceeded,
// and exit the process with status 1. Each timeout now exceeds the default
// 10s shutdown deadline so a slow request is force-closed before the
// deadline fires.
func newHTTPServer(cfg config, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       cfg.readTimeout,
		WriteTimeout:      cfg.writeTimeout,
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

	srvHTTP := newHTTPServer(cfg, newServer(log))

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