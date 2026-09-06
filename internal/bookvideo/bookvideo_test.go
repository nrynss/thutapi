package bookvideo

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// mockRunner records invoked commands and allows configuring custom returns.
type mockRunner struct {
	mu       sync.Mutex
	calls    [][]string
	output   []byte
	err      error
	customFn func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func (m *mockRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	call := append([]string{name}, args...)
	m.calls = append(m.calls, call)
	if m.customFn != nil {
		return m.customFn(ctx, name, args...)
	}
	return m.output, m.err
}

func (m *mockRunner) lastCall() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1]
}

// TestConfigDefaults verifies that leaving configuration fields empty properly resolves to documented defaults.
func TestConfigDefaults(t *testing.T) {
	runner := &mockRunner{}
	cfg := Config{
		Runner: runner,
	}

	resolved, err := resolveConfig(cfg)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}

	if resolved.FFmpegPath != DefaultFFmpegPath {
		t.Errorf("FFmpegPath = %q, want %q", resolved.FFmpegPath, DefaultFFmpegPath)
	}
	if resolved.TitleCardDuration != DefaultTitleCardDuration {
		t.Errorf("TitleCardDuration = %v, want %v", resolved.TitleCardDuration, DefaultTitleCardDuration)
	}
	if resolved.EndCardDuration != DefaultEndCardDuration {
		t.Errorf("EndCardDuration = %v, want %v", resolved.EndCardDuration, DefaultEndCardDuration)
	}
	if resolved.EndCardDomain != DefaultEndCardDomain {
		t.Errorf("EndCardDomain = %q, want %q", resolved.EndCardDomain, DefaultEndCardDomain)
	}
	if resolved.Concurrency != DefaultConcurrency {
		t.Errorf("Concurrency = %d, want %d", resolved.Concurrency, DefaultConcurrency)
	}
}

// TestConfigFontValidation verifies font path validation in resolveConfig.
func TestConfigFontValidation(t *testing.T) {
	runner := &mockRunner{}
	cfg := Config{
		Runner:   runner,
		FontFile: filepath.Join(t.TempDir(), "nonexistent-font.ttf"),
	}

	_, err := resolveConfig(cfg)
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
}

// TestConfigLookPathFailure asserts ErrFFmpegNotFound when no Runner is provided and binary is missing.
func TestConfigLookPathFailure(t *testing.T) {
	cfg := Config{
		FFmpegPath: "nonexistent-ffmpeg-binary-that-cannot-exist-12345",
	}

	_, err := resolveConfig(cfg)
	if !errors.Is(err, ErrFFmpegNotFound) {
		t.Errorf("err = %v, want ErrFFmpegNotFound", err)
	}
}

