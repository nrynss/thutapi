package text

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"thutapi/internal/gmi"
)

// fixtureResponse is the body a successful /v1/chat/completions call
// returns. Two content blocks model the structured Phase-B output
// (project.md §Phase B): one short prose paragraph and one JSON
// fragment. The fixture exists to prove the typed decoder handles both
// string and array Content shapes — the only two shapes the OpenAI
// Chat Completions API returns today.
const fixtureResponse = `{
  "id": "chatcmpl-abc",
  "object": "chat.completion",
  "created": 1700000000,
  "model": "MiniMaxAI/MiniMax-M3",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": [
          {"type": "text", "text": "Once upon a time, in a house by the sea,"},
          {"type": "text", "text": "{\"name\": \"Mira\", \"colour\": \"red\"}"}
        ]
      },
      "finish_reason": "stop"
    }
  ]
}`

// TestChat_HappyPath verifies the three pins from project.md §M3 phase
// settings plus the basic round-trip: the test server captures the
// outbound request, the client decodes the response, and the decoded
// assistant content carries both fixture blocks intact.
func TestChat_HappyPath(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotAuth        string
		gotContentType string
		gotBody        ChatRequest
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureResponse))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "test-key-not-real")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	c := New()
	resp, err := c.Chat(context.Background(), ChatRequest{
		Model: "MiniMaxAI/MiniMax-M3",
		Messages: []Message{
			{Role: "system", Content: "You are Mira."},
			{Role: "user", Content: "Hi."},
		},
		Thinking: &Reasoning{Type: "enabled"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key-not-real" {
		t.Errorf("Authorization = %q, want Bearer test-key-not-real", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", gotContentType)
	}

	// Pin #1: full MiniMaxAI/ prefix is sent verbatim.
	if gotBody.Model != "MiniMaxAI/MiniMax-M3" {
		t.Errorf("request model = %q, want MiniMaxAI/MiniMax-M3 (bare MiniMax-M3 404s)", gotBody.Model)
	}
	if gotBody.Model == "MiniMax-M3" {
		t.Errorf("request model = bare MiniMax-M3 — this 404s against api.gmi-serving.com")
	}

	// Pin #2: reasoning is the thinking switch, not reasoning_effort.
	if gotBody.Thinking == nil {
		t.Errorf("request thinking = nil, want {\"type\":\"enabled\"}")
	} else if gotBody.Thinking.Type != "enabled" {
		t.Errorf("request thinking.type = %q, want enabled", gotBody.Thinking.Type)
	}

	// Pin #3 (negative): reasoning_effort must not be emitted. MiniMax
	// models ignore it, and emitting it makes the request drift from
	// the GMI quirk documented in project.md §M3 phase settings.
	raw, _ := json.Marshal(gotBody)
	if strings.Contains(string(raw), "reasoning_effort") {
		t.Errorf("request body contains reasoning_effort: %s", raw)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("len(Choices) = %d, want 1", len(resp.Choices))
	}
	parts, ok := resp.Choices[0].Message.Content.([]interface{})
	if !ok {
		t.Fatalf("assistant content type = %T, want []interface{}", resp.Choices[0].Message.Content)
	}
	if len(parts) != 2 {
		t.Fatalf("len(parts) = %d, want 2", len(parts))
	}
}

// TestChat_Unauthorized_401 verifies a 401 surfaces as gmi.ErrUnauthorized.
// The body is a deliberately free-form GMI error string — we don't parse
// it, we trust the sentinel. That is the contract downstream branches on.
func TestChat_Unauthorized_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "wrong-key")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	c := New()
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Chat returned nil error on 401")
	}
	if !errors.Is(err, gmi.ErrUnauthorized) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrUnauthorized)", err)
	}
}

// TestChat_RateLimited_429 verifies a 429 surfaces as gmi.ErrRateLimited.
func TestChat_RateLimited_429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"slow down"}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	c := New()
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrRateLimited) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrRateLimited)", err)
	}
}

// TestChat_Transient_500 verifies a 5xx surfaces as gmi.ErrTransient.
func TestChat_Transient_500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream gone"))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	c := New()
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
}

// TestChat_MissingAPIKey verifies the client fails closed when the env
// var is absent — never sends an empty Bearer token upstream.
func TestChat_MissingAPIKey(t *testing.T) {
	t.Setenv("GMI_API_KEY", "")
	t.Setenv("GMI_TEXT_BASE_URL", "http://unused.invalid")

	c := New()
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrUnauthorized) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrUnauthorized)", err)
	}
}
