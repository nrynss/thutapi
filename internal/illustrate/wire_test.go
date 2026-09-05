package illustrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/media"
	"thutapi/internal/story"
)

// The wire pins in this file run the whole package against the REAL
// internal/gmi/media client over httptest, and assert against the
// marshalled request bytes rather than against a Go struct. That is
// the point: a rename, a struct-tag change or a helpful "tidy the
// prompt" refactor cannot silently unfix a lock that is pinned on the
// bytes (AGENTS.md §Testing rule 4).
//
// No live call is made anywhere. Image calls cost about a cent each
// and the live probe is a separate operator step.

// Imager must be satisfied by the production client, or the fake
// everything else is tested against is testing a different shape.
var _ Imager = (*media.Client)(nil)

// wireEnvelope is the {model, payload} shape the request queue takes.
// It is declared here, in the test, precisely so the assertions read
// the wire and not the client's own types.
type wireEnvelope struct {
	Model   string `json:"model"`
	Payload struct {
		Prompt string   `json:"prompt"`
		Image  []string `json:"image"`
	} `json:"payload"`
}

// capture is one recorded request: the raw bytes as they went out,
// plus the decoded envelope for the assertions that need fields.
type capture struct {
	raw []byte
	env wireEnvelope
}

// wireServer stands in for console.gmicloud.ai. It records every
// request body verbatim and answers with the terminal record shape
// the live queue is measured to produce (t6b-live-record.md §3):
// the submitted payload echoed back verbatim — the prompt on a t2i
// call, the reference URL array on an i2i call — beside an outcome
// whose media_urls is a LIST OF OBJECTS {"id","url"}, with a
// thumbnail_image_url that must never be picked. Every wire pin in
// this file therefore runs against the one response shape the queue
// is known to produce.
type wireServer struct {
	mu           sync.Mutex
	requests     []capture
	fetches      int // GETs of the page-render target (/render.png)
	thumbFetches int // GETs of the thumbnail target (must stay 0)
	srv          *httptest.Server
}

func newWireServer(t *testing.T) *wireServer {
	t.Helper()
	ws := &wireServer{}
	ws.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			ws.mu.Lock()
			defer ws.mu.Unlock()
			switch r.URL.Path {
			case "/sheet-mira.png":
				w.Header().Set("Content-Type", "image/png")
				w.Write(pngBytes("SHEET-MIRA"))
			case "/sheet-bramble.png":
				w.Header().Set("Content-Type", "image/png")
				w.Write(pngBytes("SHEET-BRAMBLE"))
			case "/sheet-other.png":
				w.Header().Set("Content-Type", "image/png")
				w.Write(pngBytes("SHEET-OTHER"))
			case "/render.png":
				ws.fetches++
				w.Header().Set("Content-Type", "image/png")
				w.Write(pngBytes("PAGE"))
			case "/thumb.png":
				ws.thumbFetches++
				w.Header().Set("Content-Type", "image/png")
				w.Write(pngBytes("THUMB"))
			default:
				http.NotFound(w, r)
			}
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var env wireEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ws.mu.Lock()
		ws.requests = append(ws.requests, capture{raw: raw, env: env})
		ws.mu.Unlock()

		// A text-to-image sheet call gets a URL naming its character,
		// so the image-lock assertion can tell one sheet from another
		// after the round trip; an image-to-image page call gets the
		// page-render target. Stories beyond twoCastStory (the wire
		// verbatim tests) name other characters and share one generic
		// sheet URL — those tests assert prompts, never sheet bytes.
		target := "/render.png"
		if len(env.Payload.Image) == 0 {
			switch {
			case strings.Contains(env.Payload.Prompt, "Bramble"):
				target = "/sheet-bramble.png"
			case strings.Contains(env.Payload.Prompt, "Mira"):
				target = "/sheet-mira.png"
			default:
				target = "/sheet-other.png"
			}
		}

		// Echo the submitted payload verbatim, exactly as the live
		// record does; the result is always a URL in media_urls.
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"request_id": "r1",
			"status":     "success",
			"model":      req["model"],
			"payload":    req["payload"], // the echo, verbatim
			"outcome": map[string]any{
				"media_urls":          []map[string]string{{"id": "0", "url": "http://" + r.Host + target}},
				"thumbnail_image_url": "http://" + r.Host + "/thumb.png",
			},
		})
	}))
	t.Cleanup(ws.srv.Close)
	t.Setenv("GMI_MEDIA_BASE_URL", ws.srv.URL)
	t.Setenv("GMI_API_KEY", "test-key-not-a-real-one")
	return ws
}

