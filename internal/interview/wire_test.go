package interview

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"thutapi/internal/gmi/text"
)

// TestInterviewTurnsOmitThinkingOnRawWire is this track's raw-wire pin
// (AGENTS.md §Testing rule 4). Interview calls are phase-A calls:
// reasoning stays OFF (project.md §M3 phase settings), so the request
// body must carry the full MiniMaxAI/MiniMax-M3 model id, NO thinking
// field at all, and the system prompt as the opening message. Asserted
// against the raw bytes the loop actually put on the wire — through
// the real internal/gmi/text client, not a fake — so neither a struct
// rename nor a new field can silently reintroduce thinking.
func TestInterviewTurnsOmitThinkingOnRawWire(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"And who tags along?\n[[filled: hero]]",
		"Bye!\n[[filled: hero, companion; end]]",
	}

	var mu sync.Mutex
	var bodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		i := len(bodies)
		bodies = append(bodies, b)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":`+
			strconv.Quote(script[min(i, len(script)-1)])+`}}]}`)
	}))
	defer upstream.Close()

	// The text client reads its base URL and key from the environment
	// (T2's recorded deviation) — the pin follows that default path.
	t.Setenv("GMI_TEXT_BASE_URL", upstream.URL)
	t.Setenv("GMI_API_KEY", "test-key-not-real")

	h, _, _, _ := newHarness(t, script)
	// Swap the fake chatter for the real client: everything else stays
	// the fake-backed default path.
	h.chat = text.New()

	res, err := h.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 1)
	if _, err := h.answer(t.Context(), res.ID, "a girl called Mira"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 3)
	if _, err := h.answer(t.Context(), res.ID, "a dragon named Spark"); err != nil {
		t.Fatalf("answer 2: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 5)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("captured %d request bodies, want 3", len(bodies))
	}
	for i, body := range bodies {
		if bytes.Contains(body, []byte("thinking")) {
			t.Errorf("call %d wire = %s\nwant: no thinking field anywhere (reasoning is OFF for interview calls)", i+1, body)
		}
		if !bytes.Contains(body, []byte(`"model":"MiniMaxAI/MiniMax-M3"`)) {
			t.Errorf("call %d wire = %s\nwant the full MiniMaxAI/ prefixed model id", i+1, body)
		}
		var wire struct {
			Model    string          `json:"model"`
			Thinking json.RawMessage `json:"thinking"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatalf("call %d: decode %q: %v", i+1, body, err)
		}
		if wire.Thinking != nil {
			t.Errorf("call %d: thinking key present (%s), want absent", i+1, wire.Thinking)
		}
		if len(wire.Messages) == 0 || wire.Messages[0].Role != "system" {
			t.Errorf("call %d: messages[0] = %+v, want the system prompt first", i+1, wire.Messages)
		}
		if len(wire.Messages) == 0 || !strings.Contains(wire.Messages[0].Content, "ONE question per turn") {
			t.Errorf("call %d: the house-style system prompt did not lead the call: %q", i+1, wire.Messages[0].Content)
		}
	}
	// The second call's history ends with the child's answer, in
	// order: system, question, answer.
	var second struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(bodies[1], &second); err != nil {
		t.Fatalf("decode second body: %v", err)
	}
	if got, want := second.Messages[len(second.Messages)-1], (struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{Role: "user", Content: "a girl called Mira"}); got.Role != want.Role || got.Content != want.Content {
		t.Fatalf("second call's last message = %+v, want %+v (the transcript goes in, in order)", got, want)
	}
}
