package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/audio"
	"thutapi/internal/gate"
	"thutapi/internal/mediastore"
	"thutapi/internal/store"
)

// --- T11 item 1: the gate, at the route wrap ------------------------------

// tightGate is a gate whose generate rule is exhausted by the second
// request, so a route test can observe a real refusal without pressing a
// $0.35 button six times.
func tightGate(t *testing.T, cfg gate.Config) *gate.Gate {
	t.Helper()
	g, err := gate.New(cfg)
	if err != nil {
		t.Fatalf("build gate: %v", err)
	}
	return g
}

// TestGateRefusesTheGenerateRouteOverBudget drives the wired route, not
// the middleware in isolation: the point of PLAN.md §T11 item 1 is that
// POST /interviews/{id}/generate is the one that cannot be pressed
// without limit.
func TestGateRefusesTheGenerateRouteOverBudget(t *testing.T) {
	srv := newTestServer(t)
	// The generate rule's per-client burst is 3. The fourth press from
	// one client must be refused before it reaches the handler.
	var last *httptest.ResponseRecorder
	for range generateRule.PerClient.Burst + 1 {
		last = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/interviews/00000000000000000000000000000000/generate", nil)
		srv.ServeHTTP(last, req)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status after the burst = %d, want 429", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatal("a 429 with no Retry-After leaves the client guessing")
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(last.Body.Bytes(), &body); err != nil {
		t.Fatalf("refusal body %q is not the {\"error\":...} envelope app.js reads: %v", last.Body.String(), err)
	}
	if body.Error != "rate_limited" {
		t.Fatalf("refusal error = %q, want rate_limited", body.Error)
	}
}

// TestGateLeavesTheShareableRoutesAlone is the other half of item 1: a
// judge must be able to open and share a book, and the box's monitoring
// must be able to hit /healthz, with the gate fully exhausted.
func TestGateLeavesTheShareableRoutesAlone(t *testing.T) {
	// A gate with a passcode nobody supplies AND a budget of nothing:
	// if an ungated route were accidentally wrapped, this refuses it.
	srv := newGatedTestServer(t, tightGate(t, gate.Config{Passcode: "locked"}))

	// Exhaust nothing — the passcode alone refuses every gated route.
	gatedReq := httptest.NewRequest(http.MethodPost, "/interviews", strings.NewReader(`{}`))
	gatedReq.Header.Set("Content-Type", "application/json")
	gated := httptest.NewRecorder()
	srv.ServeHTTP(gated, gatedReq)
	if gated.Code != http.StatusForbidden {
		t.Fatalf("POST /interviews with no passcode = %d, want 403", gated.Code)
	}

	ungated := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"healthz", http.MethodGet, "/healthz", http.StatusOK},
		{"shelf", http.MethodGet, "/", http.StatusOK},
		// staticAssets serves http.Dir("static"), which is relative to
		// the working directory — cmd/thutapi under `go test`, where
		// there is no static tree. A 404 is therefore the file server's
		// own answer, and the assertion that matters is the one above
		// it: not 403, not 429, so the route is not gated.
		{"static asset", http.MethodGet, "/static/app.css", http.StatusNotFound},
		// An unknown id is a 404 from the handler, which is still proof
		// the gate did not answer first: a gated route would be 403.
		{"media", http.MethodGet, "/media/00000000000000000000000000000000", http.StatusNotFound},
		{"book page", http.MethodGet, "/book/00000000000000000000000000000000", http.StatusNotFound},
		{"book state", http.MethodGet, "/book/00000000000000000000000000000000/state", http.StatusNotFound},
		{"book download", http.MethodGet, "/book/00000000000000000000000000000000/download/pdf", http.StatusNotFound},
		{"interview transcript", http.MethodGet, "/interviews/00000000000000000000000000000000", http.StatusNotFound},
	}
	for _, tc := range ungated {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code == http.StatusForbidden || w.Code == http.StatusTooManyRequests {
				t.Fatalf("%s was gated: status %d", tc.path, w.Code)
			}
			if w.Code != tc.want {
				t.Fatalf("%s status = %d, want %d", tc.path, w.Code, tc.want)
			}
		})
	}
}

