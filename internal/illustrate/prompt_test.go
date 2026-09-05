package illustrate

import (
	"errors"
	"strings"
	"testing"

	"thutapi/internal/story"
)

// awkwardVisual is a visual that survives nothing but concatenation:
// a straight quote, a curly quote, an em dash, a newline, trailing
// whitespace and non-ASCII. Any normaliser, trimmer, JSON round-trip
// through a lossy encoder or "tidy up the prompt" helper changes it,
// and lock 1 is then quietly broken for every page.
const awkwardVisual = "a \"tiny\" dragon — 30cm tall, scales the colour of café-au-lait,\nwith a torn left wing  "

func TestReferencePrompt(t *testing.T) {
	m := story.CastMember{Name: "Mira", Visual: miraVisual}
	got := ReferencePrompt(m)
	if !strings.Contains(got, miraVisual) {
		t.Errorf("reference prompt lost the visual (lock 1):\n%s", got)
	}
	if !strings.Contains(got, "Mira") {
		t.Errorf("reference prompt does not name the character:\n%s", got)
	}
	if !strings.HasSuffix(got, StyleSuffix) {
		t.Errorf("reference prompt does not end in StyleSuffix (lock 3):\n%s", got)
	}
	if !strings.Contains(got, referenceDirective) {
		t.Errorf("reference prompt lost the single-character framing:\n%s", got)
	}
}

// TestReferencePrompt_VisualIsVerbatim is lock 1 at the string level:
// the visual survives byte for byte, whatever is in it.
func TestReferencePrompt_VisualIsVerbatim(t *testing.T) {
	visuals := []string{
		miraVisual,
		awkwardVisual,
		liveNarratorVisual2,
		liveGrumpyRiverVisual,
		"UPPER case AND lower Case",
	}
	for _, v := range visuals {
		t.Run(v, func(t *testing.T) {
			got := ReferencePrompt(story.CastMember{Name: "X", Visual: v})
			if !strings.Contains(got, v) {
				t.Errorf("visual was altered on its way into the prompt.\nwant substring: %q\nprompt:\n%s", v, got)
			}
		})
	}
}

// planFor builds the plan the way Illustrate does, so a prompt test
// and a render test cannot disagree about which sheet leads.
func planFor(s story.Story) Plan { return PlanReferences(s) }

func TestPagePrompt(t *testing.T) {
	s := twoCastStory()
	plan := planFor(s)
	got, err := PagePrompt(s.Pages[1], s.Cast, plan)
	if err != nil {
		t.Fatalf("PagePrompt: %v", err)
	}
	for _, want := range []string{
		s.Pages[1].Prompt, // the model's own scene description leads
		miraVisual,        // lock 1, for every character on the page
		brambleVisual,
		StyleSuffix, // lock 3
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page prompt is missing %q:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, StyleSuffix) {
		t.Errorf("page prompt does not end in StyleSuffix (lock 3):\n%s", got)
	}
	// The page lists Bramble first, then Mira: payload.image carries
	// BOTH sheets — multi-reference (t6b-live-record.md item 1b) — and
	// the prompt must name every attached sheet so the model holds
	// each character identical to their own reference.
	if !strings.Contains(got, "The attached reference images are Bramble and Mira.") {
		t.Errorf("page prompt does not name both reference sheets (lock 2):\n%s", got)
	}
}

// TestPagePrompt_StyleLockOverridesTheModelsOwnStyleWords covers the
// live observation that M3's page prompts already carry style
// language of their own ("Soft storybook illustration style."). Two
// style instructions in one prompt is a real conflict; the lock has
// to be the one that wins, in words.
func TestPagePrompt_StyleLockOverridesTheModelsOwnStyleWords(t *testing.T) {
	s := twoCastStory()
	s.Pages[0].Prompt = "Mira opens the garden gate. Soft storybook illustration style."
	plan := planFor(s)
	got, err := PagePrompt(s.Pages[0], s.Cast, plan)
	if err != nil {
		t.Fatalf("PagePrompt: %v", err)
	}
	styleAt := strings.Index(got, styleDirective)
	modelStyleAt := strings.Index(got, "Soft storybook illustration style.")
	if styleAt < 0 || modelStyleAt < 0 {
		t.Fatalf("prompt is missing one of the two style clauses:\n%s", got)
	}
	if styleAt < modelStyleAt {
		t.Errorf("the style lock must come after the model's own style words so it reads as the override:\n%s", got)
	}
	if !strings.HasSuffix(got, StyleSuffix) {
		t.Errorf("prompt does not end in StyleSuffix:\n%s", got)
	}
}

// TestPagePrompt_VariantNamesBothCharacters covers the live river
// case at the prompt level: the attached sheet is the anchor's, the
// page's character is the variant, and the prompt has to say that
// they are the same character or the model draws a second river.
func TestPagePrompt_VariantNamesBothCharacters(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Grumpy River", Visual: liveGrumpyRiverVisual},
			{Name: "Happy River", Visual: liveHappyRiverVisual},
		},
		Pages: []story.Page{page(1, "The river cheers up.", "Happy River")},
	}
	got, err := PagePrompt(s.Pages[0], s.Cast, planFor(s))
	if err != nil {
		t.Fatalf("PagePrompt: %v", err)
	}
	if !strings.Contains(got, "The attached reference image is Grumpy River, who is the same character as Happy River.") {
		t.Errorf("variant prompt does not explain the shared sheet:\n%s", got)
	}
	if !strings.Contains(got, liveHappyRiverVisual) {
		t.Errorf("variant lost its own visual, so the mood change cannot render:\n%s", got)
	}
	if strings.Contains(got, "- Grumpy River:") {
		t.Errorf("Grumpy River is not on this page and must not be listed as present:\n%s", got)
	}
}

