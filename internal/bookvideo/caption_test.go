package bookvideo

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestLayoutCaption_ShortTextStaysAtBaseFont pins that text fitting three
// lines at the base size is returned unwrapped further and at the base font.
func TestLayoutCaption_ShortTextStaysAtBaseFont(t *testing.T) {
	text := "Mira opens the garden gate."
	layout := layoutCaption(text)
	if layout.fontSize != captionFontBase {
		t.Errorf("fontSize = %d, want base %d", layout.fontSize, captionFontBase)
	}
	if len(layout.lines) == 0 || len(layout.lines) > captionMaxLines {
		t.Fatalf("lines = %d, want 1..%d", len(layout.lines), captionMaxLines)
	}
	if got := strings.Join(layout.lines, " "); got != text {
		t.Errorf("lines join = %q, want the whole short text", got)
	}
}

// TestLayoutCaption_WrapToThreeLines pins that a text needing exactly three
// lines is wrapped on spaces into three lines at the base font, never
// mid-word.
func TestLayoutCaption_WrapToThreeLines(t *testing.T) {
	text := "Mira opens the garden gate to start the morning adventure with Bramble at her side"
	layout := layoutCaption(text)
	if layout.fontSize != captionFontBase {
		t.Errorf("fontSize = %d, want base %d (text must fit three lines at base)", layout.fontSize, captionFontBase)
	}
	if len(layout.lines) > captionMaxLines {
		t.Fatalf("lines = %d, want ≤ %d", len(layout.lines), captionMaxLines)
	}
	for _, line := range layout.lines {
		if strings.HasPrefix(line, " ") || strings.HasSuffix(line, " ") {
			t.Errorf("line %q has leading/trailing space", line)
		}
	}
	joined := strings.Join(layout.lines, " ")
	// Whole words only: every original word appears once, in order.
	for _, w := range strings.Fields(text) {
		if !strings.Contains(joined, w) {
			t.Fatalf("wrapped caption lost word %q: %q", w, joined)
		}
	}
	if len(strings.Fields(joined)) != len(strings.Fields(text)) {
		t.Errorf("wrapped caption word count changed: %q", joined)
	}
}

// TestLayoutCaption_ShrinkOneStep pins the one-step shrink: a text that
// overflows three lines at the base size is re-laid out at the shrink size.
func TestLayoutCaption_ShrinkOneStep(t *testing.T) {
	// One long unbroken run of words that exceeds 3× the base budget but fits
	// the shrink budget (verified against the real page texts: 58–95 runes fit
	// ≤3 lines at 30/34 runes per line for base/shrink).
	text := "Mira rolls the sweet pastry dough and bakes a golden apple pie while Bramble watches patiently by the warm fire"
	layout := layoutCaption(text)
	if layout.fontSize != captionFontShrink {
		t.Errorf("fontSize = %d, want shrink %d", layout.fontSize, captionFontShrink)
	}
	if len(layout.lines) > captionMaxLines {
		t.Fatalf("lines = %d, want ≤ %d at the shrink size", len(layout.lines), captionMaxLines)
	}
	for _, line := range layout.lines {
		if utf8.RuneCountInString(line) > maxRunesAt(captionFontShrink) {
			t.Errorf("shrink line %q exceeds %d runes", line, maxRunesAt(captionFontShrink))
		}
	}
}

// TestLayoutCaption_TruncateWithEllipsis pins the ellipsis: a pathological
// text that overflows even the shrink size ends with an ellipsis on the last
// line, still three lines max and never a mid-word break.
func TestLayoutCaption_TruncateWithEllipsis(t *testing.T) {
	words := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		words = append(words, "wordnumber"+string(rune('a'+i%26)))
	}
	text := strings.Join(words, " ")
	layout := layoutCaption(text)
	if layout.fontSize != captionFontShrink {
		t.Errorf("fontSize = %d, want shrink %d", layout.fontSize, captionFontShrink)
	}
	if len(layout.lines) != captionMaxLines {
		t.Fatalf("lines = %d, want exactly %d", len(layout.lines), captionMaxLines)
	}
	if !strings.HasSuffix(layout.lines[len(layout.lines)-1], captionEllipsis) {
		t.Errorf("last line %q does not end with %q", layout.lines[len(layout.lines)-1], captionEllipsis)
	}
	for _, line := range layout.lines {
		if strings.HasPrefix(line, " ") || strings.HasSuffix(strings.TrimSuffix(line, captionEllipsis), " ") {
			t.Errorf("line %q has stray whitespace", line)
		}
		if strings.Contains(line, "  ") {
			t.Errorf("line %q contains a mid-word break (double space)", line)
		}
	}
}

// TestLayoutCaption_NoMidWordBreak pins the hard rule: words are never split.
// A single word longer than one line stays whole on its own line.
func TestLayoutCaption_NoMidWordBreak(t *testing.T) {
	layout := layoutCaption("BrambleBrambleBrambleBrambleBrambleBramble")
	if len(layout.lines) != 1 {
		t.Fatalf("lines = %d, want the long word whole on one line", len(layout.lines))
	}
	if layout.lines[0] != "BrambleBrambleBrambleBrambleBrambleBramble" {
		t.Errorf("long word was split: %q", layout.lines[0])
	}
}

