package text

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	msg := resp.Choices[0].Message
	if len(msg.Parts) != 2 {
		t.Fatalf("len(Parts) = %d, want 2", len(msg.Parts))
	}
	if msg.TextBody != "" {
		t.Errorf("TextBody = %q on an array-content message, want empty", msg.TextBody)
	}
	if msg.Parts[0].Text != "Once upon a time, in a house by the sea," {
		t.Errorf("Parts[0].Text = %q, want the first fixture block", msg.Parts[0].Text)
	}
	if msg.Parts[1].Text != `{"name": "Mira", "colour": "red"}` {
		t.Errorf("Parts[1].Text = %q, want the second fixture block", msg.Parts[1].Text)
	}
	flat, err := msg.Text()
	if err != nil {
		t.Fatalf("Text(): %v", err)
	}
	if flat != "Once upon a time, in a house by the sea,{\"name\": \"Mira\", \"colour\": \"red\"}" {
		t.Errorf("Text() = %q, want both blocks concatenated in order", flat)
	}
}

// TestChat_EmptyReasoningOmittedOnWire pins the raw wire bytes for the
// two Reasoning states (t2-round4.md L1): an empty Reasoning{} means
// reasoning is off, so Chat nils the pointer before marshalling and
// omitempty drops the field entirely — no thinking key reaches the
// wire — while &Reasoning{Type: "enabled"} emits
// thinking:{"type":"enabled"}. Asserted against the marshalled bytes,
// not a decoded struct, so a rename cannot silently unfix it
// (AGENTS.md §Testing rule 4).
func TestChat_EmptyReasoningOmittedOnWire(t *testing.T) {
	tests := map[string]struct {
		thinking   *Reasoning
		wantSubstr string
		wantAbsent string
	}{
		"empty Reasoning omits the field": {
			thinking:   &Reasoning{},
			wantAbsent: `"thinking"`,
		},
		"enabled emits the thinking switch": {
			thinking:   &Reasoning{Type: "enabled"},
			wantSubstr: `"thinking":{"type":"enabled"}`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var raw []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				raw = b
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(fixtureResponse))
			}))
			defer srv.Close()

			t.Setenv("GMI_API_KEY", "test-key-not-real")
			t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

			c := New()
			if _, err := c.Chat(context.Background(), ChatRequest{
				Model:    "MiniMaxAI/MiniMax-M3",
				Messages: []Message{{Role: "user", Content: "hi"}},
				Thinking: tt.thinking,
			}); err != nil {
				t.Fatalf("Chat: %v", err)
			}

			if tt.wantSubstr != "" && !strings.Contains(string(raw), tt.wantSubstr) {
				t.Errorf("wire = %s, want it to contain %s", raw, tt.wantSubstr)
			}
			if tt.wantAbsent != "" && strings.Contains(string(raw), tt.wantAbsent) {
				t.Errorf("wire = %s, want the thinking key absent", raw)
			}
		})
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

// TestChat_BadRequest_400 pins M1: a 400 must surface as gmi.ErrBadRequest,
// NOT gmi.ErrTransient, so a caller retry loop will not spin. Mirrors
// the 401 test; the body shape is identical (free-form), the only thing
// that changes is the status code.
func TestChat_BadRequest_400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"unsupported field: foo"}}`))
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "any-key")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	c := New()
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("Chat returned nil error on 400")
	}
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrBadRequest)", err)
	}
	if errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, must NOT also be errors.Is(.., gmi.ErrTransient)", err)
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

// minimalChatResponse is the smallest body a successful call can
// carry: one choice, string content.
const minimalChatResponse = `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

