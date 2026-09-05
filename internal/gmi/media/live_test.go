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
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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
		"", "English_expressive_narrator", "minimax-tts-speech-2.8-hd")
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

	_, err := c.GenerateImage(ctx, "a cat", "definitely-not-a-real-model-2512", ImageOptions{})
	if err == nil {
		t.Fatal("unknown model unexpectedly succeeded")
	}
	if !errors.Is(err, gmi.ErrModelNotFound) {
		t.Errorf("unknown model: got %v, want ErrModelNotFound", err)
	}
	t.Logf("unknown-model error (expected): %v", err)
}

// TestLiveSynthesizeSpeech_EmotionKeyEchoed is the T8 round-2 H1
// settlement probe (t8-round2.md; t8-remediation-round2.md): an
// emotion-carrying narration call whose terminal record must still
// echo the "emotion" key it was submitted with. The queue echoes the
// submitted payload inside the terminal record
// (t2b-t5b-live-record.md), so an upstream that strips or rejects the
// key shows it in the echo. Before this probe no live observation of
// the emotion key on the minimax-tts-speech-2.8-hd wire existed
// anywhere: the TTS pool answered the settlement call with HTTP 503
// "Upstream capacity temporarily exhausted" on 2026-09-05, so this
// test is committed but not yet green. That is correct for a live
// probe — it fails with the upstream error until the pool recovers,
// and its first passing run is evidence that closes H1 (recorded in
// t8-remediation-round2.md), never a CI gate (//go:build live).
func TestLiveSynthesizeSpeech_EmotionKeyEchoed(t *testing.T) {
	const ttsHD = "minimax-tts-speech-2.8-hd"
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Cheap second assertion first, so a 503'd TTS pool does not hide
	// it: the request-queue model catalog must still list the model
	// this probe speaks with (live-observed present on 2026-09-05 even
	// while the pool 503'd every POST).
	t.Run("model in catalog", func(t *testing.T) {
		modelsURL, err := url.JoinPath(c.baseURL, pathModelCatalog)
		if err != nil {
			t.Fatalf("build catalog URL: %v", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
		if err != nil {
			t.Fatalf("build catalog request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+os.Getenv("GMI_API_KEY"))
		resp, err := c.httpClient.Do(req)
		if err != nil {
			t.Fatalf("GET model catalog: %v", err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read model catalog: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET model catalog: HTTP %d: %s", resp.StatusCode, truncate(raw, 1400))
		}
		var catalog struct {
			ModelIDs []string `json:"model_ids"`
		}
		if err := json.Unmarshal(raw, &catalog); err != nil {
			t.Fatalf("decode model catalog: %v (body: %s)", err, truncate(raw, 1400))
		}
		for _, id := range catalog.ModelIDs {
			if id == ttsHD {
				t.Logf("model catalog lists %s (%d model ids)", ttsHD, len(catalog.ModelIDs))
				return
			}
		}
		t.Errorf("model catalog does not list %s (%d model ids): %s", ttsHD, len(catalog.ModelIDs), truncate(raw, 1400))
	})

	// The settlement assertion: the terminal record's echoed payload
	// must still carry "emotion":"happy".
	t.Run("emotion key echoed", func(t *testing.T) {
		got, err := c.SynthesizeSpeech(ctx, "Once upon a time, a small dragon lost her shoe.",
			"happy", "English_expressive_narrator", ttsHD)
		if err != nil {
			t.Fatalf("live SynthesizeSpeech with emotion: %v", err)
		}
		var rec emotionEchoRecord
		if err := json.Unmarshal(got, &rec); err != nil {
			t.Fatalf("decode terminal record: %v (record: %s)", err, truncate(got, 1400))
		}
		if rec.Status != "success" {
			t.Errorf("terminal record status = %q, want %q (record: %s)", rec.Status, "success", truncate(got, 1400))
		}
		if rec.Payload.Emotion != "happy" {
			t.Errorf("echoed payload emotion = %q, want %q — upstream stripped or rejected the key; narration would render emotion-less with every check green (record: %s)", rec.Payload.Emotion, "happy", truncate(got, 1400))
			return
		}
		t.Logf("echoed payload carries %q — upstream accepted the emotion key on the %s wire", rec.Payload.Emotion, ttsHD)
	})
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// pathModelCatalog is the request-queue model catalog GET — the same
// apikey prefix as pathRequestQueue. It answers 200 with a model_ids
// array (live-observed 2026-09-05) but publishes no per-model
// parameter schemas, which is exactly why the emotion key needs its
// own echo probe rather than a schema lookup.
const pathModelCatalog = "/api/v1/ie/requestqueue/apikey/models"

// emotionEchoPayload is the client-authored TTS payload as the queue
// echoes it back: the four keys SynthesizeSpeech always sends plus the
// emotion key under test. Typed, matching the package's named-struct
// rule; an absent emotion key decodes to "" and fails the probe.
type emotionEchoPayload struct {
	Text                    string `json:"text"`
	VoiceID                 string `json:"voice_id"`
	NeedNoiseReduction      bool   `json:"need_noise_reduction"`
	NeedVolumnNormalization bool   `json:"need_volumn_normalization"`
	Emotion                 string `json:"emotion"`
}

// emotionEchoRecord is the minimal terminal request-queue record the
// emotion probe decodes: status plus the echoed payload subtree
// (t2b-t5b-live-record.md). Nothing else in the record is consulted.
type emotionEchoRecord struct {
	Status  string             `json:"status"`
	Payload emotionEchoPayload `json:"payload"`
}