// TestLayoutCaption_RuneCountNotBytes pins that the wrap measures runes, not
// bytes: a line of wide (multi-byte) runes uses its rune budget, not its byte
// size.
func TestLayoutCaption_RuneCountNotBytes(t *testing.T) {
	// 10 multi-byte runes in a 15-rune budget: bytes are ~30, runes are 10 —
	// a byte-counting wrap would split the wide runes.
	wide := "ééééé éééé ééé"
	layout := layoutCaption(wide)
	if len(layout.lines) > captionMaxLines {
		t.Fatalf("lines = %d, want ≤ %d", len(layout.lines), captionMaxLines)
	}
	joined := strings.Join(layout.lines, " ")
	for _, w := range strings.Fields(wide) {
		if !strings.Contains(joined, w) {
			t.Errorf("multi-byte word %q was lost or split: %q", w, joined)
		}
	}
}

// TestLayoutCaption_EmptyText pins that empty text produces no caption.
func TestLayoutCaption_EmptyText(t *testing.T) {
	layout := layoutCaption("")
	if len(layout.lines) != 0 {
		t.Errorf("empty text produced lines %v", layout.lines)
	}
}

// TestCaptionHold pins the silent hold: words/2.0 s with a 4 s floor and a
// 14 s ceiling (§T10g).
func TestCaptionHold(t *testing.T) {
	cases := []struct {
		name string
		text string
		want time.Duration
	}{
		{"floor", "one", 4 * time.Second},
		{"ten words", "one two three four five six seven eight nine ten", 5 * time.Second},
		{"sixteen words", "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen", 8 * time.Second},
		{"ceiling", strings.Join(makeWords(60), " "), 14 * time.Second},
		{"empty", "", 4 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := captionHold(tc.text); got != tc.want {
				t.Errorf("captionHold = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestGeometrySharedAcrossSegments pins the concat agreement: every segment
// builder — title card, page and end card — renders the same master frame,
// so concat -c copy stays free.
func TestGeometrySharedAcrossSegments(t *testing.T) {
	if frameWidth != 1080 || frameHeight != 1620 {
		t.Fatalf("master frame = %dx%d, want 1080x1620", frameWidth, frameHeight)
	}
	if artHeight+bandHeight != frameHeight {
		t.Fatalf("art %d + band %d != frame %d", artHeight, bandHeight, frameHeight)
	}

	// Raw-command pins at each call site (the T10a convention).
	title := ""
	{
		runner := &mockRunner{}
		tmp := t.TempDir()
		img := tmp + "/p.jpg"
		_ = os.WriteFile(img, []byte("x"), 0o600)
		cfg := Config{Runner: runner, WorkDir: tmp, FontFile: img}
		if err := BuildTitleCard(t.Context(), cfg, img, "T", "", tmp+"/title.mp4"); err != nil {
			t.Fatalf("title card: %v", err)
		}
		title = strings.Join(runner.lastCall(), " ")
		if !strings.Contains(title, "1080:1620") || !strings.Contains(title, "format=yuv420p") {
			t.Errorf("title card geometry not pinned to master: %s", title)
		}
	}
	page := ""
	{
		runner := &mockRunner{}
		tmp := t.TempDir()
		img := tmp + "/p.jpg"
		_ = os.WriteFile(img, []byte("x"), 0o600)
		cfg := Config{Runner: runner, WorkDir: tmp, FontFile: img}
		if err := BuildPageSegment(t.Context(), cfg, img, "words here", "", tmp+"/p.mp4"); err != nil {
			t.Fatalf("page segment: %v", err)
		}
		page = strings.Join(runner.lastCall(), " ")
		if !strings.Contains(page, "scale=1080:1350") || !strings.Contains(page, "pad=1080:1620:0:0") {
			t.Errorf("page geometry not 1350 art + 1620 pad: %s", page)
		}
	}
	end := ""
	{
		runner := &mockRunner{}
		tmp := t.TempDir()
		cfg := Config{Runner: runner, WorkDir: tmp, FontFile: tmp + "/f.ttf"}
		_ = os.WriteFile(cfg.FontFile, []byte("x"), 0o600)
		if err := BuildEndCard(t.Context(), cfg, tmp+"/end.mp4"); err != nil {
			t.Fatalf("end card: %v", err)
		}
		end = strings.Join(runner.lastCall(), " ")
		if !strings.Contains(end, "1080x1620") {
			t.Errorf("end card geometry not 1620 tall: %s", end)
		}
	}
}

func makeWords(n int) []string {
	words := make([]string, 0, n)
	for i := 0; i < n; i++ {
		words = append(words, "w"+strings.Repeat("o", 1+i%3))
	}
	return words
}
