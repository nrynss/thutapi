package interview

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/text"
)

// ---------------------------------------------------------------------------
// parseReply — the control-line contract, pinned leniently.
// ---------------------------------------------------------------------------

func TestParseReply(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want reply
	}{
		{
			name: "no control line is plain text",
			raw:  "What colour is the dragon?",
			want: reply{Text: "What colour is the dragon?"},
		},
		{
			name: "filled slots are parsed case-insensitively",
			raw:  "Ooh! Who tags along?\n[[filled: HERO, Companion]]",
			want: reply{Text: "Ooh! Who tags along?", Slots: []Slot{SlotHero, SlotCompanion}},
		},
		{
			name: "chips are parsed and trimmed",
			raw:  "Is she brave, or is she sneaky?\n[[chips: brave, sneaky]]",
			want: reply{Text: "Is she brave, or is she sneaky?", Chips: []string{"brave", "sneaky"}},
		},
		{
			name: "filled and chips combine",
			raw:  "Next?\n[[filled: hero; chips: yes, no]]",
			want: reply{Text: "Next?", Slots: []Slot{SlotHero}, Chips: []string{"yes", "no"}},
		},
		{
			name: "bare end marks the goodbye",
			raw:  "What a story — bye!\n[[filled: hero; end]]",
			want: reply{Text: "What a story — bye!", Slots: []Slot{SlotHero}, End: true},
		},
		{
			name: "all six slots with end is the full close",
			raw:  "Let me make your book!\n[[filled: hero, companion, want, obstacle, turn, ending; end]]",
			want: reply{
				Text:  "Let me make your book!",
				Slots: []Slot{SlotHero, SlotCompanion, SlotWant, SlotObstacle, SlotTurn, SlotEnding},
				End:   true,
			},
		},
		{
			name: "bare end field without filled",
			raw:  "Bye!\n[[end]]",
			want: reply{Text: "Bye!", End: true},
		},
		{
			name: "empty filled reports nothing",
			raw:  "Who is your hero?\n[[filled:]]",
			want: reply{Text: "Who is your hero?"},
		},
		{
			name: "unknown labels are ignored",
			raw:  "Next?\n[[mood: happy; end]]",
			want: reply{Text: "Next?", End: true},
		},
		{
			name: "unknown slot names are ignored",
			raw:  "Next?\n[[filled: hero, sidekick]]",
			want: reply{Text: "Next?", Slots: []Slot{SlotHero}},
		},
		{
			name: "labels are case-insensitive and end may carry a value",
			raw:  "Bye!\n[[Filled: hero; END: yes]]",
			want: reply{Text: "Bye!", Slots: []Slot{SlotHero}, End: true},
		},
		{
			name: "a marker with prose after it is NOT a control line",
			raw:  "What is next?\n[[filled: hero]] (that was for the grown-ups)",
			want: reply{Text: "What is next?\n[[filled: hero]] (that was for the grown-ups)"},
		},
		{
			name: "an unterminated marker is plain text",
			raw:  "What is next?\n[[filled: hero",
			want: reply{Text: "What is next?\n[[filled: hero"},
		},
		{
			name: "trailing blank lines before the marker are stripped",
			raw:  "Who is your hero?\n\n\n[[filled: hero]]\n\n",
			want: reply{Text: "Who is your hero?", Slots: []Slot{SlotHero}},
		},
		{
			name: "multi-line text keeps its shape above the marker",
			raw:  "A dragon!\nWhat does it look like?\n[[filled: hero]]",
			want: reply{Text: "A dragon!\nWhat does it look like?", Slots: []Slot{SlotHero}},
		},
		{
			name: "chips are capped at maxChips",
			raw:  "Pick one!\n[[chips: a, b, c, d, e, f]]",
			want: reply{Text: "Pick one!", Chips: []string{"a", "b", "c", "d"}},
		},
		{
			name: "whitespace-only reply stays empty text",
			raw:  "   \n  ",
			want: reply{Text: ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseReply(tt.raw)
			if got.Text != tt.want.Text {
				t.Errorf("Text = %q, want %q", got.Text, tt.want.Text)
			}
			if got.End != tt.want.End {
				t.Errorf("End = %v, want %v", got.End, tt.want.End)
			}
			if fmt.Sprint(got.Slots) != fmt.Sprint(tt.want.Slots) {
				t.Errorf("Slots = %v, want %v", got.Slots, tt.want.Slots)
			}
			if fmt.Sprint(got.Chips) != fmt.Sprint(tt.want.Chips) {
				t.Errorf("Chips = %v, want %v", got.Chips, tt.want.Chips)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// lowEffort — the stall signal.
// ---------------------------------------------------------------------------

func TestLowEffort(t *testing.T) {
	tests := []struct {
		answer string
		want   bool
	}{
		{"", true},
		{"   ", true},
		{"???", true},
		{"i dunno", true},
		{"dunno", true},
		{"IDK!", true},
		{"I don't know", true},
		{"don't know", true},
		{"no", true},
		{"hmm...", true},
		{"Whatever.", true},
		{"blue", true},      // a one-word answer — PLAN.md §T4's repeated one-word signal
		{"Blue!", true},     // punctuation does not make it substantive
		{"a dragon", false}, // two words are an answer
		{"she is brave", false},
		{"Mira of the hills", false},
		{"the lost star", false},
	}
	for _, tt := range tests {
		if got := lowEffort(tt.answer); got != tt.want {
			t.Errorf("lowEffort(%q) = %v, want %v", tt.answer, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// stallAnswer — the genuine-stall half of the end rule. A short real
// answer ("blue") is low-effort (the chip signal) but never a stall;
// the end fires on two stalls in a row or a repeated one-word answer
// (see answer in http.go and TestStallEndRequiresNoProgress).
// ---------------------------------------------------------------------------

func TestStallAnswer(t *testing.T) {
	tests := []struct {
		answer string
		want   bool
	}{
		{"i dunno", true},
		{"IDK!", true},
		{"hmm...", true},
		{"no", true},
		{"Whatever.", true},
		{"", false},     // empty answers never reach the streak (ErrEmptyAnswer)
		{"Mira", false}, // a substantive one-word answer is not a stall
		{"red", false},  // …the demo's happy path
		{"a dragon", false},
	}
	for _, tt := range tests {
		if got := stallAnswer(tt.answer); got != tt.want {
			t.Errorf("stallAnswer(%q) = %v, want %v", tt.answer, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Chip matching — a tapped chip is a real answer.
// ---------------------------------------------------------------------------

func TestMatchesChip(t *testing.T) {
	chips := []string{"brave", "sneaky"}
	tests := []struct {
		answer string
		want   bool
	}{
		{"brave", true},
		{"Brave!", true},
		{"  sneaky  ", true},
		{"SNEAKY.", true},
		{"bravest", false},
		{"", false},
		{"i dunno", false},
	}
	for _, tt := range tests {
		if got := matchesChip(chips, tt.answer); got != tt.want {
			t.Errorf("matchesChip(%q) = %v, want %v", tt.answer, got, tt.want)
		}
	}
	// Empty chip lists match nothing — a question without chips can
	// never have one tapped.
	if matchesChip(nil, "brave") {
		t.Error("matchesChip(nil, brave) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// The server-side checklist.
// ---------------------------------------------------------------------------

func TestChecklistUnionFullFilled(t *testing.T) {
	c := checklist{}
	if c.full() {
		t.Fatal("empty checklist is full, want false")
	}
	if got := c.filled(); len(got) != 0 {
		t.Fatalf("filled = %v, want empty", got)
	}
	// Duplicates and unknown names are idempotent noise.
	c.union([]Slot{SlotHero, SlotHero})
	if c.full() {
		t.Fatal("one slot is full, want false")
	}
	c.union([]Slot{SlotCompanion, SlotWant, SlotObstacle, SlotTurn, SlotEnding, SlotHero})
	if !c.full() {
		t.Fatalf("all six reported, want full: %v", c.filled())
	}
	// filled() reports in canonical spec order regardless of report order.
	got := c.filled()
	want := "[hero companion want obstacle turn ending]"
	if fmt.Sprint(got) != want {
		t.Fatalf("filled = %v, want %s", got, want)
	}
	// Zero-value read on an unknown slot is false, never a panic.
	c2 := checklist{}
	if c2[SlotHero] {
		t.Fatal("unknown slot reads true, want false")
	}
}

// ---------------------------------------------------------------------------
// assistantText — degenerate replies classify transient.
// ---------------------------------------------------------------------------

func TestAssistantTextWrapsTransientForEmptyReplies(t *testing.T) {
	resp := &text.ChatResponse{Choices: []text.Choice{{Message: text.AssistantMessage{}}}}
	_, err := assistantText(resp)
	if !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want it to wrap gmi.ErrTransient", err)
	}
	if !strings.Contains(err.Error(), "interview") {
		t.Fatalf("err = %v, want the interview context in the message", err)
	}
}
