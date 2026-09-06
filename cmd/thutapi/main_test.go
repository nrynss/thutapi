package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
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
)

// newTestServer builds the server with a real store and media store
// under a throwaway directory — /media/ is a live route, so the
// handler behind it must exist. The interview handler (T4) gets a
// canned-echo chatter so the interview routes answer without M3; the
// generation handler (T10c) gets stage fakes that fail loudly if a
// route test ever drives them (no pipeline runs here — the pipeline is
// internal/bookgen's own suite).
func newTestServer(t *testing.T) *server {
	t.Helper()
	return newGatedTestServer(t, nil)
}

// newGatedTestServer is newTestServer with a caller-supplied gate, so
// T11's refusal paths can be driven with limits a route test can
// actually exhaust. A nil guard means the default posture: rate limits
// on, no passcode.
func newGatedTestServer(t *testing.T, guard *gate.Gate) *server {
	t.Helper()
	if guard == nil {
		defaultGuard, err := gate.New(gate.Config{})
		if err != nil {
			t.Fatalf("build gate: %v", err)
		}
		guard = defaultGuard
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(t.Context(), store.Config{
		Path: filepath.Join(t.TempDir(), "thutapi.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mediaDir := filepath.Join(t.TempDir(), "media")
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir: mediaDir,
		DB:  db,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	voiceSamples, err := audio.NewVoiceSampleHandler(audio.VoiceSampleConfig{Store: media, PublicOrigin: "https://thutapi.nryn.dev", TempDir: t.TempDir(), UploadToken: "test-upload-token", Cloner: failVoiceCloner{}})
	if err != nil {
		t.Fatalf("build voice sample handler: %v", err)
	}
	broker := stream.New(stream.Config{})
	interviews, err := interview.New(interview.Config{
		Chat:   echoChatter{},
		Store:  db,
		Broker: broker,
		Jobs:   job.New(broker),
	})
	if err != nil {
		t.Fatalf("build interview handler: %v", err)
	}
	generate, err := bookgen.New(bookgen.Config{
		DB:       db,
		Blobs:    media,
		MediaDir: mediaDir,
		Chat:     echoChatter{},
		Judge:    echoChatter{},
		Imager:   failImager{},
		TTS:      failTTS{},
		Broker:   broker,
		Jobs:     job.New(broker),
		PDF:      bookpdf.NewRenderer(),
		Video:    failRenderer{},
		Film:     failFilmStore{},
		Log:      log,
	})
	if err != nil {
		t.Fatalf("build generation handler: %v", err)
	}
	srv, err := newServer(log, media, voiceSamples, interviews, generate, db, guard)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv
}

// echoChatter answers every Chat call with a one-question reply that
// never fills a checklist slot and never ends: enough for route-wiring
// assertions, never enough for an interview. It also satisfies
// bookgen's structure-chat and judge seams (same one-method shape).
// The real interview behaviour is tested in internal/interview and the
// generation pipeline in internal/bookgen.
type echoChatter struct{}

func (echoChatter) Chat(_ context.Context, _ text.ChatRequest) (*text.ChatResponse, error) {
	return &text.ChatResponse{Choices: []text.Choice{{
		Message: text.AssistantMessage{TextBody: "What happens next?\n[[filled:]]"},
	}}}, nil
}

// failImager, failTTS, failRenderer and failFilmStore are bookgen
// stage fakes that fail loudly if a cmd route test ever drives a
// generation: the pipeline itself is tested in internal/bookgen with
// its own scripted fakes, and this file only pins the route seam.
type failImager struct{}

func (failImager) GenerateImage(context.Context, string, string, media.ImageOptions) ([]byte, error) {
	return nil, fmt.Errorf("failImager: unexpected GenerateImage in a cmd route test")
}

func (failImager) EditImage(context.Context, string, string, []string, media.ImageOptions) ([]byte, error) {
	return nil, fmt.Errorf("failImager: unexpected EditImage in a cmd route test")
}

type failTTS struct{}

func (failTTS) SynthesizeSpeech(context.Context, string, string, string, string) ([]byte, error) {
	return nil, fmt.Errorf("failTTS: unexpected SynthesizeSpeech in a cmd route test")
}

type failVoiceCloner struct{}

func (failVoiceCloner) CloneVoice(context.Context, audio.VoiceCloneRequest) (audio.VoiceCloneResult, error) {
	return audio.VoiceCloneResult{}, fmt.Errorf("failVoiceCloner: unexpected CloneVoice in a cmd route test")
}

type failRenderer struct{}

func (failRenderer) Render(context.Context, bookvideo.Input) (time.Duration, error) {
	return 0, fmt.Errorf("failRenderer: unexpected Render in a cmd route test")
}

type failFilmStore struct{}

func (failFilmStore) Persist(context.Context, io.Reader, string) (string, error) {
	return "", fmt.Errorf("failFilmStore: unexpected Persist in a cmd route test")
}

func (failFilmStore) Delete(context.Context, string) error {
	return fmt.Errorf("failFilmStore: unexpected Delete in a cmd route test")
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
		Version       string `json:"version"`
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
	if body.Version == "" {
		t.Fatalf("version = %q, want non-empty (stamped via -X main.version at build time)", body.Version)
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
	t.Setenv("DATA_DIR", "")
	cfg := parseConfig()
	if cfg.addr != "0.0.0.0:8080" {
		t.Fatalf("addr = %q, want 0.0.0.0:8080", cfg.addr)
	}
	if cfg.timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", cfg.timeout)
	}
	if cfg.dataDir != "data" {
		t.Fatalf("dataDir = %q, want data (the gitignored default)", cfg.dataDir)
	}
}

func TestParseConfigDataDirEnv(t *testing.T) {
	t.Setenv("DATA_DIR", "/var/lib/thutapi")
	cfg := parseConfig()
	if cfg.dataDir != "/var/lib/thutapi" {
		t.Fatalf("dataDir = %q, want /var/lib/thutapi (DATA_DIR wins)", cfg.dataDir)
	}
}

func TestParseConfigPublicOriginEnv(t *testing.T) {
	t.Setenv("PUBLIC_ORIGIN", "https://thutapi.nryn.dev")
	if cfg := parseConfig(); cfg.publicOrigin != "https://thutapi.nryn.dev" {
		t.Fatalf("publicOrigin = %q, want configured PUBLIC_ORIGIN", cfg.publicOrigin)
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

func TestParseFlagsDataDirFlag(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	cfg, err := parseFlags([]string{"-data-dir=/tmp/books"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.dataDir != "/tmp/books" {
		t.Fatalf("dataDir = %q, want /tmp/books", cfg.dataDir)
	}
}

// TestMediaRouteServesThroughMux pins the one sanctioned route line
// (PLAN.md invariant 5): /media/{id} reaches the mediastore handler,
// where an unknown id is a 404 and a non-GET method is rejected by
// the mux.
func TestMediaRouteServesThroughMux(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/media/00000000000000000000000000000000", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotFound; got != want {
		t.Fatalf("GET unknown media id: status = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/media/not-an-id", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotFound; got != want {
		t.Fatalf("GET malformed media id: status = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodPost, "/media/00000000000000000000000000000000", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("POST media id: status = %d, want %d", got, want)
	}
}

// TestVoiceSampleRouteServesThroughMux pins T13's one POST route. The
// transcode/persist pipe is tested in internal/audio; this test proves the
// public endpoint reaches it and a non-POST cannot accidentally create media.
func TestVoiceSampleRouteServesThroughMux(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/voice-sample", strings.NewReader("not audio"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Voice-Sample-Consent", "yes")
	req.Header.Set("Authorization", "Bearer test-upload-token")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("POST invalid voice sample = %d, want 415: %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/voice-sample", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET voice sample = %d Allow %q, want 405 POST", rr.Code, rr.Header().Get("Allow"))
	}
}

// TestInterviewRoutesServeThroughMux pins T4's route lines (PLAN.md
// invariant 5): POST /interviews reaches the interview handler (201
// with an id and a topic), GET /interviews/{id}/events on an unknown
// interview is a 404 from the handler (not the mux), and a GET on the
// start route is a 405 from the mux. The interview LOOP is tested in
// internal/interview; here only the seam.
func TestInterviewRoutesServeThroughMux(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/interviews", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusCreated; got != want {
		t.Fatalf("POST /interviews: status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	var started struct {
		ID     string `json:"id"`
		BookID string `json:"book_id"`
		Topic  string `json:"topic"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start body: %v", err)
	}
	if started.ID == "" || started.BookID == "" || started.Topic != interview.Topic(started.ID) {
		t.Fatalf("start body = %+v, want id, book_id and topic= interview:<id>", started)
	}

	// T10b's C4 route stays under the singular human book path while serving
	// the machine-readable catch-up shape T9 reads after it subscribes.
	req = httptest.NewRequest(http.MethodGet, "/book/"+started.BookID+"/state", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("GET book state: status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	var bookState struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &bookState); err != nil {
		t.Fatalf("decode book state: %v", err)
	}
	if bookState.Status != "not_started" {
		t.Fatalf("book state = %+v, want not_started", bookState)
	}

	req = httptest.NewRequest(http.MethodGet, "/interviews/00000000000000000000000000000000/events", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotFound; got != want {
		t.Fatalf("GET events unknown id: status = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/interviews", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("GET /interviews: status = %d, want %d", got, want)
	}
}

// TestGenerateRoutesServeThroughMux pins T10c's route lines (PLAN.md
// invariant 5): POST /interviews/{id}/generate reaches the bookgen
// handler (an unknown interview is a 404 from the handler, not the
// mux), GET /interviews/{id}/generate/events likewise 404s on an
// unknown interview, and the wrong method on either route is a 405
// from the mux. The pipeline itself is tested in internal/bookgen;
// here only the seam.
func TestGenerateRoutesServeThroughMux(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/interviews/00000000000000000000000000000000/generate", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotFound; got != want {
		t.Fatalf("POST generate unknown id: status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode generate error body: %v", err)
	}
	if body.Error != "not_found" {
		t.Fatalf("generate error class = %q, want not_found", body.Error)
	}

	req = httptest.NewRequest(http.MethodGet, "/interviews/00000000000000000000000000000000/generate/events", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotFound; got != want {
		t.Fatalf("GET generate events unknown id: status = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodGet, "/interviews/00000000000000000000000000000000/generate", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("GET generate route: status = %d, want %d", got, want)
	}

	req = httptest.NewRequest(http.MethodPost, "/interviews/00000000000000000000000000000000/generate/events", nil)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("POST generate events route: status = %d, want %d", got, want)
	}
}

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
	t.Setenv("PUBLIC_ORIGIN", "https://thutapi.nryn.dev")
	t.Setenv("UPLOAD_TOKEN", "test-upload-token")
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

	// run() now opens the store (T3); point it at a throwaway dir so
	// the test never touches the repo's data/.
	dataDir := t.TempDir()

	done := make(chan error, 1)
	go func() { done <- run(log, []string{"-data-dir=" + dataDir}, sigs) }()

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

func TestRunRejectsMissingVoiceSampleConfiguration(t *testing.T) {
	t.Setenv("PUBLIC_ORIGIN", "")
	t.Setenv("UPLOAD_TOKEN", "test-upload-token")
	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)), []string{"-data-dir=" + t.TempDir()}, make(chan os.Signal))
	if err == nil || !strings.Contains(err.Error(), "no public voice sample origin") {
		t.Fatalf("run without PUBLIC_ORIGIN = %v, want startup configuration error", err)
	}
}

func TestShelfRouteServesLandingAndBooks(t *testing.T) {
	defaultGuard, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("build gate: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(t.Context(), store.Config{
		Path: filepath.Join(t.TempDir(), "thutapi.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mediaDir := filepath.Join(t.TempDir(), "media")
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir: mediaDir,
		DB:  db,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	voiceSamples, err := audio.NewVoiceSampleHandler(audio.VoiceSampleConfig{Store: media, PublicOrigin: "https://thutapi.nryn.dev", TempDir: t.TempDir(), UploadToken: "test-upload-token", Cloner: failVoiceCloner{}})
	if err != nil {
		t.Fatalf("build voice sample handler: %v", err)
	}
	broker := stream.New(stream.Config{})
	interviews, err := interview.New(interview.Config{
		Chat:   echoChatter{},
		Store:  db,
		Broker: broker,
		Jobs:   job.New(broker),
	})
	if err != nil {
		t.Fatalf("build interview handler: %v", err)
	}
	generate, err := bookgen.New(bookgen.Config{
		DB:       db,
		Blobs:    media,
		MediaDir: mediaDir,
		Chat:     echoChatter{},
		Judge:    echoChatter{},
		Imager:   failImager{},
		TTS:      failTTS{},
		Broker:   broker,
		Jobs:     job.New(broker),
		PDF:      bookpdf.NewRenderer(),
		Video:    failRenderer{},
		Film:     failFilmStore{},
		Log:      log,
	})
	if err != nil {
		t.Fatalf("build generation handler: %v", err)
	}
	srv, err := newServer(log, media, voiceSamples, interviews, generate, db, defaultGuard)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	// 1. Initially empty
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Make your own book") {
		t.Fatalf("body lacks CTA: %s", body)
	}
	if !strings.Contains(body, `<script type="application/json" id="shelf-books">[]</script>`) {
		t.Fatalf("body lacks empty json: %s", body)
	}

	// 2. Add books
	b1, err := db.CreateBook(t.Context(), "The Magic Fox")
	if err != nil {
		t.Fatalf("create book 1: %v", err)
	}
	if err := db.UpdateBook(t.Context(), store.Book{ID: b1.ID, Title: b1.Title, Byline: "Pip"}); err != nil {
		t.Fatalf("update book 1: %v", err)
	}
	b2, err := db.CreateBook(t.Context(), "The Cloud Dance")
	if err != nil {
		t.Fatalf("create book 2: %v", err)
	}
	// The shelf lists books there is something to read; a bare book row is
	// an interview somebody abandoned, not a book.
	for _, b := range []struct{ id, media string }{{b1.ID, "pdf-1"}, {b2.ID, "pdf-2"}} {
		if _, err := db.CreateMedia(t.Context(), store.Media{ID: b.media, ContentType: "application/pdf", SizeBytes: 1}); err != nil {
			t.Fatalf("create %s: %v", b.media, err)
		}
		if err := db.SetMediaPlace(t.Context(), b.media, store.MediaPlace{BookID: b.id}); err != nil {
			t.Fatalf("place %s: %v", b.media, err)
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body = rec.Body.String()
	if !strings.Contains(body, "The Magic Fox") || !strings.Contains(body, "By Pip") {
		t.Fatalf("body lacks book 1 details: %s", body)
	}
	if !strings.Contains(body, "The Cloud Dance") {
		t.Fatalf("body lacks book 2 title: %s", body)
	}
	if !strings.Contains(body, "/book/"+b1.ID) || !strings.Contains(body, "/book/"+b2.ID) {
		t.Fatalf("body lacks book links: %s", body)
	}

	// 3. Verify ungated: even if gate has passcode, shelf is public
	gatedPassGuard, err := gate.New(gate.Config{Passcode: "secret"})
	if err != nil {
		t.Fatalf("build gated passcode guard: %v", err)
	}
	srvGated, err := newServer(log, media, voiceSamples, interviews, generate, db, gatedPassGuard)
	if err != nil {
		t.Fatalf("new gated server: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	srvGated.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("shelf status with passcode gate = %d, want 200 (ungated)", rec.Code)
	}
}

func TestColdBootRestoresFixtureAndServesShelfBookAndDownloads(t *testing.T) {
	dataDir := t.TempDir()
	prewarmDir, err := filepath.Abs(filepath.Join("..", "..", "data", "prewarm"))
	if err != nil {
		t.Fatalf("resolve prewarm dir: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(dataDir, "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	mediaDir := filepath.Join(dataDir, "media")
	blobs, err := mediastore.Open(t.Context(), mediastore.Config{Dir: mediaDir, DB: db})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}

	// Cold boot restore of prewarm fixtures
	restored, err := prewarm.Import(t.Context(), db, blobs, prewarmDir)
	if err != nil {
		t.Fatalf("prewarm.Import: %v", err)
	}
	if len(restored) != 1 || restored[0] != "d625fd608be48227f08c33cf860e5de8" {
		t.Fatalf("restored = %v, want [d625fd608be48227f08c33cf860e5de8]", restored)
	}

	voiceSamples, err := audio.NewVoiceSampleHandler(audio.VoiceSampleConfig{
		Store:        blobs,
		PublicOrigin: "https://thutapi.nryn.dev",
		TempDir:      t.TempDir(),
		UploadToken:  "test-upload-token",
		Cloner:       failVoiceCloner{},
	})
	if err != nil {
		t.Fatalf("build voice sample handler: %v", err)
	}
	broker := stream.New(stream.Config{})
	interviews, err := interview.New(interview.Config{
		Chat:   echoChatter{},
		Store:  db,
		Broker: broker,
		Jobs:   job.New(broker),
	})
	if err != nil {
		t.Fatalf("build interview handler: %v", err)
	}
	generate, err := bookgen.New(bookgen.Config{
		DB:       db,
		Blobs:    blobs,
		MediaDir: mediaDir,
		Chat:     echoChatter{},
		Judge:    echoChatter{},
		Imager:   failImager{},
		TTS:      failTTS{},
		Broker:   broker,
		Jobs:     job.New(broker),
		PDF:      bookpdf.NewRenderer(),
		Video:    failRenderer{},
		Film:     failFilmStore{},
		Log:      log,
	})
	if err != nil {
		t.Fatalf("build generation handler: %v", err)
	}
	guard, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("build gate: %v", err)
	}
	srv, err := newServer(log, blobs, voiceSamples, interviews, generate, db, guard)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	// 1. GET / lists the prewarmed book
	shelfReq := httptest.NewRequest(http.MethodGet, "/", nil)
	shelfRec := httptest.NewRecorder()
	srv.ServeHTTP(shelfRec, shelfReq)
	if shelfRec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", shelfRec.Code)
	}
	shelfBody := shelfRec.Body.String()
	if !strings.Contains(shelfBody, "Bo and Pip&#39;s Moon Mango Dance") &&
		!strings.Contains(shelfBody, "Bo and Pip's Moon Mango Dance") {
		t.Fatalf("GET / lacks restored book title: %s", shelfBody)
	}
	if !strings.Contains(shelfBody, "/book/d625fd608be48227f08c33cf860e5de8") {
		t.Fatalf("GET / lacks link to restored book: %s", shelfBody)
	}
	if !strings.Contains(shelfBody, "d625fd608be48227f08c33cf860e5de8") {
		t.Fatalf("GET / lacks book id in JSON: %s", shelfBody)
	}

	// 2. GET /book/{id} serves the book page
	bookReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8", nil)
	bookRec := httptest.NewRecorder()
	srv.ServeHTTP(bookRec, bookReq)
	if bookRec.Code != http.StatusOK {
		t.Fatalf("GET /book/{id} status = %d, want 200", bookRec.Code)
	}

	// 3. GET /book/{id}/state returns ready state with PDF and Video URLs
	stateReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8/state", nil)
	stateRec := httptest.NewRecorder()
	srv.ServeHTTP(stateRec, stateReq)
	if stateRec.Code != http.StatusOK {
		t.Fatalf("GET /book/{id}/state status = %d, want 200", stateRec.Code)
	}
	var state struct {
		Status   string `json:"status"`
		PDFURL   string `json:"pdf_url"`
		VideoURL string `json:"video_url"`
	}
	if err := json.Unmarshal(stateRec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if state.Status != "ready" {
		t.Fatalf("state.Status = %q, want ready", state.Status)
	}
	if state.PDFURL == "" || state.VideoURL == "" {
		t.Fatalf("state lacks URLs: pdf=%q, video=%q", state.PDFURL, state.VideoURL)
	}

	// 4. Download PDF
	pdfReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8/download/pdf", nil)
	pdfRec := httptest.NewRecorder()
	srv.ServeHTTP(pdfRec, pdfReq)
	if pdfRec.Code != http.StatusOK {
		t.Fatalf("download pdf status = %d, want 200", pdfRec.Code)
	}
	if ct := pdfRec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Fatalf("download pdf content type = %q, want application/pdf", ct)
	}
	if cd := pdfRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "bo-and-pips-moon-mango-dance.pdf") {
		t.Fatalf("download pdf content disposition = %q, want filename with slug", cd)
	}

	// 5. Download Video
	videoReq := httptest.NewRequest(http.MethodGet, "/book/d625fd608be48227f08c33cf860e5de8/download/video", nil)
	videoRec := httptest.NewRecorder()
	srv.ServeHTTP(videoRec, videoReq)
	if videoRec.Code != http.StatusOK {
		t.Fatalf("download video status = %d, want 200", videoRec.Code)
	}
	if ct := videoRec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("download video content type = %q, want video/mp4", ct)
	}
	if cd := videoRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "bo-and-pips-moon-mango-dance.mp4") {
		t.Fatalf("download video content disposition = %q, want filename with slug", cd)
	}

	// 6. Play film: GET /media/{id} directly
	mediaReq := httptest.NewRequest(http.MethodGet, state.VideoURL, nil)
	mediaRec := httptest.NewRecorder()
	srv.ServeHTTP(mediaRec, mediaReq)
	if mediaRec.Code != http.StatusOK {
		t.Fatalf("play video %s status = %d, want 200", state.VideoURL, mediaRec.Code)
	}
	if ct := mediaRec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("video content type = %q, want video/mp4", ct)
	}
	if mediaRec.Body.Len() != 3477486 {
		t.Fatalf("video bytes = %d, want 3477486", mediaRec.Body.Len())
	}
}

// TestStaticAssets_CacheControlAndFiltering verifies that staticAssets() sets
// Cache-Control: no-cache, must-revalidate on served assets to prevent edge
// and browser caching of stale code, while strictly filtering developer files.
func TestStaticAssets_CacheControlAndFiltering(t *testing.T) {
	tmp := t.TempDir()
	staticDir := filepath.Join(tmp, "static")
	if err := os.Mkdir(staticDir, 0o755); err != nil {
		t.Fatalf("mkdir static: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "app.js"), []byte("console.log('thutapi');"), 0o644); err != nil {
		t.Fatalf("write app.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "app.css"), []byte("body { margin: 0; }"), 0o644); err != nil {
		t.Fatalf("write app.css: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "browser-test.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write browser-test.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	t.Chdir(tmp)

	h := staticAssets()

	// 1. Legitimate files receive 200 and Cache-Control: no-cache, must-revalidate
	for _, asset := range []struct {
		path string
		body string
	}{
		{"/app.js", "console.log('thutapi');"},
		{"/app.css", "body { margin: 0; }"},
	} {
		req := httptest.NewRequest(http.MethodGet, asset.path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", asset.path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache, must-revalidate" {
			t.Fatalf("%s Cache-Control = %q, want 'no-cache, must-revalidate'", asset.path, got)
		}
		if rec.Body.String() != asset.body {
			t.Fatalf("%s body = %q, want %q", asset.path, rec.Body.String(), asset.body)
		}
	}

	// 2. Blocked files and missing paths return 404
	for _, blocked := range []string{"/main.go", "/browser-test.html", "/index.html", "/missing.js"} {
		req := httptest.NewRequest(http.MethodGet, blocked, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", blocked, rec.Code)
		}
	}

	// 3. Routed through server GET /static/app.js
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/static/app.js", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/static/app.js status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache, must-revalidate" {
		t.Fatalf("/static/app.js Cache-Control = %q, want 'no-cache, must-revalidate'", got)
	}
}

// TestDockerfile_OptionAPrewarmAndPermissions pins the Option A prewarm baking
// contract in Dockerfile and verifies that data/prewarm fixtures on disk are
// readable by non-root users.
func TestDockerfile_OptionAPrewarmAndPermissions(t *testing.T) {
	dockerfilePath := filepath.Join("..", "..", "Dockerfile")
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	text := string(content)

	for _, want := range []string{
		"RUN chmod -R a+rX /src/data/prewarm",
		"COPY --from=builder /src/data/prewarm /prewarm",
		"ENV PREWARM_DIR=/prewarm",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Dockerfile missing expected directive %q", want)
		}
	}

	// Walk data/prewarm on disk to verify read access for others (nonroot)
	prewarmDir := filepath.Join("..", "..", "data", "prewarm")
	count := 0
	err = filepath.Walk(prewarmDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		mode := info.Mode()
		if mode.IsDir() {
			if mode.Perm()&0o005 != 0o005 {
				return fmt.Errorf("directory %s mode %o lacks read/execute permission for others", path, mode.Perm())
			}
		} else {
			if mode.Perm()&0o004 != 0o004 {
				return fmt.Errorf("file %s mode %o lacks read permission for others", path, mode.Perm())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("prewarm permissions check failed: %v", err)
	}
	if count < 5 {
		t.Fatalf("expected prewarm fixtures to be found, got %d items", count)
	}
}

// TestParseConfig_PrewarmDir verifies that parseConfig reads PREWARM_DIR from the environment.
func TestParseConfig_PrewarmDir(t *testing.T) {
	t.Setenv("PREWARM_DIR", "/custom/prewarm")
	cfg := parseConfig()
	if cfg.prewarmDir != "/custom/prewarm" {
		t.Fatalf("cfg.prewarmDir = %q, want '/custom/prewarm'", cfg.prewarmDir)
	}

	t.Setenv("PREWARM_DIR", "")
	cfgDefault := parseConfig()
	if cfgDefault.prewarmDir != "" {
		t.Fatalf("cfgDefault.prewarmDir = %q, want empty", cfgDefault.prewarmDir)
	}
}
