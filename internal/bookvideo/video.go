package bookvideo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"
)

// resolveConfig populates defaults and verifies configuration values.
func resolveConfig(cfg Config) (Config, error) {
	if cfg.FFmpegPath == "" {
		cfg.FFmpegPath = DefaultFFmpegPath
	}
	if cfg.TitleCardDuration <= 0 {
		cfg.TitleCardDuration = DefaultTitleCardDuration
	}
	if cfg.EndCardDuration <= 0 {
		cfg.EndCardDuration = DefaultEndCardDuration
	}
	if cfg.EndCardDomain == "" {
		cfg.EndCardDomain = DefaultEndCardDomain
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = DefaultConcurrency
	}
	if cfg.FontFile != "" {
		if _, err := os.Stat(cfg.FontFile); err != nil {
			return cfg, fmt.Errorf("%w: font file not accessible: %v", ErrInvalidInput, err)
		}
	}
	if cfg.Runner == nil {
		if _, err := exec.LookPath(cfg.FFmpegPath); err != nil {
			return cfg, fmt.Errorf("%w: %v", ErrFFmpegNotFound, err)
		}
		cfg.Runner = defaultRunner{}
	}
	return cfg, nil
}

// materializeFont returns a path to a drawtext font file: cfg.FontFile when
// set, otherwise the embedded Fredoka Regular instance written into dir. The
// runtime image ships no fonts and no fontconfig config, so every drawtext
// needs an explicit fontfile (verified in-container); the embedded face is
// the film's caption/card face (§T10g D4). The caller removes the returned
// file when cleanup is non-nil.
func materializeFont(cfg Config, dir string) (fontPath string, cleanup func(), err error) {
	if cfg.FontFile != "" {
		return cfg.FontFile, nil, nil
	}
	p, err := writeTempFile(dir, "fredoka-*.ttf", fredokaRegularTTF)
	if err != nil {
		return "", nil, fmt.Errorf("materialize font: %w", err)
	}
	return p, func() { _ = os.Remove(p) }, nil
}

// buildFontOpt renders the leading drawtext option fragment for a font path,
// escaped for the filtergraph, ending with the option separator colon.
func buildFontOpt(fontPath string) string {
	return "fontfile=" + escapeFilterPath(fontPath) + ":"
}

// artAndBandVF is the page segment's geometry prefix: the illustration is
// scaled to fill the 4:5 top area (increase + crop, so a stray aspect fills
// rather than bars — §T10g) and the 270 px caption band below is the --surface
// colour. Every page segment shares it so concat -c copy stays free.
func artAndBandVF() string {
	return fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,pad=%d:%d:0:0:color=%s,setsar=1,",
		artWidth, artHeight, artWidth, artHeight, frameWidth, frameHeight, surfaceColor)
}

// captionDrawtext renders one caption block: a textfile of pre-wrapped lines,
// centred per line with text_align=C at the band's vertical centre. Never one
// drawtext per line and never text= — the words are model/child output and
// belong in a file (§T10g).
func captionDrawtext(fontPath, captionFile string, fontSize int) string {
	return fmt.Sprintf("drawtext=%stextfile=%s:fontcolor=%s:fontsize=%d:line_spacing=0:expansion=none:text_align=C:x=(w-text_w)/2:y=%d+((%d-text_h)/2)",
		buildFontOpt(fontPath), escapeFilterPath(captionFile), inkColor, fontSize, artHeight, bandHeight)
}

// writeCaptionFile lays out text for the caption band and writes the wrapped
// lines to a scratch file in dir. It returns the textfile path, the drawtext
// fontsize chosen by the layout, and an error if the write fails. Empty text
// yields no file and fontSize 0 — callers then omit the caption drawtext.
func writeCaptionFile(dir, text string) (path string, fontSize int, err error) {
	layout := layoutCaption(text)
	if len(layout.lines) == 0 {
		return "", 0, nil
	}
	p, err := writeTempFile(dir, "caption-*.txt", []byte(strings.Join(layout.lines, "\n")))
	if err != nil {
		return "", 0, err
	}
	return p, layout.fontSize, nil
}

