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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"thutapi/internal/gmi/media"
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

// extractImage gets the rendered PNG and its URL out of the terminal record
// by name — outcome.media_urls, a list of objects {"id","url"} — fetching
// the first entry's URL from GMI's public bucket (t2b confirmed bucket
// objects are publicly fetchable). The thumbnail_image_url beside it is
// deliberately never consulted. The bytes are asserted to start with the PNG
// magic and written to data/live/ for the operator's eyeball.
func extractImage(t *testing.T, phase string, rec liveQueueRecord) (png []byte, url string) {
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
	if !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("%s: fetched %d bytes do not start with the PNG magic (first 16: %.16q)", phase, len(body), body)
	}
	t.Logf("%s: PNG fetched: %d bytes", phase, len(body))

	dir := filepath.Join("data", "live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("%s: creating data/live failed: %v", phase, err)
	}
	path := filepath.Join(dir, "t6b-"+phase+".png")
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
