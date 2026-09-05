package interview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/text"
	"thutapi/internal/job"
	"thutapi/internal/store"
	"thutapi/internal/stream"
)

// muxFor registers the interview routes exactly the way newServer in
// cmd/thutapi does (PLAN.md invariant 5) — the tests exercise the real
// seam, not a private shortcut.
func muxFor(t *testing.T, h *Handler) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /interviews", h.Start)
	mux.HandleFunc("GET /interviews/{id}", h.Transcript)
	mux.HandleFunc("GET /interviews/{id}/events", h.Events)
	mux.HandleFunc("POST /interviews/{id}/answers", h.Answer)
	return mux
}

// serve runs mux on an httptest server for the life of the test.
func serve(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// getJSON sends one GET and decodes the JSON body.
func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response from %s: %v", url, err)
	}
	return resp.StatusCode, out
}

// ---------------------------------------------------------------------------
// New: dependency validation and defaults.
// ---------------------------------------------------------------------------

func TestNewRejectsNilDependencies(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string // the named missing dependency
	}{
		{
			name: "nil Chat",
			cfg:  Config{Store: &testStore{}, Broker: stream.New(stream.Config{}), Jobs: job.New(stream.New(stream.Config{}))},
			want: "Chat",
		},
		{
			name: "nil Store",
			cfg:  Config{Chat: &scriptedChatter{}, Broker: stream.New(stream.Config{}), Jobs: job.New(stream.New(stream.Config{}))},
			want: "Store",
		},
		{
			name: "nil Broker",
			cfg:  Config{Chat: &scriptedChatter{}, Store: &testStore{}, Jobs: job.New(stream.New(stream.Config{}))},
			want: "Broker",
		},
		{
			name: "nil Jobs",
			cfg:  Config{Chat: &scriptedChatter{}, Store: &testStore{}, Broker: stream.New(stream.Config{})},
			want: "Jobs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("err = %v, want it to wrap ErrNotConfigured", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to name the missing dependency %q", err, tt.want)
			}
		})
	}
}

func TestConfigWithDefaults(t *testing.T) {
	tests := []struct {
		name string
		max  int
		want int
	}{
		{"zero means the default", 0, defaultMaxTurns},
		{"negative means the default", -3, defaultMaxTurns},
		{"explicit wins", 5, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{MaxTurns: tt.max}.withDefaults()
			if cfg.MaxTurns != tt.want {
				t.Fatalf("MaxTurns = %d, want %d", cfg.MaxTurns, tt.want)
			}
			if cfg.Log == nil {
				t.Fatal("Log = nil, want a discarded logger")
			}
		})
	}
}

