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

// noAppearancePhrases are the phrases that ASSERT a cast member has
// no appearance to draw. Each is matched only where such an
// assertion can begin — at the head of the visual, optionally after
// a leading article, or just after a clause boundary (see
// absenceSegments). As bare substrings these same words are ordinary
// description: a ghost "with no face", a boy "in an invisible cloak"
// and a snowman "never seen without his red scarf" are drawable
// characters, and the substring match failed the whole book for them
// (t6-round1.md H3).
//
// This is a semantic set, not a fixture set: the two real narrators
// observed live phrased it differently ("no visual" and "an unseen
// storyteller with no appearance"), so matching either literal would
// have missed the other. The set is deliberately about the *absence*
// of an appearance rather than about narrators — "Narrator" is not a
// reserved name and a child may legitimately name a character that.
//
// Both misses cost money, in opposite directions: a member whose
// visual genuinely asserts absence ("a voice only") is skipped
// loudly — Plan.Skipped with SkipNoAppearance, and a page naming
// only that member fails with ErrNoReference rather than quietly
// rendering text-to-image — while a member whose visual merely
// CONTAINS an absence word mid-description is drawn. A missed
// narrator costs one extra reference sheet; a false skip costs a
// drawable character and, on a page that names only them, the whole
// book.
var noAppearancePhrases = []string{
	"no visual",
	"no appearance",
	"no physical",
	"no image",
	"no description",
	"not visible",
	"not shown",
	"not depicted",
	"does not appear",
	"doesn't appear",
	"never appears",
	"unseen",
	"voice only",
	"voice-only",
	"off-screen",
	"offscreen",
	"off screen",
}

// subordinators are the words an absence assertion can hang on inside
// a clause — "with no appearance", "but no visual", "who is never
// depicted".
var subordinators = []string{"with ", "but ", "and ", "who ", "which ", "that "}

// articles are the articles a visual's subject can open with —
// "an unseen storyteller".
var articles = []string{"a ", "an ", "the "}

// absenceSegments splits a lowered visual into the positions an
// absence assertion can start: each punctuation-delimited clause,
// with one optional leading subordinator and then one optional
// leading article stripped. "an unseen storyteller with no
// appearance" yields "unseen storyteller with no appearance";
// "a shy little ghost with no face, just two floating eyes" yields
// "shy little ghost with no face" and "just two floating eyes" —
// neither is an assertion that the member as a whole has no
// appearance, which is the point.
func absenceSegments(v string) []string {
	parts := strings.FieldsFunc(v, func(r rune) bool {
		switch r {
		case ',', ';', ':', '.', '!', '?', '—', '–':
			return true
		}
		return false
	})
	segs := make([]string, 0, len(parts))
	for _, p := range parts {
		p = stripAny(strings.TrimSpace(p), subordinators)
		p = stripAny(p, articles)
		if p != "" {
			segs = append(segs, p)
		}
	}
	return segs
}

// stripAny removes one leading occurrence of any prefix.
func stripAny(s string, prefixes []string) string {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return strings.TrimPrefix(s, p)
		}
	}
	return s
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
// Matching is clause-anchored — see absenceSegments and
// noAppearancePhrases — so a phrase counts only where an absence
// assertion can begin, and ordinary description ("a shy little ghost
// with no face, just two floating eyes and a wobbly white sheet") is
// an appearance like any other.
func NeedsReferenceSheet(m story.CastMember) bool {
	v := strings.ToLower(strings.TrimSpace(m.Visual))
	v = strings.TrimRight(v, ". ")
	for _, exact := range noAppearanceExact {
		if v == exact {
			return false
		}
	}
	for _, seg := range absenceSegments(v) {
		for _, phrase := range noAppearancePhrases {
			if seg == phrase || strings.HasPrefix(seg, phrase+" ") {
				return false
			}
		}
	}
	return true
}

// PlanReferences decides the reference sheets for a story's cast.
//
// Members are considered in cast order. A member with no appearance
// (NeedsReferenceSheet) is skipped. A member whose visual
// back-references an earlier member — "the same wide blue river, now
// smiling" — and, in doing so, names that member's kind (or whose
// name is an alias of theirs) is locked to that member's sheet rather
// than getting its own. Everything else anchors a sheet of its own.
// Finally, a sheet no page can use (the anchor and all its variants
// are named by no page) is dropped: a paid call for a picture
// nothing renders against.
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
// The visual must still OPEN by pointing at another character (a
// backReferenceMarker: "the same ...", "identical to ...") — a member
// that is another character in a second mood says so from its first
// words, and requiring the marker keeps mid-sentence comparisons
// ("a tall woman with the same red hair") out. Given the marker,
// fusion needs identity evidence, and only two things count:
//
//   - the back-reference names the anchor's kind: "the same wide
//     blue river" heads with the noun the anchor's own name heads
//     with ("Grumpy River" → river), so it asserts "I am that same
//     river". "The same red hair as Mira" heads with "hair" and
//     "the same size as her brother" with "size" — body parts and
//     comparisons are not people, so neither fuses (t6-round1.md H2:
//     Mira's Mum and the two dragons); or
//   - the two names are aliases of one referent: their identifying
//     token sets, species words removed, are identical and non-empty
//     ("The Sock" / "Sock").
//
// A token of the anchor's name merely APPEARING in the visual is not
// identity evidence — a comparative sentence is precisely a sentence
// that names another character, and "the same height as Mira's knee,
// a shaggy brown dog with one white ear" would otherwise lock the
// dog to the girl's sheet. A shared species noun is never identity
// evidence either: see speciesWords.
//
// The earliest matching group wins, so the result is deterministic.
func variantGroup(m story.CastMember, groups []refGroup, cast []story.CastMember) (int, bool) {
	lowerVisual := strings.ToLower(strings.TrimSpace(m.Visual))
	if !opensWithBackReference(lowerVisual) {
		return 0, false
	}
	for gi, g := range groups {
		anchor := cast[g.anchor]
		if variantNamesTheAnchor(lowerVisual, anchor.Name) {
			return gi, true
		}
		if namesAreAliases(m.Name, anchor.Name) {
			return gi, true
		}
	}
	return 0, false
}

