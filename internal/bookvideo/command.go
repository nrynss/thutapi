package bookvideo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// defaultRunner runs commands using exec.CommandContext.
type defaultRunner struct{}

func (defaultRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", ErrFFmpegFailed, ctx.Err())
		}
		return nil, fmt.Errorf("%w: %v (%s)", ErrFFmpegFailed, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// runFFmpeg executes an ffmpeg command via the configured runner and ensures any returned error wraps ErrFFmpegFailed.
func runFFmpeg(ctx context.Context, runner Runner, ffmpegPath string, args []string) error {
	_, err := runner.Run(ctx, ffmpegPath, args...)
	if err != nil {
		if !errors.Is(err, ErrFFmpegFailed) {
			return fmt.Errorf("%w: %w", ErrFFmpegFailed, err)
		}
		return err
	}
	return nil
}

// escapeFilterPath escapes special characters for ffmpeg filtergraph parameter values.
// In ffmpeg filtergraphs, parameters undergo two levels of parsing:
// Level 1: Option value escaping (':', '\', '\”)
// Level 2: Filtergraph description escaping ('\', '\”, ',', ';', '[', ']', and whitespace)
// Escaping with backslashes without enclosing quotes ensures paths containing single quotes,
// colons, commas, brackets, or spaces are parsed correctly without shell-style quote mismatches.
func escapeFilterPath(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '\\':
			b.WriteString(`\\\\`)
		case '\'':
			b.WriteString(`\\\'`)
		case ':':
			b.WriteString(`\\:`)
		case ',', ';', '[', ']', ' ':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeTempFile creates a temporary file in dir matching pattern, writes data, and closes it.
func writeTempFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()       // best effort file descriptor cleanup on write error
		_ = os.Remove(name) // best effort temp file cleanup on write error
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name) // best effort temp file cleanup on close error
		return "", err
	}
	return name, nil
}

// escapeConcatPath escapes single quotes for ffmpeg concat demuxer directives.
func escapeConcatPath(p string) string {
	s := strings.ReplaceAll(p, `'`, `'\''`)
	return "'" + s + "'"
}

// formatByline formats a child's name into a byline string (e.g. "by Mira").
func formatByline(b string) string {
	trimmed := strings.TrimSpace(b)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "by ") {
		return trimmed
	}
	return "by " + trimmed
}

// copyOrMoveFile moves src to dst, falling back to a file copy if cross-device link fails.
func copyOrMoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy file data: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync destination file: %w", err)
	}
	return nil
}