func TestTopicFormat(t *testing.T) {
	if got, want := Topic("abc123"), "interview:abc123"; got != want {
		t.Fatalf("Topic = %q, want %q", got, want)
	}
	if got, want := TopicPrefix, "interview:"; got != want {
		t.Fatalf("TopicPrefix = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Core error sentinels, asserted directly (invariant 8).
// ---------------------------------------------------------------------------

func TestAnswerSentinels(t *testing.T) {
	h, _, _, _ := newHarness(t, checklistScript)
	res, err := h.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 1)

	// Blank answer text.
	if _, err := h.answer(t.Context(), res.ID, "   "); !errors.Is(err, ErrEmptyAnswer) {
		t.Fatalf("err = %v, want it to wrap ErrEmptyAnswer", err)
	}
	// Unknown interview.
	if _, err := h.answer(t.Context(), "00000000000000000000000000000000", "hello"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want it to wrap store.ErrNotFound", err)
	}
	// Drive the run to its end, waiting for each turn to finish so no
	// ErrBusy masks the sentinel under test.
	for i, answer := range checklistAnswers {
		if _, err := h.answer(t.Context(), res.ID, answer); err != nil {
			t.Fatalf("answer %d: %v", i+1, err)
		}
		waitTranscriptTurn(t, h, res.ID, 2*i+3)
	}
	if _, err := h.answer(t.Context(), res.ID, "more"); !errors.Is(err, ErrEnded) {
		t.Fatalf("err = %v, want it to wrap ErrEnded", err)
	}
	// Transcript catch-up on an unknown id.
	if _, err := h.transcript(t.Context(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want it to wrap store.ErrNotFound", err)
	}
}

func TestAssistantTextClassifiesDegenerateRepliesAsTransient(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		_, err := assistantText(nil)
		if !errors.Is(err, gmi.ErrTransient) {
			t.Fatalf("err = %v, want it to wrap gmi.ErrTransient", err)
		}
	})
	t.Run("empty choice list", func(t *testing.T) {
		_, err := assistantText(&text.ChatResponse{})
		if !errors.Is(err, gmi.ErrTransient) {
			t.Fatalf("err = %v, want it to wrap gmi.ErrTransient", err)
		}
	})
	t.Run("choice carries no text", func(t *testing.T) {
		resp := &text.ChatResponse{Choices: []text.Choice{{Message: text.AssistantMessage{}}}}
		_, err := assistantText(resp)
		if !errors.Is(err, gmi.ErrTransient) {
			t.Fatalf("err = %v, want it to wrap gmi.ErrTransient", err)
		}
	})
	t.Run("a normal reply comes back as text", func(t *testing.T) {
		resp := &text.ChatResponse{Choices: []text.Choice{{
			Message: text.AssistantMessage{TextBody: "What colour is the dragon?"},
		}}}
		got, err := assistantText(resp)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got != "What colour is the dragon?" {
			t.Fatalf("text = %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// HTTP status mapping.
// ---------------------------------------------------------------------------

func TestHTTPStatusMapping(t *testing.T) {
	h, _, _, _ := newHarness(t, []string{"Who is your hero?\n[[filled:]]"})
	srv := serve(t, muxFor(t, h))
	res, err := h.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 1)

	tests := []struct {
		name      string
		method    string
		url       string
		body      string
		want      int
		wantClass string
	}{
		{
			name:      "answer unknown interview is 404",
			method:    http.MethodPost,
			url:       "/interviews/00000000000000000000000000000000/answers",
			body:      `{"text":"hi"}`,
			want:      404,
			wantClass: "not_found",
		},
		{
			name:      "answer with a broken body is 400",
			method:    http.MethodPost,
			url:       "/interviews/" + res.ID + "/answers",
			body:      `{not json`,
			want:      400,
			wantClass: "invalid",
		},
		{
			name:      "answer with an empty text is 400",
			method:    http.MethodPost,
			url:       "/interviews/" + res.ID + "/answers",
			body:      `{"text":"  "}`,
			want:      400,
			wantClass: "invalid",
		},
		{
			name:      "transcript unknown interview is 404",
			method:    http.MethodGet,
			url:       "/interviews/00000000000000000000000000000000",
			want:      404,
			wantClass: "not_found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+tt.url, strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error != tt.wantClass {
				t.Fatalf("error body = %q, want the machine class %q", body.Error, tt.wantClass)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Transcript route.
// ---------------------------------------------------------------------------

func TestTranscriptRouteServesTheCatchUpShape(t *testing.T) {
	h, _, _, _ := newHarness(t, []string{"Who is your hero?\n[[filled:]]"})
	srv := serve(t, muxFor(t, h))

	res, err := h.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 1)

	code, body := getJSON(t, srv.URL+"/interviews/"+res.ID)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["id"] != res.ID {
		t.Fatalf("id = %v, want %q", body["id"], res.ID)
	}
	if body["book_id"] != res.BookID {
		t.Fatalf("book_id = %v, want %q", body["book_id"], res.BookID)
	}
	if body["status"] != statusOpen {
		t.Fatalf("status = %v, want %q", body["status"], statusOpen)
	}
	turns, ok := body["turns"].([]any)
	if !ok || len(turns) != 1 {
		t.Fatalf("turns = %v, want one", body["turns"])
	}
	first, _ := turns[0].(map[string]any)
	if first["role"] != RoleInterviewer || first["text"] != "Who is your hero?" {
		t.Fatalf("turn[0] = %v, want the opening question with the control line stripped", first)
	}
	if _, ok := body["created_at"]; !ok {
		t.Fatal("created_at missing from the catch-up body")
	}
}

// ---------------------------------------------------------------------------
// Events route: real SSE over HTTP.
// ---------------------------------------------------------------------------

func TestEventsRouteStreamsSSEOverHTTP(t *testing.T) {
	h, chat, _, _ := newHarness(t, []string{"Who is your hero?\n[[filled:]]"})
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate
	srv := serve(t, muxFor(t, h))

	// Start while the opening turn is gated: no event can fly past.
	res, err := h.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Open the SSE stream, then release the gate.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/interviews/"+res.ID+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}

	gate <- struct{}{}

	// Read the first SSE frame off the wire.
	reader := bufio.NewReader(resp.Body)
	var eventLine, dataLine string
	for eventLine == "" || dataLine == "" {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			eventLine = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLine = strings.TrimPrefix(line, "data: ")
		}
	}
	if eventLine != "question" {
		t.Fatalf("first event = %q, want question", eventLine)
	}
	var payload questionEvent
	if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
		t.Fatalf("decode data %q: %v", dataLine, err)
	}
	if payload.Turn != 1 || payload.Text != "Who is your hero?" {
		t.Fatalf("payload = %+v, want turn 1 with the stripped question", payload)
	}
}
