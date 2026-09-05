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
// unchanged (lock 1), in the order the page lists them. The pages
// render with ALL of those characters' reference sheets attached —
// payload.image is an array of their URLs (t6b-live-record.md item
// 1b) — so the prompt names every attached sheet and tells the model
// to hold each character identical to their reference image (lock 2).
// styleDirective and StyleSuffix close it (lock 3).
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

// buildPage builds a page's prompt and resolves which reference
// sheets it is rendered against, as indexes into plan.Sheets — one per
// character the page names, in named order, distinct (two names on one
// sheet appear once).
//
// Prompt and sheets are decided together on one pass over
// p.Characters, so the images Illustrate attaches are always the ones
// the prompt says are attached — lock 2 cannot come apart through two
// functions disagreeing about which character leads.
func buildPage(p story.Page, cast []story.CastMember, plan Plan, refIndex map[string]int) (string, []int, error) {
	if len(p.Characters) == 0 {
		return "", nil, fmt.Errorf("illustrate: page %d: %w: the page names no cast member, so there is no reference sheet to draw it against", p.N, ErrNoReference)
	}
	byName := make(map[string]story.CastMember, len(cast))
	for _, m := range cast {
		byName[m.Name] = m
	}
	var (
		drawable []story.CastMember
		sheets   []string
		seen     = map[string]bool{}
	)
	for _, name := range p.Characters {
		m, ok := byName[name]
		if !ok {
			return "", nil, fmt.Errorf("illustrate: page %d: %w: character %q is not in the cast bible, so no reference sheet exists for them", p.N, ErrNoReference, name)
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
		if _, planned := refIndex[anchor]; !planned {
			continue
		}
		if !seen[anchor] {
			seen[anchor] = true
			sheets = append(sheets, anchor)
		}
		drawable = append(drawable, m)
	}
	if len(sheets) == 0 {
		return "", nil, fmt.Errorf("illustrate: page %d: %w: none of the characters it names has a reference sheet (%s)", p.N, ErrNoReference, strings.Join(p.Characters, ", "))
	}

	bases := make([]int, len(sheets))
	for j, s := range sheets {
		bases[j] = refIndex[s]
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
	if len(sheets) == 1 {
		sheet := sheets[0]
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
	} else {
		// Several sheets ride along (t6b-live-record.md item 1b:
		// multi-reference keeps every entity). Name each one, and say
		// so where a sheet belongs to a variant the page names rather
		// than to a character under their own name — the same clause
		// the single-sheet case uses.
		parts := make([]string, len(sheets))
		for j, s := range sheets {
			part := s
			named := false
			variant := ""
			for _, m := range drawable {
				if m.Name == s {
					named = true
					break
				}
				if plan.SheetOf[m.Name] == s && variant == "" {
					variant = m.Name
				}
			}
			if !named && variant != "" {
				part = s + ", who is the same character as " + variant
			}
			parts[j] = part
		}
		b.WriteString("The attached reference images are ")
		b.WriteString(strings.Join(parts, " and "))
		b.WriteString(". Keep each character identical to their reference image - same face, same hair, same clothing, same colours.\n\n")
	}
	b.WriteString(styleDirective)
	b.WriteString("\n")
	b.WriteString(StyleSuffix) // lock 3
	return b.String(), bases, nil
}