// TestBuildTitleCard_ArgumentConstruction asserts the exact filter and arguments sent to ffmpeg.
func TestBuildTitleCard_ArgumentConstruction(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	cfg := Config{
		Runner:            runner,
		WorkDir:           tmpDir,
		TitleCardDuration: 4 * time.Second,
	}

	img := filepath.Join(tmpDir, "p1.jpg")
	if err := os.WriteFile(img, []byte("fake-jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmpDir, "title.mp4")

	// With byline "Mira"
	if err := BuildTitleCard(context.Background(), cfg, img, "Mira's Big Adventure", "Mira", out); err != nil {
		t.Fatalf("BuildTitleCard: %v", err)
	}

	call := runner.lastCall()
	if len(call) == 0 {
		t.Fatal("no command run")
	}

	joined := strings.Join(call, " ")
	if !strings.Contains(joined, "boxblur=18:2,eq=brightness=-0.22:saturation=0.8") {
		t.Errorf("filter missing boxblur/brightness: %s", joined)
	}
	if !strings.Contains(joined, "scale=1080:1620:force_original_aspect_ratio=decrease,pad=1080:1620:(ow-iw)/2:(oh-ih)/2:color=0x12241e,setsar=1") {
		t.Errorf("filter missing 1080x1620 --film geometry: %s", joined)
	}
	if !strings.Contains(joined, "fontfile=") {
		t.Errorf("title card drawtext missing fontfile (embedded Fredoka): %s", joined)
	}
	if !strings.Contains(joined, "drawtext=") || !strings.Contains(joined, "textfile=") {
		t.Errorf("title card missing drawtext textfile: %s", joined)
	}
	if strings.Contains(joined, "text=") && !strings.Contains(joined, "textfile=") {
		t.Errorf("inline text= detected in arguments, must use textfile=: %s", joined)
	}
	if !strings.Contains(joined, "-shortest") {
		t.Errorf("missing -shortest flag: %s", joined)
	}
	if !strings.Contains(joined, "-movflags +faststart") {
		t.Errorf("missing -movflags +faststart: %s", joined)
	}
	if !strings.Contains(joined, "4.000") {
		t.Errorf("missing 4.000s duration argument: %s", joined)
	}

	// Without byline
	runner.calls = nil
	if err := BuildTitleCard(context.Background(), cfg, img, "No Byline Story", "", out); err != nil {
		t.Fatalf("BuildTitleCard without byline: %v", err)
	}
	callNoByline := runner.lastCall()
	joinedNoByline := strings.Join(callNoByline, " ")
	if strings.Count(joinedNoByline, "drawtext=") != 1 {
		t.Errorf("expected exactly 1 drawtext filter when byline is empty, got %s", joinedNoByline)
	}
}

// TestBuildTitleCard_WithFontFile asserts that FontFile is incorporated into drawtext.
func TestBuildTitleCard_WithFontFile(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	font := filepath.Join(tmpDir, "font.ttf")
	if err := os.WriteFile(font, []byte("font"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Runner:   runner,
		WorkDir:  tmpDir,
		FontFile: font,
	}

	img := filepath.Join(tmpDir, "p1.jpg")
	if err := os.WriteFile(img, []byte("fake-jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmpDir, "title.mp4")

	if err := BuildTitleCard(context.Background(), cfg, img, "Title", "by Mira", out); err != nil {
		t.Fatalf("BuildTitleCard: %v", err)
	}
	joined := strings.Join(runner.lastCall(), " ")
	if !strings.Contains(joined, "fontfile=") {
		t.Errorf("expected fontfile in drawtext filter: %s", joined)
	}
}

// TestBuildPageSegment_ArgumentConstruction asserts page segment arguments
// and geometry: 1080×1350 art over a 1080×270 --surface band with a caption
// drawtext (textfile, text_align=C, --ink words), narrated with -shortest.
func TestBuildPageSegment_ArgumentConstruction(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	cfg := Config{
		Runner:  runner,
		WorkDir: tmpDir,
	}

	img := filepath.Join(tmpDir, "p1.jpg")
	aud := filepath.Join(tmpDir, "narration.mp3")
	out := filepath.Join(tmpDir, "seg.mp4")
	_ = os.WriteFile(img, []byte("jpg"), 0o600) // test fixture
	_ = os.WriteFile(aud, []byte("mp3"), 0o600) // test fixture

	if err := BuildPageSegment(context.Background(), cfg, img, "Mira opens the garden gate.", aud, out); err != nil {
		t.Fatalf("BuildPageSegment: %v", err)
	}

	call := runner.lastCall()
	joined := strings.Join(call, " ")
	wantSubstrings := []string{
		"-loop 1",
		"-i " + img,
		"-i " + aud,
		"scale=1080:1350:force_original_aspect_ratio=increase,crop=1080:1350,pad=1080:1620:0:0:color=0xd8efe3,setsar=1,",
		"drawtext=",
		"textfile=",
		"fontcolor=0x17332b",
		"expansion=none",
		"text_align=C",
		"y=1350+((270-text_h)/2)",
		"-r 25",
		"-c:v libx264",
		"-preset veryfast",
		"-crf 20",
		"-c:a aac",
		"-b:a 128k",
		"-ar 44100",
		"-ac 2",
		"-shortest",
		"-movflags +faststart",
		out,
	}

	for _, sub := range wantSubstrings {
		if !strings.Contains(joined, sub) {
			t.Errorf("BuildPageSegment missing substring %q in command %s", sub, joined)
		}
	}
	if strings.Contains(joined, ":text=") && !strings.Contains(joined, "textfile=") {
		t.Errorf("inline text= detected in page segment, must use textfile=: %s", joined)
	}
}

// TestBuildPageSegment_SilentTier asserts that a page with no narration
// synthesises anullsrc of the words-derived hold and still carries the
// caption: no narration input, no -shortest against a clip, same geometry.
func TestBuildPageSegment_SilentTier(t *testing.T) {
	var captions []string
	runner := &mockRunner{
		customFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			// Read the caption textfile while BuildPageSegment still owns it.
			for _, a := range args {
				if strings.Contains(a, "textfile=") {
					idx := strings.Index(a, "textfile=")
					rest := a[idx+len("textfile="):]
					end := strings.Index(rest, ":")
					if end < 0 {
						return nil, fmt.Errorf("textfile option unterminated in %q", a)
					}
					data, err := os.ReadFile(rest[:end])
					if err != nil {
						return nil, fmt.Errorf("read caption file: %w", err)
					}
					captions = append(captions, string(data))
				}
			}
			return []byte("ok"), nil
		},
	}
	tmpDir := t.TempDir()
	cfg := Config{
		Runner:  runner,
		WorkDir: tmpDir,
	}

	img := filepath.Join(tmpDir, "p1.jpg")
	out := filepath.Join(tmpDir, "seg.mp4")
	_ = os.WriteFile(img, []byte("jpg"), 0o600) // test fixture

	// Ten words → 5.000 s hold (10/2.0).
	text := "one two three four five six seven eight nine ten"
	if err := BuildPageSegment(context.Background(), cfg, img, text, "", out); err != nil {
		t.Fatalf("BuildPageSegment silent: %v", err)
	}

	call := runner.lastCall()
	joined := strings.Join(call, " ")
	want := []string{
		"-i " + img,
		"-t 5.000",
		"anullsrc=channel_layout=stereo:sample_rate=44100",
		"-shortest",
		"scale=1080:1350:force_original_aspect_ratio=increase,crop=1080:1350,pad=1080:1620:0:0:color=0xd8efe3,setsar=1,",
		"drawtext=",
		"textfile=",
		"text_align=C",
		"fontfile=",
	}
	for _, sub := range want {
		if !strings.Contains(joined, sub) {
			t.Errorf("silent page segment missing %q in %s", sub, joined)
		}
	}
	if strings.Contains(joined, "-i "+filepath.Join(tmpDir, "narration")) {
		t.Errorf("silent page segment carries a narration input: %s", joined)
	}

	// The caption textfile holds the wrapped words; no inline text=.
	if len(captions) != 1 {
		t.Fatalf("caption files read = %d, want 1", len(captions))
	}
	if !strings.Contains(captions[0], "one two three") {
		t.Errorf("caption file content = %q, want the page words", captions[0])
	}
	if strings.Contains(joined, ":text=") {
		t.Errorf("inline text= detected, must use textfile=: %s", joined)
	}
}

// TestBuildPageSegment_SilentHoldClamps pins the hold duration floor and
// ceiling on the silent tier.
func TestBuildPageSegment_SilentHoldClamps(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	cfg := Config{
		Runner:  runner,
		WorkDir: tmpDir,
	}
	img := filepath.Join(tmpDir, "p1.jpg")
	_ = os.WriteFile(img, []byte("jpg"), 0o600) // test fixture

	// One word → below the 4 s floor.
	out := filepath.Join(tmpDir, "seg-floor.mp4")
	if err := BuildPageSegment(context.Background(), cfg, img, "hello", "", out); err != nil {
		t.Fatalf("BuildPageSegment floor: %v", err)
	}
	if got := strings.Join(runner.lastCall(), " "); !strings.Contains(got, "-t 4.000") {
		t.Errorf("one-word page hold = %s, want the 4.000 s floor", got)
	}

	// 60 words → 30 s, above the 14 s ceiling.
	words := strings.Fields("one two three four five six seven eight nine ten " +
		"eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty " +
		"twentyone twentytwo twentythree twentyfour twentyfive twentysix twentyseven twentyeight twentynine thirty " +
		"thirtyone thirtytwo thirtythree thirtyfour thirtyfive thirtysix thirtyseven thirtyeight thirtynine forty " +
		"fortyone fortytwo fortythree fortyfour fortyfive fortysix fortyseven fortyeight fortynine fifty " +
		"fiftyone fiftytwo fiftythree fiftyfour fiftyfive fiftysix fiftyseven fiftyeight fiftynine sixty")
	out = filepath.Join(tmpDir, "seg-cap.mp4")
	if err := BuildPageSegment(context.Background(), cfg, img, strings.Join(words, " "), "", out); err != nil {
		t.Fatalf("BuildPageSegment ceiling: %v", err)
	}
	if got := strings.Join(runner.lastCall(), " "); !strings.Contains(got, "-t 14.000") {
		t.Errorf("60-word page hold = %s, want the 14.000 s ceiling", got)
	}
}

// TestBuildEndCard_ArgumentConstruction asserts end card arguments, color, and drawtext.
func TestBuildEndCard_ArgumentConstruction(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	cfg := Config{
		Runner:          runner,
		WorkDir:         tmpDir,
		EndCardDuration: 2500 * time.Millisecond,
		EndCardDomain:   "custom.thutapi.dev",
	}

	out := filepath.Join(tmpDir, "end.mp4")
	if err := BuildEndCard(context.Background(), cfg, out); err != nil {
		t.Fatalf("BuildEndCard: %v", err)
	}

	call := runner.lastCall()
	joined := strings.Join(call, " ")
	wantSubstrings := []string{
		"color=c=0x12241e:s=1080x1620:r=25",
		"anullsrc=channel_layout=stereo:sample_rate=44100",
		"textfile=",
		"-shortest",
		"2.500",
		out,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(joined, sub) {
			t.Errorf("BuildEndCard missing %q in %s", sub, joined)
		}
	}
}

// TestConcatSegments_ArgumentConstruction asserts concat demuxer arguments and metadata tags.
func TestConcatSegments_ArgumentConstruction(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	cfg := Config{
		Runner:        runner,
		WorkDir:       tmpDir,
		EndCardDomain: "thutapi.nryn.dev",
	}

	segs := []string{
		filepath.Join(tmpDir, "title.mp4"),
		filepath.Join(tmpDir, "seg1.mp4"),
		filepath.Join(tmpDir, "end.mp4"),
	}
	out := filepath.Join(tmpDir, "final.mp4")

	title := "Bramble's Journey"
	if err := ConcatSegments(context.Background(), cfg, segs, title, out); err != nil {
		t.Fatalf("ConcatSegments: %v", err)
	}

	call := runner.lastCall()
	joined := strings.Join(call, " ")
	wantSubstrings := []string{
		"-f concat",
		"-safe 0",
		"-c copy",
		"-movflags +faststart",
		"-metadata title=" + title,
		"-metadata artist=Thutapi",
		"-metadata comment=Made with Thutapi - thutapi.nryn.dev",
		out,
	}
	for _, sub := range wantSubstrings {
		if !strings.Contains(joined, sub) {
			t.Errorf("ConcatSegments missing %q in %s", sub, joined)
		}
	}
}

// TestRender_EndToEnd_Mock verifies Render coordinating pages, title card, end card, and concat.
func TestRender_EndToEnd_Mock(t *testing.T) {
	runner := &mockRunner{
		customFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			// Mock command creates the requested output file
			out := args[len(args)-1]
			if err := os.WriteFile(out, []byte("mock-mp4-data"), 0o600); err != nil {
				return nil, err
			}
			return []byte("ok"), nil
		},
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "output", "book.mp4")

	in := Input{
		Title:  "The Secret Garden",
		Byline: "Mira",
		Pages: []PageInput{
			{
				N:          1,
				ImageBytes: []byte("page1-image-data"),
				AudioBytes: []byte("page1-audio-data"),
				Duration:   2 * time.Second,
				Text:       "The secret garden gate opens.",
			},
			{
				N:          2,
				ImageBytes: []byte("page2-image-data"),
				AudioBytes: []byte("page2-audio-data"),
				Duration:   3 * time.Second,
				Text:       "Bramble chases the butterfly home.",
			},
		},
		OutputPath: outPath,
	}

	cfg := Config{
		Runner: runner,
	}

	total, err := Render(context.Background(), cfg, in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// The returned total is the render phase's arithmetic (contract
	// row C3 of t12-round1.md): title 3.5 s + page 1 (2 s clip) +
	// page 2 (3 s clip) + end card 3.0 s. The mix's wind-down anchors
	// to exactly this value.
	if want := DefaultTitleCardDuration + 2*time.Second + 3*time.Second + DefaultEndCardDuration; total != want {
		t.Errorf("render total = %v, want %v (cards + Go-known clip durations)", total, want)
	}

	// Output file must exist and contain the mocked mp4 content
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) != "mock-mp4-data" {
		t.Errorf("output data = %q, want %q", string(data), "mock-mp4-data")
	}

	// 1 title card + 2 pages + 1 end card + 1 concat = 5 ffmpeg calls
	if len(runner.calls) != 5 {
		t.Errorf("runner call count = %d, want 5", len(runner.calls))
	}
}

// TestErrorSentinels asserts all ErrInvalidInput and ErrFFmpegFailed branches.
func TestErrorSentinels(t *testing.T) {
	runner := &mockRunner{}
	tmpDir := t.TempDir()
	cfg := Config{Runner: runner, WorkDir: tmpDir}

	// BuildTitleCard errors
	if err := BuildTitleCard(context.Background(), cfg, "", "Title", "By", "out.mp4"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty image, got %v", err)
	}
	if err := BuildTitleCard(context.Background(), cfg, "img.jpg", "", "By", "out.mp4"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty title, got %v", err)
	}
	if err := BuildTitleCard(context.Background(), cfg, "img.jpg", "Title", "By", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty outPath, got %v", err)
	}

	// BuildPageSegment errors
	if err := BuildPageSegment(context.Background(), cfg, "", "words", "aud.mp3", "out.mp4"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty image, got %v", err)
	}
	if err := BuildPageSegment(context.Background(), cfg, "img.jpg", "words", "aud.mp3", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty outPath, got %v", err)
	}
	// No audio is valid — silent tier (image + words only).
	if err := BuildPageSegment(context.Background(), cfg, "img.jpg", "words", "", "out.mp4"); err != nil {
		t.Errorf("page segment without audio must be valid (silent tier), got %v", err)
	}

	// BuildEndCard errors
	if err := BuildEndCard(context.Background(), cfg, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty outPath, got %v", err)
	}

	// ConcatSegments errors
	if err := ConcatSegments(context.Background(), cfg, nil, "Title", "out.mp4"); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for nil segments, got %v", err)
	}
	if err := ConcatSegments(context.Background(), cfg, []string{"seg1.mp4"}, "Title", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty outPath, got %v", err)
	}

	// Render input validation errors
	if _, err := Render(context.Background(), cfg, Input{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty Title, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{Title: "T"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for empty OutputPath, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{Title: "T", OutputPath: "out.mp4"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for 0 pages, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages:      []PageInput{{N: 1}},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for page without image or words, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages: []PageInput{{
			N:          1,
			ImageBytes: []byte("img"),
		}},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for page without words, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages: []PageInput{{
			N:          1,
			ImagePath:  "/nonexistent/path/to/img.jpg",
			Text:       "page words",
			AudioBytes: []byte("aud"),
			Duration:   time.Second,
		}},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for non-existent image path, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages: []PageInput{{
			N:          1,
			ImageBytes: []byte("img"),
			Text:       "page words",
			AudioPath:  "/nonexistent/path/to/aud.mp3",
			Duration:   time.Second,
		}},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for non-existent audio path, got %v", err)
	}
	// T12 (contract row C3): a narration page without its Go-known
	// Duration, and a Duration on a page without narration, are both
	// invalid — the film total must be fully computable in Go.
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages: []PageInput{{
			N:          1,
			ImageBytes: []byte("img"),
			Text:       "page words",
			AudioBytes: []byte("aud"),
		}},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("narration without Duration must fail validation, got %v", err)
	}
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages: []PageInput{{
			N:          1,
			ImageBytes: []byte("img"),
			Text:       "page words",
			Duration:   2 * time.Second,
		}},
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Duration without narration must fail validation, got %v", err)
	}
	// A silent page (image + words, no audio) is valid — the run may still
	// fail later (mock runner writes no files), but not at validation.
	if _, err := Render(context.Background(), cfg, Input{
		Title:      "T",
		OutputPath: "out.mp4",
		Pages: []PageInput{{
			N:          1,
			ImageBytes: []byte("img"),
			Text:       "page words",
		}},
	}); errors.Is(err, ErrInvalidInput) {
		t.Errorf("silent page must pass validation, got %v", err)
	}

	// Runner failure in BuildPageSegment
	failRunner := &mockRunner{err: errors.New("exit status 1")}
	failCfg := Config{Runner: failRunner, WorkDir: tmpDir}
	err := BuildPageSegment(context.Background(), failCfg, "img.jpg", "words", "aud.mp3", "out.mp4")
	if !errors.Is(err, ErrFFmpegFailed) {
		t.Errorf("expected ErrFFmpegFailed on runner error, got %v", err)
	}
}

// TestDefaultRunner_ContextCancellation asserts clean context cancellation behavior with defaultRunner.
func TestDefaultRunner_ContextCancellation(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep binary not found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	runner := defaultRunner{}
	out, err := runner.Run(ctx, "sleep", "1")
	if !errors.Is(err, ErrFFmpegFailed) {
		t.Errorf("err = %v, want ErrFFmpegFailed", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled wrapped", err)
	}
	if out != nil {
		t.Errorf("out = %v, want nil slice on context cancellation error", out)
	}
}

// TestDefaultRunner_NilOutputOnError asserts that defaultRunner.Run returns a nil slice alongside non-nil error.
func TestDefaultRunner_NilOutputOnError(t *testing.T) {
	runner := defaultRunner{}
	ctx := context.Background()

	// Non-existent command
	out, err := runner.Run(ctx, "nonexistent-cmd-xyz-98765")
	if err == nil {
		t.Fatal("expected error for nonexistent command")
	}
	if out != nil {
		t.Errorf("out = %v, want nil slice on error", out)
	}
	if !errors.Is(err, ErrFFmpegFailed) {
		t.Errorf("err = %v, want ErrFFmpegFailed", err)
	}

	// Command that produces output before failing
	if sh, err := exec.LookPath("sh"); err == nil {
		out, err = runner.Run(ctx, sh, "-c", "echo 'fail output'; exit 1")
		if err == nil {
			t.Fatal("expected error for exit status 1")
		}
		if out != nil {
			t.Errorf("out = %v, want nil slice on error when command produced output", out)
		}
		if !errors.Is(err, ErrFFmpegFailed) {
			t.Errorf("err = %v, want ErrFFmpegFailed", err)
		}
	}
}

// TestFormatByline exercises all byline formatting variations.
func TestFormatByline(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"Mira", "by Mira"},
		{"by Mira", "by Mira"},
		{"By Mira", "By Mira"},
		{"BY Mira", "BY Mira"},
		{"  Mira  ", "by Mira"},
	}

	for _, c := range cases {
		got := formatByline(c.in)
		if got != c.want {
			t.Errorf("formatByline(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEscapeHelpers exercises string escaping helpers.
func TestEscapeHelpers(t *testing.T) {
	p := `/tmp/dir with'quotes:and\backslashes,brackets[1];file.txt`
	escapedFilter := escapeFilterPath(p)
	if strings.HasPrefix(escapedFilter, "'") || strings.HasSuffix(escapedFilter, "'") {
		t.Errorf("escapeFilterPath should not enclose in single quotes: %s", escapedFilter)
	}
	if !strings.Contains(escapedFilter, `\\:`) {
		t.Errorf("escapeFilterPath should escape colons with \\\\: %s", escapedFilter)
	}
	if !strings.Contains(escapedFilter, `\\\'`) {
		t.Errorf("escapeFilterPath should escape single quotes with \\\\\\': %s", escapedFilter)
	}
	if !strings.Contains(escapedFilter, `\\\\`) {
		t.Errorf("escapeFilterPath should escape backslashes with \\\\\\\\: %s", escapedFilter)
	}
	if !strings.Contains(escapedFilter, `\,`) || !strings.Contains(escapedFilter, `\[`) || !strings.Contains(escapedFilter, `\;`) {
		t.Errorf("escapeFilterPath should escape filtergraph separators: %s", escapedFilter)
	}

	escapedConcat := escapeConcatPath(p)
	if !strings.HasPrefix(escapedConcat, "'") || !strings.HasSuffix(escapedConcat, "'") {
		t.Errorf("escapeConcatPath should enclose in single quotes: %s", escapedConcat)
	}
	if !strings.Contains(escapedConcat, `'\''`) {
		t.Errorf("escapeConcatPath should escape single quotes with '\\'': %s", escapedConcat)
	}
}

// makeSilentWAV generates a valid 44.1kHz stereo 16-bit PCM WAV byte slice of silence.
func makeSilentWAV(seconds int) []byte {
	sampleRate := 44100
	channels := 2
	bitsPerSample := 16
	byteRate := sampleRate * channels * (bitsPerSample / 8)
	blockAlign := channels * (bitsPerSample / 8)
	dataSize := seconds * byteRate

	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bitsPerSample))
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	return buf
}

// makeTestJPEG creates a simple valid JPEG image in memory.
func makeTestJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 120, G: 80, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil) // in-memory encode cannot fail
	return buf.Bytes()
}

// TestRender_RealFFmpeg executes the entire pipeline with a real ffmpeg binary if available.
func TestRender_RealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "book.mp4")

	// Use real fixture if present, or generate valid JPEG bytes.
	var img1, img2 []byte
	realFixturePath := filepath.Join("..", "..", "data", "live", "t6b-book", "page-01.jpg")
	if data, err := os.ReadFile(realFixturePath); err == nil {
		img1 = data
		img2 = data
	} else {
		img1 = makeTestJPEG(1080, 1350)
		img2 = makeTestJPEG(1080, 1350)
	}

	aud1 := makeSilentWAV(2)
	aud2 := makeSilentWAV(3)

	in := Input{
		Title:  "Mira and Bramble's Big Adventure",
		Byline: "Mira",
		Pages: []PageInput{
			{
				N:          1,
				ImageBytes: img1,
				AudioBytes: aud1,
				Duration:   2 * time.Second,
				Text:       "Mira opens the garden gate to start the morning adventure.",
			},
			{
				N:          2,
				ImageBytes: img2,
				AudioBytes: aud2,
				Duration:   3 * time.Second,
				Text:       "Bramble chases a yellow butterfly across the sunny green lawn.",
			},
		},
		OutputPath: outPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Default Config exercises all default paths with real ffmpeg. The
	// two narration clips are 2 s and 3 s WAVs, so the returned total
	// is the exact render arithmetic: 3.5 + 2 + 3 + 3.0.
	total, err := Render(ctx, Config{}, in)
	if err != nil {
		t.Fatalf("Render with real ffmpeg: %v", err)
	}
	if want := DefaultTitleCardDuration + 2*time.Second + 3*time.Second + DefaultEndCardDuration; total != want {
		t.Errorf("render total = %v, want %v", total, want)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("output file stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output MP4 file is empty")
	}
	t.Logf("rendered MP4 size: %d bytes at %s", info.Size(), outPath)

	// If ffprobe is available, verify video geometry and stream types.
	if _, err := exec.LookPath("ffprobe"); err == nil {
		probeCmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,codec_name", "-of", "csv=p=0", outPath)
		out, err := probeCmd.Output()
		if err != nil {
			t.Logf("ffprobe error: %v", err)
		} else {
			probeStr := strings.TrimSpace(string(out))
			t.Logf("ffprobe stream output: %s", probeStr)
			if !strings.Contains(probeStr, "1080") || !strings.Contains(probeStr, "1620") {
				t.Errorf("expected 1080x1620 in ffprobe output, got %s", probeStr)
			}
		}

		// Verify metadata tags
		tagCmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags=title,artist,comment", "-of", "default=noprint_wrappers=1", outPath)
		tagOut, err := tagCmd.Output()
		if err == nil {
			tagStr := string(tagOut)
			t.Logf("ffprobe tags: %s", tagStr)
			if !strings.Contains(tagStr, "TAG:title=Mira and Bramble's Big Adventure") {
				t.Errorf("missing title tag in output: %s", tagStr)
			}
			if !strings.Contains(tagStr, "TAG:artist=Thutapi") {
				t.Errorf("missing artist tag in output: %s", tagStr)
			}
			if !strings.Contains(tagStr, "TAG:comment=Made with Thutapi - thutapi.nryn.dev") {
				t.Errorf("missing comment tag in output: %s", tagStr)
			}
		}
	}
}

// TestCopyOrMoveFile exercises copyOrMoveFile success, error, and cross-device fallback paths.
func TestCopyOrMoveFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	dst := filepath.Join(tmpDir, "dst.txt")

	if err := os.WriteFile(src, []byte("hello copy"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Normal move/rename
	if err := copyOrMoveFile(src, dst); err != nil {
		t.Fatalf("copyOrMoveFile: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "hello copy" {
		t.Fatalf("unexpected content: %s, err: %v", string(data), err)
	}

	// Error when source does not exist
	if err := copyOrMoveFile("/nonexistent/file/xyz", filepath.Join(tmpDir, "out.txt")); err == nil {
		t.Error("expected error for non-existent source file")
	}

	// Error when destination directory does not exist
	if err := os.WriteFile(src, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyOrMoveFile(src, filepath.Join(tmpDir, "no-such-dir", "out.txt")); err == nil {
		t.Error("expected error for non-existent destination directory")
	}
}

// TestRender_SubcommandFailures asserts that Render bubbles up failures from individual stages.
func TestRender_SubcommandFailures(t *testing.T) {
	tmpDir := t.TempDir()

	in := Input{
		Title:      "Fail Story",
		OutputPath: filepath.Join(tmpDir, "book.mp4"),
		Pages: []PageInput{
			{
				N:          1,
				ImageBytes: []byte("img"),
				AudioBytes: []byte("aud"),
				Duration:   2 * time.Second,
				Text:       "A page that fails.",
			},
		},
	}

	// Fail on concat
	failConcatRunner := &mockRunner{
		customFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			for _, a := range args {
				if a == "concat" {
					return nil, errors.New("concat failed")
				}
			}
			out := args[len(args)-1]
			_ = os.WriteFile(out, []byte("mock"), 0o600) // mock intermediate segment
			return []byte("ok"), nil
		},
	}

	_, err := Render(context.Background(), Config{Runner: failConcatRunner}, in)
	if !errors.Is(err, ErrFFmpegFailed) {
		t.Errorf("expected ErrFFmpegFailed on concat failure, got %v", err)
	}

	// Fail on segment build
	failSegRunner := &mockRunner{
		customFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, errors.New("segment failed")
		},
	}
	_, err = Render(context.Background(), Config{Runner: failSegRunner}, in)
	if !errors.Is(err, ErrFFmpegFailed) {
		t.Errorf("expected ErrFFmpegFailed on segment build failure, got %v", err)
	}
}

// TestBuildTitleCard_ConcurrentScratchFiles pins H1: multiple concurrent goroutines calling
// BuildTitleCard in the same workDir must not collide on scratch text files or delete each other's files.
func TestBuildTitleCard_ConcurrentScratchFiles(t *testing.T) {
	tmpDir := t.TempDir()
	img := filepath.Join(tmpDir, "p1.jpg")
	if err := os.WriteFile(img, []byte("fake-jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}

	const concurrency = 12
	var readTitles sync.Map

	runner := &mockRunner{
		customFn: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			// Find textfile=<path> in filter arguments and verify file content while BuildTitleCard runs.
			for _, a := range args {
				if strings.Contains(a, "textfile=") {
					parts := strings.Split(a, ",")
					for _, part := range parts {
						if strings.Contains(part, "fontsize=56") {
							idx := strings.Index(part, "textfile=")
							if idx != -1 {
								sub := part[idx+len("textfile="):]
								endIdx := strings.Index(sub, ":")
								if endIdx != -1 {
									filePath := sub[:endIdx]
									data, err := os.ReadFile(filePath)
									if err != nil {
										return nil, fmt.Errorf("read textfile %q: %w", filePath, err)
									}
									readTitles.Store(string(data), true)
								}
							}
						}
					}
				}
			}
			return []byte("ok"), nil
		},
	}

	cfg := Config{
		Runner:  runner,
		WorkDir: tmpDir,
	}

	g, ctx := errgroup.WithContext(context.Background())
	for i := 0; i < concurrency; i++ {
		idx := i
		g.Go(func() error {
			title := fmt.Sprintf("Story Distinct Title %d", idx)
			out := filepath.Join(tmpDir, fmt.Sprintf("out-%d.mp4", idx))
			return BuildTitleCard(ctx, cfg, img, title, "", out)
		})
	}

	if err := g.Wait(); err != nil {
		t.Fatalf("concurrent BuildTitleCard failed: %v", err)
	}

	for i := 0; i < concurrency; i++ {
		title := fmt.Sprintf("Story Distinct Title %d", i)
		if _, ok := readTitles.Load(title); !ok {
			t.Errorf("title %q was not successfully read by runner", title)
		}
	}
}

// TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg pins M1: paths containing single quotes
// must not break ffmpeg's filtergraph parser.
func TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}

	tmpDir := t.TempDir()
	quoteDir := filepath.Join(tmpDir, "child's_stories:and spaces")
	if err := os.MkdirAll(quoteDir, 0o755); err != nil {
		t.Fatal(err)
	}

	img := filepath.Join(quoteDir, "page'01.jpg")
	if err := os.WriteFile(img, makeTestJPEG(100, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(quoteDir, "title'card.mp4")

	cfg := Config{
		WorkDir: quoteDir,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := BuildTitleCard(ctx, cfg, img, "Bramble's Big Adventure", "Mira", out); err != nil {
		t.Fatalf("BuildTitleCard failed on single quote path: %v", err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output MP4 file is empty")
	}
}

// TestDockerfile_FFmpegPinnedByDigest pins L2: ffmpeg base image must be pinned by sha256 digest.
func TestDockerfile_FFmpegPinnedByDigest(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	content := string(data)
	want := "FROM mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4 AS ff"
	if !strings.Contains(content, want) {
		t.Errorf("Dockerfile does not contain expected pinned ffmpeg base image line:\nwant: %s", want)
	}
}
