package race_test

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRaceHTML_SelfContained ensures static/race/race.html is completely self-contained
// with zero external dependencies (no remote scripts, stylesheets, or images).
func TestRaceHTML_SelfContained(t *testing.T) {
	for _, filename := range []string{"race.html", "index.html"} {
		t.Run(filename, func(t *testing.T) {
			path := filepath.Join(".", filename)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to read %s: %v", path, err)
			}
			content := string(data)

			if len(content) == 0 {
				t.Fatalf("%s is empty", filename)
			}

			// Must not contain external http/https resource links
			if strings.Contains(content, "href=\"http") || strings.Contains(content, "src=\"http") {
				t.Errorf("%s contains external http/https resource links", filename)
			}
			if strings.Contains(content, "<link") {
				t.Errorf("%s must have inline CSS only; found <link> tag", filename)
			}
			if strings.Contains(content, "<script src") {
				t.Errorf("%s must not load external scripts", filename)
			}

			// Ensure inline style and SVGs exist
			if !strings.Contains(content, "<style>") {
				t.Errorf("%s missing <style> tag", filename)
			}
			if !strings.Contains(content, "<svg") {
				t.Errorf("%s missing inline <svg> elements", filename)
			}
		})
	}
}

// TestRaceHTML_IdenticalCopies verifies that race.html and index.html stay in sync.
func TestRaceHTML_IdenticalCopies(t *testing.T) {
	raceData, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	indexData, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}
	if string(raceData) != string(indexData) {
		t.Errorf("race.html and index.html differ; both must be kept identical")
	}
}

// TestRaceHTML_InterfaceIsOneNumber checks that the interface is purely --done on .race.
func TestRaceHTML_InterfaceIsOneNumber(t *testing.T) {
	data, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	content := string(data)

	// Must have .race container with --done
	if !strings.Contains(content, `class="race"`) {
		t.Errorf("missing .race container")
	}
	if !strings.Contains(content, `--done:`) {
		t.Errorf("missing --done property on .race container")
	}

	// Must have 8 page markers
	markersMatch := regexp.MustCompile(`<div class="markers">(.*?)</div>`).FindStringSubmatch(content)
	if len(markersMatch) < 2 {
		t.Fatalf("missing .markers element")
	}
	markerCount := strings.Count(markersMatch[1], "<i></i>")
	if markerCount != 8 {
		t.Errorf("expected 8 marker cells (one per page), got %d", markerCount)
	}

	// Must derive marker progress from --done and --pages via composite-only scaleX
	if !strings.Contains(content, `scaleX(calc(var(--done) / var(--pages)))`) {
		t.Errorf("markers fill progress must derive from scaleX(calc(var(--done) / var(--pages)))")
	}

	// Must derive runner position from --done and cqw
	if !strings.Contains(content, `var(--done) / var(--pages)`) {
		t.Errorf("runner transform must derive from var(--done) / var(--pages)")
	}
	if !strings.Contains(content, `100cqw`) {
		t.Errorf("runner transform must resolve against container query width 100cqw")
	}
}

// TestRaceHTML_SVGOverflowVisible verifies that .runner svg has overflow: visible
// so that upward translation in the bob animation (translateY(-4px)) does not truncate ear tips (H1).
func TestRaceHTML_SVGOverflowVisible(t *testing.T) {
	data, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	content := string(data)

	// Must contain .runner svg rule specifying overflow: visible
	overflowRegex := regexp.MustCompile(`\.runner\s+svg\s*\{[^}]*overflow\s*:\s*visible`)
	if !overflowRegex.MatchString(content) {
		t.Errorf(".runner svg must specify overflow: visible to prevent ear clipping during bob animation")
	}
}