// chatServer builds an httptest server that counts hits and answers
// every request with status and body. It exists so the retry tests
// can script sequences and count attempts.
func chatServer(t *testing.T, hits *atomic.Int64, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestAssistantMessage_StringContentRawJSON pins the string arm of
// the content union at the raw-wire level: a plain-string content
// populates TextBody and Text() returns it (t2-round3.md L1 — before
// the fix, the union decoded into interface{} and every consumer had
// to hand-roll the type switch).
func TestAssistantMessage_StringContentRawJSON(t *testing.T) {
	const raw = `{
	  "choices": [
	    {
	      "index": 0,
	      "message": {"role": "assistant", "content": "Once upon a time"},
	      "finish_reason": "stop"
	    }
	  ]
	}`
	var hits atomic.Int64
	srv := chatServer(t, &hits, http.StatusOK, raw)

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	resp, err := New().Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msg := resp.Choices[0].Message
	if msg.TextBody != "Once upon a time" {
		t.Errorf("TextBody = %q, want the string content verbatim", msg.TextBody)
	}
	if msg.Parts != nil {
		t.Errorf("Parts = %v, want nil on the string arm", msg.Parts)
	}
	flat, err := msg.Text()
	if err != nil || flat != "Once upon a time" {
		t.Errorf("Text() = %q, %v; want the string content", flat, err)
	}
}

// TestAssistantMessage_PartArrayRawJSON pins the array arm: typed
// ContentPart values, text parts carrying their text, and a non-text
// part surviving with only its Type.
func TestAssistantMessage_PartArrayRawJSON(t *testing.T) {
	const raw = `{
	  "choices": [
	    {
	      "index": 0,
	      "message": {"role": "assistant", "content": [
	        {"type": "text", "text": "part one"},
	        {"type": "some_other_block", "payload": {"ignored": true}},
	        {"type": "text", "text": "part two"}
	      ]},
	      "finish_reason": "stop"
	    }
	  ]
	}`
	var hits atomic.Int64
	srv := chatServer(t, &hits, http.StatusOK, raw)

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	resp, err := New().Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	msg := resp.Choices[0].Message
	if len(msg.Parts) != 3 {
		t.Fatalf("len(Parts) = %d, want 3", len(msg.Parts))
	}
	if msg.Parts[0].Text != "part one" || msg.Parts[2].Text != "part two" {
		t.Errorf("text parts = %q, %q; want their block text", msg.Parts[0].Text, msg.Parts[2].Text)
	}
	if msg.Parts[1].Type != "some_other_block" {
		t.Errorf("Parts[1].Type = %q, want the non-text block preserved by type", msg.Parts[1].Type)
	}
	flat, err := msg.Text()
	if err != nil || flat != "part onepart two" {
		t.Errorf("Text() = %q, %v; want the text parts concatenated in order", flat, err)
	}
}

// TestAssistantMessage_UnusableContent: content of a third shape
// (an object) must fail loudly, and the failure is classified
// transient like every other decode failure.
func TestAssistantMessage_UnusableContent(t *testing.T) {
	const raw = `{"choices":[{"index":0,"message":{"role":"assistant","content":{"weird":true}}}]}`
	var hits atomic.Int64
	srv := chatServer(t, &hits, http.StatusOK, raw)

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	_, err := New().Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
}

// TestAssistantMessage_Text pins the helper's three outcomes: the
// string arm, the array arm, and no text at all (an error — a caller
// must not mistake an empty reply for a successful decode).
func TestAssistantMessage_Text(t *testing.T) {
	cases := []struct {
		name    string
		msg     AssistantMessage
		want    string
		wantErr bool
	}{
		{"string arm", AssistantMessage{TextBody: "hello"}, "hello", false},
		{"array arm concatenates in order", AssistantMessage{Parts: []ContentPart{{Type: "text", Text: "a"}, {Type: "text", Text: "b"}}}, "ab", false},
		{"non-text parts contribute nothing", AssistantMessage{Parts: []ContentPart{{Type: "image"}}}, "", true},
		{"no content at all", AssistantMessage{}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.msg.Text()
			if got != tc.want {
				t.Errorf("Text() = %q, want %q", got, tc.want)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("Text() err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// TestChat_Retry_5xxHitTwice pins the H2 contract on the text path:
// a 5xx is retried exactly once, so the upstream sees two hits and
// the caller one gmi.ErrTransient.
func TestChat_Retry_5xxHitTwice(t *testing.T) {
	var hits atomic.Int64
	srv := chatServer(t, &hits, http.StatusBadGateway, "upstream gone")

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	_, err := New().Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if hits.Load() != 2 {
		t.Errorf("upstream hit %d time(s) on 5xx, want 2 (one call + one retry per PLAN.md §T2)", hits.Load())
	}
}

// TestChat_PaymentRequired_402: billing gets its own sentinel on the
// text path too, and a 4xx is never retried.
func TestChat_PaymentRequired_402(t *testing.T) {
	var hits atomic.Int64
	srv := chatServer(t, &hits, http.StatusPaymentRequired, `{"error":"free window closed"}`)

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	_, err := New().Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrPaymentRequired) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrPaymentRequired)", err)
	}
	if errors.Is(err, gmi.ErrTransient) || errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("err = %v, must NOT also be errors.Is(.., gmi.ErrTransient) or errors.Is(.., gmi.ErrBadRequest)", err)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d time(s) on a 4xx, want 1 (never retry a 4xx)", hits.Load())
	}
}

// TestChatClassifyStatus_Table walks the remaining classifier arms
// through the endpoint: every 4xx lands on a non-transient sentinel
// and is hit exactly once; the 5xx and the unmapped-default arms are
// transient and retried once. The 418 and 302 rows are the arms no
// round executed before this remediation.
func TestChatClassifyStatus_Table(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		want     error
		notWant  []error
		wantHits int
	}{
		{"402 payment required", http.StatusPaymentRequired, gmi.ErrPaymentRequired, []error{gmi.ErrTransient, gmi.ErrBadRequest}, 1},
		{"404 model not found", http.StatusNotFound, gmi.ErrModelNotFound, []error{gmi.ErrTransient}, 1},
		{"413 payload too large", http.StatusRequestEntityTooLarge, gmi.ErrBadRequest, []error{gmi.ErrTransient}, 1},
		{"418 teapot is the 4xx catch-all", http.StatusTeapot, gmi.ErrBadRequest, []error{gmi.ErrTransient}, 1},
		{"429 rate limited", http.StatusTooManyRequests, gmi.ErrRateLimited, []error{gmi.ErrTransient}, 1},
		{"302 not followed is the default arm", http.StatusFound, gmi.ErrTransient, nil, 2},
		{"502 bad gateway", http.StatusBadGateway, gmi.ErrTransient, nil, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int64
			srv := chatServer(t, &hits, tc.status, `{"error":"upstream says no"}`)

			t.Setenv("GMI_API_KEY", "k")
			t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

			_, err := New().Chat(context.Background(), ChatRequest{
				Model:    "MiniMaxAI/MiniMax-M3",
				Messages: []Message{{Role: "user", Content: "hi"}},
			})
			if err == nil {
				t.Fatal("Chat returned nil error")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want errors.Is(.., %v)", err, tc.want)
			}
			for _, nw := range tc.notWant {
				if errors.Is(err, nw) {
					t.Errorf("err = %v, must NOT also be errors.Is(.., %v)", err, nw)
				}
			}
			if hits.Load() != int64(tc.wantHits) {
				t.Errorf("upstream hit %d time(s), want %d", hits.Load(), tc.wantHits)
			}
		})
	}
}

