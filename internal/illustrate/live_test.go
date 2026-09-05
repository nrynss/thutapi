//go:build live

// Live probe for the post-remediation shape of an image generation: one
// GenerateImage call (t2i) followed by one EditImage call whose reference is
// the generated image's own media_urls[0].url — the exact chain
// internal/illustrate renders a book with (t6b-live-record.md §4, item 1b).
// Model: seedream-5.0-lite, the one measured generating (the T6b item-1 probe
// against Flux2-Klein/Z-Image proved those ids accept and never run).
//
// What this verifies against production, not a fixture:
//  1. the t2i record's result is a URL under outcome.media_urls[].url;
//  2. that URL is publicly fetchable and feeds straight back into
//     payload.image as an array element;
//  3. the i2i record echoes the submitted payload (the URL array) and the
//     render decodes from outcome again, never from the echo — round-1 H1's
//     exact failure mode, now with URLs in the echo instead of bytes;
//  4. the rendered bytes differ from the reference: a real edit happened.
//
// Run with:
//
//	set -a; . ./.env; set +a
//	go test -tags live -run TestLiveURLChaining -v ./internal/illustrate/
//
// The rendered PNGs land in data/live/ (gitignored) as eyeball evidence; the
// record's JSON shape is logged for the operator record.
package illustrate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"thutapi/internal/gmi/media"
	"thutapi/internal/story"
)

