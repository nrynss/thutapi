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
// H1 (re-opened in round 2) — Pin: TestShutdownDeadlineIsHonouredAgainstSlowBody
//
// The H1 defect: a request accepted just before SIGTERM can keep its body
// open past the graceful-shutdown deadline. If the http.Server's ReadTimeout
// outlives cfg.timeout, srv.Shutdown returns context.DeadlineExceeded, the
// process logs "graceful shutdown failed" and exits 1 — which the Hetzner
// Traefik orchestrator treats as unhealthy and restart-loops on SIGTERM
// during deploy.
//
// The round-1 fix set ReadTimeout = WriteTimeout = IdleTimeout to a uniform
// 30/30/120s. That was structurally present but semantically unsafe under
// the production defaults: ReadTimeout (30s) > cfg.timeout (10s), so a slow
// body accepted just before SIGTERM could still hang the shutdown past the
// 10s deadline. The round-1 Pin scaled everything uniformly (80ms read /
// 500ms shutdown) and therefore flipped the inequality to the safe side
// inside the test, hiding the defect from the Pin's own assertion.
// The round-2 fix encodes the safe relationship directly in newHTTPServer:
// ReadTimeout = cfg.timeout - 2s (so the slow body is force-closed before
// the deadline with a deterministic 2s window for Shutdown to drain),
// WriteTimeout = 0 (SSE-friendly, see main.go), IdleTimeout stays >
// cfg.timeout (idle connections are not in the shutdown path). The Pin
// below uses parseConfig() unchanged so the production relationship is the
// asserted relationship.
//
// Pin behaviour:
//
//  1. Bind cfg := parseConfig() — production defaults.
//  2. Stand up newHTTPServer(cfg, slowHandler) on an ephemeral port.
//  3. Issue a request that holds its Content-Length body open forever.
//  4. Call srv.Shutdown(ctxWithTimeout(cfg.timeout)) and require nil.
//
// With the fix, ReadTimeout fires at cfg.timeout - min(2s, cfg.timeout/2)
// (8s for the cfg.timeout=10s default — a deterministic headroom window for
// Shutdown to drain), the handler's body read errors out, the handler exits,
// Shutdown sees the connection drained, and returns nil within the cfg.timeout
// budget.
//
// Mutation: restore ReadTimeout to > cfg.timeout (the old 30s default).
// The body read then does not fire inside the cfg.timeout budget, the
// handler is still active when the shutdown context expires, Shutdown
// returns context.DeadlineExceeded, and the Pin fails for the named H1
// reason. See t0-remediation-round2.md for the verification transcripts.
// ---------------------------------------------------------------------

