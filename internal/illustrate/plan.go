package illustrate

import (
	"strings"
	"unicode"

	"thutapi/internal/story"
)

// SkipReason names why a cast member got no reference sheet of its
// own. Every skip is recorded in Plan.Skipped with one of these — a
// cast member is never dropped silently, because a quietly missing
// sheet is indistinguishable from a sheet that was never wanted.
type SkipReason string

const (
	// SkipNoAppearance is a cast member whose visual describes no
	// appearance at all — a narrator, a voice, an unseen storyteller.
	// See NeedsReferenceSheet.
	SkipNoAppearance SkipReason = "no appearance"
	// SkipVariant is a cast member that is another cast member in a
	// different mood, and is locked to that member's sheet instead of
	// getting a second, unrelated one. See Skip.SheetOf.
	SkipVariant SkipReason = "variant of another cast member"
	// SkipUnused is a cast member (with its variants, if any) that no
	// page names. Nothing can be rendered against their sheet, so
	// generating one is a paid call for an image no page will use.
	SkipUnused SkipReason = "named by no page"
)

// Skip records one cast member that got no reference sheet of its
// own, and why.
type Skip struct {
	// Name is the cast member's name, as spelled in the cast bible.
	Name string
	// Visual is the cast member's visual string, unchanged — the
	// evidence for Reason.
	Visual string
	// Reason says why no sheet was generated.
	Reason SkipReason
	// SheetOf names the cast member whose reference sheet this one is
	// locked to instead. Set only for SkipVariant; empty otherwise.
	SheetOf string
}

// Plan is the reference-sheet plan for one story's cast: which
// members get a sheet, which share another's, and which get none.
//
// It exists because "one reference image per cast member" is wrong on
// real Phase-B output in two ways, both observed live on 2026-09-05:
//
//   - M3 emits cast members with no appearance. Two runs over the same
//     transcript produced a Narrator whose visual was "no visual" and
//     "an unseen storyteller with no appearance" respectively. Both
//     pass story.Validate. A sheet for either is a paid call for a
//     picture no page should ever be locked against, and the two
//     phrasings differ, so no literal-string test would catch both.
//   - M3 emits the same entity twice under different names. One run
//     produced both "Grumpy River" ("a wide, swirling blue river with
//     frothy waves and a sulky, wrinkled face in the water") and
//     "Happy River" ("the same wide blue river, now smiling brightly
//     with sparkles and bubbles dancing on its surface"). Names are
//     unique, so story.Validate accepts them; separate sheets would
//     make them two unrelated rivers, which is precisely the drift
//     this track exists to prevent.
//
// Both decisions are loud: every member without a sheet of its own is
// listed in Skipped with a reason.
type Plan struct {
	// Sheets are the cast members a reference sheet is generated for,
	// in cast order.
	Sheets []story.CastMember
	// SheetOf maps every drawable cast name to the name of the cast
	// member whose reference sheet it is locked to. A member with its
	// own sheet maps to itself; a variant maps to its anchor. A name
	// absent from this map has no sheet and cannot anchor a page.
	SheetOf map[string]string
	// Skipped lists every cast member that got no sheet of its own,
	// in cast order, with the reason.
	Skipped []Skip
}

// noAppearancePhrases are the phrases that assert a cast member has
// no appearance to draw. They are matched case-insensitively as
// substrings of the visual.
//
// This is a semantic set, not a fixture set: the two real narrators
// observed live phrased it differently ("no visual" and "an unseen
// storyteller with no appearance"), so matching either literal would
// have missed the other. The set is deliberately about the *absence*
// of an appearance rather than about narrators — "Narrator" is not a
// reserved name and a child may legitimately name a character that.
//
// The known cost: a character whose appearance genuinely is
// invisibility ("an invisible boy") is skipped too. That is a
// documented false positive and it stays loud rather than silent —
// the member lands in Plan.Skipped, and a page naming only that
// member fails with ErrNoReference instead of quietly rendering
// text-to-image.
var noAppearancePhrases = []string{
	"no visual",
	"no appearance",
	"no physical",
	"no body",
	"no face",
	"no image",
	"no description",
	"not visible",
	"not seen",
	"not shown",
	"not depicted",
	"never seen",
	"never appears",
	"does not appear",
	"doesn't appear",
	"unseen",
	"invisible",
	"faceless",
	"voice only",
	"voice-only",
	"off-screen",
	"offscreen",
	"off screen",
}

