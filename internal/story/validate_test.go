package story

import (
	"fmt"
	"strings"
	"testing"
)

// validStory is a minimal eight-page book — the PageCount the prompt
// promises and Validate enforces — that passes every rule; each table
// case mutates a copy to break exactly one rule.
func validStory() Story {
	return Story{
		Title: "Mira and the Rain Dragon",
		Cast: []CastMember{
			{
				Name:   "Mira",
				Visual: "a small girl, red raincoat, black bob haircut, round glasses",
				Voice:  Voice{Pitch: 0},
			},
			{
				Name:   "Puff",
				Visual: "a small green dragon, pale belly, soot-speckled wings",
				Voice:  Voice{Pitch: -8, SoundEffects: "spacious_echo"},
			},
		},
		Pages: []Page{
			{
				N:          1,
				Text:       "Mira heard a sniffle under the porch.",
				Prompt:     "Mira in her red raincoat peering under a porch in the rain",
				Characters: []string{"Mira"},
				Emotion:    "surprised",
				Lines:      []Line{{Character: "Mira", Text: "Who is there?"}},
			},
			{
				N:          2,
				Text:       "A small dragon blinked back at her.",
				Prompt:     "Puff the green dragon under the porch, shy",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "happy",
				Lines:      []Line{{Character: "Puff", Text: "Just me."}},
			},
			{
				N:          3,
				Text:       "Puff sneezed a tiny puff of smoke.",
				Prompt:     "Puff sneezing soot-speckled smoke in the rain, Mira laughing",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "surprised",
				Lines:      []Line{{Character: "Mira", Text: "You sneezed!"}},
			},
			{
				N:          4,
				Text:       "Puff had blown far from home and could not find the hills.",
				Prompt:     "Puff looking at the grey rainy sky, wings drooping",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "sad",
				Lines:      []Line{{Character: "Puff", Text: "I cannot find my hill."}},
			},
			{
				N:          5,
				Text:       "Mira spread her umbrella over them both.",
				Prompt:     "Mira and Puff under one red umbrella in the rain",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "neutral",
				Lines:      []Line{{Character: "Mira", Text: "Then we will look together."}},
			},
			{
				N:          6,
				Text:       "Thunder rolled all the way down the street.",
				Prompt:     "Mira and Puff under the umbrella, lightning across the dark sky",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "fearful",
				Lines:      []Line{{Character: "Puff", Text: "The thunder is loud."}},
			},
			{
				N:          7,
				Text:       "The gutter was full of slimy leaves and old boots.",
				Prompt:     "Mira wrinkling her nose at a slimy leaf-clogged gutter",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "disgusted",
				Lines:      []Line{{Character: "Mira", Text: "Yuck, what a mess!"}},
			},
			{
				N:          8,
				Text:       "Then the porch light glowed warm, and home was where they stood.",
				Prompt:     "Mira and Puff at a glowing front door in the evening",
				Characters: []string{"Mira", "Puff"},
				Emotion:    "happy",
				Lines:      []Line{{Character: "Mira", Text: "We made it."}, {Character: "Puff", Text: "My kind of hill."}},
			},
		},
	}
}