// speciesWords are shared-noun tokens that identify a KIND, never an
// individual. Two members whose names share only one of these ("Blue
// Dragon" / "Green Dragon", "Grumpy River" / "Happy River") are not
// thereby the same entity, so species words are removed before two
// names are tested for aliasing. The list is the vocabulary a
// child's cast is actually made of; extend it as live output teaches
// more. It does NOT govern variantNamesTheAnchor: there the evidence
// is the explicit "the same <noun>" construction and its head-noun
// agreement, not the bare shared word.
var speciesWords = map[string]bool{
	"dragon": true, "river": true, "dog": true, "robot": true,
	"mouse": true, "cat": true, "bear": true, "bird": true,
	"horse": true, "monster": true, "wizard": true, "witch": true,
}

// opensWithBackReference reports whether a lowered visual opens with
// a backReferenceMarker.
func opensWithBackReference(lowerVisual string) bool {
	for _, marker := range backReferenceMarkers {
		if strings.HasPrefix(lowerVisual, marker) {
			return true
		}
	}
	return false
}

// variantNamesTheAnchor reports whether a lowered visual's
// back-reference is about the anchor's own kind: the noun the
// "the same ..." phrase heads with equals the noun the anchor's name
// heads with.
func variantNamesTheAnchor(lowerVisual, anchorName string) bool {
	head, ok := samePhraseHead(lowerVisual)
	return ok && head == headNoun(anchorName)
}

// phraseBoundaries end the noun phrase a back-reference is about:
// punctuation, and the words that turn "the same X" into a
// comparison or an appositive — "the same size as her brother",
// "the same river, now smiling".
var phraseBoundaries = []string{
	",", ";", ":",
	" as ", " but ", " except ", " now ", " with ",
	" who ", " which ", " that ", " when ", " and ",
}

// samePhraseHead extracts the noun a leading back-reference is
// about: the last word of the noun phrase between the marker and the
// first boundary. "the same wide blue river, now smiling" → "river";
// "the same red hair as mira" → "hair"; "the same height as mira's
// knee" → "height". ok is false when no marker opens the visual or
// the phrase holds no identifying word.
func samePhraseHead(lowerVisual string) (string, bool) {
	for _, marker := range backReferenceMarkers {
		rest, ok := strings.CutPrefix(lowerVisual, marker)
		if !ok {
			continue
		}
		end := len(rest)
		for _, b := range phraseBoundaries {
			if i := strings.Index(rest, b); i >= 0 && i < end {
				end = i
			}
		}
		words := tokenList(rest[:end])
		if len(words) == 0 {
			return "", false
		}
		return words[len(words)-1], true
	}
	return "", false
}

// headNoun is the noun a name heads with — its last identifying
// token: "Grumpy River" → "river", "Mira" → "mira".
func headNoun(name string) string {
	words := tokenList(name)
	if len(words) == 0 {
		return ""
	}
	return words[len(words)-1]
}

// namesAreAliases reports whether two cast names denote one referent:
// their identifying token sets — species words removed — are
// identical and non-empty. "The Sock" and "Sock" do; "Mira" and
// "Mira's Mum" do not, and "Blue Dragon" and "Green Dragon" share
// only their species. Both members still need the back-reference
// marker in the visual; tokens alone are never enough.
//
// A possessive construction ("Mira's Mum") never aliases anything: a
// name that marks possession names a relation to another referent,
// not the referent itself. The rule is load-bearing precisely where
// the token test alone would lie — "mum" is shorter than
// minNameToken, so "Mira's Mum" tokenises to the same set as "Mira"
// and the possessive suffix is the only evidence left that these are
// two people (t6-round1.md H2: marker-first, Mum must anchor her own
// sheet).
func namesAreAliases(a, b string) bool {
	if strings.Contains(a, "'s") || strings.Contains(a, "’s") ||
		strings.Contains(b, "'s") || strings.Contains(b, "’s") {
		return false
	}
	ta, tb := identityTokens(a), identityTokens(b)
	if len(ta) == 0 || len(ta) != len(tb) {
		return false
	}
	for t := range ta {
		if !tb[t] {
			return false
		}
	}
	return true
}

// identityTokens is a name's tokens with the species words removed.
func identityTokens(name string) map[string]bool {
	m := tokens(name)
	for w := range speciesWords {
		delete(m, w)
	}
	return m
}

// tokens lowercases s and reports the words long enough and
// distinctive enough to identify a character: at least minNameToken
// runes and not a nameStopword.
func tokens(s string) map[string]bool {
	out := make(map[string]bool)
	for _, f := range tokenList(s) {
		out[f] = true
	}
	return out
}

// tokenList is tokens in order. It splits on everything that is not a
// letter or a digit. Cast names are a child's wording, so the split
// is Unicode-aware rather than ASCII-only.
func tokenList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(f)) < minNameToken || nameStopwords[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}
