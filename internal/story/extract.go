package story

import "encoding/json"

// extractStory pulls the validated picture book out of a model reply
// with the leniency the structuring contract promises: prose and code
// fences are tolerated, because the model sometimes wraps its JSON in
// a sentence or a fence — and prose can carry JSON of its own. The
// scan therefore walks every brace-balanced candidate and the FIRST
// one that decodes as a Story and passes Validate wins; a well-formed
// but invalid decoy sitting in front of the real book is skipped
// instead of winning its slot or burning the corrective retry on its
// reason.
//
// When no candidate survives, the returned error wraps ErrInvalidStory
// and names the FIRST candidate's failure — the model's primary
// attempt — so the corrective retry still carries a precise reason. A
// reply with no well-formed object at all is its own loud failure.
func extractStory(s string) (Story, error) {
	var (
		story    Story
		firstErr error // the first candidate's decode or validation failure
	)
	accepted := scanObjects(s, func(raw json.RawMessage) bool {
		var cand Story
		if err := json.Unmarshal(raw, &cand); err != nil {
			if firstErr == nil {
				firstErr = invalidf("reply JSON did not decode as a story: %v", err)
			}
			return false
		}
		if err := Validate(cand); err != nil {
			if firstErr == nil {
				firstErr = err // already ErrInvalidStory-wrapped, with the precise rule
			}
			return false
		}
		story = cand
		return true
	})
	if !accepted {
		if firstErr == nil {
			return Story{}, invalidf("reply contained no JSON object: %q", excerpt(s))
		}
		return Story{}, firstErr
	}
	return story, nil
}

// scanObjects calls visit on each brace-balanced, well-formed JSON
// object embedded in s, in order of appearance, stopping at the first
// visit that returns true. Braces inside JSON strings do not confuse
// the scan (objectEnd tracks string escapes), and every brace-balanced
// region must pass json.Valid before it is visited, so a balanced
// non-JSON region like "{spelling}" is skipped rather than visited. It
// reports whether any visit returned true.
func scanObjects(s string, visit func(json.RawMessage) bool) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '{' {
			continue
		}
		end := objectEnd(s[i:])
		if end < 0 {
			continue
		}
		cand := json.RawMessage(s[i : i+end])
		if json.Valid(cand) && visit(cand) {
			return true
		}
	}
	return false
}

// objectEnd returns the length of the balanced object starting at
// s[0] — the index just past its closing brace — tracking JSON string
// escapes so braces and brackets inside strings do not count. It
// returns -1 when the object never closes.
func objectEnd(s string) int {
	depth := 0
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch c {
			case '\\':
				i++ // the escaped byte is payload; step over it
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}
