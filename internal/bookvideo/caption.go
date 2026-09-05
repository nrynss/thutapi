package bookvideo

import (
	"strings"
	"time"
	"unicode/utf8"
)

// T10g geometry and typography for the film's words. Every segment — title
// card, page and end card — renders at the same frame and pixel format so
// concat -c copy stays free (§T10g, PLAN.md "One constant, three call
// sites"). The page is split into the 4:5 illustration on top (full bleed)
// and the caption band beneath it on --surface; the cards use --film as
// their ground.
const (
	frameWidth  = 1080
	frameHeight = 1620
	artWidth    = 1080
	artHeight   = 1350
	bandHeight  = frameHeight - artHeight // 270

	// Colours from §The look, as ffmpeg filter values.
	surfaceColor = "0xd8efe3" // --surface: caption band ground
	inkColor     = "0x17332b" // --ink: caption words (11.25:1 on --surface)
	filmColor    = "0x12241e" // --film: title/end card ground (was 0x1b1614)
)

// Caption layout limits. drawtext neither wraps nor per-line centres, so the
// words are wrapped in Go against the band width and drawn as one textfile
// block with text_align=C (§T10g). The band fits three lines at the base
// size with comfortable margins (verified in-container: 3 lines at 60 px
// span ~200 px in the 270 px band).
const (
	captionMaxLines    = 3
	captionFontBase    = 60 // drawtext fontsize for ≤3 lines at the base size
	captionFontShrink  = 52 // one shrink step before truncating with an ellipsis
	captionSideMargins = 40 // 40 px of band width each side stays clear

	captionAdvanceEm  = 0.55 // planning average advance per rune (measured: lower+space 0.50, caps 0.66)
	captionEllipsis   = "…"
	silentWordsPerSec = 2.0 // read-aloud-to-a-child pace, §T10g
	silentHoldFloor   = 4 * time.Second
	silentHoldCeiling = 14 * time.Second
)

// captionLayout is the wrapped caption for one page: the drawtext fontsize
// that fits it and the lines to write to the caption textfile.
type captionLayout struct {
	fontSize int
	lines    []string
}

// layoutCaption wraps text for the caption band. It fits as many words as
// possible in three lines at captionFontBase, shrinks one step to
// captionFontShrink when needed, and when even the shrunk size overflows it
// truncates at a word boundary with an ellipsis (§T10g: "a sentence that
// overflows three lines is shrunk one step, then truncated with an
// ellipsis"). Words are never broken mid-word; spaces are the only break
// points; rune count is measured in runes, never bytes.
func layoutCaption(text string) captionLayout {
	if text == "" {
		return captionLayout{}
	}
	base := wrapText(text, maxRunesAt(captionFontBase))
	if len(base) <= captionMaxLines {
		return captionLayout{fontSize: captionFontBase, lines: base}
	}
	shrink := wrapText(text, maxRunesAt(captionFontShrink))
	if len(shrink) <= captionMaxLines {
		return captionLayout{fontSize: captionFontShrink, lines: shrink}
	}
	return captionLayout{fontSize: captionFontShrink, lines: truncateWithEllipsis(shrink)}
}

// maxRunesAt returns how many runes fit one caption line at the given
// drawtext fontsize, measured against the band width with captionSideMargins
// of breathing room on each side. The 0.55 em planning advance sits between
// the measured lowercase+space average (0.50 em) and all-uppercase (0.66 em)
// for this face, so ordinary sentence-case page text never overflows the
// band.
func maxRunesAt(fontSize int) int {
	usable := frameWidth - 2*captionSideMargins
	return int(float64(usable) / (float64(fontSize) * captionAdvanceEm))
}

// wrapText greedily wraps text into lines of at most maxRunes runes, breaking
// only at spaces. A word longer than maxRunes keeps its own line whole —
// words are never split mid-word.
func wrapText(text string, maxRunes int) []string {
	if maxRunes < 1 {
		maxRunes = 1
	}
	var lines []string
	current := ""
	for _, word := range strings.Fields(text) {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if utf8.RuneCountInString(candidate) <= maxRunes {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
		}
		current = word
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// truncateWithEllipsis takes the result of wrapping text at the shrink size
// and, when it still exceeds three lines, keeps the first three whole lines
// and ends the last one with an ellipsis, trimming trailing words so the
// marker fits the line budget. The input is already wrapped; only the
// three-line overflow case reaches here.
func truncateWithEllipsis(wrapped []string) []string {
	if len(wrapped) <= captionMaxLines {
		return wrapped
	}
	kept := append([]string(nil), wrapped[:captionMaxLines]...)
	maxRunes := maxRunesAt(captionFontShrink)
	last := len(kept) - 1
	for utf8.RuneCountInString(kept[last])+utf8.RuneCountInString(captionEllipsis) > maxRunes {
		line := kept[last]
		if i := strings.LastIndex(line, " "); i >= 0 {
			kept[last] = line[:i]
			continue
		}
		// A single word longer than the whole line: never split it — drop
		// the line rather than break the word (children's text never hits
		// this; the ellipsis then ends the previous line).
		kept = kept[:last]
		if len(kept) == 0 {
			return []string{captionEllipsis}
		}
		last = len(kept) - 1
	}
	kept[last] += captionEllipsis
	return kept
}

// captionHold returns how long a page with no narration holds: one second
// per silentWordsPerSec words of the caption actually shown, with a floor of
// silentHoldFloor and a ceiling of silentHoldCeiling (§T10g). Empty text
// (no words) holds the floor.
func captionHold(text string) time.Duration {
	words := len(strings.Fields(text))
	seconds := float64(words) / silentWordsPerSec
	d := time.Duration(seconds * float64(time.Second))
	if d < silentHoldFloor {
		d = silentHoldFloor
	}
	if d > silentHoldCeiling {
		d = silentHoldCeiling
	}
	return d
}