// TestRaceHTML_FiveDistinctAnimals verifies that 5 clearly distinguishable animal sprites
// are present with distinct silhouettes and features.
func TestRaceHTML_FiveDistinctAnimals(t *testing.T) {
	data, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	content := string(data)

	// Count lanes and runners
	laneCount := strings.Count(content, `class="lane"`)
	if laneCount != 5 {
		t.Errorf("expected 5 lanes, found %d", laneCount)
	}

	runnerCount := strings.Count(content, `class="runner"`)
	if runnerCount != 5 {
		t.Errorf("expected 5 runners, found %d", runnerCount)
	}

	// Extract all 5 SVGs
	svgRegex := regexp.MustCompile(`(?s)<svg[^>]*>.*?</svg>`)
	svgs := svgRegex.FindAllString(content, -1)
	if len(svgs) != 5 {
		t.Fatalf("expected 5 inline SVGs, found %d", len(svgs))
	}

	// Verify each SVG parses as valid XML
	for i, svgStr := range svgs {
		decoder := xml.NewDecoder(strings.NewReader(svgStr))
		for {
			_, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("SVG %d is not valid XML: %v", i+1, err)
			}
		}
	}

	// Verify all 5 SVGs are unique (no duplicate silhouettes)
	seen := make(map[string]int)
	for i, svgStr := range svgs {
		if prev, ok := seen[svgStr]; ok {
			t.Errorf("SVG %d is identical to SVG %d; all 5 animals must have unique shapes", i+1, prev+1)
		}
		seen[svgStr] = i
	}

	// 1. Hare / Rabbit: tall upright ears, fluffy cotton tail, hind haunch
	hareSVG := svgs[0]
	if !strings.Contains(hareSVG, `aria-label="Hare"`) {
		t.Errorf("expected lane 1 to be Hare, got %s", hareSVG)
	}
	if !strings.Contains(hareSVG, `var(--hare-tail,#fff9f0)`) { // cotton puff tail
		t.Errorf("Hare missing fluffy white tail token var(--hare-tail,#fff9f0)")
	}
	if !strings.Contains(hareSVG, `var(--hare,#c9885a)`) {
		t.Errorf("Hare missing warm fawn palette token")
	}

	// 2. Tortoise: domed carapace, scutes, shell rim, neck, stubby legs
	tortoiseSVG := svgs[1]
	if !strings.Contains(tortoiseSVG, `aria-label="Tortoise"`) {
		t.Errorf("expected lane 2 to be Tortoise, got %s", tortoiseSVG)
	}
	if !strings.Contains(tortoiseSVG, `var(--tortoise,#5f7a4d)`) {
		t.Errorf("Tortoise missing olive carapace color")
	}
	if !strings.Contains(tortoiseSVG, `var(--tortoise-scute,#789a63)`) {
		t.Errorf("Tortoise missing scute plates pattern")
	}
	if !strings.Contains(tortoiseSVG, `polygon`) {
		t.Errorf("Tortoise missing multi-panel polygon scutes")
	}

	// 3. Fox: sharp pointed ears with dark tips, sleek snout, large bushy tail with white brush tip
	foxSVG := svgs[2]
	if !strings.Contains(foxSVG, `aria-label="Fox"`) {
		t.Errorf("expected lane 3 to be Fox, got %s", foxSVG)
	}
	if !strings.Contains(foxSVG, `var(--fox,#d25a2a)`) {
		t.Errorf("Fox missing rich russet coat color")
	}
	if !strings.Contains(foxSVG, `var(--fox-tail-tip,#fff9f2)`) { // white brush tip on tail
		t.Errorf("Fox missing white brush tip on tail token var(--fox-tail-tip,#fff9f2)")
	}
	if !strings.Contains(foxSVG, `var(--fox-dark,#2b1d16)`) { // black stocking/ear tips
		t.Errorf("Fox missing dark ear tips and stockings")
	}

	// 4. Duck / Bird: duck bill/beak, rounded bird head, fluttering wing, webbed feet
	duckSVG := svgs[3]
	if !strings.Contains(duckSVG, `aria-label="Duck"`) {
		t.Errorf("expected lane 4 to be Duck, got %s", duckSVG)
	}
	if !strings.Contains(duckSVG, `var(--duck-bill,#d85f1e)`) {
		t.Errorf("Duck missing prominent orange duck bill")
	}
	if !strings.Contains(duckSVG, `class="wing"`) {
		t.Errorf("Duck missing fluttering wing")
	}
	if !strings.Contains(duckSVG, `var(--duck,#f4be42)`) {
		t.Errorf("Duck missing cheerful golden feather color")
	}

	// 5. Little Mouse: giant circular saucer ears, pointy snout with whiskers, long thin whip tail
	mouseSVG := svgs[4]
	if !strings.Contains(mouseSVG, `aria-label="Little Mouse"`) {
		t.Errorf("expected lane 5 to be Little Mouse, got %s", mouseSVG)
	}
	if !strings.Contains(mouseSVG, `var(--mouse-ear,#e8a598)`) {
		t.Errorf("Mouse missing pink saucer ear centers")
	}
	if !strings.Contains(mouseSVG, `var(--mouse-whisker,#4a3b32)`) { // whiskers
		t.Errorf("Mouse missing whiskers token var(--mouse-whisker,#4a3b32)")
	}
	if !strings.Contains(mouseSVG, `var(--mouse-tail,#d4887b)`) { // sinuous whip tail
		t.Errorf("Mouse missing long sinuous whip tail token var(--mouse-tail,#d4887b)")
	}
}