// fetches reports how many times the page-render target was
// dereferenced: those links expire, so the bytes must be taken on
// receipt (PLAN.md invariant 7).
func (ws *wireServer) renderFetches() int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.fetches
}

func (ws *wireServer) thumbnailFetches() int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.thumbFetches
}

func (ws *wireServer) captured() []capture {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return append([]capture(nil), ws.requests...)
}

// client returns the real media client, paced so the poll loop cannot
// slow a test down if a fixture ever answers non-terminally.
func wireClient() *media.Client {
	return media.NewWithPoll(media.PollConfig{Interval: 5 * time.Millisecond, Timeout: 2 * time.Second})
}

// runWire drives a full Illustrate against the real client.
func runWire(t *testing.T, cfg Config, s story.Story) (*wireServer, Book) {
	t.Helper()
	ws := newWireServer(t)
	cfg.Imager = wireClient()
	book, err := Illustrate(context.Background(), cfg, s)
	if err != nil {
		t.Fatalf("Illustrate over the real media client: %v", err)
	}
	return ws, book
}

// TestVisualVerbatimInRawJSON is lock 1's raw-wire pin. The cast
// bible's visual string must reach the provider byte for byte, in
// every prompt that draws the character — reference sheet and page
// alike. project.md §2: "pasted into every page prompt unchanged.
// Never paraphrased, never regenerated."
func TestVisualVerbatimInRawJSON(t *testing.T) {
	s := twoCastStory()
	ws, _ := runWire(t, Config{}, s)

	reqs := ws.captured()
	if len(reqs) != len(s.Cast)+len(s.Pages) {
		t.Fatalf("%d requests, want %d", len(reqs), len(s.Cast)+len(s.Pages))
	}

	// Every visual must appear in the marshalled bytes of at least one
	// reference-sheet request and of every page request that names its
	// character.
	for _, m := range s.Cast {
		found := false
		for _, req := range reqs {
			if bytes.Contains(req.raw, []byte(m.Visual)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("visual for %q never appears verbatim in any request body", m.Name)
		}
	}

	// Page 2 names both characters, so both visuals are on that wire.
	var pageTwo *capture
	for i, req := range reqs {
		if strings.HasPrefix(req.env.Payload.Prompt, s.Pages[1].Prompt) {
			pageTwo = &reqs[i]
		}
	}
	if pageTwo == nil {
		t.Fatal("page 2's request was not captured")
	}
	for _, want := range []string{miraVisual, brambleVisual} {
		if !bytes.Contains(pageTwo.raw, []byte(want)) {
			t.Errorf("page 2's raw request body does not contain %q verbatim:\n%s", want, pageTwo.raw)
		}
	}
}

// TestVisualVerbatimInRawJSON_AwkwardCharacters is the half that
// catches a normaliser. Quotes, newlines, em dashes, accents and
// trailing whitespace are exactly what a "clean up the prompt" helper
// eats, and the resulting drift would be invisible in a happy-path
// test written with plain ASCII.
func TestVisualVerbatimInRawJSON_AwkwardCharacters(t *testing.T) {
	s := story.Story{
		Title: "The Tiny Dragon",
		Cast:  []story.CastMember{{Name: "Dragon", Visual: awkwardVisual}},
		Pages: []story.Page{page(1, "The dragon sneezes.", "Dragon")},
	}
	ws, _ := runWire(t, Config{}, s)

	// JSON escapes the quote and the newline on the wire, so raw
	// bytes.Contains is the wrong instrument here: decode the payload
	// and compare the string the provider will actually see.
	for _, req := range ws.captured() {
		if !strings.Contains(req.env.Payload.Prompt, awkwardVisual) {
			t.Errorf("the decoded prompt lost or altered the visual.\nwant substring: %q\ngot:\n%s", awkwardVisual, req.env.Payload.Prompt)
		}
		// And the escaped form must be present in the raw bytes, so a
		// change of encoder is caught too.
		escaped, err := json.Marshal(awkwardVisual)
		if err != nil {
			t.Fatal(err)
		}
		inner := escaped[1 : len(escaped)-1] // strip the surrounding quotes
		if !bytes.Contains(req.raw, inner) {
			t.Errorf("the raw request body does not carry the JSON-escaped visual:\n%s", req.raw)
		}
	}
}

// TestStyleSuffixVerbatimOnRawWire is lock 3's raw-wire pin: the one
// constant clause, on every request, unchanged.
func TestStyleSuffixVerbatimOnRawWire(t *testing.T) {
	s := twoCastStory()
	ws, _ := runWire(t, Config{}, s)
	reqs := ws.captured()
	if len(reqs) == 0 {
		t.Fatal("no requests captured")
	}
	for i, req := range reqs {
		if !bytes.Contains(req.raw, []byte(StyleSuffix)) {
			t.Errorf("request %d does not carry the style suffix verbatim:\n%s", i, req.raw)
		}
		if !strings.HasSuffix(req.env.Payload.Prompt, StyleSuffix) {
			t.Errorf("request %d's prompt does not end in the style suffix:\n%s", i, req.env.Payload.Prompt)
		}
	}
}

// TestImageLockOnRawWire is lock 2's raw-wire pin: every page goes out
// as an image-to-image call whose payload.image carries the reference
// sheets' URLs — every character the page names, in named order.
// payload.image is an ARRAY OF URL STRINGS (t6b-live-record.md §4;
// seedream takes references as URLs, not inline bytes). A page with no
// "image" field is a text-to-image call and the character lock is gone.
func TestImageLockOnRawWire(t *testing.T) {
	s := twoCastStory()
	ws, book := runWire(t, Config{}, s)

	sheets := map[string]Reference{}
	for _, ref := range book.References {
		sheets[ref.Name] = ref
	}
	if string(sheets["Mira"].Image) != string(pngBytes("SHEET-MIRA")) {
		t.Fatalf("Mira's sheet did not survive the round trip: %q", sheets["Mira"].Image)
	}
	if !strings.HasSuffix(sheets["Mira"].URL, "/sheet-mira.png") {
		t.Errorf("Mira's sheet URL = %q, want the character-named URL the next call chains", sheets["Mira"].URL)
	}

	pages := 0
	for _, req := range ws.captured() {
		if len(req.env.Payload.Image) == 0 {
			continue // a reference sheet: text-to-image is correct there
		}
		pages++
		// Which URLs should this page carry? The prompt says so, and
		// the attached array must agree — that agreement IS lock 2.
		var want []string
		switch {
		case strings.Contains(req.env.Payload.Prompt, "Mira opens the garden gate."):
			want = []string{ws.srv.URL + "/sheet-mira.png"}
		case strings.Contains(req.env.Payload.Prompt, "Bramble digs a hole while Mira watches."):
			want = []string{ws.srv.URL + "/sheet-bramble.png", ws.srv.URL + "/sheet-mira.png"}
		default:
			t.Errorf("page prompt names no reference sheet:\n%s", req.env.Payload.Prompt)
			continue
		}
		if strings.Join(req.env.Payload.Image, " ") != strings.Join(want, " ") {
			t.Errorf("page carried reference URLs %v, want %v:\n%s", req.env.Payload.Image, want, req.env.Payload.Prompt)
		}
	}
	if pages != len(s.Pages) {
		t.Errorf("%d image-to-image requests, want %d — every page is i2i, never t2i", pages, len(s.Pages))
	}
}

// TestDefaultModelOnRawWire is AGENTS.md §Testing rule 2 applied to
// the constant that broke T2: Config.Model is left EMPTY, and the
// assertion is on the bytes that reached the provider. A test that
// passes an explicit model cannot see this defect, which is exactly
// how Qwen-Image-2512 survived two review rounds (t2-round3.md H1).
func TestDefaultModelOnRawWire(t *testing.T) {
	s := twoCastStory()
	// Config.Model deliberately not set.
	ws, _ := runWire(t, Config{}, s)
	reqs := ws.captured()
	if len(reqs) == 0 {
		t.Fatal("no requests captured")
	}
	for i, req := range reqs {
		if !bytes.Contains(req.raw, []byte(`"model":"seedream-5.0-lite"`)) {
			t.Errorf("request %d does not carry the default model on the wire:\n%s", i, req.raw)
		}
		if req.env.Model != DefaultModel {
			t.Errorf("request %d model = %q, want %q", i, req.env.Model, DefaultModel)
		}
		if bytes.Contains(bytes.ToLower(req.raw), []byte("qwen-image-2512")) {
			t.Errorf("request %d carries the forbidden t2i-only model:\n%s", i, req.raw)
		}
	}
}

// TestExplicitModelOnRawWire is the provider switch on the wire: one
// config field, and every request moves — project.md §3's "keep the
// provider behind a one-line switch".
func TestExplicitModelOnRawWire(t *testing.T) {
	const model = "gemini-2.5-flash-image"
	ws, _ := runWire(t, Config{Model: model}, twoCastStory())
	for i, req := range ws.captured() {
		if !bytes.Contains(req.raw, []byte(`"model":"`+model+`"`)) {
			t.Errorf("request %d does not carry %q on the wire:\n%s", i, model, req.raw)
		}
	}
}

// TestEditImageRejectsAnEmptyModel_ContractPin pins the T2 contract
// this package depends on: media.EditImage refuses an empty model
// rather than substituting one, because a silent default there is how
// the forbidden model shipped (t2-round3.md H1). If that ever changed
// back to a default, Illustrate's guarantee that an explicit model
// always reaches the wire would rest on nothing.
func TestEditImageRejectsAnEmptyModel_ContractPin(t *testing.T) {
	newWireServer(t)
	_, err := wireClient().EditImage(context.Background(), "a prompt", "", []string{"https://ref.example/sheet.png"}, media.ImageOptions{})
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Fatalf("media.EditImage with an empty model: err = %v, want gmi.ErrBadRequest", err)
	}
}