func TestShutdownDeadlineIsHonouredAgainstSlowBody(t *testing.T) {
	cfg := parseConfig()

	// Sanity-check: the production defaults must satisfy the safe
	// ordering so a reviewer reading the Pin can confirm we are not
	// scaling the relationship away.
	if cfg.timeout != 10*time.Second {
		t.Fatalf("cfg.timeout = %v; want 10s (H1 Pin must exercise parseConfig() defaults)", cfg.timeout)
	}

	handlerEntered := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal that we have entered the handler so the test can be
		// sure an in-flight body read is active when Shutdown is
		// called. Otherwise Shutdown would return immediately for
		// the wrong reason and the Pin would no longer be load-
		// bearing against the unsafe ordering.
		close(handlerEntered)
		// Read the body one byte at a time so the handler stays
		// alive until ReadTimeout fires. With the fix, the read
		// returns a timeout error after cfg.timeout - min(2s, cfg.timeout/2)
		// (8s for the cfg.timeout=10s default) and the handler exits; without
		// it (the Mutation), the read is still going when cfg.timeout expires
		// and Shutdown returns DeadlineExceeded.
		buf := make([]byte, 1)
		for {
			if _, err := r.Body.Read(buf); err != nil {
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

	// Slow client: send headers + a partial body and never send the
	// remainder. Content-Length is huge so the server keeps reading.
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

	// Wait until the handler is actually blocked inside r.Body.Read;
	// otherwise Shutdown could return immediately with no active
	// request and the Pin would pass for the wrong reason.
	select {
	case <-handlerEntered:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler did not enter body read within 2s; cannot pin the slow-body shutdown behaviour")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown = %v after %v; default ReadTimeout must be ≤ cfg.timeout=%v so the slow body is force-closed before the shutdown deadline (H1)", err, cfg.timeout, cfg.timeout)
	}
}

// ---------------------------------------------------------------------
// H1 floor (round 2 follow-up) — Pin: TestNewHTTPServerReadTimeoutIsAlwaysPositive
//
// The round-2 H1 fix derived ReadTimeout = cfg.timeout - 2*time.Second
// and clamped any negative result to 0. Go's http.Server treats
// ReadTimeout: 0 as "no timeout", so legal flag values like
// -shutdown-timeout=1s or -shutdown-timeout=2s would silently re-open
// H1: the slow body would never be force-closed, srv.Shutdown would
// return context.DeadlineExceeded, the process would exit 1, and the
// Hetzner Traefik orchestrator would restart-loop the deploy.
//
// The follow-up fix replaces the fixed 2s headroom with the smaller of
// 2s and cfg.timeout/2, so readTimeout = cfg.timeout - headroom is
// strictly positive for every legal cfg.timeout. This Pin encodes that
// property directly: for cfg.timeout in {1s, 2s} ReadTimeout must be
// > 0, and for the production default cfg.timeout = 10s ReadTimeout
// must be strictly less than cfg.timeout (the round-1 ordering that
// H1 protects). The existing slow-body Pin
// (TestShutdownDeadlineIsHonouredAgainstSlowBody) continues to pin the
// shutdown behaviour against the 10s default; this Pin pins the floor
// under smaller legal timeouts where the original derivation silently
// collapsed to 0.
//
// Mutation: restore the naive derivation in newHTTPServer, e.g.
//     readTimeout := cfg.timeout - 2*time.Second
//     if readTimeout < 0 { readTimeout = 0 }
// Then cfg.timeout=1s yields ReadTimeout=0 (no timeout), cfg.timeout=2s
// yields ReadTimeout=0 (no timeout), and the Pin fails with the named
// floor reason. cfg.timeout=10s still satisfies the strict-ordering
// half but the floor half exposes the regression. See
// dev-diary/adversarial-review/t0-remediation-round2.md for the
// verification transcripts.
// ---------------------------------------------------------------------

func TestNewHTTPServerReadTimeoutIsAlwaysPositive(t *testing.T) {
	cases := []struct {
		name            string
		timeout         time.Duration
		wantPositive    bool
		wantStrictOrder bool
	}{
		{name: "1s floor", timeout: 1 * time.Second, wantPositive: true},
		{name: "2s floor", timeout: 2 * time.Second, wantPositive: true},
		{name: "10s default", timeout: 10 * time.Second, wantPositive: true, wantStrictOrder: true},
	}

	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config{
				addr:        "127.0.0.1:0",
				timeout:     tc.timeout,
				idleTimeout: 120 * time.Second,
			}
			srv := newHTTPServer(cfg, handler)
			if srv == nil {
				t.Fatalf("newHTTPServer returned nil for cfg.timeout=%v", tc.timeout)
			}
			if tc.wantPositive && srv.ReadTimeout <= 0 {
				t.Fatalf("ReadTimeout = %v for cfg.timeout=%v; want > 0 (H1 floor: ReadTimeout=0 is Go's 'no timeout' and silently re-opens H1 for legal -shutdown-timeout=1s and -shutdown-timeout=2s)", srv.ReadTimeout, tc.timeout)
			}
			if tc.wantStrictOrder && srv.ReadTimeout >= tc.timeout {
				t.Fatalf("ReadTimeout = %v for cfg.timeout=%v; want < cfg.timeout (H1 strict ordering: ReadTimeout must expire before the shutdown deadline so srv.Shutdown can drain the slow body in time)", srv.ReadTimeout, tc.timeout)
			}
		})
	}
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
	// L2: pin run() to an ephemeral port so the test does not depend
	// on port 8080 being free. Without this, any process holding
	// 0.0.0.0:8080 makes run() return a bind error before the
	// synthetic SIGTERM is consumed and the Pin fails for an
	// unrelated environmental reason.
	t.Setenv("ADDR", "127.0.0.1:0")

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