// noAppearanceExact are whole visuals that assert no appearance
// without using any of the phrases above — the placeholder answers a
// model gives when it has nothing to say. Compared against the
// lowercased, trimmed visual, with any trailing period removed.
var noAppearanceExact = []string{"", "-", "--", "n/a", "na", "none", "nothing", "unknown", "tbd"}

// backReferenceMarkers are the openings a visual uses when it defines
// a character by pointing at another one instead of describing them —
// "the same wide blue river, now smiling brightly".
//
// The marker must OPEN the visual, not merely appear in it. A member
// that is another character in a second mood says so from the first
// words; a marker mid-sentence is describing a detail, and matching
// it there fuses real characters: "Mira's Mum", whose visual reads "a
// tall woman with the same red hair", shares the token "mira" with
// "Mira" and would otherwise be locked to her daughter's sheet. The
// rule deliberately under-matches — a missed variant costs one extra
// reference sheet, a false one costs two characters drawn as one.
var backReferenceMarkers = []string{"the same ", "same as ", "identical to ", "as above", "unchanged from "}

// nameStopwords are name tokens too common to identify a character.
// Two members sharing only one of these ("Little Bear", "Little
// Mouse") are not the same entity.
var nameStopwords = map[string]bool{
	"the": true, "and": true, "with": true, "little": true,
	"big": true, "old": true, "young": true, "baby": true,
	"mister": true, "missus": true, "uncle": true, "aunty": true,
}

// minNameToken is the shortest name token that may identify a
// character. Short tokens ("a", "of", "mr") carry no identity.
const minNameToken = 4

// NeedsReferenceSheet reports whether a cast member has an appearance
// worth generating a reference sheet for.
//
// It is false for the members M3 emits that describe no appearance at
// all — narrators, voices, unseen storytellers. Such a member costs a
// paid image call and yields a sheet no page should be locked
// against, so it never gets one; the skip is recorded in
// Plan.Skipped, and the member is left out of page prompts (there is
// no appearance to paste, so lock 1 has nothing to hold).
//
// The predicate is over the *visual*, never the name: "Narrator" is
// not a reserved word and a child may name a real character that.
// See noAppearancePhrases for the matched set and its documented
// false positive.
func NeedsReferenceSheet(m story.CastMember) bool {
	v := strings.ToLower(strings.TrimSpace(m.Visual))
	v = strings.TrimRight(v, ". ")
	for _, exact := range noAppearanceExact {
		if v == exact {
			return false
		}
	}
	for _, phrase := range noAppearancePhrases {
		if strings.Contains(v, phrase) {
			return false
		}
	}
	return true
}

