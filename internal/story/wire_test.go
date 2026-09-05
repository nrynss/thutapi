package story

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"thutapi/internal/gmi/text"
)

// jsonString marshals s as a JSON string literal for embedding in a
// canned completion body.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal reply: %v", err)
	}
	return string(b)
}

// completionBody wraps a reply in the OpenAI-compatible response
// shape the text client decodes.
func completionBody(reply string) string {
	return fmt.Sprintf(`{"choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`,
		reply)
}

// TestStructureThinkingOnRawWire is the §T5 wire pin: the raw request
// bytes must carry thinking:{"type":"enabled"} (reasoning is ON for
// the one Phase-B call) and the full MiniMaxAI/MiniMax-M3 model id.
// Asserted against the marshalled bytes, so neither a rename nor a
// dropped field can pass silently.
func TestStructureThinkingOnRawWire(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		mu.Lock()
		n := len(bodies)
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// First answer broken, second corrected: both requests ride
		// this wire, so the thinking pin below holds for the
		// corrective retry too.
		if n == 0 {
			fmt.Fprint(w, completionBody(jsonString(t, badReply)))
			return
		}
		fmt.Fprint(w, completionBody(jsonString(t, goodReply)))
	}))
	defer upstream.Close()

	t.Setenv("GMI_TEXT_BASE_URL", upstream.URL)
	t.Setenv("GMI_API_KEY", "test-key-not-real")

	if _, err := Structure(context.Background(), text.New(), transcript); err != nil {
		t.Fatalf("Structure() = %v, want nil", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("calls = %d, want 2 (initial plus corrective retry)", len(bodies))
	}
	for i, body := range bodies {
		if !bytes.Contains(body, []byte(`"thinking":{"type":"enabled"}`)) {
			t.Errorf("call %d wire = %s\nwant thinking:{\"type\":\"enabled\"} verbatim (reasoning is ON for the Phase-B call)", i+1, body)
		}
		if !bytes.Contains(body, []byte(`"model":"MiniMaxAI/MiniMax-M3"`)) {
			t.Errorf("call %d wire = %s\nwant the full prefixed model id verbatim", i+1, body)
		}
	}
	// A fragment of the system prompt that survives JSON encoding
	// verbatim (no newlines, no quotes): the interpolated emotion
	// vocabulary must reach the wire.
	if !bytes.Contains(bodies[0], []byte("exactly one of happy, sad, angry, fearful, disgusted, surprised, calm")) {
		t.Errorf("initial wire = %s\nwant the emotion vocabulary from the system prompt present", bodies[0])
	}
	if !bytes.Contains(bodies[1], []byte("not a valid story")) {
		t.Errorf("retry wire = %s\nwant the corrective system message present", bodies[1])
	}
}