// TestRaceHTML_AllSVGColorsUseVar verifies that every SVG fill and stroke color is wrapped in a var() token (L1).
func TestRaceHTML_AllSVGColorsUseVar(t *testing.T) {
	data, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	content := string(data)

	// Extract all SVG content
	svgRegex := regexp.MustCompile(`(?s)<svg[^>]*>.*?</svg>`)
	svgs := svgRegex.FindAllString(content, -1)
	if len(svgs) == 0 {
		t.Fatalf("no SVGs found in race.html")
	}

	// Regex to find raw hex color attributes: (fill|stroke)="#..."
	rawHexRegex := regexp.MustCompile(`(?:fill|stroke)\s*=\s*"(#[0-9a-fA-F]{3,8})"`)
	for i, svgStr := range svgs {
		matches := rawHexRegex.FindAllStringSubmatch(svgStr, -1)
		for _, m := range matches {
			t.Errorf("SVG %d contains raw hex color attribute %q without var() wrapper (violation of PLAN.md §T9a)", i+1, m[0])
		}
	}

	// Ensure all fill and stroke attributes (except fill="none") use var(--...)
	attrRegex := regexp.MustCompile(`(fill|stroke)\s*=\s*"([^"]+)"`)
	for i, svgStr := range svgs {
		matches := attrRegex.FindAllStringSubmatch(svgStr, -1)
		for _, m := range matches {
			attrName := m[1]
			attrVal := m[2]
			if attrVal == "none" {
				continue
			}
			if !strings.HasPrefix(attrVal, "var(--") {
				t.Errorf("SVG %d %s attribute %q does not use var(--token, fallback)", i+1, attrName, attrVal)
			}
		}
	}
}

// TestRaceHTML_RunnerTransitionNoClipping asserts that runner transition timing function
// does not overshoot past container boundaries (M2).
func TestRaceHTML_RunnerTransitionNoClipping(t *testing.T) {
	data, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	content := string(data)

	// Extract .runner transition
	runnerRegex := regexp.MustCompile(`\.runner\s*\{([^}]+)\}`)
	rMatch := runnerRegex.FindStringSubmatch(content)
	if len(rMatch) < 2 {
		t.Fatalf("missing .runner rule")
	}
	rBody := rMatch[1]

	// Extract cubic-bezier parameters
	cbRegex := regexp.MustCompile(`cubic-bezier\s*\(\s*([0-9.]+)\s*,\s*([0-9.]+)\s*,\s*([0-9.]+)\s*,\s*([0-9.]+)\s*\)`)
	cbMatch := cbRegex.FindStringSubmatch(rBody)
	if len(cbMatch) < 5 {
		t.Fatalf("missing cubic-bezier timing function in .runner transition: %s", rBody)
	}

	var y1, y2 float64
	if _, err := fmt.Sscanf(cbMatch[2], "%f", &y1); err != nil {
		t.Fatalf("failed to parse y1: %v", err)
	}
	if _, err := fmt.Sscanf(cbMatch[4], "%f", &y2); err != nil {
		t.Fatalf("failed to parse y2: %v", err)
	}

	// In a cubic bezier transition curve, y1 and y2 control points determine the peak value.
	// If y1 > 1.0 or y2 > 1.0, the spring curve overshoots 1.0 (target translation).
	// With runners positioned near the right border at --done: 8, any overshoot > 1.0
	// causes runners to clip against .race { overflow: hidden }.
	if y1 > 1.0 || y2 > 1.0 {
		t.Errorf("runner cubic-bezier(%s, %s, %s, %s) has overshoot (y1=%.2f, y2=%.2f > 1.0); must be <= 1.0 to prevent clipping at --done: 8",
			cbMatch[1], cbMatch[2], cbMatch[3], cbMatch[4], y1, y2)
	}

	// Verify static right edge bounds at --done: 8 across all lanes
	// runner-w is 64px, track-pad is 8px.
	// travel = (100cqw - 64px - 16px) = 100cqw - 80px.
	// At --done: 8 (ratio 1.0): translateX = 8px + 1.0 * (100cqw - 80px) + jit = 100cqw - 72px + jit.
	// runner.right = translateX + 64px = 100cqw - 8px + jit.
	// Container width is 100cqw. So runner.right <= 100cqw requires -8px + jit <= 0, or jit <= 8px.
	jitRegex := regexp.MustCompile(`--jit\s*:\s*(-?[0-9]+)px`)
	jitMatches := jitRegex.FindAllStringSubmatch(content, -1)
	if len(jitMatches) == 0 {
		t.Fatalf("no --jit declarations found")
	}
	for _, jm := range jitMatches {
		var jit int
		if _, err := fmt.Sscanf(jm[1], "%d", &jit); err != nil {
			t.Fatalf("failed to parse jit %q: %v", jm[1], err)
		}
		// Calculate clearance at --done: 8: 100cqw - (100cqw - 8px + jit) = 8px - jit
		clearance := 8 - jit
		if clearance < 0 {
			t.Errorf("lane with --jit: %dpx exceeds container bounds at --done: 8 (overflows by %dpx)", jit, -clearance)
		}
	}
}