// TestWire_UpstreamErrorsReachTheCallerAsSentinels closes the loop
// arrive as an internal/gmi sentinel, matched with errors.Is and never
// by reading the provider's message (PLAN.md invariant 8).
func TestWire_UpstreamErrorsReachTheCallerAsSentinels(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, gmi.ErrModelNotFound},
		{http.StatusUnauthorized, gmi.ErrUnauthorized},
		{http.StatusPaymentRequired, gmi.ErrPaymentRequired},
		{http.StatusTooManyRequests, gmi.ErrRateLimited},
		{http.StatusBadRequest, gmi.ErrBadRequest},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "upstream says no", tc.status)
			}))
			defer srv.Close()
			t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)
			t.Setenv("GMI_API_KEY", "test-key-not-a-real-one")

			_, err := Illustrate(context.Background(), Config{Imager: wireClient(), Limit: 1}, twoCastStory())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestWire_EchoedPayloadNeverBecomesThePage is round 1 H1's pin, on
// the wire. The live queue echoes the submitted payload verbatim in
// its terminal record — the prompt on a t2i record, the reference URL
// array on an i2i record (t6b-live-record.md §3) — beside an outcome
// whose media_urls names the real result. The pages must come back as
// the render fetched from that URL, never as a sheet the echoed
// payload could have handed back, and the thumbnail beside it must
// never be consulted. The polled record is byte-identical in the
// parts that matter, so this one fixture covers both terminal
// arrivals.
func TestWire_EchoedPayloadNeverBecomesThePage(t *testing.T) {
	sheets := [][]byte{pngBytes("SHEET-MIRA"), pngBytes("SHEET-BRAMBLE")}
	want := pngBytes("PAGE")

	ws, book := runWire(t, Config{}, twoCastStory())
	if len(book.Pages) != 2 {
		t.Fatalf("Pages = %d, want 2", len(book.Pages))
	}
	for _, p := range book.Pages {
		for _, sheet := range sheets {
			if bytes.Equal(p.Image, sheet) {
				t.Errorf("page %d came back as the REFERENCE SHEET: the echoed payload beat the outcome", p.N)
			}
		}
		if !bytes.Equal(p.Image, want) {
			t.Errorf("page %d image = %q, want the rendered page %q", p.N, p.Image, want)
		}
	}
	// The link expires, so the bytes must have been dereferenced now,
	// once per page — not stored for later (PLAN.md invariant 7).
	if got := ws.renderFetches(); got != 2 {
		t.Errorf("render URL dereferenced %d times, want 2", got)
	}
	// The thumbnail rides beside the answer and must never be picked
	// as the picture.
	if got := ws.thumbnailFetches(); got != 0 {
		t.Errorf("thumbnail_image_url fetched %d times, want 0", got)
	}
}
