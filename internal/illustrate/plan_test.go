package illustrate

import (
	"reflect"
	"testing"

	"thutapi/internal/story"
)

// The two narrators below are verbatim from live Phase-B output on
// 2026-09-05: two Structure runs over the same real interview
// transcript, both schema-valid under story.Validate. They are
// fixtures because they are the actual hazard — the two runs phrased
// "no appearance" differently, so any predicate that matched one
// literal would have shipped a paid reference sheet for the other.
const (
	liveNarratorVisual1 = "no visual"
	liveNarratorVisual2 = "an unseen storyteller with no appearance"
)

// The two rivers are verbatim from the same live run: one entity, two
// cast members, unique names — so story.Validate accepts them — and
// the second visual literally begins "the same wide blue river".
// Separate reference sheets would make them two unrelated rivers,
// which is the drift this whole track exists to prevent.
const (
	liveGrumpyRiverVisual = "a wide, swirling blue river with frothy waves and a sulky, wrinkled face in the water"
	liveHappyRiverVisual  = "the same wide blue river, now smiling brightly with sparkles and bubbles dancing on its surface"
)

// TestNeedsReferenceSheet is H3's both-directions table
// (t6-round1.md). Down one side: a member whose visual ASSERTS no
// appearance — two live narrator phrasings, placeholders, clause-
// and whole-visual anchored — costs no sheet. Down the other: a
// member whose visual merely CONTAINS an absence word mid-description
// is a drawable character, because a ghost "with no face", a snowman
// "never seen without his red scarf" and a boy "in an invisible
// cloak" are a six-year-old's cast, and under the old substring
// match one of those words failed the whole book.
func TestNeedsReferenceSheet(t *testing.T) {
	tests := []struct {
		name   string
		member story.CastMember
		want   bool
	}{
		{"live narrator run 1", story.CastMember{Name: "Narrator", Visual: liveNarratorVisual1}, false},
		{"live narrator run 2", story.CastMember{Name: "Narrator", Visual: liveNarratorVisual2}, false},
		{"live grumpy river", story.CastMember{Name: "Grumpy River", Visual: liveGrumpyRiverVisual}, true},
		{"live happy river", story.CastMember{Name: "Happy River", Visual: liveHappyRiverVisual}, true},
		{"ordinary child character", story.CastMember{Name: "Mira", Visual: "a small girl with two red plaits and green wellies"}, true},
		{"name Narrator is not reserved", story.CastMember{Name: "Narrator", Visual: "a tall man in a striped coat holding a lantern"}, true},
		{"empty visual", story.CastMember{Name: "Voice", Visual: ""}, false},
		{"whitespace visual", story.CastMember{Name: "Voice", Visual: "   "}, false},
		{"placeholder n/a", story.CastMember{Name: "Voice", Visual: "N/A"}, false},
		{"placeholder none with period", story.CastMember{Name: "Voice", Visual: "None."}, false},
		{"trailing period on phrase", story.CastMember{Name: "Voice", Visual: "No visual."}, false},
		{"mixed case phrase", story.CastMember{Name: "Voice", Visual: "An UNSEEN narrator, never seen on the page"}, false},
		{"voice only", story.CastMember{Name: "Radio", Visual: "voice only, heard from the kitchen"}, false},
		{"absence asserted at a clause head", story.CastMember{Name: "Voice", Visual: "a warm presence, but no visual"}, false},
		{"absence asserted whole-visual", story.CastMember{Name: "Ghost", Visual: "not shown in the story, only heard"}, false},
		{"ghost with no face is drawable", story.CastMember{Name: "Boo", Visual: "a shy little ghost with no face, just two floating eyes and a wobbly white sheet"}, true},
		{"faceless rag doll is drawable", story.CastMember{Name: "Dolly", Visual: "a faceless rag doll with button eyes sewn on crooked"}, true},
		{"snowman never seen without his scarf is drawable", story.CastMember{Name: "Snowy", Visual: "a snowman in a top hat who is never seen without his red scarf"}, true},
		{"boy in an invisible cloak is drawable", story.CastMember{Name: "Will", Visual: "a boy in an invisible cloak, only his boots showing"}, true},
		{"invisibility described is an appearance", story.CastMember{Name: "Ghost Boy", Visual: "an invisible boy in a blue coat"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsReferenceSheet(tc.member); got != tc.want {
				t.Errorf("NeedsReferenceSheet(%q / %q) = %v, want %v", tc.member.Name, tc.member.Visual, got, tc.want)
			}
		})
	}
}

