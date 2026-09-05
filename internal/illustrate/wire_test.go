package illustrate

import (
	"bytes"
	"context"
	"encoding/base64"
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
		Prompt string `json:"prompt"`
		Image  string `json:"image"`
	} `json:"payload"`
}

// capture is one recorded request: the raw bytes as they went out,
// plus the decoded envelope for the assertions that need fields.
type capture struct {
	raw []byte
	env wireEnvelope
}

// wireServer stands in for console.gmicloud.ai. It records every
// request body verbatim and answers in the envelope the live queue
// uses — {"request_id","status","outcome"} — with the picture inline,
// so the whole pipeline runs without a network call to GMI.
type wireServer struct {
	mu       sync.Mutex
	requests []capture
	srv      *httptest.Server
}

func newWireServer(t *testing.T) *wireServer {
	t.Helper()
	ws := &wireServer{}
	ws.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		// Answer each reference sheet with bytes that name the
		// character its prompt is for, so the image-lock assertion can
		// tell one sheet from another after the round trip.
		img := pngBytes("PAGE")
		if env.Payload.Image == "" {
			switch {
			case strings.Contains(env.Payload.Prompt, "Mira"):
				img = pngBytes("SHEET-MIRA")
			case strings.Contains(env.Payload.Prompt, "Bramble"):
				img = pngBytes("SHEET-BRAMBLE")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"request_id":"r1","status":"success","outcome":{"image":"data:image/png;base64,%s"}}`,
			base64.StdEncoding.EncodeToString(img))
	}))
	t.Cleanup(ws.srv.Close)
	t.Setenv("GMI_MEDIA_BASE_URL", ws.srv.URL)
	t.Setenv("GMI_API_KEY", "test-key-not-a-real-one")
	return ws
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
// as an image-to-image call whose payload carries the reference
// sheet's own bytes, base64-inlined. A page with no "image" field is a
// text-to-image call and the character lock is gone.
func TestImageLockOnRawWire(t *testing.T) {
	s := twoCastStory()
	ws, book := runWire(t, Config{}, s)

	sheets := map[string][]byte{}
	for _, ref := range book.References {
		sheets[ref.Name] = ref.Image
	}
	if string(sheets["Mira"]) != string(pngBytes("SHEET-MIRA")) {
		t.Fatalf("Mira's sheet did not survive the round trip: %q", sheets["Mira"])
	}

	pages := 0
	for _, req := range ws.captured() {
		if req.env.Payload.Image == "" {
			continue // a reference sheet: text-to-image is correct there
		}
		pages++
		prefix := "data:image/png;base64,"
		if !strings.HasPrefix(req.env.Payload.Image, prefix) {
			t.Errorf("page payload image is not an inline data URI: %.60q", req.env.Payload.Image)
			continue
		}
		got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(req.env.Payload.Image, prefix))
		if err != nil {
			t.Errorf("page payload image is not decodable base64: %v", err)
			continue
		}
		// Which sheet should this page carry? The prompt says so, and
		// the bytes must agree — that agreement IS lock 2.
		var want []byte
		switch {
		case strings.Contains(req.env.Payload.Prompt, "The attached reference image is Mira."):
			want = sheets["Mira"]
		case strings.Contains(req.env.Payload.Prompt, "The attached reference image is Bramble."):
			want = sheets["Bramble"]
		default:
			t.Errorf("page prompt names no reference sheet:\n%s", req.env.Payload.Prompt)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("page carried %q as its reference, but its prompt says otherwise:\n%s", got, req.env.Payload.Prompt)
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
		if !bytes.Contains(req.raw, []byte(`"model":"Flux2-Klein"`)) {
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
	const model = "Z-Image"
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
	_, err := wireClient().EditImage(context.Background(), pngBytes("ref"), "a prompt", "")
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Fatalf("media.EditImage with an empty model: err = %v, want gmi.ErrBadRequest", err)
	}
}

// TestWire_UpstreamErrorsReachTheCallerAsSentinels closes the loop
// through the real client: an HTTP status from the request queue must
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
