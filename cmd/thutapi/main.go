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

// config holds the runtime configuration. T0 only reads ADDR / PORT and the
// shutdown-timeout flag; later tracks will extend this struct and the
// parseFlags surface, not replace it.
type config struct {
	addr    string        // bind address, e.g. "0.0.0.0:8080"
	timeout time.Duration // graceful shutdown deadline
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
		addr:    resolveAddr(),
		timeout: 10 * time.Second,
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

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		log.Error("flag parse failed", "err", err.Error())
		os.Exit(2)
	}

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           newServer(log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("thutapi listening", "addr", cfg.addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received", "signal", ctx.Err().Error())
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server exited", "err", err.Error())
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err.Error())
		os.Exit(1)
	}
	log.Info("thutapi stopped cleanly")
}