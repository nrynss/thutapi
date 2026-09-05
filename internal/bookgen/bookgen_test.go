package bookgen

import (
	"encoding/json"
	"errors"
	"testing"

	"thutapi/internal/job"
	"thutapi/internal/store"
)

// TestNewRefusesMissingConfig pins the required-dependency check: every
// nil Config field (and an empty MediaDir) is ErrNotConfigured.
func TestNewRefusesMissingConfig(t *testing.T) {
	ph := newPipelineHarness(t)
	base := Config{
		DB:       ph.db,
		Blobs:    ph.blobs,
		MediaDir: ph.mediaDir,
		Chat:     ph.chat,
		Judge:    ph.judge,
		Imager:   ph.imager,
		TTS:      ph.tts,
		Broker:   ph.broker,
		Jobs:     ph.runner,
		PDF:      ph.pdf,
		Video:    ph.render,
		Film:     ph.film,
	}
	cases := []struct {
		name string
		brk  func(*Config)
	}{
		{"DB", func(c *Config) { c.DB = nil }},
		{"Blobs", func(c *Config) { c.Blobs = nil }},
		{"MediaDir empty", func(c *Config) { c.MediaDir = "" }},
		{"Chat", func(c *Config) { c.Chat = nil }},
		{"Judge", func(c *Config) { c.Judge = nil }},
		{"Imager", func(c *Config) { c.Imager = nil }},
		{"TTS", func(c *Config) { c.TTS = nil }},
		{"Broker", func(c *Config) { c.Broker = nil }},
		{"Jobs", func(c *Config) { c.Jobs = nil }},
		{"PDF", func(c *Config) { c.PDF = nil }},
		{"Video", func(c *Config) { c.Video = nil }},
		{"Film", func(c *Config) { c.Film = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.brk(&cfg)
			if _, err := New(cfg); !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("New with broken %s = %v, want ErrNotConfigured", tc.name, err)
			}
		})
	}
}

// TestTopicConvention pins the book topic format: BookTopicPrefix plus
// the book id — the convention screen 5's subscription derives.
func TestTopicConvention(t *testing.T) {
	if BookTopicPrefix != "book:" {
		t.Fatalf("BookTopicPrefix = %q, want book:", BookTopicPrefix)
	}
	if got := Topic("abc"); got != "book:abc" {
		t.Fatalf("Topic(abc) = %q, want book:abc", got)
	}
}

// TestClassify pins the error-to-class/status mapping of the generate
// route's refusals.
func TestClassify(t *testing.T) {
	cases := []struct {
		err      error
		wantCls  string
		wantCode int
	}{
		{store.ErrNotFound, classNotFound, 404},
		{ErrOpenInterview, classNotEnded, 409},
		{ErrBusy, classBusy, 409},
		{job.ErrLimit, classCapacity, 503},
		{errors.New("something else"), classInternal, 500},
	}
	for _, tc := range cases {
		cls, code := classify(tc.err)
		if cls != tc.wantCls || code != tc.wantCode {
			t.Fatalf("classify(%v) = (%q, %d), want (%q, %d)", tc.err, cls, code, tc.wantCls, tc.wantCode)
		}
	}
}

// TestWirePayloadRawJSON pins the exact SSE data lines PLAN.md §T10c/§T10f
// name: the field spellings (n, image_url, pdf_url, video_url) and the bare {}
// failure and narration_unavailable payloads. A rename on the wire shape must fail here, loudly.
func TestWirePayloadRawJSON(t *testing.T) {
	b, err := json.Marshal(pageApprovedEvent{N: 3, ImageURL: "/media/abc"})
	if err != nil {
		t.Fatalf("marshal page_approved: %v", err)
	}
	want := `{"n":3,"image_url":"/media/abc"}`
	if string(b) != want {
		t.Fatalf("page_approved wire = %s, want %s", b, want)
	}

	// Full book_ready with both PDF and Video:
	b, err = json.Marshal(bookReadyEvent{PDFURL: "/media/pdf1", VideoURL: "/media/vid1"})
	if err != nil {
		t.Fatalf("marshal book_ready: %v", err)
	}
	want = `{"pdf_url":"/media/pdf1","video_url":"/media/vid1"}`
	if string(b) != want {
		t.Fatalf("book_ready wire = %s, want %s", b, want)
	}

	// Outage book_ready with PDF only (video_url omitted):
	b, err = json.Marshal(bookReadyEvent{PDFURL: "/media/pdf1"})
	if err != nil {
		t.Fatalf("marshal book_ready outage: %v", err)
	}
	want = `{"pdf_url":"/media/pdf1"}`
	if string(b) != want {
		t.Fatalf("book_ready outage wire = %s, want %s", b, want)
	}

	if failedData != "{}" {
		t.Fatalf("failedData = %q, want {}", failedData)
	}
	if narrationUnavailableData != "{}" {
		t.Fatalf("narrationUnavailableData = %q, want {}", narrationUnavailableData)
	}
}

// TestGenerateResponseWireJSON pins the generate POST response field
// names screen 5 decodes.
func TestGenerateResponseWireJSON(t *testing.T) {
	b, err := json.Marshal(generateResponse{
		JobID: "j", BookID: "b", Topic: "book:b", Events: "/interviews/i/generate/events", Status: "running",
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, k := range []string{"job_id", "book_id", "topic", "events_url", "status"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("generate response %s lacks %q", b, k)
		}
	}
}

// TestEnded pins the closed-interview detection: a transcript whose
// last turn is the closing role is ended; anything else is not.
func TestEnded(t *testing.T) {
	closed := store.Interview{Turns: []store.Turn{
		{Role: "interviewer", Text: "q"},
		{Role: "child", Text: "a"},
		{Role: "closing", Text: "bye"},
	}}
	if !ended(closed) {
		t.Fatalf("transcript ending in a closing turn must read as ended")
	}
	open := store.Interview{Turns: []store.Turn{
		{Role: "interviewer", Text: "q"},
		{Role: "child", Text: "a"},
	}}
	if ended(open) {
		t.Fatalf("transcript ending in a child turn must not read as ended")
	}
	if ended(store.Interview{}) {
		t.Fatalf("an empty transcript must not read as ended")
	}
}

// TestEventsPath pins the SSE stream URL shape a reload derives from
// the interview id alone.
func TestEventsPath(t *testing.T) {
	if got, want := eventsPath("iv1"), "/interviews/iv1/generate/events"; got != want {
		t.Fatalf("eventsPath(iv1) = %q, want %q", got, want)
	}
}