// TestPlanReferences_LiveNarratorGetsNoSheet pins the first live
// hazard: a Narrator cast member that story.Validate accepts must not
// cost a paid reference-sheet call, and the skip must be visible.
func TestPlanReferences_LiveNarratorGetsNoSheet(t *testing.T) {
	for _, visual := range []string{liveNarratorVisual1, liveNarratorVisual2} {
		t.Run(visual, func(t *testing.T) {
			s := story.Story{
				Title: "The River",
				Cast: []story.CastMember{
					{Name: "Narrator", Visual: visual},
					{Name: "Mira", Visual: "a small girl with two red plaits"},
				},
				Pages: []story.Page{page(1, "Mira waves", "Narrator", "Mira")},
			}
			plan := PlanReferences(s)
			if len(plan.Sheets) != 1 || plan.Sheets[0].Name != "Mira" {
				t.Fatalf("Sheets = %v, want exactly Mira — a narrator with no appearance costs a paid image for nothing", names(plan.Sheets))
			}
			if _, ok := plan.SheetOf["Narrator"]; ok {
				t.Errorf("SheetOf[Narrator] is set; no page may be locked to a narrator's sheet")
			}
			if len(plan.Skipped) != 1 {
				t.Fatalf("Skipped = %+v, want exactly one entry", plan.Skipped)
			}
			got := plan.Skipped[0]
			if got.Name != "Narrator" || got.Reason != SkipNoAppearance || got.Visual != visual {
				t.Errorf("Skipped[0] = %+v, want {Narrator, %q, %s}", got, visual, SkipNoAppearance)
			}
		})
	}
}

// TestPlanReferences_LiveRiverVariantSharesOneSheet pins the second
// live hazard: one entity emitted as two cast members must be drawn
// from one reference sheet, not two unrelated ones.
func TestPlanReferences_LiveRiverVariantSharesOneSheet(t *testing.T) {
	s := story.Story{
		Title: "The River",
		Cast: []story.CastMember{
			{Name: "Grumpy River", Visual: liveGrumpyRiverVisual},
			{Name: "Happy River", Visual: liveHappyRiverVisual},
		},
		Pages: []story.Page{
			page(1, "The river sulks", "Grumpy River"),
			page(2, "The river smiles", "Happy River"),
		},
	}
	plan := PlanReferences(s)
	if len(plan.Sheets) != 1 || plan.Sheets[0].Name != "Grumpy River" {
		t.Fatalf("Sheets = %v, want exactly [Grumpy River]: two sheets are two unrelated rivers", names(plan.Sheets))
	}
	if got := plan.SheetOf["Happy River"]; got != "Grumpy River" {
		t.Errorf("SheetOf[Happy River] = %q, want %q", got, "Grumpy River")
	}
	if got := plan.SheetOf["Grumpy River"]; got != "Grumpy River" {
		t.Errorf("SheetOf[Grumpy River] = %q, want itself", got)
	}
	if len(plan.Skipped) != 1 {
		t.Fatalf("Skipped = %+v, want exactly one entry", plan.Skipped)
	}
	if got := plan.Skipped[0]; got.Name != "Happy River" || got.Reason != SkipVariant || got.SheetOf != "Grumpy River" {
		t.Errorf("Skipped[0] = %+v, want {Happy River, %s, SheetOf: Grumpy River}", got, SkipVariant)
	}
}

// TestPlanReferences_NoBackReferenceKeepsSeparateSheets is the other
// half of the variant rule: a shared name token alone must not fuse
// two real characters. Without it, "Mira" and "Mira's Mum" collapse.
func TestPlanReferences_NoBackReferenceKeepsSeparateSheets(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Mira", Visual: "a small girl with two red plaits"},
			{Name: "Mira's Mum", Visual: "a tall woman with the same red hair, in a yellow raincoat"},
			{Name: "Blue River", Visual: "a wide blue river"},
			{Name: "Green Hill", Visual: "a soft green hill"},
		},
		Pages: []story.Page{page(1, "everyone", "Mira", "Mira's Mum", "Blue River", "Green Hill")},
	}
	plan := PlanReferences(s)
	if len(plan.Sheets) != 4 {
		t.Fatalf("Sheets = %v, want 4 — %q back-references hair, not an earlier character's identity", names(plan.Sheets), "Mira's Mum")
	}
	if len(plan.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none", plan.Skipped)
	}
}

