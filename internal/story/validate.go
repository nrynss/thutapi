package story

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidStory reports a model reply (or a caller-supplied Story)
// that failed the Phase-B rules. Match it with errors.Is; the wrapped
// text names the first rule broken. It is the single trigger for the
// corrective retry in Structure.
var ErrInvalidStory = errors.New("story: invalid story")

// Validate checks s against the Phase-B rules: a non-empty title; a
// non-empty cast whose names are unique — two names differing only by
// case are one character with two spellings and rejected — and whose
// visual is non-empty each; exactly PageCount (8) pages numbered
// ascending from 1 with no gaps or duplicates (the two-day cut to 6
// pages would change exactly this rule); each page carrying non-empty
// text and prompt, an emotion from Emotions, and character references
// — page characters and line speakers alike — that all name cast
// members exactly as spelled in the cast.
//
// The first violation is returned wrapped around ErrInvalidStory with
// a precise reason; a nil error means the book is safe to hand to
// T6/T8/T10. Strings are "non-empty" in the trimmed sense: a value of
// only whitespace is empty.
func Validate(s Story) error {
	if strings.TrimSpace(s.Title) == "" {
		return invalidf("title is empty")
	}
	if len(s.Cast) == 0 {
		return invalidf("cast is empty")
	}
	cast := make(map[string]bool, len(s.Cast)) // exact spellings, for the page/line reference lookups
	var names []string                         // cast order, for the case-folded uniqueness scan
	for i, m := range s.Cast {
		if strings.TrimSpace(m.Name) == "" {
			return invalidf("cast member %d has an empty name", i)
		}
		for _, seen := range names {
			if strings.EqualFold(seen, m.Name) {
				if seen == m.Name {
					return invalidf("cast name %q appears twice", m.Name)
				}
				return invalidf("cast name %q appears twice, differing from %q only by case", m.Name, seen)
			}
		}
		names = append(names, m.Name)
		cast[m.Name] = true
		if strings.TrimSpace(m.Visual) == "" {
			return invalidf("cast member %q has an empty visual", m.Name)
		}
	}
	if len(s.Pages) != PageCount {
		return invalidf("story has %d pages, want exactly %d", len(s.Pages), PageCount)
	}
	ns := make([]int, len(s.Pages))
	for i, p := range s.Pages {
		ns[i] = p.N
	}
	// Ascending from 1 in slice order: this one comparison catches
	// gaps, duplicates, zero/negative numbers and mis-ordered pages.
	for i, p := range s.Pages {
		if p.N != i+1 {
			return invalidf("page numbers must run 1, 2, 3, ... with no gaps or duplicates; got %v", ns)
		}
	}
	for _, p := range s.Pages {
		if strings.TrimSpace(p.Text) == "" {
			return invalidf("page %d has empty text", p.N)
		}
		if strings.TrimSpace(p.Prompt) == "" {
			return invalidf("page %d has empty prompt", p.N)
		}
		if !isEmotion(p.Emotion) {
			return invalidf("page %d emotion %q is not one of %s", p.N, p.Emotion, strings.Join(Emotions, ", "))
		}
		for _, c := range p.Characters {
			if !cast[c] {
				return invalidf("page %d names character %q, who is not in the cast", p.N, c)
			}
		}
		for _, l := range p.Lines {
			if !cast[l.Character] {
				return invalidf("page %d line speaker %q is not in the cast", p.N, l.Character)
			}
		}
	}
	return nil
}

// isEmotion reports whether e is in Emotions.
func isEmotion(e string) bool {
	for _, want := range Emotions {
		if e == want {
			return true
		}
	}
	return false
}

// invalidf builds the error every validation and reply failure wraps:
// ErrInvalidStory plus the precise reason.
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidStory, fmt.Sprintf(format, args...))
}
