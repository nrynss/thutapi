package story

import (
	"encoding/json"
	"strings"
	"testing"
)

// scanAll visits every scanObjects candidate in s and returns their
// texts in order, never stopping early.
func scanAll(s string) []string {
	var got []string
	scanObjects(s, func(raw json.RawMessage) bool {
		got = append(got, string(raw))
		return false
	})
	return got
}

// TestScanObjects is the leniency corpus at the scan level: prose and
// code fences are ignored, braces inside strings and balanced non-JSON
// regions do not derail the scan, and a reply without a well-formed
// object visits nothing.
func TestScanObjects(t *testing.T) {
	obj := `{"title":"x","pages":[]}`
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "bare object", in: obj, want: []string{obj}},
		{
			name: "code fence",
			in:   "```json\n" + obj + "\n```",
			want: []string{obj},
		},
		{
			name: "prose around the object",
			in:   "Here is your book:\n" + obj + "\nEnjoy!",
			want: []string{obj},
		},
		{
			name: "nested braces and arrays",
			in:   "Sure. " + `{"cast":[{"name":"a } b"}],"pages":[{"lines":[]}]}` + " done",
			want: []string{
				`{"cast":[{"name":"a } b"}],"pages":[{"lines":[]}]}`,
				`{"name":"a } b"}`,
				`{"lines":[]}`,
			},
		},
		{
			name: "escaped quote inside a string",
			in:   `{"title":"she said \"} hi"}`,
			want: []string{`{"title":"she said \"} hi"}`},
		},
		{
			name: "balanced non-JSON region skipped",
			in:   "use {these braces} then " + obj,
			want: []string{obj},
		},
		{name: "no json at all", in: "I cannot do that."},
		{name: "unbalanced object", in: `{"title": "x"`},
		{name: "array is not an object", in: "[1, 2, 3]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanAll(tt.in)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Errorf("scanObjects(%q) visited %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// validStoryJSON marshals the canonical valid book, so the extraction
// fixtures and the validator fixture cannot drift apart.
func validStoryJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(validStory())
	if err != nil {
		t.Fatalf("marshal valid story: %v", err)
	}
	return string(b)
}

// TestExtractStoryLenientWrapping: prose and code fences around the
// real book extract to the same validated story as the bare object.
func TestExtractStoryLenientWrapping(t *testing.T) {
	book := validStoryJSON(t)
	for _, in := range []string{
		book,
		"```json\n" + book + "\n```",
		"Here is your book:\n" + book + "\nEnjoy!",
	} {
		s, err := extractStory(in)
		if err != nil {
			t.Fatalf("extractStory(%.20q…) = %v, want nil", in, err)
		}
		if s.Title != validStory().Title {
			t.Errorf("Title = %q, want the wrapped book", s.Title)
		}
	}
}

// TestExtractStoryFirstValidBookWins is the L1 pin at the unit level:
// a well-formed but invalid decoy in front of the real book is
// skipped — the first candidate that decodes AND validates wins, not
// the first well-formed object.
func TestExtractStoryFirstValidBookWins(t *testing.T) {
	in := `You asked for a book like {"title":"x"} - here it is: ` + validStoryJSON(t)
	s, err := extractStory(in)
	if err != nil {
		t.Fatalf("extractStory() = %v, want nil", err)
	}
	if s.Title != validStory().Title {
		t.Errorf("Title = %q, want the real book, not the prose decoy", s.Title)
	}
}

// TestExtractStoryFallbackNamesFirstCandidate: when no candidate
// decodes and validates, the error names the FIRST candidate's
// failure — not a later candidate's.
func TestExtractStoryFallbackNamesFirstCandidate(t *testing.T) {
	in := `a book like {"title":"x"} or maybe {"title": 42}`
	_, err := extractStory(in)
	if !isInvalidStory(err) {
		t.Fatalf("extractStory() = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if !strings.Contains(err.Error(), "cast is empty") {
		t.Errorf("error %q should name the first candidate's reason", err)
	}
	if strings.Contains(err.Error(), "did not decode") {
		t.Errorf("error %q names a later candidate; want the first candidate's reason", err)
	}
}

// TestExtractStoryDecodeFailureNamesDecode: a well-formed object that
// is not a story (a number where the title belongs) fails the decode
// branch loudly.
func TestExtractStoryDecodeFailureNamesDecode(t *testing.T) {
	_, err := extractStory(`{"title": 42, "cast": [], "pages": []}`)
	if !isInvalidStory(err) {
		t.Fatalf("extractStory() = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if !strings.Contains(err.Error(), "did not decode") {
		t.Errorf("error %q should name the decode failure", err)
	}
}

// TestExtractStoryNoObject: a reply with no well-formed JSON object at
// all is its own loud failure, named as such.
func TestExtractStoryNoObject(t *testing.T) {
	for _, in := range []string{"I cannot do that.", `{"title": "x"`, "[1, 2, 3]"} {
		_, err := extractStory(in)
		if !isInvalidStory(err) {
			t.Errorf("extractStory(%q) = %v, want errors.Is(ErrInvalidStory)", in, err)
		}
		if !strings.Contains(err.Error(), "no JSON object") {
			t.Errorf("extractStory(%q) = %v, want the missing-JSON reason", in, err)
		}
	}
}