// BuildTitleCard renders a blurred title card from the provided page image with
// story title and optional byline text. The card shares the master geometry
// (1080×1620) so it concat-copies with pages and the end card; its ground is
// --film where the 4:5 page art does not reach (§T10g).
func BuildTitleCard(ctx context.Context, cfg Config, imagePath, title, byline, outPath string) error {
	if imagePath == "" {
		return fmt.Errorf("%w: title card requires an image path", ErrInvalidInput)
	}
	if title == "" {
		return fmt.Errorf("%w: title card requires a title", ErrInvalidInput)
	}
	if outPath == "" {
		return fmt.Errorf("%w: title card requires an output path", ErrInvalidInput)
	}

	resolved, err := resolveConfig(cfg)
	if err != nil {
		return err
	}

	workDir := resolved.WorkDir
	if workDir == "" {
		workDir = filepath.Dir(outPath)
	}

	fontPath, cleanup, err := materializeFont(resolved, workDir)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	titleFile, err := writeTempFile(workDir, "title-*.txt", []byte(title))
	if err != nil {
		return fmt.Errorf("write title text: %w", err)
	}
	defer os.Remove(titleFile) // best effort cleanup

	var bylineFile string
	formattedByline := formatByline(byline)
	if formattedByline != "" {
		var err error
		bylineFile, err = writeTempFile(workDir, "byline-*.txt", []byte(formattedByline))
		if err != nil {
			return fmt.Errorf("write byline text: %w", err)
		}
		defer os.Remove(bylineFile) // best effort cleanup
	}

	fontOpt := buildFontOpt(fontPath)

	var vf strings.Builder
	vf.WriteString(fmt.Sprintf(
		"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=%s,setsar=1,",
		frameWidth, frameHeight, frameWidth, frameHeight, filmColor))
	vf.WriteString("boxblur=18:2,eq=brightness=-0.22:saturation=0.8,")

	if formattedByline != "" {
		vf.WriteString(fmt.Sprintf("drawtext=%stextfile=%s:fontcolor=white:fontsize=56:x=(w-text_w)/2:y=(h-text_h)/2-40,", fontOpt, escapeFilterPath(titleFile)))
		vf.WriteString(fmt.Sprintf("drawtext=%stextfile=%s:fontcolor=white:fontsize=36:x=(w-text_w)/2:y=(h-text_h)/2+40,", fontOpt, escapeFilterPath(bylineFile)))
	} else {
		vf.WriteString(fmt.Sprintf("drawtext=%stextfile=%s:fontcolor=white:fontsize=56:x=(w-text_w)/2:y=(h-text_h)/2,", fontOpt, escapeFilterPath(titleFile)))
	}
	vf.WriteString("format=yuv420p")

	durStr := fmt.Sprintf("%.3f", resolved.TitleCardDuration.Seconds())

	args := []string{
		"-loop", "1",
		"-t", durStr,
		"-i", imagePath,
		"-f", "lavfi",
		"-t", durStr,
		"-i", "anullsrc=channel_layout=stereo:sample_rate=44100",
		"-vf", vf.String(),
		"-r", "25",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "20",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-ac", "2",
		"-shortest",
		"-movflags", "+faststart",
		"-y",
		outPath,
	}

	return runFFmpeg(ctx, resolved.Runner, resolved.FFmpegPath, args)
}

// BuildPageSegment encodes a single page image, its caption words and its
// narration audio (when present) into an MP4 segment. The page is 1080×1350 of
// art over a 270 px caption band on --surface carrying the page's own words.
//
// Audio tier (T10g): when audioPath is non-empty the narration drives the
// segment exactly as before (-shortest against the clip, captions add no
// duration). When it is empty the page is silent: a synthesized anullsrc
// track holds the page for captionHold(text) — words/2.0 s, floor 4 s, cap
// 14 s — so every tier still produces a concat-copyable aac 44100 stereo
// segment.
func BuildPageSegment(ctx context.Context, cfg Config, imagePath, text, audioPath, outPath string) error {
	if imagePath == "" || outPath == "" {
		return fmt.Errorf("%w: page segment requires image and output paths", ErrInvalidInput)
	}

	resolved, err := resolveConfig(cfg)
	if err != nil {
		return err
	}

	workDir := resolved.WorkDir
	if workDir == "" {
		workDir = filepath.Dir(outPath)
	}

	fontPath, cleanup, err := materializeFont(resolved, workDir)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	vf := artAndBandVF()
	captionFile, fontSize, err := writeCaptionFile(workDir, text)
	if err != nil {
		return fmt.Errorf("write caption text: %w", err)
	}
	if captionFile != "" {
		defer os.Remove(captionFile) // best effort cleanup
		vf += captionDrawtext(fontPath, captionFile, fontSize)
	}
	vf += ",format=yuv420p"

	args := []string{
		"-loop", "1",
		"-i", imagePath,
	}
	if audioPath != "" {
		// Narrated tier: the clip's length governs via -shortest.
		args = append(args, "-i", audioPath)
	} else {
		// Silent tier: anullsrc of the words-derived hold length; -shortest
		// ends the looped image when the silence does.
		holdStr := fmt.Sprintf("%.3f", captionHold(text).Seconds())
		args = append(args,
			"-f", "lavfi",
			"-t", holdStr,
			"-i", "anullsrc=channel_layout=stereo:sample_rate=44100")
	}
	args = append(args,
		"-vf", vf,
		"-r", "25",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "20",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-ac", "2",
		"-shortest",
		"-movflags", "+faststart",
		"-y",
		outPath,
	)

	return runFFmpeg(ctx, resolved.Runner, resolved.FFmpegPath, args)
}

