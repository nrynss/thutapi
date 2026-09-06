package bookgen

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"thutapi/internal/story"
)

// sseFrame is one parsed SSE event read off the wire.
type sseFrame struct {
	event string
	data  string
}

// openSSE opens the generate events stream and returns the response
// body reader.
func openSSE(t *testing.T, ctx context.Context, srv *httptest.Server, ivID string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/interviews/"+ivID+"/generate/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}
	return resp, bufio.NewReader(resp.Body)
}

// readFrames reads SSE frames until the terminal frame (book_ready or
// failed) arrives.
func readFrames(t *testing.T, reader *bufio.Reader) []sseFrame {
	t.Helper()
	var frames []sseFrame
	var cur sseFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read stream: %v (frames so far: %+v)", err, frames)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if cur.event != "" || cur.data != "" {
				frames = append(frames, cur)
				cur = sseFrame{}
				if frames[len(frames)-1].event == "book_ready" || frames[len(frames)-1].event == "failed" {
					return frames
				}
			}
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data += strings.TrimPrefix(line, "data: ")
		}
	}
}

// TestEventsRouteStreamsSSEOverHTTP pins the full SSE surface over a
// real HTTP connection: the stream carries the page_approved events as
// pages approve and the terminal book_ready, framed with the event:
// and data: lines screen 5's EventSource reads, on the book's topic.
func TestEventsRouteStreamsSSEOverHTTP(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, bookID := ph.makeEndedInterview("Mira")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	// Open the stream first: no event can fly before the POST, so the
	// subscription is guaranteed to catch the whole run.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	_, reader := openSSE(t, ctx, srv, ivID)

	code, res, _ := ph.postGenerate(srv, ivID)
	if code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want 202", code)
	}
	if res.Topic != Topic(bookID) {
		t.Fatalf("topic = %q, want %q", res.Topic, Topic(bookID))
	}

	frames := readFrames(t, reader)
	var approved, ready, failed int
	ns := map[float64]bool{}
	for _, f := range frames {
		switch f.event {
		case "page_approved":
			approved++
			var p map[string]any
			if err := json.Unmarshal([]byte(f.data), &p); err != nil {
				t.Fatalf("decode page_approved %q: %v", f.data, err)
			}
			n, _ := p["n"].(float64)
			img, _ := p["image_url"].(string)
			if n < 1 || n > story.PageCount || ns[n] {
				t.Fatalf("page_approved n=%v out of range or duplicated (data %q)", n, f.data)
			}
			ns[n] = true
			if !strings.HasPrefix(img, "/media/") {
				t.Fatalf("page_approved image_url = %q, want /media/<id>", img)
			}
		case "book_ready":
			ready++
			var p map[string]any
			if err := json.Unmarshal([]byte(f.data), &p); err != nil {
				t.Fatalf("decode book_ready %q: %v", f.data, err)
			}
			if u, _ := p["video_url"].(string); !strings.HasPrefix(u, "/media/") {
				t.Fatalf("book_ready video_url = %q, want /media/<id>", u)
			}
		case "failed":
			failed++
		case "stage":
			// One per pipeline stage; the stage ordering is pinned by
			// TestPipeline_StagesAnnounceEveryStepInOrder, not here.
		default:
			t.Fatalf("unexpected event %q on the book topic", f.event)
		}
	}
	if approved != story.PageCount || ready != 1 || failed != 0 {
		t.Fatalf("frames = %d approved, %d ready, %d failed; want %d approved, 1 ready, 0 failed",
			approved, ready, failed, story.PageCount)
	}
}

// TestEventsRoute404 pins the events route's 404s: an unknown
// interview has no stream.
func TestEventsRoute404(t *testing.T) {
	ph := newPipelineHarness(t)
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/interviews/00000000000000000000000000000000/generate/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET events unknown interview = %d, want 404", resp.StatusCode)
	}
}

// TestMethodMismatches pins the mux-level 405s: GET on the generate
// route and POST on its events route are not the registered methods.
func TestMethodMismatches(t *testing.T) {
	ph := newPipelineHarness(t)
	ivID, _ := ph.makeEndedInterview("")
	srv := httptest.NewServer(ph.mux())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/interviews/"+ivID+"/generate", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET generate: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET generate = %d, want 405", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/interviews/"+ivID+"/generate/events", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST events: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST events = %d, want 405", resp.StatusCode)
	}
}