// liveQueueRecord mirrors the terminal request-queue envelope's documented
// top level. Payload and Outcome stay raw so the probe records their shape
// verbatim instead of trusting a decode of it — the decoder under suspicion
// is exactly the thing this probe must not depend on. (Named for the live
// tag: the package's own queueRecord already models the outcome half.)
type liveQueueRecord struct {
	RequestID string          `json:"request_id"`
	Model     string          `json:"model"`
	Status    string          `json:"status"`
	Payload   json.RawMessage `json:"payload"`
	Outcome   json.RawMessage `json:"outcome"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
}

func liveMediaClient(t *testing.T) *media.Client {
	t.Helper()
	if os.Getenv("GMI_API_KEY") == "" {
		t.Skip("GMI_API_KEY not set")
	}
	// A seedream image answers synchronously on the POST (~15-45s), but
	// keep the generous poll budget in case the queue parks the job.
	return media.NewWithPoll(media.PollConfig{Timeout: 10 * time.Minute})
}

// dumpRecord logs the shape facts the record file needs: top-level keys,
// whether payload is echoed, the outcome subtree verbatim, and the byte size
// of the payload echo.
func dumpRecord(t *testing.T, phase string, raw []byte) liveQueueRecord {
	t.Helper()

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("%s: terminal record is not JSON: %v\nbody (first 400 bytes): %.400q", phase, err, raw)
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	t.Logf("%s: top-level keys (%d): %v", phase, len(names), names)

	var rec liveQueueRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("%s: terminal record does not match the documented envelope: %v", phase, err)
	}
	if len(rec.Payload) > 0 && len(rec.Payload) <= 600 {
		t.Logf("%s: payload verbatim: %s", phase, rec.Payload)
	} else if len(rec.Payload) > 600 {
		t.Logf("%s: payload (first 600 bytes): %.600s", phase, rec.Payload)
	}
	t.Logf("%s: outcome verbatim: %s", phase, rec.Outcome)
	return rec
}

// extractImage gets the rendered image and its URL out of the terminal
// record by name — outcome.media_urls, a list of objects {"id","url"} —
// fetching the first entry's URL from GMI's public bucket (t2b confirmed
// bucket objects are publicly fetchable). The thumbnail_image_url beside
// it is deliberately never consulted. The bytes are asserted to start with
// PNG or JPEG magic (the pinned production format is jpeg — the payload
// sends output_format:"jpeg" unless overridden) and written to data/live/
// for the operator's eyeball.
func extractImage(t *testing.T, phase string, rec liveQueueRecord) (img []byte, url string) {
	t.Helper()

	var outcome struct {
		MediaURLs []struct {
			URL string `json:"url"`
		} `json:"media_urls"`
		ThumbnailImageURL string `json:"thumbnail_image_url"`
	}
	if err := json.Unmarshal(rec.Outcome, &outcome); err != nil {
		t.Fatalf("%s: outcome does not decode: %v (outcome verbatim: %s)", phase, err, rec.Outcome)
	}
	if len(outcome.MediaURLs) == 0 {
		t.Fatalf("%s: outcome.media_urls is empty (verbatim: %s)", phase, rec.Outcome)
	}
	url = outcome.MediaURLs[0].URL
	if url == "" {
		t.Fatalf("%s: outcome.media_urls[0].url is empty", phase)
	}
	t.Logf("%s: result at outcome.media_urls[0].url: %s", phase, url)
	if outcome.ThumbnailImageURL != "" {
		t.Logf("%s: thumbnail_image_url present (%s) and NOT consulted", phase, outcome.ThumbnailImageURL)
	}

	resp, err := http.Get(url) //nolint:noctx,gosec // live probe; URL from the authenticated queue's own answer
	if err != nil {
		t.Fatalf("%s: fetching media_urls[0].url failed: %v", phase, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: fetching media_urls[0].url: HTTP %d", phase, resp.StatusCode)
	}
	t.Logf("%s: fetch: HTTP %d content-type=%q", phase, resp.StatusCode, resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		t.Fatalf("%s: reading body failed: %v", phase, err)
	}
	ext := ".bin"
	switch {
	case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
		ext = ".png"
	case bytes.HasPrefix(body, []byte("\xff\xd8\xff")):
		ext = ".jpg"
	default:
		t.Fatalf("%s: fetched %d bytes start with neither PNG nor JPEG magic (first 16: %.16q)", phase, len(body), body)
	}
	t.Logf("%s: image fetched: %d bytes (%s)", phase, len(body), ext)

	dir := filepath.Join("data", "live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("%s: creating data/live failed: %v", phase, err)
	}
	path := filepath.Join(dir, "t6b-"+phase+ext)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("%s: writing %s failed: %v", phase, path, err)
	}
	t.Logf("%s: saved %s", phase, path)
	return body, url
}

// TestLiveURLChaining drives the exact chain the renderer uses: a t2i sheet,
// then an i2i page whose reference array carries the sheet's own URL.
func TestLiveURLChaining(t *testing.T) {
	c := liveMediaClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	genRaw, err := c.GenerateImage(ctx,
		"a single flat red circle centered on a plain white background, thick black outline, children's picture book style",
		"seedream-5.0-lite", media.ImageOptions{})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	t.Logf("generate: terminal record %d bytes", len(genRaw))
	genRec := dumpRecord(t, "generate", genRaw)
	if genRec.Status != "success" {
		t.Fatalf("generate: status = %q, want success", genRec.Status)
	}
	genPNG, sheetURL := extractImage(t, "generate", genRec)

	editRaw, err := c.EditImage(ctx,
		"change the circle's colour from red to blue. Keep everything else on the page exactly the same: the white background, the black outline, the composition.",
		"seedream-5.0-lite", []string{sheetURL}, media.ImageOptions{})
	if err != nil {
		t.Fatalf("EditImage: %v", err)
	}
	t.Logf("edit: terminal record %d bytes", len(editRaw))
	editRec := dumpRecord(t, "edit", editRaw)
	if editRec.Status != "success" {
		t.Fatalf("edit: status = %q, want success", editRec.Status)
	}
	// The i2i echo must repeat the submitted reference URL array (H1's
	// mechanism, now URL-shaped); assert it is there, then prove the
	// decode still picked the outcome, never the echo.
	if !bytes.Contains(editRec.Payload, []byte(sheetURL)) {
		t.Errorf("edit: the echoed payload does not carry the reference URL %s — H1's echo mechanism is not present in this record", sheetURL)
	}
	editPNG, editURL := extractImage(t, "edit", editRec)
	if editURL == sheetURL {
		t.Logf("edit: media_urls URL equals the reference URL — worth recording")
	}
	if bytes.Equal(genPNG, editPNG) {
		t.Errorf("edit: rendered bytes are identical to the reference — the decode may have picked the echo (round-1 H1)")
	} else {
		t.Logf("edit: rendered bytes differ from the reference (%d vs %d bytes) — a real edit", len(genPNG), len(editPNG))
	}
}

// savingImager wraps the live client so every render is written to disk
// the instant its terminal record arrives — before the pipeline's own
// decode runs. Illustrate holds the whole book in memory and returns the
// zero Book on any error, so without this a single failed page would
// discard every render that already completed, and with it the run's
// evidence. Each save writes the terminal record verbatim (for the shape
// record) and the image its media_urls names; the probe renames the
// files by byte-matching them to the finished book afterwards.
type savingImager struct {
	inner Imager
	dir   string
	mu    sync.Mutex
	seq   int
}

func (s *savingImager) GenerateImage(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
	raw, err := s.inner.GenerateImage(ctx, prompt, model, opts)
	if err == nil {
		s.save("sheet", raw)
	}
	return raw, err
}

func (s *savingImager) EditImage(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
	raw, err := s.inner.EditImage(ctx, prompt, model, refImages, opts)
	if err == nil {
		s.save("page", raw)
	}
	return raw, err
}

// save writes one terminal record and the image it names. It parses
// media_urls independently of decodeImage — the shape evidence must not
// depend on the decoder under suspicion. A fetch or write failure is
// logged and swallowed: the render itself succeeded, and the probe must
// not lose the book to a disk hiccup after the money was spent.
func (s *savingImager) save(kind string, raw []byte) {
	var rec struct {
		Outcome struct {
			MediaURLs []struct {
				URL string `json:"url"`
			} `json:"media_urls"`
		} `json:"outcome"`
	}
	if json.Unmarshal(raw, &rec) != nil || len(rec.Outcome.MediaURLs) == 0 || rec.Outcome.MediaURLs[0].URL == "" {
		return // not the terminal image shape; nothing to save
	}
	u := rec.Outcome.MediaURLs[0].URL
	resp, err := http.Get(u) //nolint:noctx,gosec // live probe; URL from the authenticated queue's own answer
	if err != nil {
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	ext := ".bin"
	switch {
	case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
		ext = ".png"
	case bytes.HasPrefix(body, []byte("\xff\xd8\xff")):
		ext = ".jpg"
	}
	s.mu.Lock()
	s.seq++
	n := s.seq
	s.mu.Unlock()
	name := fmt.Sprintf("%s-%02d%s", kind, n, ext)
	if err := os.WriteFile(filepath.Join(s.dir, name), body, 0o644); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.dir, kind+"-"+fmt.Sprintf("%02d", n)+".json"), raw, 0o644)
}

// TestLiveEightPagesConstantCast is T6b item 2: T6's original Done
// when, run live — one book of eight pages against a constant cast,
// through the real client and the real decode, on seedream-5.0-lite.
// Cost: 2 reference sheets + 8 pages at $0.035 each ≈ $0.35.
//
// The mechanically checkable half is asserted here: two sheets, eight
// pages, no skips, every page a decode of the OUTCOME (never its
// reference sheet — the round-1 H1 echo), every page's lead reference
// matching the story. The "recognisably constant cast" half cannot be
// asserted by a test: the operator eyeballs the renders saved under
// data/live/t6b-book/ and records the verdict in t6b-live-record.md.
func TestLiveEightPagesConstantCast(t *testing.T) {
	s := story.Story{
		Title: "Mira and Bramble's Long Day",
		Cast: []story.CastMember{
			{Name: "Mira", Visual: "a small girl with two red plaits, round glasses and green wellington boots"},
			{Name: "Bramble", Visual: "a shaggy brown dog with one white ear and a red collar"},
		},
		Pages: []story.Page{
			{N: 1, Prompt: "Mira opens the garden gate.", Characters: []string{"Mira"}},
			{N: 2, Prompt: "Bramble chases a butterfly across the lawn.", Characters: []string{"Bramble"}},
			{N: 3, Prompt: "Mira and Bramble pick apples from the old tree.", Characters: []string{"Mira", "Bramble"}},
			{N: 4, Prompt: "The rain comes and they shelter under the oak.", Characters: []string{"Mira", "Bramble"}},
			{N: 5, Prompt: "Bramble shakes the raindrops off in the kitchen.", Characters: []string{"Bramble"}},
			{N: 6, Prompt: "Mira bakes an apple pie while Bramble watches.", Characters: []string{"Mira"}},
			{N: 7, Prompt: "They share the pie on the porch at sunset.", Characters: []string{"Mira", "Bramble"}},
			{N: 8, Prompt: "Bramble curls up beside Mira's bed, fast asleep.", Characters: []string{"Bramble", "Mira"}},
		},
	}

	dir := filepath.Join("data", "live", "t6b-book")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s failed: %v", dir, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	book, err := Illustrate(ctx, Config{
		Imager: &savingImager{inner: liveMediaClient(t), dir: dir},
		Model:  "seedream-5.0-lite",
		Limit:  2,
	}, s)
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}

	if len(book.References) != len(s.Cast) {
		t.Errorf("References = %v, want both cast members drawn", refNamesLive(book.References))
	}
	if len(book.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none — every cast member is drawable", book.Skipped)
	}
	if len(book.Pages) != len(s.Pages) {
		t.Fatalf("Pages = %d, want %d", len(book.Pages), len(s.Pages))
	}
	sheetByName := map[string]Reference{}
	for _, r := range book.References {
		sheetByName[r.Name] = r
	}
	for i, p := range book.Pages {
		want := s.Pages[i]
		if p.N != want.N {
			t.Errorf("Pages[%d].N = %d, want %d", i, p.N, want.N)
		}
		// H1's exact failure mode, live: the page must not be its own
		// reference sheet, whatever the queue echoed.
		for _, sheet := range sheetByName {
			if bytes.Equal(p.Image, sheet.Image) {
				t.Errorf("page %d is byte-identical to %s's sheet — the decode picked the echo", p.N, sheet.Name)
			}
		}
		lead := sheetByName[p.Reference]
		if lead.Name == "" {
			t.Errorf("page %d names a lead reference %q that is not a sheet", p.N, p.Reference)
		}
		t.Logf("page %d: lead=%s bytes=%d ct=%s", p.N, p.Reference, len(p.Image), p.ContentType)
	}
	for _, r := range book.References {
		t.Logf("sheet %s: bytes=%d ct=%s url=%s", r.Name, len(r.Image), r.ContentType, r.URL)
	}

	// Name the incrementally-saved files after the finished book by
	// byte-matching them to its pages and sheets.
	if err := nameSavedRenders(dir, book); err != nil {
		t.Errorf("naming saved renders: %v", err)
	}
	t.Logf("book renders saved under %s — operator eyeball verdict goes in t6b-live-record.md item 2", dir)
}

// nameSavedRenders renames the savingImager's arrival-order files to
// page-01..08 and sheet-<Name>, keeping each file's own extension, by
// matching their bytes to the finished book's pages and sheets.
func nameSavedRenders(dir string, book Book) error {
	ext := filepath.Ext
	match := func(want []byte) (string, bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", false
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err == nil && bytes.Equal(b, want) {
				return e.Name(), true
			}
		}
		return "", false
	}
	rename := func(want []byte, base string) error {
		cur, ok := match(want)
		if !ok {
			return fmt.Errorf("no saved file carries these bytes")
		}
		final := base + ext(cur)
		if cur == final {
			return nil
		}
		return os.Rename(filepath.Join(dir, cur), filepath.Join(dir, final))
	}
	for _, p := range book.Pages {
		if err := rename(p.Image, fmt.Sprintf("page-%02d", p.N)); err != nil {
			return fmt.Errorf("page %d: %v", p.N, err)
		}
	}
	for _, r := range book.References {
		if err := rename(r.Image, "sheet-"+r.Name); err != nil {
			return fmt.Errorf("sheet %s: %v", r.Name, err)
		}
	}
	return nil
}

func refNamesLive(refs []Reference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}