// BuildEndCard encodes a flat --film color end card with site branding and
// domain text, at the same 1080×1620 geometry as every other segment
// (§T10g: the cards' ground is --film).
func BuildEndCard(ctx context.Context, cfg Config, outPath string) error {
	if outPath == "" {
		return fmt.Errorf("%w: end card requires an output path", ErrInvalidInput)
	}

	resolved, err := resolveConfig(cfg)
	if err != nil {
		return err
	}

	workDir := resolved.WorkDir
	if workDir == "" {
		workDir = filepath.Dir(outPath)
	}

	fontPath, cleanup, err := materializeFont(resolved, workDir)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	endTitleFile, err := writeTempFile(workDir, "end_title-*.txt", []byte("Made with Thutapi"))
	if err != nil {
		return fmt.Errorf("write end title text: %w", err)
	}
	defer os.Remove(endTitleFile) // best effort cleanup

	endDomainFile, err := writeTempFile(workDir, "end_domain-*.txt", []byte(resolved.EndCardDomain))
	if err != nil {
		return fmt.Errorf("write end domain text: %w", err)
	}
	defer os.Remove(endDomainFile) // best effort cleanup

	fontOpt := buildFontOpt(fontPath)

	var vf strings.Builder
	vf.WriteString(fmt.Sprintf("drawtext=%stextfile=%s:fontcolor=white:fontsize=56:x=(w-text_w)/2:y=(h-text_h)/2-30,", fontOpt, escapeFilterPath(endTitleFile)))
	vf.WriteString(fmt.Sprintf("drawtext=%stextfile=%s:fontcolor=0xaaaaaa:fontsize=32:x=(w-text_w)/2:y=(h-text_h)/2+40,", fontOpt, escapeFilterPath(endDomainFile)))
	vf.WriteString("format=yuv420p")

	durStr := fmt.Sprintf("%.3f", resolved.EndCardDuration.Seconds())

	args := []string{
		"-f", "lavfi",
		"-i", fmt.Sprintf("color=c=%s:s=%dx%d:r=25", filmColor, frameWidth, frameHeight),
		"-f", "lavfi",
		"-t", durStr,
		"-i", "anullsrc=channel_layout=stereo:sample_rate=44100",
		"-vf", vf.String(),
		"-r", "25",
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "20",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "44100",
		"-ac", "2",
		"-shortest",
		"-movflags", "+faststart",
		"-y",
		outPath,
	}

	return runFFmpeg(ctx, resolved.Runner, resolved.FFmpegPath, args)
}

// ConcatSegments concatenates pre-encoded video segments into a single MP4 and writes metadata tags.
func ConcatSegments(ctx context.Context, cfg Config, segmentPaths []string, title, outPath string) error {
	if len(segmentPaths) == 0 {
		return fmt.Errorf("%w: concat requires at least one segment", ErrInvalidInput)
	}
	if outPath == "" {
		return fmt.Errorf("%w: concat requires an output path", ErrInvalidInput)
	}

	resolved, err := resolveConfig(cfg)
	if err != nil {
		return err
	}

	workDir := resolved.WorkDir
	if workDir == "" {
		workDir = filepath.Dir(outPath)
	}

	var b strings.Builder
	for _, p := range segmentPaths {
		b.WriteString(fmt.Sprintf("file %s\n", escapeConcatPath(p)))
	}
	listFile, err := writeTempFile(workDir, "concat_list-*.txt", []byte(b.String()))
	if err != nil {
		return fmt.Errorf("write concat list: %w", err)
	}
	defer os.Remove(listFile) // best effort cleanup

	args := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", listFile,
		"-c", "copy",
		"-movflags", "+faststart",
	}

	if title != "" {
		args = append(args, "-metadata", "title="+title)
	}
	args = append(args,
		"-metadata", "artist=Thutapi",
		"-metadata", "comment=Made with Thutapi - "+resolved.EndCardDomain,
		"-y",
		outPath,
	)

	return runFFmpeg(ctx, resolved.Runner, resolved.FFmpegPath, args)
}