// TestPlanReferences_VariantNeedsIdentityEvidence is round 1 H2's
// corpus — the T5b live cast plus the reviewer's probes. A
// back-reference marker alone never fuses, and neither does a shared
// token: fusion needs the "the same <noun>" construction to head
// with the anchor's own head noun ("the same wide blue river" heads
// with river, the anchor's kind), or the two names to be aliases of
// one referent. A shared species word, a comparison, a possessive
// mention and a dog measured against the girl are different
// characters, and each anchors a sheet of their own; the river pair
// fuses, deterministically onto the earlier cast member.
func TestPlanReferences_VariantNeedsIdentityEvidence(t *testing.T) {
	tests := []struct {
		name        string
		cast        []story.CastMember
		wantSheets  []string
		wantVariant string // non-empty: this member is the group's variant
	}{
		{
			name: "mum phrased marker-first is her own character",
			cast: []story.CastMember{
				{Name: "Mira", Visual: "a small girl with two red plaits"},
				{Name: "Mira's Mum", Visual: "the same red hair as Mira, on a tall woman in a yellow raincoat"},
			},
			wantSheets: []string{"Mira", "Mira's Mum"},
		},
		{
			name: "two dragons share a species, not an identity",
			cast: []story.CastMember{
				{Name: "Blue Dragon", Visual: "a small blue dragon with silvery wings"},
				{Name: "Green Dragon", Visual: "the same size as her brother, but bright green from nose to tail"},
			},
			wantSheets: []string{"Blue Dragon", "Green Dragon"},
		},
		{
			name: "a dog described against the girl is not a variant of her",
			cast: []story.CastMember{
				{Name: "Mira", Visual: "a small girl with two red plaits"},
				{Name: "Bramble", Visual: "the same height as Mira's knee, a shaggy brown dog with one white ear"},
			},
			wantSheets: []string{"Mira", "Bramble"},
		},
		{
			name: "one river in two moods is one entity, anchored by the earlier cast member",
			cast: []story.CastMember{
				{Name: "Grumpy River", Visual: liveGrumpyRiverVisual},
				{Name: "Happy River", Visual: liveHappyRiverVisual},
			},
			wantSheets:  []string{"Grumpy River"},
			wantVariant: "Happy River",
		},
		{
			name: "a name and its alias are one referent",
			cast: []story.CastMember{
				{Name: "The Sock", Visual: "an odd striped sock with a hole in the toe"},
				{Name: "Sock", Visual: "the same sock, now lost under the sofa"},
			},
			wantSheets:  []string{"The Sock"},
			wantVariant: "Sock",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			all := make([]string, len(tc.cast))
			for i, m := range tc.cast {
				all[i] = m.Name
			}
			s := story.Story{
				Title: "x",
				Cast:  tc.cast,
				Pages: []story.Page{page(1, "everyone is here", all...)},
			}
			plan := PlanReferences(s)
			if got := names(plan.Sheets); !reflect.DeepEqual(got, tc.wantSheets) {
				t.Fatalf("Sheets = %v, want %v — a false fusion is two characters drawn as one", got, tc.wantSheets)
			}
			if tc.wantVariant == "" {
				if len(plan.Skipped) != 0 {
					t.Errorf("Skipped = %+v, want none", plan.Skipped)
				}
				return
			}
			if got := plan.SheetOf[tc.wantVariant]; got != tc.wantSheets[0] {
				t.Errorf("SheetOf[%s] = %q, want %q (the deterministic anchor)", tc.wantVariant, got, tc.wantSheets[0])
			}
			if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipVariant {
				t.Fatalf("Skipped = %+v, want one %s entry for %s", plan.Skipped, SkipVariant, tc.wantVariant)
			}
		})
	}
}

// TestPlanReferences_UnusedCastMemberGetsNoSheet pins the money rule:
// a sheet no page can render against is a paid call for nothing.
func TestPlanReferences_UnusedCastMemberGetsNoSheet(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Mira", Visual: "a small girl with two red plaits"},
			{Name: "Bramble", Visual: "a shaggy brown dog with one white ear"},
		},
		Pages: []story.Page{page(1, "Mira alone", "Mira")},
	}
	plan := PlanReferences(s)
	if len(plan.Sheets) != 1 || plan.Sheets[0].Name != "Mira" {
		t.Fatalf("Sheets = %v, want exactly [Mira]", names(plan.Sheets))
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipUnused || plan.Skipped[0].Name != "Bramble" {
		t.Fatalf("Skipped = %+v, want one %s entry for Bramble", plan.Skipped, SkipUnused)
	}
	if _, ok := plan.SheetOf["Bramble"]; ok {
		t.Errorf("SheetOf[Bramble] is set, but no sheet was generated for it")
	}
}