// TestChatNoRetryWhenContextDead: a caller deadline that fires
// mid-attempt must not buy a second attempt on an already-dead
// context.
func TestChatNoRetryWhenContextDead(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(800 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_TEXT_BASE_URL", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := New().Chat(ctx, ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hit %d time(s) with a dead context, want 1 (no retry on a spent context)", hits.Load())
	}
}

// chatDeadlineRecorder records the context deadline the client put on
// the outbound request — the only place the PLAN.md §T2 deadline
// default is observable without waiting out a real timeout.
type chatDeadlineRecorder struct {
	deadline time.Time
	hasOne   bool
}

func (r *chatDeadlineRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.deadline, r.hasOne = req.Context().Deadline()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(minimalChatResponse)),
		Header:     make(http.Header),
	}, nil
}

// TestChatDefaultDeadlineApplied: a caller passing context.Background()
// still gets a bounded call — the client applies defaultCallTimeout.
func TestChatDefaultDeadlineApplied(t *testing.T) {
	t.Setenv("GMI_API_KEY", "k")

	rec := &chatDeadlineRecorder{}
	c := &Client{baseURL: "http://unused.invalid", httpClient: &http.Client{Transport: rec}}
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !rec.hasOne {
		t.Fatal("outbound request carries no deadline; PLAN.md §T2 requires one on every call")
	}
	if left := time.Until(rec.deadline); left <= 0 || left > defaultCallTimeout {
		t.Errorf("deadline in %v, want within (0, %v]", left, defaultCallTimeout)
	}
}

// TestChatCallerDeadlineRespected: a caller-supplied deadline wins —
// the default must not extend it.
func TestChatCallerDeadlineRespected(t *testing.T) {
	t.Setenv("GMI_API_KEY", "k")

	rec := &chatDeadlineRecorder{}
	c := &Client{baseURL: "http://unused.invalid", httpClient: &http.Client{Transport: rec}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.Chat(ctx, ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !rec.hasOne {
		t.Fatal("outbound request carries no deadline")
	}
	if left := time.Until(rec.deadline); left <= 0 || left > 5*time.Second {
		t.Errorf("deadline in %v, want within the caller's 5s, not the %v default", left, defaultCallTimeout)
	}
}
