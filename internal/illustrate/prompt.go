package illustrate

import (
	"fmt"
	"strings"

	"thutapi/internal/story"
)

// StyleSuffix is the style lock (project.md §2, PLAN.md §T6 lock 3):
// one constant clause appended to every prompt this package builds,
// reference sheets and pages alike, so the whole book is drawn in one
// register. It is a single string on purpose — a per-call style
// parameter is a way for pages to drift apart, which is the failure
// this track exists to prevent.
//
// The wording is quoted from PLAN.md §T6 and project.md §2 and is
// pinned byte-for-byte on the marshalled request payload by
// TestStyleSuffixVerbatimOnRawWire.
const StyleSuffix = "flat 2D children's picture book illustration, thick outlines, gouache texture, soft palette"

// styleDirective introduces StyleSuffix and settles which style wins.
//
// M3's Phase-B output already carries style language of its own —
// observed live on 2026-09-05, a page prompt ended "Soft storybook
// illustration style." — so appending the style lock puts two style
// instructions in one prompt. That is a real conflict and it gets a
// decision rather than an accident: the lock is authoritative, and
// says so in words, because it is the only clause guaranteed
// identical on all ten images. The alternative — editing the model's
// page prompt to strip its style sentence — was rejected: rewriting
// generated text to make a lock hold is how the verbatim lock next
// door gets broken.
//
// StyleSuffix itself stays untouched by this, so the raw-wire pin
// still matches the constant byte for byte.
const styleDirective = "Draw it in this exact style, which overrides any other style wording above:"

// referenceDirective is the framing every reference sheet asks for.
// A reference sheet is only useful as an image-to-image base if it
// shows one character, plainly, with nothing else competing for the
// frame — a busy sheet drags scene furniture into every page render.
const referenceDirective = "Full body, front view, neutral pose, plain flat background, one character only, no text and no speech bubbles."

// ReferencePrompt builds the text-to-image prompt for one cast
// member's reference sheet.
//
// The member's Visual is emitted on its own line, unchanged: this
// function concatenates and never rewrites, which is lock 1 (the
// verbatim text lock, project.md §2). Whatever the cast bible says
// the character looks like is what the model is told, byte for byte,
// here and on every page the character appears on. Lock 3's
// StyleSuffix closes the prompt.
func ReferencePrompt(m story.CastMember) string {
	var b strings.Builder
	b.WriteString("Character reference sheet for ")
	b.WriteString(m.Name)
	b.WriteString(".\n\n")
	b.WriteString(m.Visual) // verbatim — lock 1
	b.WriteString("\n\n")
	b.WriteString(referenceDirective)
	b.WriteString("\n\n")
	b.WriteString(styleDirective)
	b.WriteString("\n")
	b.WriteString(StyleSuffix) // lock 3
	return b.String()
}

// PagePrompt builds the image-to-image prompt for one page, given the
// cast bible and the reference-sheet plan.
//
// The page's own Prompt leads. Every character the page names that
// has a reference sheet then gets a line carrying their Visual
// unchanged (lock 1), in the order the page lists them. The first
// such character decides the image-to-image base - the reference
// sheet Illustrate sends alongside this prompt - so the prompt names
// that sheet explicitly and tells the model to hold it identical
// (lock 2). styleDirective and StyleSuffix close it (lock 3).
//
// A character whose sheet belongs to another cast member (a variant -
// "Happy River" against "Grumpy River"'s sheet) still gets its own
// visual pasted verbatim; only the sheet is shared, so the mood
// change the variant describes still renders.
//
// PagePrompt returns an error wrapping ErrNoReference when the page
// names no characters at all, names one the cast bible does not
// contain, or names only characters with no reference sheet. All
// three mean the page has no sheet to be drawn against, and
// rendering it text-to-image anyway would return a pretty picture
// with the wrong cast - the exact silent failure PLAN.md T6 forbids.
// Character names are matched exactly as spelled, the same way
// story.Validate resolves them.
func PagePrompt(p story.Page, cast []story.CastMember, plan Plan) (string, error) {
	prompt, _, err := buildPage(p, cast, plan, sheetIndex(plan))
	return prompt, err
}

// sheetIndex maps each planned sheet's cast name to its position in
// plan.Sheets - the index of the Reference that Illustrate will send
// as the image-to-image base.
func sheetIndex(plan Plan) map[string]int {
	idx := make(map[string]int, len(plan.Sheets))
	for i, m := range plan.Sheets {
		idx[m.Name] = i
	}
	return idx
}

// buildPage builds a page's prompt and resolves which reference sheet
// it is rendered against, as an index into plan.Sheets.
//
// Prompt and sheet are decided together on one pass over
// p.Characters, so the image Illustrate attaches is always the one
// the prompt says is attached - lock 2 cannot come apart through two
// functions disagreeing about which character leads.
func buildPage(p story.Page, cast []story.CastMember, plan Plan, refIndex map[string]int) (string, int, error) {
	if len(p.Characters) == 0 {
		return "", 0, fmt.Errorf("illustrate: page %d: %w: the page names no cast member, so there is no reference sheet to draw it against", p.N, ErrNoReference)
	}
	byName := make(map[string]story.CastMember, len(cast))
	for _, m := range cast {
		byName[m.Name] = m
	}
	var drawable []story.CastMember
	lead, sheet := -1, ""
	for _, name := range p.Characters {
		m, ok := byName[name]
		if !ok {
			return "", 0, fmt.Errorf("illustrate: page %d: %w: character %q is not in the cast bible, so no reference sheet exists for them", p.N, ErrNoReference, name)
		}
		anchor, hasSheet := plan.SheetOf[name]
		if !hasSheet {
			// No appearance to draw and no sheet to lock against -
			// the member is in Plan.Skipped with the reason. Leaving
			// them out of the prompt is deliberate: pasting "no
			// visual" into an image prompt invites the model to draw
			// the words.
			continue
		}
		i, planned := refIndex[anchor]
		if !planned {
			continue
		}
		if lead < 0 {
			lead, sheet = i, anchor
		}
		drawable = append(drawable, m)
	}
	if lead < 0 {
		return "", 0, fmt.Errorf("illustrate: page %d: %w: none of the characters it names has a reference sheet (%s)", p.N, ErrNoReference, strings.Join(p.Characters, ", "))
	}

	first := drawable[0].Name

	var b strings.Builder
	b.WriteString(p.Prompt)
	b.WriteString("\n\nCharacters in this scene, drawn exactly as described:\n")
	for _, m := range drawable {
		b.WriteString("- ")
		b.WriteString(m.Name)
		b.WriteString(": ")
		b.WriteString(m.Visual) // verbatim - lock 1
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if sheet == first {
		b.WriteString("The attached reference image is ")
		b.WriteString(first)
		b.WriteString(". Keep ")
		b.WriteString(first)
		b.WriteString(" identical to that reference image - same face, same hair, same clothing, same colours.\n\n")
	} else {
		b.WriteString("The attached reference image is ")
		b.WriteString(sheet)
		b.WriteString(", who is the same character as ")
		b.WriteString(first)
		b.WriteString(". Keep ")
		b.WriteString(first)
		b.WriteString(" identical to that reference image - same face, same hair, same clothing, same colours; only the expression may change.\n\n")
	}
	b.WriteString(styleDirective)
	b.WriteString("\n")
	b.WriteString(StyleSuffix) // lock 3
	return b.String(), lead, nil
}
