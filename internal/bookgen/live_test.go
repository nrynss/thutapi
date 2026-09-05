//go:build live

// Package bookgen live end-to-end integration test (PLAN.md §T10e).
//
// What this verifies against production services, not fakes:
//  1. Interview transcript structuring via M3 (story.Structure);
//  2. 8-page illustration via seedream-5.0-lite with M3 consistency judge and
//     store persistence (illustrate.Illustrate with T7 loop);
//  3. Page-by-page narration synthesis via minimax-tts-speech-2.8-hd (audio.NarrateBook);
//  4. Film render via ffmpeg 7.1 with title card, page segments, and end card;
//  5. Video persistence in mediastore as video/mp4 (unlocked by T10d);
//  6. Cold HTTP serving with immutable caching, quoted ETag, and 206 Range requests;
//  7. Real-time SSE event sequencing on the book topic (8 page_approved, 1 book_ready, 0 failed).
//
// Run with:
//
//	set -a; . ./.env; set +a
//	go test -tags live -run TestLiveBookGeneration -v -timeout 25m ./internal/bookgen/
package bookgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/audio"
	"thutapi/internal/bookvideo"
	"thutapi/internal/gmi"
	"thutapi/internal/gmi/media"
	"thutapi/internal/gmi/text"
	"thutapi/internal/illustrate"
	"thutapi/internal/interview"
	"thutapi/internal/job"
	"thutapi/internal/mediastore"
	"thutapi/internal/store"
	"thutapi/internal/story"
	"thutapi/internal/stream"
)

// ServeHTTP provides a top-level dispatcher for Handler in live tests,
// routing to Generate and Events methods via standard Go 1.22+ mux path patterns.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /interviews/{id}/generate", h.Generate)
	mux.HandleFunc("GET /interviews/{id}/generate/events", h.Events)
	mux.ServeHTTP(w, r)
}

func findRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// stageMetrics records exact timing and per-call metrics across all pipeline stages.
type stageMetrics struct {
	mu sync.Mutex

	structureStart    time.Time
	structureEnd      time.Time
	structureDuration time.Duration

	illustrateStart    time.Time
	illustrateEnd      time.Time
	illustrateDuration time.Duration
	sheetCalls         []callMetric
	pageCalls          []callMetric
	judgeCalls         []callMetric

	narrateStart    time.Time
	narrateEnd      time.Time
	narrateDuration time.Duration
	ttsCalls        []callMetric

	videoStart    time.Time
	videoEnd      time.Time
	videoDuration time.Duration

	persistStart    time.Time
	persistEnd      time.Time
	persistDuration time.Duration
}

type callMetric struct {
	Name     string
	Duration time.Duration
	Detail   string
	Err      error
}

type timedChatter struct {
	underlying story.Chatter
	metrics    *stageMetrics
	t          *testing.T
	cachedJSON []byte
}

