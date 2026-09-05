package bookvideo

import (
	"context"
	"errors"
	"time"
)

// Default configuration parameters for the book video pipeline.
const (
	// DefaultFFmpegPath is the default executable name for ffmpeg.
	DefaultFFmpegPath = "ffmpeg"

	// DefaultTitleCardDuration is the default duration for the title card segment.
	DefaultTitleCardDuration = 3500 * time.Millisecond

	// DefaultEndCardDuration is the default duration for the end card segment.
	DefaultEndCardDuration = 3000 * time.Millisecond

	// DefaultEndCardDomain is the default domain name drawn on the end card and in MP4 metadata.
	DefaultEndCardDomain = "thutapi.nryn.dev"

	// DefaultConcurrency is the default maximum number of concurrent ffmpeg segment renders.
	DefaultConcurrency = 4
)

var (
	// ErrInvalidInput indicates that the provided video pipeline input is missing required fields or malformed.
	ErrInvalidInput = errors.New("bookvideo: invalid input")

	// ErrFFmpegNotFound indicates that the ffmpeg executable could not be found.
	ErrFFmpegNotFound = errors.New("bookvideo: ffmpeg executable not found")

	// ErrFFmpegFailed indicates that an ffmpeg command failed during execution.
	ErrFFmpegFailed = errors.New("bookvideo: ffmpeg command failed")
)

// Runner executes an external command with an argument slice.
type Runner interface {
	// Run executes the command name with the provided args, returning combined output or an error.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Config holds configuration options for the book video generator.
type Config struct {
	// FFmpegPath is the path or command name of the ffmpeg binary.
	// Defaults to DefaultFFmpegPath ("ffmpeg") if empty.
	FFmpegPath string

	// WorkDir is the base directory used for writing temporary files.
	// If empty, an OS temporary directory is created for the run.
	WorkDir string

	// TitleCardDuration is the display duration for the title card.
	// Defaults to DefaultTitleCardDuration (3.5s) if zero or negative.
	TitleCardDuration time.Duration

	// EndCardDuration is the display duration for the end card.
	// Defaults to DefaultEndCardDuration (3.0s) if zero or negative.
	EndCardDuration time.Duration

	// EndCardDomain is the site domain drawn on the end card and embedded in metadata.
	// Defaults to DefaultEndCardDomain ("thutapi.nryn.dev") if empty.
	EndCardDomain string

	// FontFile is an optional path to a TTF or OTF font file for drawtext filters.
	// If empty, the package's embedded Fredoka-Regular instance is materialised
	// into the render workdir and passed as fontfile= to every drawtext — the
	// runtime image ships no fonts and no fontconfig config, so there is no
	// system fallback (§T10g D4). An explicit path overrides the embedded
	// default.
	FontFile string

	// Concurrency limits the number of page segments rendered in parallel.
	// Defaults to DefaultConcurrency (4) if zero or negative.
	Concurrency int

	// Runner executes external commands. If nil, exec.CommandContext is used.
	Runner Runner
}

// PageInput contains image and narration audio data for a single story page.
type PageInput struct {
	// N is the 1-indexed page number.
	N int

	// ImagePath is the filesystem path to the page image.
	ImagePath string

	// ImageBytes is the raw byte content of the page image, used if ImagePath is empty.
	ImageBytes []byte

	// AudioPath is the filesystem path to the narration audio.
	AudioPath string

	// AudioBytes is the raw byte content of the narration audio, used if AudioPath is empty.
	AudioBytes []byte

	// Text is the page's own words, drawn in the caption band beneath the
	// illustration (T10g). Required for every page: the film shows the words
	// whether or not narration exists, and a silent page's hold duration is
	// derived from them (silentHoldFor).
	Text string
}

// Input specifies the story details and sequence of pages to render into video.
type Input struct {
	// Title is the story title drawn on the title card. Required.
	Title string

	// Byline is the child's name or author byline drawn on the title card. Optional.
	Byline string

	// Pages is the ordered sequence of pages to encode. Must contain at least one page.
	Pages []PageInput

	// OutputPath is the destination path for the final MP4 video. Required.
	OutputPath string
}