// TestGatePasscodeAdmitsALegitimateRequest is the "and actually lets a
// legitimate request through" half. The passcode is supplied and the
// request reaches the interview handler, which answers 201.
func TestGatePasscodeAdmitsALegitimateRequest(t *testing.T) {
	srv := newGatedTestServer(t, tightGate(t, gate.Config{Passcode: "moon-mango"}))
	req := httptest.NewRequest(http.MethodPost, "/interviews", strings.NewReader(`{"byline":"Bo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Thutapi-Passcode", "moon-mango")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /interviews with the passcode = %d, want 201: %s", w.Code, w.Body.String())
	}
}

// TestVoiceSampleRouteKeepsItsOwnBearerAuth: the gate is added in front
// of T13's UPLOAD_TOKEN check (its TODO(§T11) asked for a rate limit,
// not a replacement), so an authorized-looking request with no token is
// still a 401 from the handler behind the gate.
func TestVoiceSampleRouteKeepsItsOwnBearerAuth(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/voice-sample", strings.NewReader("not audio"))
	req.Header.Set("Content-Type", "audio/mpeg")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("POST /voice-sample without the bearer = %d, want 401 from T13's own auth", w.Code)
	}
}

// TestVoiceSampleRouteIsRateLimited is what the TODO(§T11) in
// internal/audio/clone.go asked for: one token holder can no longer
// drive unbounded 5 MiB transcodes.
func TestVoiceSampleRouteIsRateLimited(t *testing.T) {
	srv := newTestServer(t)
	var last *httptest.ResponseRecorder
	for range voiceSampleRule.PerClient.Burst + 1 {
		last = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/voice-sample", strings.NewReader("not audio"))
		req.Header.Set("Content-Type", "audio/mpeg")
		req.Header.Set("Authorization", "Bearer test-upload-token")
		req.Header.Set("X-Voice-Sample-Consent", "yes")
		srv.ServeHTTP(last, req)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("status after the voice-sample burst = %d, want 429", last.Code)
	}
}

// TestGatedRoutesStillSeePathValues pins that wrapping a handler does
// not cost it the mux's path wildcards — the generate handler reads
// {id} out of the request the gate forwarded.
func TestGatedRoutesStillSeePathValues(t *testing.T) {
	srv := newTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/interviews/00000000000000000000000000000000/generate", nil))
	if w.Code == http.StatusTooManyRequests || w.Code == http.StatusForbidden {
		t.Fatalf("first request was gated: %d", w.Code)
	}
	// The id is unknown, so the handler's own answer is a 404 — which it
	// can only produce by having read the path value.
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 from the generate handler", w.Code)
	}
}

func TestNewServerRejectsANilGate(t *testing.T) {
	if _, err := newServer(nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("newServer accepted a nil gate; an ungated generate route is the open wallet §T11 closes")
	}
}

// --- T11 item 4: the retention sweep next to T13's expiry -----------------

func TestResolveMediaMaxBytes(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		env  string
		want int64
	}{
		{"unset falls back to the package default", false, "", mediastore.DefaultMaxBytes},
		{"empty falls back to the package default", true, "", mediastore.DefaultMaxBytes},
		{"garbage falls back to the package default", true, "six gigs", mediastore.DefaultMaxBytes},
		{"negative falls back to the package default", true, "-1", mediastore.DefaultMaxBytes},
		{"zero means unbounded", true, "0", mediastore.Unbounded},
		{"an explicit budget wins", true, "12345", 12345},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("MEDIA_MAX_BYTES", tc.env)
			} else {
				t.Setenv("MEDIA_MAX_BYTES", "")
				os.Unsetenv("MEDIA_MAX_BYTES")
			}
			if got := resolveMediaMaxBytes(); got != tc.want {
				t.Fatalf("resolveMediaMaxBytes() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseConfigReadsGatePasscode(t *testing.T) {
	t.Setenv("GATE_PASSCODE", "moon-mango")
	if got := parseConfig().gatePasscode; got != "moon-mango" {
		t.Fatalf("gatePasscode = %q, want moon-mango", got)
	}
}

// stubFFmpeg stands in for the shipping static binary. Run writes the
// caller's output path (the last argument of T13's fixed argument list)
// so the transcode's post-conditions — a non-empty file under the cap —
// hold without a real encode. No ffprobe anywhere: the project forbids
// it in production code and this is the test side of the same rule.
type stubFFmpeg struct {
	duration time.Duration
	output   []byte
}

func (s stubFFmpeg) Run(_ context.Context, _ string, args ...string) error {
	return os.WriteFile(args[len(args)-1], s.output, 0o600)
}

func (s stubFFmpeg) Duration(context.Context, string, string) (time.Duration, error) {
	return s.duration, nil
}

type okVoiceCloner struct{}

func (okVoiceCloner) CloneVoice(context.Context, audio.VoiceCloneRequest) (audio.VoiceCloneResult, error) {
	return audio.VoiceCloneResult{VoiceID: "voice-clone-1"}, nil
}

// testClock is a settable clock for the voice sample's 15-minute
// lifetime.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// TestRetentionSweepAndVoiceSampleExpiryCoexist is the T11/T13 boundary
// proved end to end against the real mediastore and the real
// VoiceSampleHandler, wired the way run() wires them.
//
// A voice sample is an unplaced media row like every spoken interview
// question, so a retention sweep that classified unplaced rows by age
// alone would eventually delete one out from under the 15-minute
// promise the consent copy makes to a parent. Two claims are pinned:
//
//  1. The sweep, run with a clock three days in the future, does not
//     touch a sample T13 still tracks.
//  2. T13's own expiry still removes it on time afterwards.
func TestRetentionSweepAndVoiceSampleExpiryCoexist(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(dir, "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	blobs, err := mediastore.Open(t.Context(), mediastore.Config{Dir: filepath.Join(dir, "media"), DB: db})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	clock := &testClock{at: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}
	voiceSamples, err := audio.NewVoiceSampleHandler(audio.VoiceSampleConfig{
		Store:        blobs,
		PublicOrigin: "https://thutapi.nryn.dev",
		TempDir:      t.TempDir(),
		ExpiryFile:   filepath.Join(dir, "voice-sample-expiry.json"),
		UploadToken:  "test-upload-token",
		Cloner:       okVoiceCloner{},
		Runner:       stubFFmpeg{duration: 8 * time.Second, output: []byte("ID3 pretend mp3 bytes")},
		Now:          clock.now,
	})
	if err != nil {
		t.Fatalf("build voice sample handler: %v", err)
	}
	t.Cleanup(voiceSamples.Close)

	// A parent records a sample.
	req := httptest.NewRequest(http.MethodPost, "/voice-sample", strings.NewReader("pretend webm bytes"))
	req.Header.Set("Content-Type", "audio/webm")
	req.Header.Set("Authorization", "Bearer test-upload-token")
	req.Header.Set("X-Voice-Sample-Consent", "yes")
	w := httptest.NewRecorder()
	voiceSamples.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /voice-sample = %d, want 201: %s", w.Code, w.Body.String())
	}
	var uploaded audio.VoiceSampleResponse
	if err := json.Unmarshal(w.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	sampleID := uploaded.MediaURL[strings.LastIndex(uploaded.MediaURL, "/")+1:]
	if !voiceSamples.Tracked(sampleID) {
		t.Fatal("the handler does not report the sample it just persisted as tracked")
	}

	// A spoken interview question, for contrast: the same unplaced shape,
	// owned by nobody.
	question, err := blobs.Persist(t.Context(), strings.NewReader("question audio"), "audio/mpeg")
	if err != nil {
		t.Fatalf("persist question audio: %v", err)
	}

	// Claim 1. Three days in the future, with the veto wired exactly as
	// run() wires it.
	sweeper, err := blobs.NewSweeper(mediastore.RetentionConfig{
		Now:    func() time.Time { return time.Now().Add(72 * time.Hour) },
		Retain: voiceSamples.Tracked,
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}
	result, err := sweeper.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if result.Retained != 1 {
		t.Fatalf("Retained = %d, want 1 (the live voice sample)", result.Retained)
	}
	if result.UnplacedDeleted != 1 {
		t.Fatalf("UnplacedDeleted = %d, want 1 (the question clip)", result.UnplacedDeleted)
	}
	if _, err := db.Media(t.Context(), question); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the question clip survived the sweep: %v", err)
	}
	if _, err := db.Media(t.Context(), sampleID); err != nil {
		t.Fatalf("the sweep deleted a live voice sample: %v", err)
	}
	// It still serves, still no-store, while it is alive.
	serveReq := httptest.NewRequest(http.MethodGet, "/media/"+sampleID, nil)
	serveReq.SetPathValue("id", sampleID)
	served := httptest.NewRecorder()
	if !voiceSamples.ServeMedia(served, serveReq, blobs) {
		t.Fatal("ServeMedia declined a sample it tracks")
	}
	if served.Code != http.StatusOK {
		t.Fatalf("serving a live sample = %d, want 200", served.Code)
	}
	if got := served.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}

	// Claim 2. T13's own 15 minutes still expire it, and the blob goes.
	clock.advance(16 * time.Minute)
	expiredReq := httptest.NewRequest(http.MethodGet, "/media/"+sampleID, nil)
	expiredReq.SetPathValue("id", sampleID)
	expired := httptest.NewRecorder()
	voiceSamples.ServeMedia(expired, expiredReq, blobs)
	if expired.Code != http.StatusNotFound {
		t.Fatalf("serving an expired sample = %d, want 404", expired.Code)
	}
	if _, err := db.Media(t.Context(), sampleID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the expired sample's row survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "media", sampleID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the expired sample's blob survived: %v", err)
	}
}
