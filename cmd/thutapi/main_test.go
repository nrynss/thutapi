package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newServer(log)
}

func TestHealthzReturnsOK(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json prefix", got)
	}

	var body struct {
		Status        string `json:"status"`
		UptimeSeconds int    `json:"uptime_seconds"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q, want ok", body.Status)
	}
	if body.UptimeSeconds < 0 {
		t.Fatalf("uptime_seconds = %d, want >= 0", body.UptimeSeconds)
	}
}

func TestHealthzRejectsNonGET(t *testing.T) {
	srv := newTestServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/healthz", nil)
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s /healthz = %d, want 405", method, rr.Code)
		}
	}
}

func TestParseConfigDefaults(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "")
	cfg := parseConfig()
	if cfg.addr != "0.0.0.0:8080" {
		t.Fatalf("addr = %q, want 0.0.0.0:8080", cfg.addr)
	}
	if cfg.timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", cfg.timeout)
	}
}

func TestParseConfigPortOverridesAddr(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "9090")
	cfg := parseConfig()
	if cfg.addr != "0.0.0.0:9090" {
		t.Fatalf("addr = %q, want 0.0.0.0:9090", cfg.addr)
	}
}

func TestParseConfigAddrWins(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:7070")
	t.Setenv("PORT", "9090")
	cfg := parseConfig()
	if cfg.addr != "127.0.0.1:7070" {
		t.Fatalf("addr = %q, want 127.0.0.1:7070 (ADDR overrides PORT)", cfg.addr)
	}
}

func TestParseFlagsUsesEnvDefaults(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "")
	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags(nil): %v", err)
	}
	if cfg.addr != "0.0.0.0:8080" {
		t.Fatalf("addr = %q, want 0.0.0.0:8080", cfg.addr)
	}
}

func TestParseFlagsTimeoutOverride(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("PORT", "")
	cfg, err := parseFlags([]string{"-shutdown-timeout=2s"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", cfg.timeout)
	}
}

func TestParseFlagsBadFlagReturnsError(t *testing.T) {
	_, err := parseFlags([]string{"-this-flag-does-not-exist"})
	if err == nil {
		t.Fatalf("parseFlags with bad flag should return error")
	}
}

// ---------------------------------------------------------------------
// H1 — Pin: TestShutdownDeadlineIsHonouredAgainstSlowBody
//
// The H1 defect: the http.Server literal in main() set ReadHeaderTimeout but
// left ReadTimeout / WriteTimeout / IdleTimeout at Go's zero value, meaning
// no timeout. A slow request body therefore held past the shutdown
// deadline, Shutdown returned DeadlineExceeded, and the process exited 1.
// The fix must set all three to a non-zero value larger than the default
// shutdown timeout (10s) so a stuck request is force-closed before the
// shutdown deadline fires.
//
// The Pin asserts two things:
//
//  1. Structural: the http.Server returned by newHTTPServer has
//     ReadTimeout / WriteTimeout / IdleTimeout each larger than 10s.
//  2. Behavioural: a slow handler that holds the request body open sees
//     its body read error with a timeout once ReadTimeout fires, so the
//     handler exits naturally and Shutdown sees the connection drained.
//
// Mutation: remove the three timeout fields from newHTTPServer. Both
// assertions fail.
// ---------------------------------------------------------------------

func TestShutdownDeadlineIsHonouredAgainstSlowBody(t *testing.T) {
	cfg := config{
		addr:         "ignored",
		timeout:      500 * time.Millisecond,
		readTimeout:  80 * time.Millisecond,
		writeTimeout: 80 * time.Millisecond,
		idleTimeout:  1 * time.Second,
	}

	// Structural: each timeout must be set and exceed the default 10s
	// shutdown deadline so the fix actually protects graceful shutdown.
	probe := newHTTPServer(parseConfig(), http.NotFoundHandler())
	if probe.ReadTimeout <= 10*time.Second {
		t.Fatalf("newHTTPServer.ReadTimeout = %v; want > 10s (H1 fix sets it to 30s)", probe.ReadTimeout)
	}
	if probe.WriteTimeout <= 10*time.Second {
		t.Fatalf("newHTTPServer.WriteTimeout = %v; want > 10s (H1 fix sets it to 30s)", probe.WriteTimeout)
	}
	if probe.IdleTimeout <= 10*time.Second {
		t.Fatalf("newHTTPServer.IdleTimeout = %v; want > 10s (H1 fix sets it to 120s)", probe.IdleTimeout)
	}

	// Behavioural: the handler reads the body one byte at a time and
	// records whether the read failed with a timeout. With ReadTimeout
	// set, the body read fails after ReadTimeout; without it, the read
	// would block until the client closes the connection. The handler
	// signals exit via a channel so the test can wait for the handler
	// to return before checking Shutdown.
	handlerExited := make(chan struct{})
	var bodyErr error
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerExited)
		buf := make([]byte, 1)
		for {
			if _, err := r.Body.Read(buf); err != nil {
				bodyErr = err
				return
			}
		}
	})

	srv := newHTTPServer(cfg, slow)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() { _ = srv.Serve(ln) }()

	// Slow client: send headers + partial body, then stall.
	go func() {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 1000000\r\n\r\npartial"))
		for {
			if _, err := conn.Read(make([]byte, 1)); err != nil {
				return
			}
		}
	}()

	// Wait for the handler to exit (it should, once ReadTimeout fires
	// and the body read errors out).
	select {
	case <-handlerExited:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler did not exit within 2s; ReadTimeout did not fire and the body read blocked forever")
	}

	// With ReadTimeout=80ms, the body read should have failed with a
	// timeout error. Without ReadTimeout (the unfixed state), the body
	// read would have blocked until the test's client closed the
	// socket, which would not produce a timeout error here.
	if bodyErr == nil {
		t.Fatalf("handler body read returned no error; ReadTimeout did not fire")
	}
	if !isTimeout(bodyErr) {
		t.Fatalf("handler body read error = %v; want a timeout error (ReadTimeout should fire)", bodyErr)
	}

	// Shutdown should return cleanly now that the connection was
	// force-closed by ReadTimeout. Give it a generous window because
	// Go's HTTP server uses closeWriteAndWait which sleeps briefly
	// (rstAvoidanceDelay, ~25ms by default).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("srv.Shutdown = %v; want nil after ReadTimeout forced the slow body closed", err)
	}
}

// isTimeout reports whether err is a net error that indicates a read deadline
// expired. Go's net package returns *net.OpError wrapping "i/o timeout" when
// SetReadDeadline fires; we test for that via the Timeout() method.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// ---------------------------------------------------------------------
// H3 — Pin: TestParseFlagsRejectsNonPositiveTimeout
//
// The H3 defect: flag.DurationVar parses "-shutdown-timeout=0s" and
// negative durations without complaint, so a caller asking for a
// non-positive shutdown deadline silently gets a context that fires
// immediately. With even one slow request, SIGTERM then logs
// "graceful shutdown failed" and exits 1.
//
// The fix rejects cfg.timeout <= 0 with errShutdownTimeout. The Pin
// pins both zero and negative values.
//
// Mutation: drop the validation in parseFlags. The Pin fails because
// parseFlags returns (cfg, nil) for both inputs.
// ---------------------------------------------------------------------

func TestParseFlagsRejectsNonPositiveTimeout(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"zero", "-shutdown-timeout=0s"},
		{"negative", "-shutdown-timeout=-5s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseFlags([]string{c.arg})
			if err == nil {
				t.Fatalf("parseFlags(%s) returned nil error; want errShutdownTimeout", c.arg)
			}
			if !errors.Is(err, errShutdownTimeout) {
				t.Fatalf("parseFlags(%s) = %v; want errShutdownTimeout", c.arg, err)
			}
		})
	}
}

// ---------------------------------------------------------------------
// M2 — Pin: TestParseFlagsAfterOperandIsNotSilentlyDropped
//
// The M2 defect: flag.NewFlagSet stops parsing at the first
// positional operand. A caller running the binary with
// `unexpected -shutdown-timeout=1s` got the default 10s timeout and
// never saw a flag error — the trailing flag was silently dropped.
// The fix rejects fs.NArg() != 0 after parsing.
//
// Mutation: drop the fs.NArg() check in parseFlags. The Pin fails
// because parseFlags returns (cfg, nil).
// ---------------------------------------------------------------------

func TestParseFlagsAfterOperandIsNotSilentlyDropped(t *testing.T) {
	cfg, err := parseFlags([]string{"unexpected", "-shutdown-timeout=1s"})
	if err == nil {
		t.Fatalf("parseFlags(unexpected -shutdown-timeout=1s) returned nil error; trailing flag is being silently dropped (M2)")
	}
	if !errors.Is(err, errUnexpectedOperand) {
		t.Fatalf("parseFlags err = %v; want errUnexpectedOperand", err)
	}
	// cfg.timeout must remain the default (10s) since the flag after the
	// operand was never applied; that's the bug being pinned.
	if cfg.timeout == 1*time.Second {
		t.Fatalf("parseFlags accepted the flag after a positional operand; M2 not fixed")
	}
}

// ---------------------------------------------------------------------
// L1 — Pin: TestShutdownLogRecordsSignalName
//
// The L1 defect: the shutdown log line printed `signal=context canceled`
// because the code logged ctx.Err().Error(). signal.NotifyContext cancels
// the context on SIGINT/SIGTERM, so the cancel message drowned out the
// operator signal name.
//
// The fix injects a typed os.Signal channel and logs sig.String()
// instead of ctx.Err().Error().
//
// The Pin feeds a synthetic SIGTERM into the injected sigs channel and
// asserts the captured JSON log line has signal="terminated" (Go's
// String() form for syscall.SIGTERM).
//
// Mutation: revert run() to the old ctx.Err().Error() source. The Pin
// fails because the log line contains "context canceled" instead of
// the signal name.
// ---------------------------------------------------------------------

func TestShutdownLogRecordsSignalName(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Empty sigs channel: run() will block on <-sigs until the test
	// sends a value.
	sigs := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() { done <- run(log, nil, sigs) }()

	// Give run() a moment to enter its select, then send SIGTERM.
	time.Sleep(20 * time.Millisecond)
	sigs <- syscall.SIGTERM

	select {
	case err := <-done:
		if err != nil {
			t.Logf("run returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("run did not return within 3s after synthetic SIGTERM")
	}

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if msg, _ := rec["msg"].(string); msg == "shutdown signal received" {
			found = true
			if got, _ := rec["signal"].(string); got != "terminated" {
				t.Fatalf("shutdown log signal field = %q; want %q (L1 fix logs sig.String(), not ctx.Err().Error())", got, "terminated")
			}
		}
	}
	if !found {
		t.Fatalf("no `shutdown signal received` log line in output:\n%s", logBuf.String())
	}
}