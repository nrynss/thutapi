package stream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// readUntil reads from body until pred holds of the accumulated bytes,
// returning what was read. It fails the test if the stream ends or the
// timeout fires first. Predicates look at CONTENT (which event, in what
// order), never at event counts — a count-based read cannot tell a ping
// from a progress event, which is exactly the race that made this
// helper's first version flaky.
func readUntil(t *testing.T, body io.Reader, pred func(string) bool, timeout time.Duration) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 256)
		for !pred(sb.String()) {
			k, err := body.Read(buf)
			sb.Write(buf[:k])
			if err != nil {
				got <- sb.String()
				return
			}
		}
		got <- sb.String()
	}()
	select {
	case s := <-got:
		if !pred(s) {
			t.Fatalf("stream ended before the expected traffic: %q", s)
		}
		return s
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for the expected traffic")
		return ""
	}
}

// readN reads until n SSE-terminated events — blank-line terminated —
// have been seen.
func readN(t *testing.T, body io.Reader, n int, timeout time.Duration) string {
	t.Helper()
	return readUntil(t, body, func(s string) bool {
		return strings.Count(s, "\n\n") >= n
	}, timeout)
}

// serve spins up a real HTTP server streaming topic from b and returns
// the server and the response. The caller owns the server's Close and the
// response body's Close.
func serve(t *testing.T, b *Broker, topic string, clientTimeout time.Duration) (*httptest.Server, *http.Response) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.ServeTopic(w, r, topic)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	client := &http.Client{Timeout: clientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return srv, resp
}

// TestServeTopicHeadersAndFraming pins the SSE surface: the three headers
// from PLAN.md §T1, a 200, and the exact wire framing — event field,
// data lines split on line breaks (including a normalised CR), blank-line
// terminator.
func TestServeTopicHeadersAndFraming(t *testing.T) {
	b := New(Config{Heartbeat: time.Hour}) // no heartbeat noise in the framing assertions
	_, resp := serve(t, b, "frame", 5*time.Second)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}

	b.Publish("frame", Event{Name: "progress", Data: "page 1\npage 2"})
	if got, want := readN(t, resp.Body, 1, 5*time.Second), "event: progress\ndata: page 1\ndata: page 2\n\n"; got != want {
		t.Errorf("framed event = %q, want %q", got, want)
	}

	// A bare CR must normalise to a line break before the split, or a
	// client rejoining data lines would splice "a\rb" into one line.
	b.Publish("frame", Event{Name: "cr", Data: "a\r\nb"})
	if got, want := readN(t, resp.Body, 1, 5*time.Second), "event: cr\ndata: a\ndata: b\n\n"; got != want {
		t.Errorf("framed CR event = %q, want %q", got, want)
	}
}

// TestServeTopicUnnamedEventOmitsEventField: an Event with no Name frames
// as data-only, which SSE clients deliver as "message".
func TestServeTopicUnnamedEventOmitsEventField(t *testing.T) {
	b := New(Config{Heartbeat: time.Hour})
	_, resp := serve(t, b, "anon", 5*time.Second)
	defer resp.Body.Close()

	b.Publish("anon", Event{Data: "x"})
	if got, want := readN(t, resp.Body, 1, 5*time.Second), "data: x\n\n"; got != want {
		t.Errorf("framed event = %q, want %q", got, want)
	}
}

// TestServeTopicHeartbeatBetweenEvents is the done-when probe: pings
// precede the event and pings resume after it — the heartbeat is observed
// between events. The interval is injected (25ms); the default itself is
// pinned on the wire by TestDefaultHeartbeatReachesTheWire.
//
// Deterministic by margins, not luck: the event is published at 60ms, so
// the 25ms and 50ms ticks are on the wire first and the 75ms tick follows
// the event. One read accumulates the stream from connect and one
// predicate asserts the ping-before / event / ping-after sequence —
// content-based, so no assertion can be thrown off by where the client's
// Read boundaries happen to fall.
func TestServeTopicHeartbeatBetweenEvents(t *testing.T) {
	b := New(Config{Heartbeat: 25 * time.Millisecond})
	_, resp := serve(t, b, "hb", 5*time.Second)
	defer resp.Body.Close()

	go func() {
		time.Sleep(60 * time.Millisecond)
		b.Publish("hb", Event{Name: "progress", Data: "done"})
	}()

	readUntil(t, resp.Body, func(s string) bool {
		i := strings.Index(s, "event: progress")
		if i < 0 {
			return false
		}
		return strings.Contains(s[:i], ": ping") && strings.Contains(s[i:], ": ping")
	}, 5*time.Second)
}

// TestDefaultHeartbeatReachesTheWire pins the 15s default through its
// default path: a Broker built with Config{} and no injected interval must
// put the first `: ping` on the wire at the default cadence. This is the
// AGENTS.md defaults rule — the substitution is pinned by
// TestSubscribeDefaultConfigSubstituted, this test proves the defaulted
// value is what the heartbeat loop actually uses. It costs ~15s of wall
// clock; that is the price of asserting the default at the wire.
func TestDefaultHeartbeatReachesTheWire(t *testing.T) {
	b := New(Config{})
	start := time.Now()
	_, resp := serve(t, b, "default-hb", 30*time.Second)
	defer resp.Body.Close()

	got := readN(t, resp.Body, 1, 25*time.Second)
	elapsed := time.Since(start)
	if !strings.Contains(got, ": ping") {
		t.Fatalf("first traffic = %q, want a : ping", got)
	}
	if elapsed < 14*time.Second || elapsed > 22*time.Second {
		t.Errorf("first ping after %v, want ~15s (the DefaultHeartbeat, on the wire)", elapsed)
	}
}

// TestServeTopicClientDisconnectRemovesSubscriber: when the client goes
// away the request context ends and the subscriber is removed — the
// no-leaked-writers contract, observed through the real HTTP surface.
func TestServeTopicClientDisconnectRemovesSubscriber(t *testing.T) {
	b := New(Config{Heartbeat: time.Hour})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.ServeTopic(w, r, "disc")
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// resp arriving proves the handler subscribed and flushed its
	// headers; there are no body bytes yet (the heartbeat is an hour
	// out), so reading one would block. Close straight away: the
	// server must notice the dead connection and drop the subscriber.
	resp.Body.Close()

	deadline := time.Now().Add(2 * time.Second)
	for b.Subscribers("disc") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber still registered 2s after the client disconnected")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestServeTopicEventNameNewlineSanitized is the S1 pin: the event Name
// gets the same newline hardening as Data — a newline inside a name can
// never inject a field line into the frame, and the payload is untouched.
func TestServeTopicEventNameNewlineSanitized(t *testing.T) {
	b := New(Config{Heartbeat: time.Hour})
	_, resp := serve(t, b, "nameframe", 5*time.Second)
	defer resp.Body.Close()

	b.Publish("nameframe", Event{Name: "done\ndata: injected", Data: "real"})
	if got, want := readN(t, resp.Body, 1, 5*time.Second), "event: done data: injected\ndata: real\n\n"; got != want {
		t.Errorf("framed event = %q, want %q (exactly two field lines, Name sanitized)", got, want)
	}

	// CRLF and bare CR flatten the same way.
	b.Publish("nameframe", Event{Name: "a\r\nb", Data: "x"})
	if got, want := readN(t, resp.Body, 1, 5*time.Second), "event: a b\ndata: x\n\n"; got != want {
		t.Errorf("framed CR event = %q, want %q", got, want)
	}
}
