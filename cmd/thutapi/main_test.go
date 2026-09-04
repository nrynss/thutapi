package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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