// TestRaceHTML_MotionAndReducedMotion ensures animations use transform only and that
// prefers-reduced-motion is strictly honoured (M1, L2).
func TestRaceHTML_MotionAndReducedMotion(t *testing.T) {
	data, err := os.ReadFile("race.html")
	if err != nil {
		t.Fatalf("failed to read race.html: %v", err)
	}
	content := string(data)

	// Animate transform only: check keyframes
	keyframesRegex := regexp.MustCompile(`@keyframes\s+([a-zA-Z0-9_-]+)\s*\{([^}]+)\}`)
	matches := keyframesRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatalf("no @keyframes found in race.html")
	}

	for _, m := range matches {
		name := m[1]
		body := m[2]
		// Body must only animate transform
		lines := strings.Split(body, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			// Look for forbidden layout properties: width, height, top, left, margin
			for _, forbidden := range []string{"width:", "height:", "top:", "left:", "bottom:", "right:", "margin:"} {
				if strings.Contains(trimmed, forbidden) {
					t.Errorf("keyframe %q animates forbidden layout property %q: %s", name, forbidden, trimmed)
				}
			}
			// Must include transform
			if !strings.Contains(trimmed, "transform:") {
				t.Errorf("keyframe %q line does not animate transform: %s", name, trimmed)
			}
		}
	}

	// In addition to @keyframes, scan all transition: properties for layout properties (M1, L2)
	transitionRegex := regexp.MustCompile(`transition\s*:\s*([^;}\n]+)`)
	transMatches := transitionRegex.FindAllStringSubmatch(content, -1)
	if len(transMatches) == 0 {
		t.Fatalf("no transition declarations found in race.html")
	}

	layoutProps := []string{"width", "height", "top", "left", "bottom", "right", "margin", "padding"}
	for _, tm := range transMatches {
		val := tm[1]
		// Skip transition: none in prefers-reduced-motion
		if strings.TrimSpace(val) == "none" || strings.TrimSpace(val) == "none !important" {
			continue
		}
		for _, lp := range layoutProps {
			if regexp.MustCompile(`\b` + lp + `\b`).MatchString(val) {
				t.Errorf("transition animates forbidden layout property %q: %s", lp, val)
			}
		}
	}

	// Verify .markers::before uses composite-only scaleX transform with transform-origin: left
	markersBeforeRegex := regexp.MustCompile(`\.markers::before\s*\{([^}]+)\}`)
	mbMatch := markersBeforeRegex.FindStringSubmatch(content)
	if len(mbMatch) < 2 {
		t.Fatalf("missing .markers::before rule")
	}
	mbBody := mbMatch[1]
	if !strings.Contains(mbBody, "transform-origin:left") && !strings.Contains(mbBody, "transform-origin: left") {
		t.Errorf(".markers::before missing transform-origin: left")
	}
	if !strings.Contains(mbBody, "scaleX(calc(var(--done) / var(--pages)))") {
		t.Errorf(".markers::before must use scaleX(calc(var(--done) / var(--pages))) for composite-only progress fill")
	}
	if !strings.Contains(mbBody, "transition:transform") && !strings.Contains(mbBody, "transition: transform") {
		t.Errorf(".markers::before must transition transform (composite-only)")
	}

	// prefers-reduced-motion must be present
	if !strings.Contains(content, "prefers-reduced-motion:reduce") && !strings.Contains(content, "prefers-reduced-motion: reduce") {
		t.Errorf("missing prefers-reduced-motion media query")
	}

	// Under reduced motion: animations must be none and transitions must be none
	mediaIdx := strings.Index(content, "@media (prefers-reduced-motion")
	if mediaIdx == -1 {
		mediaIdx = strings.Index(content, "@media(prefers-reduced-motion")
	}
	if mediaIdx == -1 {
		t.Fatalf("could not find @media (prefers-reduced-motion query")
	}
	openBrace := strings.Index(content[mediaIdx:], "{")
	if openBrace == -1 {
		t.Fatalf("could not find opening brace for prefers-reduced-motion")
	}
	// Find matching outer closing brace
	depth := 0
	start := mediaIdx + openBrace
	end := -1
	for i := start; i < len(content); i++ {
		if content[i] == '{' {
			depth++
		} else if content[i] == '}' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if end == -1 {
		t.Fatalf("could not find closing brace for prefers-reduced-motion")
	}
	rb := content[start:end]
	if !strings.Contains(rb, "animation:none") && !strings.Contains(rb, "animation: none") {
		t.Errorf("reduced-motion block must disable animations (animation: none); got rb: %q", rb)
	}
	if !strings.Contains(rb, "transition:none") && !strings.Contains(rb, "transition: none") {
		t.Errorf("reduced-motion block must disable transitions (transition: none); got rb: %q", rb)
	}
}
