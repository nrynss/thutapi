//go:build live

// Live probes for T6b item 1: the terminal request-queue record shape for
// image calls. One GenerateImage call followed by one EditImage call that
// uses the generated PNG as its reference — both against Flux2-Klein
// (~$0.02 total, operator-sanctioned 2026-09-05). The point is the
// envelope's shape, not the picture: does the record echo the submitted
// payload back, and does the result arrive inline or as a URL under
// outcome? That decides whether t6-round1.md H1 is Critical or High and
// hands the remediation agent the real response shape. Run with:
//
//	set -a; . ./.env; set +a
//	go test -tags live -run TestLiveGenerateThenEdit -v ./internal/illustrate/
//
// The rendered PNGs land in data/live/ (gitignored) as eyeball evidence for
// the record file; the record's JSON shape is logged and pasted into
// dev-diary/adversarial-review/t6b-live-record.md.
package illustrate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"thutapi/internal/gmi/media"
)

// queueRecord mirrors the terminal request-queue envelope's documented top
// level. Payload and Outcome stay raw so the probe records their shape
// verbatim instead of trusting a decode of it — the decoder under suspicion
// is exactly the thing this probe must not depend on.
type queueRecord struct {
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
	// The default poll budget is 120s; the live image queue was observed
	// still `queued` at 120s on the first run of this probe (request
	// 88d7ffdc-707f-4694-8456-8b93037ae5ea), so give it ten minutes.
	return media.NewWithPoll(media.PollConfig{Timeout: 10 * time.Minute})
}

// dumpRecord logs the shape facts the record file needs: top-level keys,
// whether payload is echoed, the outcome subtree verbatim, and the byte size
// of the payload echo.
func dumpRecord(t *testing.T, phase string, raw []byte) queueRecord {
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

	var rec queueRecord
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

// extractImage gets the rendered PNG out of the terminal record without
// decodeImage: inline data: URI under outcome, or a URL fetched from GMI's
// public bucket (the T2b record confirmed bucket objects are publicly
// fetchable). The bytes are asserted to start with the PNG magic and written
// to data/live/ for the operator's eyeball.
func extractImage(t *testing.T, phase string, rec queueRecord) []byte {
	t.Helper()

	var outcome struct {
		Image    string `json:"image"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(rec.Outcome, &outcome); err != nil {
		t.Fatalf("%s: outcome does not decode: %v (outcome verbatim: %s)", phase, err, rec.Outcome)
	}

	var png []byte
	switch {
	case outcome.Image != "":
		t.Logf("%s: result arrived INLINE under outcome.image (%d chars)", phase, len(outcome.Image))
		const prefix = "data:image/png;base64,"
		if len(outcome.Image) <= len(prefix) || outcome.Image[:len(prefix)] != prefix {
			t.Fatalf("%s: outcome.image is not a data:image/png;base64 URI (prefix: %.40q)", phase, outcome.Image)
		}
		decoded, err := base64.StdEncoding.DecodeString(outcome.Image[len(prefix):])
		if err != nil {
			t.Fatalf("%s: outcome.image does not base64-decode: %v", phase, err)
		}
		png = decoded
	case outcome.ImageURL != "":
		t.Logf("%s: result arrived as a URL under outcome.image_url", phase)
		resp, err := http.Get(outcome.ImageURL) //nolint:noctx // live probe, bounded below
		if err != nil {
			t.Fatalf("%s: fetching outcome.image_url failed: %v", phase, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: fetching outcome.image_url: HTTP %d", phase, resp.StatusCode)
		}
		t.Logf("%s: image_url fetch: HTTP %d content-type=%q", phase, resp.StatusCode, resp.Header.Get("Content-Type"))
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			t.Fatalf("%s: reading image_url body failed: %v", phase, err)
		}
		png = body
	default:
		t.Fatalf("%s: outcome carries neither image nor image_url (verbatim: %s)", phase, rec.Outcome)
	}

	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("%s: extracted %d bytes do not start with the PNG magic (first 16: %.16q)", phase, len(png), png)
	}
	t.Logf("%s: PNG extracted: %d bytes", phase, len(png))

	dir := filepath.Join("data", "live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("%s: creating data/live failed: %v", phase, err)
	}
	path := filepath.Join(dir, "t6b-"+phase+".png")
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatalf("%s: writing %s failed: %v", phase, path, err)
	}
	t.Logf("%s: saved %s", phase, path)
	return png
}

// TestLiveGenerateThenEdit is T6b item 1: generate a PNG, then edit it with
// the generated PNG as the reference, recording both terminal envelopes.
func TestLiveGenerateThenEdit(t *testing.T) {
	c := liveMediaClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	genRaw, err := c.GenerateImage(ctx, "a single flat red circle centered on a plain white background, thick black outline, children's picture book style", "Flux2-Klein")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	t.Logf("generate: terminal record %d bytes", len(genRaw))
	genRec := dumpRecord(t, "generate", genRaw)
	if genRec.Status != "success" {
		t.Fatalf("generate: status = %q, want success", genRec.Status)
	}
	genPNG := extractImage(t, "generate", genRec)

	editRaw, err := c.EditImage(ctx, genPNG, "change the circle's colour from red to blue. Keep everything else on the page exactly the same: the white background, the black outline, the composition.", "Flux2-Klein")
	if err != nil {
		t.Fatalf("EditImage: %v", err)
	}
	t.Logf("edit: terminal record %d bytes", len(editRaw))
	editRec := dumpRecord(t, "edit", editRaw)
	if editRec.Status != "success" {
		t.Fatalf("edit: status = %q, want success", editRec.Status)
	}
	editPNG := extractImage(t, "edit", editRec)

	if bytes.Equal(genPNG, editPNG) {
		t.Logf("edit: rendered bytes are identical to the reference — the echo case t6-round1 H1 predicts")
	} else {
		t.Logf("edit: rendered bytes differ from the reference (%d vs %d bytes) — a real edit", len(genPNG), len(editPNG))
	}
}