// TestPagePrompt_NonAppearanceMemberIsLeftOut pins the decision that
// a narrator's "no visual" is never pasted into an image prompt: the
// model would draw the words.
func TestPagePrompt_NonAppearanceMemberIsLeftOut(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Narrator", Visual: liveNarratorVisual1},
			{Name: "Mira", Visual: miraVisual},
		},
		Pages: []story.Page{page(1, "Mira waves.", "Narrator", "Mira")},
	}
	got, err := PagePrompt(s.Pages[0], s.Cast, planFor(s))
	if err != nil {
		t.Fatalf("PagePrompt: %v", err)
	}
	if strings.Contains(got, "Narrator") {
		t.Errorf("the narrator reached the image prompt:\n%s", got)
	}
	if !strings.Contains(got, "The attached reference image is Mira.") {
		t.Errorf("the page must fall through to the first character that HAS a sheet:\n%s", got)
	}
}

// TestPagePrompt_VisualsAreVerbatim is lock 1 on the page path, with
// the awkward visual that only concatenation survives.
func TestPagePrompt_VisualsAreVerbatim(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Dragon", Visual: awkwardVisual},
			{Name: "Mira", Visual: miraVisual},
		},
		Pages: []story.Page{page(1, "The dragon meets Mira.", "Dragon", "Mira")},
	}
	got, err := PagePrompt(s.Pages[0], s.Cast, planFor(s))
	if err != nil {
		t.Fatalf("PagePrompt: %v", err)
	}
	if !strings.Contains(got, awkwardVisual) {
		t.Errorf("visual was altered on its way into the page prompt.\nwant substring: %q\nprompt:\n%s", awkwardVisual, got)
	}
	if !strings.Contains(got, miraVisual) {
		t.Errorf("second character's visual was altered:\n%s", got)
	}
}

// TestPagePrompt_CharacterOrderFollowsThePage pins determinism: the
// prompt lists characters in the page's order, so the same story
// always builds the same prompt.
func TestPagePrompt_CharacterOrderFollowsThePage(t *testing.T) {
	s := twoCastStory()
	plan := planFor(s)
	got, err := PagePrompt(s.Pages[1], s.Cast, plan)
	if err != nil {
		t.Fatalf("PagePrompt: %v", err)
	}
	bramble := strings.Index(got, "- Bramble:")
	mira := strings.Index(got, "- Mira:")
	if bramble < 0 || mira < 0 {
		t.Fatalf("prompt is missing a character line:\n%s", got)
	}
	if bramble > mira {
		t.Errorf("characters are not in page order (page lists Bramble then Mira):\n%s", got)
	}
	for i := 0; i < 20; i++ {
		again, err := PagePrompt(s.Pages[1], s.Cast, plan)
		if err != nil || again != got {
			t.Fatalf("PagePrompt is not deterministic: %v / %v", err, again != got)
		}
	}
}

func TestPagePrompt_ErrNoReference(t *testing.T) {
	cast := []story.CastMember{
		{Name: "Mira", Visual: miraVisual},
		{Name: "Narrator", Visual: liveNarratorVisual1},
	}
	full := planFor(story.Story{
		Cast:  cast,
		Pages: []story.Page{page(1, "Mira waves.", "Mira", "Narrator")},
	})

	tests := []struct {
		name string
		p    story.Page
		plan Plan
	}{
		{
			name: "page names nobody",
			p:    page(1, "an empty landscape"),
			plan: full,
		},
		{
			name: "page names someone outside the cast",
			p:    page(1, "p", "Gerald"),
			plan: full,
		},
		{
			name: "page names only members with no sheet",
			p:    page(1, "p", "Narrator"),
			plan: full,
		},
		{
			// A hand-built plan whose SheetOf points at a sheet that
			// is not in Sheets. Illustrate never builds one, but
			// PagePrompt is exported and T7 will pass its own plan;
			// the answer must be the loud error, never a page
			// rendered against nothing.
			name: "plan maps a name to a sheet it does not carry",
			p:    page(1, "p", "Mira"),
			plan: Plan{SheetOf: map[string]string{"Mira": "Mira"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PagePrompt(tc.p, cast, tc.plan)
			if !errors.Is(err, ErrNoReference) {
				t.Fatalf("err = %v, want ErrNoReference", err)
			}
			if got != "" {
				t.Errorf("prompt = %q on error, want empty — a non-nil value beside a non-nil error is a lie", got)
			}
		})
	}
}

// TestStyleSuffixWording pins the constant against PLAN.md §T6 lock 3
// and project.md §2. It is quoted from the spec; a reworded style
// lock is a different book.
func TestStyleSuffixWording(t *testing.T) {
	const want = "flat 2D children's picture book illustration, thick outlines, gouache texture, soft palette"
	if StyleSuffix != want {
		t.Errorf("StyleSuffix = %q, want %q", StyleSuffix, want)
	}
}