// TestPlanReferences_VariantKeepsAnchorSheetWhenOnlyVariantIsOnPage
// covers the interaction between the variant rule and the unused
// rule: the anchor is on no page, but its variant is, so the group is
// still needed and the sheet must survive.
func TestPlanReferences_VariantKeepsAnchorSheetWhenOnlyVariantIsOnPage(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Grumpy River", Visual: liveGrumpyRiverVisual},
			{Name: "Happy River", Visual: liveHappyRiverVisual},
		},
		Pages: []story.Page{page(1, "The river smiles", "Happy River")},
	}
	plan := PlanReferences(s)
	if len(plan.Sheets) != 1 || plan.Sheets[0].Name != "Grumpy River" {
		t.Fatalf("Sheets = %v, want [Grumpy River]: the anchor's sheet is what Happy River is locked to", names(plan.Sheets))
	}
	if got := plan.SheetOf["Happy River"]; got != "Grumpy River" {
		t.Errorf("SheetOf[Happy River] = %q, want Grumpy River", got)
	}
}

// TestPlanReferences_SkippedOrderIsCastOrder pins determinism: the
// same story must always plan the same sheets and report the same
// skips in the same order, or the book is unreproducible.
func TestPlanReferences_SkippedOrderIsCastOrder(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Narrator", Visual: liveNarratorVisual1},
			{Name: "Grumpy River", Visual: liveGrumpyRiverVisual},
			{Name: "Happy River", Visual: liveHappyRiverVisual},
			{Name: "Bramble", Visual: "a shaggy brown dog with one white ear"},
		},
		Pages: []story.Page{page(1, "the river", "Narrator", "Grumpy River", "Happy River")},
	}
	want := []Skip{
		{Name: "Narrator", Visual: liveNarratorVisual1, Reason: SkipNoAppearance},
		{Name: "Happy River", Visual: liveHappyRiverVisual, Reason: SkipVariant, SheetOf: "Grumpy River"},
		{Name: "Bramble", Visual: "a shaggy brown dog with one white ear", Reason: SkipUnused},
	}
	for i := 0; i < 20; i++ {
		plan := PlanReferences(s)
		if len(plan.Skipped) != len(want) {
			t.Fatalf("Skipped = %+v, want %+v", plan.Skipped, want)
		}
		for j := range want {
			if plan.Skipped[j] != want[j] {
				t.Fatalf("Skipped[%d] = %+v, want %+v", j, plan.Skipped[j], want[j])
			}
		}
	}
}

// TestPlanReferences_EmptyStory covers the degenerate input: no cast
// means no sheets and no skips, and PlanReferences must not panic on
// the way to Illustrate's ErrInvalidCast.
func TestPlanReferences_EmptyStory(t *testing.T) {
	plan := PlanReferences(story.Story{})
	if len(plan.Sheets) != 0 || len(plan.Skipped) != 0 || len(plan.SheetOf) != 0 {
		t.Errorf("PlanReferences(zero story) = %+v, want an empty plan", plan)
	}
}

// names renders a sheet list for a failure message.
func names(members []story.CastMember) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.Name)
	}
	return out
}

// TestPlanReferences_BackReferenceWithNoAnchorStaysItsOwnCharacter
// covers the half of the variant rule where the marker is there and
// the anchor is not: the first cast member has nobody to be a variant
// of, and a back-reference to a character sharing no identifying
// token is not evidence of anything.
func TestPlanReferences_BackReferenceWithNoAnchorStaysItsOwnCharacter(t *testing.T) {
	s := story.Story{
		Cast: []story.CastMember{
			{Name: "Happy River", Visual: liveHappyRiverVisual},
			{Name: "Bramble", Visual: "the same size as a breadbin, shaggy and brown with one white ear"},
		},
		Pages: []story.Page{page(1, "the river and the dog", "Happy River", "Bramble")},
	}
	plan := PlanReferences(s)
	if len(plan.Sheets) != 2 {
		t.Fatalf("Sheets = %v, want 2: neither member has an earlier character to be a variant of", names(plan.Sheets))
	}
	if len(plan.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none", plan.Skipped)
	}
}
