package bookgen

import (
	"context"

	"thutapi/internal/bookvideo"
)

// FFmpegRenderer adapts bookvideo.Render to the videoRenderer seam this
// package declares (PLAN.md invariant 3): the pipeline depends on
// Render(ctx, Input), never on the concrete package or the ffmpeg
// binary on the PATH. Construction is cheap; the zero-config render
// resolves bookvideo's defaults (ffmpeg via exec.LookPath) at the
// first render, exactly as bookvideo itself does.
type FFmpegRenderer struct {
	cfg bookvideo.Config
}

// NewFFmpegRenderer returns a renderer that runs bookvideo.Render with
// cfg — the ffmpeg path, durations, work directory and font knobs
// cmd/thutapi's run() passes down (the zero Config is usable and means
// bookvideo's defaults).
func NewFFmpegRenderer(cfg bookvideo.Config) *FFmpegRenderer {
	return &FFmpegRenderer{cfg: cfg}
}

// Render renders in through ffmpeg into in.OutputPath.
func (r *FFmpegRenderer) Render(ctx context.Context, in bookvideo.Input) error {
	return bookvideo.Render(ctx, r.cfg, in)
}
