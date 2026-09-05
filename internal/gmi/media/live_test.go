//go:build live

// Live probes for T2b. Excluded from every normal build and from CI by
// the `live` tag. Run with:
//
//	set -a; . ./.env; set +a; go test -tags live -run Live -v ./internal/gmi/media/
//
// These deliberately exercise only the FREE paths. Speech 2.8 is free
// during the campaign window; image generation is ~$0.01 a call, so
// image probes here are limited to model-resolution checks that fail
// before anything is rendered. A real render belongs to T6's own gated
// live step, not to a test anyone might run in a loop.
package media

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"thutapi/internal/gmi"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("GMI_API_KEY") == "" {
		t.Skip("GMI_API_KEY not set")
	}
	return New()
}

// TestLiveSynthesizeSpeech is T2b's media-endpoint probe: a real
// request-queue call against the real host, returning real audio.
func TestLiveSynthesizeSpeech(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	got, err := c.SynthesizeSpeech(ctx, "Once upon a time, a small dragon lost her shoe.",
		"English_expressive_narrator", "minimax-tts-speech-2.8-hd")
	if err != nil {
		t.Fatalf("live SynthesizeSpeech: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("live SynthesizeSpeech returned zero bytes")
	}
	t.Logf("bytes=%d sniffed=%q head=%q", len(got), http.DetectContentType(got), truncate(got, 1400))
}

// TestLiveUnknownModel404s proves the request queue's model-resolution
// failure maps onto the sentinel the package documents, against
// production rather than a fake. Costs nothing: it fails before any
// generation happens.
func TestLiveUnknownModel404s(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := c.GenerateImage(ctx, "a cat", "definitely-not-a-real-model-2512")
	if err == nil {
		t.Fatal("unknown model unexpectedly succeeded")
	}
	if !errors.Is(err, gmi.ErrModelNotFound) {
		t.Errorf("unknown model: got %v, want ErrModelNotFound", err)
	}
	t.Logf("unknown-model error (expected): %v", err)
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}