// TestValidateRules is the validation table: one row per rule and one
// per failure reason, each asserting the ErrInvalidStory sentinel and
// the precise wrapped reason.
func TestValidateRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Story)
		wantIn string // substring of the wrapped reason; "" means valid
	}{
		{name: "valid"},
		{name: "empty title", mutate: func(s *Story) { s.Title = "" }, wantIn: "title is empty"},
		{name: "whitespace title", mutate: func(s *Story) { s.Title = "   " }, wantIn: "title is empty"},
		{name: "empty cast", mutate: func(s *Story) { s.Cast = nil }, wantIn: "cast is empty"},
		{
			name:   "cast member empty name",
			mutate: func(s *Story) { s.Cast[1].Name = " " },
			wantIn: "cast member 1 has an empty name",
		},
		{
			name:   "duplicate cast name",
			mutate: func(s *Story) { s.Cast[1].Name = "Mira" },
			wantIn: `cast name "Mira" appears twice`,
		},
		{
			name:   "cast names differing only by case",
			mutate: func(s *Story) { s.Cast[1].Name = "mira" },
			wantIn: `cast name "mira" appears twice, differing from "Mira" only by case`,
		},
		{
			name:   "empty visual",
			mutate: func(s *Story) { s.Cast[0].Visual = "" },
			wantIn: `cast member "Mira" has an empty visual`,
		},
		{name: "no pages", mutate: func(s *Story) { s.Pages = nil }, wantIn: "story has 0 pages, want exactly 8"},
		{
			name:   "too few pages",
			mutate: func(s *Story) { s.Pages = s.Pages[:7] },
			wantIn: "story has 7 pages, want exactly 8",
		},
		{
			name:   "too many pages",
			mutate: func(s *Story) { s.Pages = append(s.Pages, Page{N: 9, Text: "x", Prompt: "y", Emotion: "happy"}) },
			wantIn: "story has 9 pages, want exactly 8",
		},
		{
			name:   "pages start at two",
			mutate: func(s *Story) { s.Pages[0].N = 2 },
			wantIn: "no gaps or duplicates; got [2 2 3 4 5 6 7 8]",
		},
		{
			name:   "page gap",
			mutate: func(s *Story) { s.Pages[1].N = 3 },
			wantIn: "no gaps or duplicates; got [1 3 3 4 5 6 7 8]",
		},
		{
			name:   "duplicate page number",
			mutate: func(s *Story) { s.Pages[1].N = 1 },
			wantIn: "no gaps or duplicates; got [1 1 3 4 5 6 7 8]",
		},
		{
			name:   "pages out of order",
			mutate: func(s *Story) { s.Pages[0].N, s.Pages[1].N = 2, 1 },
			wantIn: "no gaps or duplicates; got [2 1 3 4 5 6 7 8]",
		},
		{
			name:   "zero page number",
			mutate: func(s *Story) { s.Pages[0].N = 0 },
			wantIn: "no gaps or duplicates; got [0 2 3 4 5 6 7 8]",
		},
		{
			name:   "empty page text",
			mutate: func(s *Story) { s.Pages[0].Text = "" },
			wantIn: "page 1 has empty text",
		},
		{
			name:   "empty page prompt",
			mutate: func(s *Story) { s.Pages[1].Prompt = "" },
			wantIn: "page 2 has empty prompt",
		},
		{
			name:   "unknown emotion",
			mutate: func(s *Story) { s.Pages[0].Emotion = "curious" },
			wantIn: `page 1 emotion "curious" is not one of`,
		},
		{
			name:   "emotion wrong case",
			mutate: func(s *Story) { s.Pages[1].Emotion = "Happy" },
			wantIn: `page 2 emotion "Happy" is not one of`,
		},
		{
			name:   "page character not in cast",
			mutate: func(s *Story) { s.Pages[1].Characters = []string{"Mira", "Sparky"} },
			wantIn: `page 2 names character "Sparky", who is not in the cast`,
		},
		{
			name:   "empty page character entry",
			mutate: func(s *Story) { s.Pages[0].Characters = []string{""} },
			wantIn: `page 1 names character "", who is not in the cast`,
		},
		{
			name:   "line speaker not in cast",
			mutate: func(s *Story) { s.Pages[0].Lines = []Line{{Character: "Dragon", Text: "Hello"}} },
			wantIn: `page 1 line speaker "Dragon" is not in the cast`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validStory()
			if tt.mutate != nil {
				tt.mutate(&s)
			}
			err := Validate(s)
			if tt.wantIn == "" {
				if err != nil {
					t.Fatalf("Validate(valid) = %v, want nil", err)
				}
				return
			}
			if !isInvalidStory(err) {
				t.Fatalf("Validate() = %v, want errors.Is(ErrInvalidStory)", err)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.wantIn)
			}
		})
	}
}

// TestValidateWrapsSentinel pins the error contract the corrective
// retry and every caller branch on: every Validate failure is
// errors.Is(ErrInvalidStory).
func TestValidateWrapsSentinel(t *testing.T) {
	s := validStory()
	s.Pages[0].Emotion = "curious"
	err := Validate(s)
	if !isInvalidStory(err) {
		t.Errorf("Validate(bad) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if isInvalidStory(Validate(validStory())) {
		t.Error("Validate(valid) matched ErrInvalidStory, want clean nil")
	}
}

// TestValidatePageCountContract is the M3 pin: the system prompt
// teaches the page count from the same constant Validate enforces,
// and any other count fails loudly naming the number — so the
// prompt's promise and the validator's rule cannot drift apart.
func TestValidatePageCountContract(t *testing.T) {
	if !strings.Contains(systemPrompt, fmt.Sprintf("Exactly %d pages", PageCount)) {
		t.Errorf("system prompt does not teach exactly %d pages; the count contract has drifted", PageCount)
	}
	for _, n := range []int{0, 2, 6, 7, 9} {
		s := validStory()
		for len(s.Pages) > n {
			s.Pages = s.Pages[:len(s.Pages)-1]
		}
		for len(s.Pages) < n {
			s.Pages = append(s.Pages, Page{N: len(s.Pages) + 1, Text: "more", Prompt: "more", Emotion: "happy"})
		}
		err := Validate(s)
		if !isInvalidStory(err) {
			t.Errorf("Validate(%d pages) = %v, want errors.Is(ErrInvalidStory)", n, err)
			continue
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("want exactly %d", PageCount)) {
			t.Errorf("Validate(%d pages) = %q, want the reason to name the required count", n, err)
		}
	}
	if err := Validate(validStory()); err != nil {
		t.Errorf("Validate(%d pages) = %v, want nil", PageCount, err)
	}
}

// TestValidateCastUniquenessCaseFolded is the L2 pin: the uniqueness
// key is case-insensitive — Mira and mira are one character with two
// spellings and are rejected — while page and line references stay
// exact-match, so a drifted-casing reference still fails loudly.
func TestValidateCastUniquenessCaseFolded(t *testing.T) {
	s := validStory()
	s.Cast[1].Name = "mira" // same character, cased differently
	err := Validate(s)
	if !isInvalidStory(err) {
		t.Fatalf("Validate(Mira + mira) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if !strings.Contains(err.Error(), `cast name "mira" appears twice`) {
		t.Errorf("error %q should name the case-folded duplicate", err)
	}

	s = validStory()
	s.Pages[0].Characters = []string{"mira"} // reference drifted, cast spelling unchanged
	err = Validate(s)
	if !isInvalidStory(err) {
		t.Fatalf("Validate(drifted reference) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if !strings.Contains(err.Error(), `names character "mira", who is not in the cast`) {
		t.Errorf("error %q should name the exact-match miss", err)
	}
}