// PlanReferences decides the reference sheets for a story's cast.
//
// Members are considered in cast order. A member with no appearance
// (NeedsReferenceSheet) is skipped. A member whose visual
// back-references an earlier member — "the same wide blue river, now
// smiling" — and shares an identifying name token with them is locked
// to that member's sheet rather than getting its own. Everything else
// anchors a sheet of its own. Finally, a sheet no page can use (the
// anchor and all its variants are named by no page) is dropped: a
// paid call for a picture nothing renders against.
//
// The result is deterministic — cast order throughout — so the same
// story always plans the same sheets.
func PlanReferences(s story.Story) Plan {
	var groups []refGroup
	// anchorOf maps a cast index to its group index; a member with no
	// appearance is absent.
	anchorOf := make(map[int]int, len(s.Cast))

	for i, m := range s.Cast {
		if !NeedsReferenceSheet(m) {
			continue
		}
		if gi, ok := variantGroup(m, groups, s.Cast); ok {
			groups[gi].members = append(groups[gi].members, i)
			groups[gi].names = append(groups[gi].names, m.Name)
			anchorOf[i] = gi
			continue
		}
		groups = append(groups, refGroup{anchor: i, members: []int{i}, names: []string{m.Name}})
		anchorOf[i] = len(groups) - 1
	}

	// A group is kept only if some page names one of its members.
	onPage := make(map[string]bool)
	for _, p := range s.Pages {
		for _, c := range p.Characters {
			onPage[c] = true
		}
	}
	keep := make([]bool, len(groups))
	for gi, g := range groups {
		for _, name := range g.names {
			if onPage[name] {
				keep[gi] = true
				break
			}
		}
	}

	plan := Plan{SheetOf: make(map[string]string, len(s.Cast))}
	for i, m := range s.Cast {
		gi, drawable := anchorOf[i]
		switch {
		case !drawable:
			plan.Skipped = append(plan.Skipped, Skip{Name: m.Name, Visual: m.Visual, Reason: SkipNoAppearance})
		case !keep[gi]:
			plan.Skipped = append(plan.Skipped, Skip{Name: m.Name, Visual: m.Visual, Reason: SkipUnused})
		case groups[gi].anchor == i:
			plan.Sheets = append(plan.Sheets, m)
			plan.SheetOf[m.Name] = m.Name
		default:
			anchor := s.Cast[groups[gi].anchor].Name
			plan.SheetOf[m.Name] = anchor
			plan.Skipped = append(plan.Skipped, Skip{Name: m.Name, Visual: m.Visual, Reason: SkipVariant, SheetOf: anchor})
		}
	}
	return plan
}

// refGroup is one reference sheet under construction: the cast member
// the sheet is generated from (anchor) and every cast member locked to
// it, as indexes into the story's cast plus their names.
type refGroup struct {
	anchor  int
	members []int
	names   []string
}

// variantGroup reports whether m is an existing group's character in
// a different mood, and which group.
//
// Two conditions must both hold, because either alone over-matches. m
// must define itself by pointing at another character (a
// backReferenceMarker: "the same ...", "identical to ..."), AND it
// must share an identifying token with a member of that group —
// either between the two names ("Grumpy River" / "Happy River") or as
// that member's name token appearing in m's visual ("the same wide
// blue river"). A shared name token alone would fuse "Mira" with
// "Mira's Mum"; a back-reference alone would fuse every character
// whose visual happens to say "the same size".
//
// The earliest matching group wins, so the result is deterministic.
func variantGroup(m story.CastMember, groups []refGroup, cast []story.CastMember) (int, bool) {
	lowerVisual := strings.ToLower(strings.TrimSpace(m.Visual))
	backRef := false
	for _, marker := range backReferenceMarkers {
		if strings.HasPrefix(lowerVisual, marker) {
			backRef = true
			break
		}
	}
	if !backRef {
		return 0, false
	}
	mine := tokens(m.Name)
	visualTokens := tokens(m.Visual)
	for gi, g := range groups {
		for _, idx := range g.members {
			for tok := range tokens(cast[idx].Name) {
				if mine[tok] || visualTokens[tok] {
					return gi, true
				}
			}
		}
	}
	return 0, false
}

// tokens lowercases s, splits it on everything that is not a letter or
// a digit, and keeps the tokens long enough and distinctive enough to
// identify a character: at least minNameToken runes and not a
// nameStopword. Cast names are a child's wording, so the split is
// Unicode-aware rather than ASCII-only.
func tokens(s string) map[string]bool {
	out := make(map[string]bool)
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(f)) < minNameToken || nameStopwords[f] {
			continue
		}
		out[f] = true
	}
	return out
}