// Render encodes an entire story book into a finished MP4 video file at in.OutputPath.
func Render(ctx context.Context, cfg Config, in Input) error {
	if in.Title == "" {
		return fmt.Errorf("%w: story title is required", ErrInvalidInput)
	}
	if in.OutputPath == "" {
		return fmt.Errorf("%w: output path is required", ErrInvalidInput)
	}
	if len(in.Pages) == 0 {
		return fmt.Errorf("%w: story has no pages", ErrInvalidInput)
	}

	for _, p := range in.Pages {
		if p.ImagePath == "" && len(p.ImageBytes) == 0 {
			return fmt.Errorf("%w: page %d has no image path or bytes", ErrInvalidInput, p.N)
		}
		// T10g: the page's words are required — every page of the film shows
		// its own words, whether or not narration exists.
		if strings.TrimSpace(p.Text) == "" {
			return fmt.Errorf("%w: page %d has no words", ErrInvalidInput, p.N)
		}
		if p.ImagePath != "" {
			if _, err := os.Stat(p.ImagePath); err != nil {
				return fmt.Errorf("%w: page %d image file not accessible: %v", ErrInvalidInput, p.N, err)
			}
		}
		if p.AudioPath != "" {
			if _, err := os.Stat(p.AudioPath); err != nil {
				return fmt.Errorf("%w: page %d audio file not accessible: %v", ErrInvalidInput, p.N, err)
			}
		}
	}

	resolved, err := resolveConfig(cfg)
	if err != nil {
		return err
	}

	var workDir string
	if resolved.WorkDir != "" {
		workDir, err = os.MkdirTemp(resolved.WorkDir, "bookvideo-")
	} else {
		workDir, err = os.MkdirTemp("", "thutapi-bookvideo-")
	}
	if err != nil {
		return fmt.Errorf("create temp work dir: %w", err)
	}
	defer os.RemoveAll(workDir) // best effort cleanup

	resolved.WorkDir = workDir

	// Materialise the drawtext face once so every concurrent segment builder
	// reuses the same fontfile path (the runtime image has no fonts).
	fontPath, cleanup, err := materializeFont(resolved, workDir)
	if err != nil {
		return err
	}
	resolved.FontFile = fontPath
	if cleanup != nil {
		defer cleanup()
	}

	// Materialize page assets into workDir if passed as raw bytes.
	materializedImages := make([]string, len(in.Pages))
	materializedAudios := make([]string, len(in.Pages))

	for i, p := range in.Pages {
		n := p.N
		if n <= 0 {
			n = i + 1
		}

		if p.ImagePath != "" {
			materializedImages[i] = p.ImagePath
		} else {
			imgPath := filepath.Join(workDir, fmt.Sprintf("page-%02d.jpg", n))
			if err := os.WriteFile(imgPath, p.ImageBytes, 0o600); err != nil {
				return fmt.Errorf("write page %d image bytes: %w", n, err)
			}
			materializedImages[i] = imgPath
		}

		if p.AudioPath != "" {
			materializedAudios[i] = p.AudioPath
		} else if len(p.AudioBytes) > 0 {
			audPath := filepath.Join(workDir, fmt.Sprintf("narration-%02d.mp3", n))
			if err := os.WriteFile(audPath, p.AudioBytes, 0o600); err != nil {
				return fmt.Errorf("write page %d audio bytes: %w", n, err)
			}
			materializedAudios[i] = audPath
		}
		// No audio at all → the silent tier; BuildPageSegment synthesises a
		// words-derived hold.
	}

	titleSeg := filepath.Join(workDir, "seg-title.mp4")
	pageSegs := make([]string, len(in.Pages))
	for i := range in.Pages {
		pageSegs[i] = filepath.Join(workDir, fmt.Sprintf("seg-%02d.mp4", i+1))
	}
	endSeg := filepath.Join(workDir, "seg-end.mp4")

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(resolved.Concurrency)

	// Render title card.
	g.Go(func() error {
		return BuildTitleCard(gctx, resolved, materializedImages[0], in.Title, in.Byline, titleSeg)
	})

	// Render each page segment.
	for i := range in.Pages {
		idx := i
		g.Go(func() error {
			return BuildPageSegment(gctx, resolved, materializedImages[idx], in.Pages[idx].Text, materializedAudios[idx], pageSegs[idx])
		})
	}

	// Render end card.
	g.Go(func() error {
		return BuildEndCard(gctx, resolved, endSeg)
	})

	if err := g.Wait(); err != nil {
		return err
	}

	allSegments := make([]string, 0, len(in.Pages)+2)
	allSegments = append(allSegments, titleSeg)
	allSegments = append(allSegments, pageSegs...)
	allSegments = append(allSegments, endSeg)

	tmpOut := filepath.Join(workDir, "final.mp4")
	if err := ConcatSegments(ctx, resolved, allSegments, in.Title, tmpOut); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(in.OutputPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	return copyOrMoveFile(tmpOut, in.OutputPath)
}