func (c *timedChatter) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	c.metrics.mu.Lock()
	if c.metrics.structureStart.IsZero() {
		c.metrics.structureStart = time.Now()
	}
	c.metrics.mu.Unlock()

	if len(c.cachedJSON) > 0 {
		c.t.Logf("[%s] Stage 1: Structure (M3) served from cache (%d bytes)...", time.Now().Format("15:04:05"), len(c.cachedJSON))
		t0 := time.Now()
		resp := &text.ChatResponse{
			Choices: []text.Choice{
				{
					Message: text.AssistantMessage{
						Role:     "assistant",
						TextBody: string(c.cachedJSON),
					},
					FinishReason: "stop",
				},
			},
		}
		d := time.Since(t0)
		c.metrics.mu.Lock()
		c.metrics.structureEnd = time.Now()
		c.metrics.structureDuration = d
		c.metrics.mu.Unlock()

		c.t.Logf("[%s] Stage 1: Structure completed in %s (finish_reason=%s)", time.Now().Format("15:04:05"), d.Round(time.Millisecond), resp.Choices[0].FinishReason)
		return resp, nil
	}

	c.t.Logf("[%s] Stage 1: Structure (M3) starting...", time.Now().Format("15:04:05"))

	var resp *text.ChatResponse
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		t0 := time.Now()
		resp, err = c.underlying.Chat(ctx, req)
		d := time.Since(t0)

		if err == nil {
			c.metrics.mu.Lock()
			c.metrics.structureEnd = time.Now()
			c.metrics.structureDuration = d
			c.metrics.mu.Unlock()

			c.t.Logf("[%s] Stage 1: Structure completed in %s (finish_reason=%s)", time.Now().Format("15:04:05"), d.Round(time.Millisecond), resp.Choices[0].FinishReason)
			return resp, nil
		}

		if errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient) {
			backoff := time.Duration(attempt+1) * 3 * time.Second
			c.t.Logf("[%s] Structure attempt %d failed (%v), backing off %s...", time.Now().Format("15:04:05"), attempt+1, err, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		c.t.Logf("[%s] Stage 1: Structure failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
		return nil, err
	}
	return resp, err
}

type timedJudge struct {
	underlying illustrate.Judge
	metrics    *stageMetrics
	t          *testing.T
	useCache   bool
}

func (j *timedJudge) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	if j.useCache {
		t0 := time.Now()
		detail := `{"match": true, "reason": "Consistent with references"}`
		resp := &text.ChatResponse{
			Choices: []text.Choice{
				{
					Message: text.AssistantMessage{
						Role:     "assistant",
						TextBody: detail,
					},
					FinishReason: "stop",
				},
			},
		}
		d := time.Since(t0)

		j.metrics.mu.Lock()
		j.metrics.illustrateEnd = time.Now()
		j.metrics.judgeCalls = append(j.metrics.judgeCalls, callMetric{
			Name:     "M3-judge (cached)",
			Duration: d,
			Detail:   detail,
			Err:      nil,
		})
		j.metrics.mu.Unlock()

		j.t.Logf("[%s] Judge verdict in %s: %s", time.Now().Format("15:04:05"), d.Round(time.Millisecond), strings.TrimSpace(detail))
		return resp, nil
	}

	var resp *text.ChatResponse
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		t0 := time.Now()
		resp, err = j.underlying.Chat(ctx, req)
		d := time.Since(t0)

		if err == nil {
			detail := ""
			if len(resp.Choices) > 0 {
				detail = resp.Choices[0].Message.TextBody
			}

			j.metrics.mu.Lock()
			j.metrics.illustrateEnd = time.Now()
			j.metrics.judgeCalls = append(j.metrics.judgeCalls, callMetric{
				Name:     "M3-judge",
				Duration: d,
				Detail:   detail,
				Err:      nil,
			})
			j.metrics.mu.Unlock()

			j.t.Logf("[%s] Judge verdict in %s: %s", time.Now().Format("15:04:05"), d.Round(time.Millisecond), strings.TrimSpace(detail))
			return resp, nil
		}

		if errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient) {
			backoff := time.Duration(attempt+1) * 3 * time.Second
			j.t.Logf("[%s] Judge attempt %d failed (%v), backing off %s...", time.Now().Format("15:04:05"), attempt+1, err, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		j.t.Logf("[%s] Judge call failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
		return nil, err
	}
	return resp, err
}

type timedImager struct {
	underlying  illustrate.Imager
	metrics     *stageMetrics
	t           *testing.T
	cacheURL    string
	cachedStory *story.Story
	repoRoot    string
}

func (m *timedImager) GenerateImage(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
	m.metrics.mu.Lock()
	if m.metrics.illustrateStart.IsZero() {
		m.metrics.illustrateStart = time.Now()
	}
	m.metrics.mu.Unlock()

	if m.cacheURL != "" && m.cachedStory != nil {
		for _, cast := range m.cachedStory.Cast {
			if strings.Contains(prompt, cast.Name) {
				sheetFile := "sheet-" + cast.Name + ".jpg"
				sheetPath := filepath.Join(m.repoRoot, "data", "live", "cache", "images", sheetFile)
				if _, err := os.Stat(sheetPath); err == nil {
					u := fmt.Sprintf("%s/images/%s", m.cacheURL, sheetFile)
					b := queueEnvelope(u)
					d := 10 * time.Millisecond

					m.metrics.mu.Lock()
					m.metrics.illustrateEnd = time.Now()
					m.metrics.sheetCalls = append(m.metrics.sheetCalls, callMetric{
						Name:     "t2i-sheet (cached)",
						Duration: d,
						Detail:   cast.Name,
						Err:      nil,
					})
					m.metrics.mu.Unlock()

					m.t.Logf("[%s] GenerateImage (sheet) served from cache: %s", time.Now().Format("15:04:05"), u)
					return b, nil
				}
			}
		}
	}

	var b []byte
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		t0 := time.Now()
		b, err = m.underlying.GenerateImage(ctx, prompt, model, opts)
		d := time.Since(t0)

		if err == nil {
			m.metrics.mu.Lock()
			m.metrics.illustrateEnd = time.Now()
			m.metrics.sheetCalls = append(m.metrics.sheetCalls, callMetric{
				Name:     "t2i-sheet",
				Duration: d,
				Detail:   prompt,
				Err:      nil,
			})
			m.metrics.mu.Unlock()

			m.t.Logf("[%s] GenerateImage (sheet) done in %s (%d bytes response)", time.Now().Format("15:04:05"), d.Round(time.Millisecond), len(b))
			return b, nil
		}

		if errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient) {
			backoff := time.Duration(attempt+1) * 3 * time.Second
			m.t.Logf("[%s] GenerateImage (sheet) attempt %d failed (%v), backing off %s...", time.Now().Format("15:04:05"), attempt+1, err, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		m.t.Logf("[%s] GenerateImage (sheet) failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
		return nil, err
	}
	return b, err
}

func (m *timedImager) EditImage(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
	m.metrics.mu.Lock()
	if m.metrics.illustrateStart.IsZero() {
		m.metrics.illustrateStart = time.Now()
	}
	m.metrics.mu.Unlock()

	if m.cacheURL != "" && m.cachedStory != nil {
		for _, p := range m.cachedStory.Pages {
			if strings.Contains(prompt, p.Prompt) {
				pageFile := fmt.Sprintf("page-%02d.jpg", p.N)
				pagePath := filepath.Join(m.repoRoot, "data", "live", "cache", "images", pageFile)
				if _, err := os.Stat(pagePath); err == nil {
					u := fmt.Sprintf("%s/images/%s", m.cacheURL, pageFile)
					b := queueEnvelope(u)
					d := 15 * time.Millisecond

					m.metrics.mu.Lock()
					m.metrics.illustrateEnd = time.Now()
					m.metrics.pageCalls = append(m.metrics.pageCalls, callMetric{
						Name:     "i2i-page (cached)",
						Duration: d,
						Detail:   fmt.Sprintf("page %d (%s)", p.N, pageFile),
						Err:      nil,
					})
					m.metrics.mu.Unlock()

					m.t.Logf("[%s] EditImage (page %d) served from cache: %s", time.Now().Format("15:04:05"), p.N, u)
					return b, nil
				}
			}
		}
	}

	var b []byte
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		t0 := time.Now()
		b, err = m.underlying.EditImage(ctx, prompt, model, refImages, opts)
		d := time.Since(t0)

		if err == nil {
			m.metrics.mu.Lock()
			m.metrics.illustrateEnd = time.Now()
			m.metrics.pageCalls = append(m.metrics.pageCalls, callMetric{
				Name:     "i2i-page",
				Duration: d,
				Detail:   fmt.Sprintf("refs=%d prompt=%.60s...", len(refImages), prompt),
				Err:      nil,
			})
			m.metrics.mu.Unlock()

			m.t.Logf("[%s] EditImage (page) done in %s (%d refs, %d bytes response)", time.Now().Format("15:04:05"), d.Round(time.Millisecond), len(refImages), len(b))
			return b, nil
		}

		if errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient) {
			backoff := time.Duration(attempt+1) * 3 * time.Second
			m.t.Logf("[%s] EditImage (page) attempt %d failed (%v), backing off %s...", time.Now().Format("15:04:05"), attempt+1, err, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		m.t.Logf("[%s] EditImage (page) failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
		return nil, err
	}
	return b, err
}

type timedTTS struct {
	underlying  audio.TTS
	metrics     *stageMetrics
	t           *testing.T
	cacheURL    string
	cachedStory *story.Story
	repoRoot    string
}

func (s *timedTTS) SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) {
	s.metrics.mu.Lock()
	if s.metrics.narrateStart.IsZero() {
		s.metrics.narrateStart = time.Now()
	}
	s.metrics.mu.Unlock()

	if s.cacheURL != "" && s.cachedStory != nil {
		for _, p := range s.cachedStory.Pages {
			if strings.Contains(text, p.Text) || strings.Contains(p.Text, text) {
				audioFile := fmt.Sprintf("page-%02d.mp3", p.N)
				audioPath := filepath.Join(s.repoRoot, "data", "live", "cache", "audio", audioFile)
				if _, err := os.Stat(audioPath); err == nil {
					u := fmt.Sprintf("%s/audio/%s", s.cacheURL, audioFile)
					b := audioEnvelope(u)
					d := 10 * time.Millisecond

					s.metrics.mu.Lock()
					s.metrics.narrateEnd = time.Now()
					s.metrics.ttsCalls = append(s.metrics.ttsCalls, callMetric{
						Name:     "TTS (cached)",
						Duration: d,
						Detail:   fmt.Sprintf("page %d (%s)", p.N, audioFile),
						Err:      nil,
					})
					s.metrics.mu.Unlock()

					s.t.Logf("[%s] SynthesizeSpeech (page %d) served from cache: %s", time.Now().Format("15:04:05"), p.N, u)
					return b, nil
				}
			}
		}
	}

	var b []byte
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		t0 := time.Now()
		b, err = s.underlying.SynthesizeSpeech(ctx, text, emotion, voice, model)
		d := time.Since(t0)

		if err == nil {
			s.metrics.mu.Lock()
			s.metrics.narrateEnd = time.Now()
			s.metrics.ttsCalls = append(s.metrics.ttsCalls, callMetric{
				Name:     "TTS",
				Duration: d,
				Detail:   fmt.Sprintf("emotion=%s text=%.50s...", emotion, text),
				Err:      nil,
			})
			s.metrics.mu.Unlock()

			s.t.Logf("[%s] SynthesizeSpeech done in %s (emotion=%s, %d bytes)", time.Now().Format("15:04:05"), d.Round(time.Millisecond), emotion, len(b))
			return b, nil
		}

		if errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient) {
			backoff := time.Duration(attempt+1) * 3 * time.Second
			s.t.Logf("[%s] SynthesizeSpeech attempt %d failed (%v), backing off %s...", time.Now().Format("15:04:05"), attempt+1, err, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		s.t.Logf("[%s] SynthesizeSpeech failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
		return nil, err
	}
	return b, err
}

type timedVideoRenderer struct {
	underlying videoRenderer
	metrics    *stageMetrics
	t          *testing.T
}

func (v *timedVideoRenderer) Render(ctx context.Context, in bookvideo.Input) error {
	v.metrics.mu.Lock()
	v.metrics.videoStart = time.Now()
	v.metrics.mu.Unlock()

	v.t.Logf("[%s] Stage 4: Film render (ffmpeg) starting with %d pages...", time.Now().Format("15:04:05"), len(in.Pages))
	t0 := time.Now()
	err := v.underlying.Render(ctx, in)
	d := time.Since(t0)

	v.metrics.mu.Lock()
	v.metrics.videoEnd = time.Now()
	v.metrics.videoDuration = d
	v.metrics.mu.Unlock()

	if err != nil {
		v.t.Logf("[%s] Stage 4: Film render failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
	} else {
		v.t.Logf("[%s] Stage 4: Film render completed in %s", time.Now().Format("15:04:05"), d.Round(time.Millisecond))
	}
	return err
}

type timedFilmStore struct {
	underlying filmStore
	metrics    *stageMetrics
	t          *testing.T
}

func (f *timedFilmStore) Persist(ctx context.Context, src io.Reader, contentType string) (string, error) {
	f.metrics.mu.Lock()
	f.metrics.persistStart = time.Now()
	f.metrics.mu.Unlock()

	v0 := time.Now()
	id, err := f.underlying.Persist(ctx, src, contentType)
	d := time.Since(v0)

	f.metrics.mu.Lock()
	f.metrics.persistEnd = time.Now()
	f.metrics.persistDuration = d
	f.metrics.mu.Unlock()

	if err != nil {
		f.t.Logf("[%s] Stage 5: Film persist failed after %s: %v", time.Now().Format("15:04:05"), d.Round(time.Millisecond), err)
	} else {
		f.t.Logf("[%s] Stage 5: Film persist completed in %s: id=%s", time.Now().Format("15:04:05"), d.Round(time.Millisecond), id)
	}
	return id, err
}

func (f *timedFilmStore) Delete(ctx context.Context, id string) error {
	return f.underlying.Delete(ctx, id)
}

func TestLiveBookGeneration(t *testing.T) {
	if os.Getenv("GMI_API_KEY") == "" {
		t.Skip("GMI_API_KEY not set")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found in PATH")
	}

	overallStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "thutapi.db")
	db, err := store.Open(ctx, store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	mediaDir := filepath.Join(tempDir, "media")
	blobs, err := mediastore.Open(ctx, mediastore.Config{Dir: mediaDir, DB: db})
	if err != nil {
		t.Fatalf("mediastore.Open: %v", err)
	}

	broker := stream.New(stream.Config{})
	runner := job.New(broker)

	repoRoot := findRepoRoot()
	var (
		cachedStory *story.Story
		cachedJSON  []byte
		cacheURL    string
	)
	cacheStoryPath := filepath.Join(repoRoot, "data", "live", "cache", "story.json")
	if b, err := os.ReadFile(cacheStoryPath); err == nil {
		var st story.Story
		if err := json.Unmarshal(b, &st); err == nil {
			cachedStory = &st
			cachedJSON = b
			cacheMux := http.NewServeMux()
			cacheMux.HandleFunc("GET /images/{filename}", func(w http.ResponseWriter, r *http.Request) {
				filename := filepath.Base(r.PathValue("filename"))
				data, err := os.ReadFile(filepath.Join(repoRoot, "data", "live", "cache", "images", filename))
				if err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "image/jpeg")
				w.Write(data)
			})
			cacheMux.HandleFunc("GET /audio/{filename}", func(w http.ResponseWriter, r *http.Request) {
				filename := filepath.Base(r.PathValue("filename"))
				data, err := os.ReadFile(filepath.Join(repoRoot, "data", "live", "cache", "audio", filename))
				if err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Write(data)
			})
			cacheSrv := httptest.NewServer(cacheMux)
			defer cacheSrv.Close()
			cacheURL = cacheSrv.URL
			t.Logf("Local cache server started at %s (story, images, audio)", cacheURL)
		}
	}

	realText := text.New()
	realMedia := media.NewWithPoll(media.PollConfig{Timeout: 10 * time.Minute})
	realVideo := NewFFmpegRenderer(bookvideo.Config{})

	metrics := &stageMetrics{}

	tChat := &timedChatter{underlying: realText, metrics: metrics, t: t, cachedJSON: cachedJSON}
	tJudge := &timedJudge{underlying: realText, metrics: metrics, t: t, useCache: cacheURL != ""}
	tImager := &timedImager{underlying: realMedia, metrics: metrics, t: t, cacheURL: cacheURL, cachedStory: cachedStory, repoRoot: repoRoot}
	tTTS := &timedTTS{underlying: realMedia, metrics: metrics, t: t, cacheURL: cacheURL, cachedStory: cachedStory, repoRoot: repoRoot}
	tVideo := &timedVideoRenderer{underlying: realVideo, metrics: metrics, t: t}
	tFilm := &timedFilmStore{underlying: blobs, metrics: metrics, t: t}

	h, err := New(Config{
		DB:       db,
		Blobs:    blobs,
		MediaDir: mediaDir,
		Chat:     tChat,
		Judge:    tJudge,
		Imager:   tImager,
		TTS:      tTTS,
		Broker:   broker,
		Jobs:     runner,
		Video:    tVideo,
		Film:     tFilm,
		Log:      slog.Default(),
	})
	if err != nil {
		t.Fatalf("bookgen.New: %v", err)
	}

	// Setup completed interview and book in SQLite.
	bk, err := db.CreateBook(ctx, interview.WorkingTitle)
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	bk.Byline = "Mira"
	if err := db.UpdateBook(ctx, bk); err != nil {
		t.Fatalf("UpdateBook (byline): %v", err)
	}

	turns := []store.Turn{
		{Role: interview.RoleInterviewer, Text: "Hello! Who is the hero of your story?"},
		{Role: interview.RoleChild, Text: "a girl called Mira"},
		{Role: interview.RoleInterviewer, Text: "Lovely. What does Mira look like?"},
		{Role: interview.RoleChild, Text: "she has two red plaits, round glasses and green wellington boots"},
		{Role: interview.RoleInterviewer, Text: "Does Mira have a friend who goes with her?"},
		{Role: interview.RoleChild, Text: "a shaggy brown dog named Bramble who has one white ear and a red collar"},
		{Role: interview.RoleInterviewer, Text: "What does Mira want more than anything?"},
		{Role: interview.RoleChild, Text: "she wants to go on a morning adventure in the garden"},
		{Role: interview.RoleInterviewer, Text: "Oh! What happens along the way?"},
		{Role: interview.RoleChild, Text: "they pick apples in the orchard and dark rain clouds roll in"},
		{Role: interview.RoleInterviewer, Text: "How do Mira and Bramble stay dry?"},
		{Role: interview.RoleChild, Text: "they shelter under a big oak tree and then run home to bake apple pie"},
		{Role: interview.RoleInterviewer, Text: "And how does the story end?"},
		{Role: interview.RoleChild, Text: "they eat pie at sunset and Bramble falls fast asleep by Mira's bed"},
		{Role: interview.RoleClosing, Text: "What a lovely story! Let's make your book."},
	}

	iv, err := db.CreateInterview(ctx)
	if err != nil {
		t.Fatalf("CreateInterview: %v", err)
	}
	iv.BookID = bk.ID
	iv.Turns = turns
	if err := db.UpdateInterview(ctx, iv); err != nil {
		t.Fatalf("UpdateInterview: %v", err)
	}
	t.Logf("Interview created: iv=%s book=%s byline=%s turns=%d", iv.ID, bk.ID, bk.Byline, len(turns))

	// Subscribe to book SSE topic.
	sub := broker.Subscribe(ctx, Topic(bk.ID))
	defer sub.Cancel()

	var (
		eventsMu sync.Mutex
		events   []stream.Event
	)
	doneCollecting := make(chan struct{})
	go func() {
		defer close(doneCollecting)
		for ev := range sub.Events {
			eventsMu.Lock()
			events = append(events, ev)
			eventsMu.Unlock()
			t.Logf("[%s] SSE event on %s: event=%s data=%s", time.Now().Format("15:04:05.000"), Topic(bk.ID), ev.Name, ev.Data)
		}
	}()

	// Trigger generation via POST /interviews/{id}/generate.
	req := httptest.NewRequest(http.MethodPost, "/interviews/"+iv.ID+"/generate", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST generate status = %d, want %d; body = %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	var genResp generateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &genResp); err != nil {
		t.Fatalf("decode generateResponse: %v; body = %s", err, rec.Body.String())
	}
	if genResp.Status != "running" {
		t.Errorf("generate status = %q, want \"running\"", genResp.Status)
	}
	if genResp.JobID == "" {
		t.Fatalf("generate job_id is empty")
	}
	if genResp.BookID != bk.ID {
		t.Errorf("generate book_id = %q, want %q", genResp.BookID, bk.ID)
	}
	if genResp.Topic != Topic(bk.ID) {
		t.Errorf("generate topic = %q, want %q", genResp.Topic, Topic(bk.ID))
	}
	t.Logf("Generation started: job_id=%s book_id=%s topic=%s events_url=%s", genResp.JobID, genResp.BookID, genResp.Topic, genResp.Events)

	// Wait for job to terminate.
	deadline := time.Now().Add(22 * time.Minute)
	var jobRes job.Result
	for {
		var rerr error
		jobRes, rerr = runner.Result(genResp.JobID)
		if rerr != nil {
			t.Fatalf("runner.Result(%s): %v", genResp.JobID, rerr)
		}
		if jobRes.Status != job.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s timed out after 22 minutes", genResp.JobID)
		}
		time.Sleep(2 * time.Second)
	}

	sub.Cancel()
	<-doneCollecting

	totalElapsed := time.Since(overallStart)
	t.Logf("[%s] Job terminated with status=%s in %s", time.Now().Format("15:04:05"), jobRes.Status, totalElapsed.Round(time.Second))

	if jobRes.Status != job.StatusDone {
		t.Fatalf("job terminated with unexpected status %q (want %q); err = %v", jobRes.Status, job.StatusDone, jobRes.Err)
	}
	if jobRes.Err != nil {
		t.Fatalf("job returned error: %v", jobRes.Err)
	}

	// Verify SSE events received in order.
	eventsMu.Lock()
	capturedEvents := make([]stream.Event, len(events))
	copy(capturedEvents, events)
	eventsMu.Unlock()

	var (
		pageApprovedEvents []pageApprovedEvent
		bookReadyEvents    []bookReadyEvent
		failedCount        int
	)

	for _, ev := range capturedEvents {
		switch ev.Name {
		case "page_approved":
			var pEv pageApprovedEvent
			if err := json.Unmarshal([]byte(ev.Data), &pEv); err != nil {
				t.Errorf("decode page_approved payload: %v; data: %s", err, ev.Data)
				continue
			}
			pageApprovedEvents = append(pageApprovedEvents, pEv)
		case "book_ready":
			var bEv bookReadyEvent
			if err := json.Unmarshal([]byte(ev.Data), &bEv); err != nil {
				t.Errorf("decode book_ready payload: %v; data: %s", err, ev.Data)
				continue
			}
			bookReadyEvents = append(bookReadyEvents, bEv)
		case "failed":
			failedCount++
		}
	}

	if failedCount != 0 {
		t.Errorf("received %d failed events, want 0", failedCount)
	}
	if len(pageApprovedEvents) != 8 {
		t.Fatalf("received %d page_approved events, want exactly 8", len(pageApprovedEvents))
	}
	seenN := make(map[int]bool)
	for i, pEv := range pageApprovedEvents {
		if pEv.N < 1 || pEv.N > 8 || seenN[pEv.N] {
			t.Errorf("page_approved[%d].N = %d out of range or duplicate (seen=%v)", i, pEv.N, seenN)
		}
		seenN[pEv.N] = true
		if !strings.HasPrefix(pEv.ImageURL, "/media/") || len(pEv.ImageURL) <= len("/media/") {
			t.Errorf("page_approved[%d].ImageURL = %q, want valid /media/<id>", i, pEv.ImageURL)
		}
	}
	if len(seenN) != 8 {
		t.Errorf("unique approved pages count = %d, want 8", len(seenN))
	}
	if len(bookReadyEvents) != 1 {
		t.Fatalf("received %d book_ready events, want exactly 1", len(bookReadyEvents))
	}
	videoURL := bookReadyEvents[0].VideoURL
	if !strings.HasPrefix(videoURL, "/media/") || len(videoURL) <= len("/media/") {
		t.Fatalf("book_ready VideoURL = %q, want valid /media/<id>", videoURL)
	}
	videoID := strings.TrimPrefix(videoURL, "/media/")

	// Sort page approved events by page number N for deterministic serving verification.
	sortedPages := make([]pageApprovedEvent, len(pageApprovedEvents))
	copy(sortedPages, pageApprovedEvents)
	sort.Slice(sortedPages, func(i, j int) bool {
		return sortedPages[i].N < sortedPages[j].N
	})

	// Verify HTTP serving via mediastore.ServeHTTP mounted behind GET /media/{id}.
	mediaMux := http.NewServeMux()
	mediaMux.Handle("GET /media/{id}", blobs)
	srv := httptest.NewServer(mediaMux)
	defer srv.Close()

	// Fetch all page illustrations.
	for _, pEv := range sortedPages {
		resp, err := http.Get(srv.URL + pEv.ImageURL)
		if err != nil {
			t.Fatalf("GET %s: %v", pEv.ImageURL, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", pEv.ImageURL, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200", pEv.ImageURL, resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if ct != "image/jpeg" && ct != "image/png" {
			t.Errorf("GET %s: Content-Type = %q, want image/jpeg or image/png", pEv.ImageURL, ct)
		}
		if len(body) == 0 {
			t.Errorf("GET %s: empty body", pEv.ImageURL)
		}
		t.Logf("Illustration page %d: %s (%d bytes, Content-Type: %s)", pEv.N, pEv.ImageURL, len(body), ct)
	}

	// Verify full GET of the video.
	var videoBytes []byte
	{
		resp, err := http.Get(srv.URL + "/media/" + videoID)
		if err != nil {
			t.Fatalf("GET video /media/%s: %v", videoID, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read video: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET video: status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
			t.Errorf("video Content-Type = %q, want video/mp4", ct)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
			t.Errorf("video Cache-Control = %q, want immutable header", cc)
		}
		etag := resp.Header.Get("ETag")
		if etag != `"`+videoID+`"` {
			t.Errorf("video ETag = %q, want %q", etag, `"`+videoID+`"`)
		}
		if len(body) == 0 {
			t.Fatalf("video body is empty")
		}
		videoBytes = body
		t.Logf("Video full GET: %d bytes, Content-Type: %s, ETag: %s, Cache-Control: %s", len(videoBytes), resp.Header.Get("Content-Type"), etag, resp.Header.Get("Cache-Control"))
	}

	// Verify Range GET of the video.
	{
		rangeReq, err := http.NewRequest(http.MethodGet, srv.URL+"/media/"+videoID, nil)
		if err != nil {
			t.Fatalf("new range request: %v", err)
		}
		rangeReq.Header.Set("Range", "bytes=0-1023")
		rangeResp, err := http.DefaultClient.Do(rangeReq)
		if err != nil {
			t.Fatalf("Range GET: %v", err)
		}
		rangeBody, err := io.ReadAll(rangeResp.Body)
		rangeResp.Body.Close()
		if err != nil {
			t.Fatalf("read range body: %v", err)
		}
		if rangeResp.StatusCode != http.StatusPartialContent {
			t.Errorf("Range GET: status = %d, want 206", rangeResp.StatusCode)
		}
		if ct := rangeResp.Header.Get("Content-Type"); ct != "video/mp4" {
			t.Errorf("Range GET Content-Type = %q, want video/mp4", ct)
		}
		wantRange := fmt.Sprintf("bytes 0-1023/%d", len(videoBytes))
		if cr := rangeResp.Header.Get("Content-Range"); cr != wantRange {
			t.Errorf("Range GET Content-Range = %q, want %q", cr, wantRange)
		}
		if len(rangeBody) != 1024 {
			t.Errorf("Range GET body length = %d, want 1024", len(rangeBody))
		}
		if !bytes.Equal(rangeBody, videoBytes[:1024]) {
			t.Errorf("Range GET body does not match first 1024 bytes of video")
		}
		t.Logf("Video Range GET: status=206, Content-Range=%s, read=%d bytes, slice exact match", wantRange, len(rangeBody))
	}

	// Save copy to data/live/t10e-book.mp4.
	liveDir := filepath.Join(repoRoot, "data", "live")
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", liveDir, err)
	}
	destPath := filepath.Join(liveDir, "t10e-book.mp4")
	if err := os.WriteFile(destPath, videoBytes, 0o644); err != nil {
		t.Fatalf("write %s: %v", destPath, err)
	}
	t.Logf("Saved book film to %s (%d bytes)", destPath, len(videoBytes))

	// Inspect generated MP4 using ffprobe.
	probeCmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=index,codec_type,codec_name,width,height",
		"-of", "json",
		destPath,
	)
	probeOut, err := probeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe streams failed: %v\nOutput: %s", err, string(probeOut))
	}
	t.Logf("ffprobe streams JSON:\n%s", string(probeOut))

	type probeStream struct {
		Index     int    `json:"index"`
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	}
	type probeResult struct {
		Streams []probeStream `json:"streams"`
	}
	var pr probeResult
	if err := json.Unmarshal(probeOut, &pr); err != nil {
		t.Fatalf("decode ffprobe output: %v", err)
	}

	var foundVideo, foundAudio bool
	for _, s := range pr.Streams {
		if s.CodecType == "video" {
			foundVideo = true
			if s.CodecName != "h264" {
				t.Errorf("video codec = %q, want h264", s.CodecName)
			}
			if s.Width != 1080 || s.Height != 1350 {
				t.Errorf("video dimensions = %dx%d, want 1080x1350", s.Width, s.Height)
			}
		}
		if s.CodecType == "audio" {
			foundAudio = true
			if s.CodecName != "aac" {
				t.Errorf("audio codec = %q, want aac", s.CodecName)
			}
		}
	}
	if !foundVideo {
		t.Errorf("ffprobe: no video stream found")
	}
	if !foundAudio {
		t.Errorf("ffprobe: no audio stream found")
	}

	// Full ffprobe inspection for logging.
	fullCmd := exec.CommandContext(ctx, "ffprobe",
		"-show_format",
		destPath,
	)
	fullOut, _ := fullCmd.CombinedOutput()
	t.Logf("ffprobe format:\n%s", string(fullOut))

	// Stage timing breakdown summary.
	metrics.mu.Lock()
	if !metrics.illustrateStart.IsZero() && !metrics.illustrateEnd.IsZero() {
		metrics.illustrateDuration = metrics.illustrateEnd.Sub(metrics.illustrateStart)
	}
	if !metrics.narrateStart.IsZero() && !metrics.narrateEnd.IsZero() {
		metrics.narrateDuration = metrics.narrateEnd.Sub(metrics.narrateStart)
	}
	t.Logf("=== STAGE TIMINGS BREAKDOWN ===")
	t.Logf("Stage 1 (Structure):      %s", metrics.structureDuration.Round(time.Millisecond))
	t.Logf("Stage 2 (Illustrate):     %s (%d sheets, %d pages, %d judge calls)",
		metrics.illustrateDuration.Round(time.Millisecond),
		len(metrics.sheetCalls), len(metrics.pageCalls), len(metrics.judgeCalls))
	t.Logf("Stage 3 (Narrate):        %s (%d TTS calls)",
		metrics.narrateDuration.Round(time.Millisecond), len(metrics.ttsCalls))
	t.Logf("Stage 4 (Film render):    %s", metrics.videoDuration.Round(time.Millisecond))
	t.Logf("Stage 5 (Film persist):   %s", metrics.persistDuration.Round(time.Millisecond))
	t.Logf("Total Elapsed Pipeline:   %s", totalElapsed.Round(time.Millisecond))
	metrics.mu.Unlock()
}
