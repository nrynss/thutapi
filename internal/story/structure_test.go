package story

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/text"
)

// transcript is a finished two-exchange Phase-A transcript.
var transcript = []Turn{
	{Role: "interviewer", Text: "Who is your story about?"},
	{Role: "child", Text: "a girl called Mira and a dragon!"},
}

// goodReply is the model's well-formed answer, exactly the §T5 JSON.
const goodReply = `{"title":"Mira and the Rain Dragon","cast":[{"name":"Mira","visual":"a small girl, red raincoat, black bob haircut, round glasses","voice":{"pitch":0,"sound_effects":""}},{"name":"Puff","visual":"a small green dragon, pale belly, soot-speckled wings","voice":{"pitch":-8,"sound_effects":"spacious_echo"}}],"pages":[` +
	`{"n":1,"text":"Mira heard a sniffle under the porch.","prompt":"Mira in her red raincoat peering under a porch in the rain","characters":["Mira"],"emotion":"surprised","lines":[{"character":"Mira","text":"Who is there?"}]},` +
	`{"n":2,"text":"A small dragon blinked back at her.","prompt":"Puff the green dragon under the porch, shy","characters":["Mira","Puff"],"emotion":"happy","lines":[{"character":"Puff","text":"Just me."}]},` +
	`{"n":3,"text":"Puff sneezed a tiny puff of smoke.","prompt":"Puff sneezing soot-speckled smoke in the rain, Mira laughing","characters":["Mira","Puff"],"emotion":"surprised","lines":[{"character":"Mira","text":"You sneezed!"}]},` +
	`{"n":4,"text":"Puff had blown far from home and could not find the hills.","prompt":"Puff looking at the grey rainy sky, wings drooping","characters":["Mira","Puff"],"emotion":"sad","lines":[{"character":"Puff","text":"I cannot find my hill."}]},` +
	`{"n":5,"text":"Mira spread her umbrella over them both.","prompt":"Mira and Puff under one red umbrella in the rain","characters":["Mira","Puff"],"emotion":"calm","lines":[{"character":"Mira","text":"Then we will look together."}]},` +
	`{"n":6,"text":"Thunder rolled all the way down the street.","prompt":"Mira and Puff under the umbrella, lightning across the dark sky","characters":["Mira","Puff"],"emotion":"fearful","lines":[{"character":"Puff","text":"The thunder is loud."}]},` +
	`{"n":7,"text":"The gutter was full of slimy leaves and old boots.","prompt":"Mira wrinkling her nose at a slimy leaf-clogged gutter","characters":["Mira","Puff"],"emotion":"disgusted","lines":[{"character":"Mira","text":"Yuck, what a mess!"}]},` +
	`{"n":8,"text":"Then the porch light glowed warm, and home was where they stood.","prompt":"Mira and Puff at a glowing front door in the evening","characters":["Mira","Puff"],"emotion":"happy","lines":[{"character":"Mira","text":"We made it."},{"character":"Puff","text":"My kind of hill."}]}]}`

// badReply wraps a broken book (an out-of-set emotion) in the prose
// and fence dressing models like to add — the malformed first answer.
var badReply = "Here is your book:\n```json\n" +
	strings.Replace(goodReply, `"emotion":"surprised"`, `"emotion":"curious"`, 1) +
	"\n```\nEnjoy!"

// scriptedChatter is the fake text client: each Chat call consumes
// the next scripted reply (the last one repeats) and records the
// request, so tests can assert both what Structure sent and how many
// calls a scenario took.
type scriptedChatter struct {
	mu       sync.Mutex
	replies  []string
	i        int
	requests []text.ChatRequest
	err      error // returned by every call when set
	empty    bool  // reply with a choiceless response when set
}

func (f *scriptedChatter) Chat(_ context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.empty {
		return &text.ChatResponse{}, nil
	}
	reply := f.replies[min(f.i, len(f.replies)-1)]
	f.i++
	return &text.ChatResponse{Choices: []text.Choice{{
		Message: text.AssistantMessage{TextBody: reply},
	}}}, nil
}

func (f *scriptedChatter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *scriptedChatter) request(i int) text.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i-1]
}

// isInvalidStory reports whether err is the story-retry trigger.
func isInvalidStory(err error) bool { return errors.Is(err, ErrInvalidStory) }

// TestStructureValidReply covers the happy path: one call, a decoded
// and validated story, the transcript rendered as the user turn, and
// the thinking flag carried in the request.
func TestStructureValidReply(t *testing.T) {
	fake := &scriptedChatter{replies: []string{goodReply}}
	s, err := Structure(context.Background(), fake, transcript)
	if err != nil {
		t.Fatalf("Structure() = %v, want nil", err)
	}
	if s.Title != "Mira and the Rain Dragon" {
		t.Errorf("Title = %q, want %q", s.Title, "Mira and the Rain Dragon")
	}
	if len(s.Cast) != 2 || s.Cast[1].Voice.Pitch != -8 || s.Cast[1].Voice.SoundEffects != "spacious_echo" {
		t.Errorf("Cast = %+v, want two members with Puff at pitch -8 and echo", s.Cast)
	}
	if len(s.Pages) != PageCount || s.Pages[0].Emotion != "surprised" || s.Pages[1].Lines[0].Character != "Puff" {
		t.Errorf("Pages = %+v, want %d pages with the right emotion and line speaker", s.Pages, PageCount)
	}
	if fake.count() != 1 {
		t.Errorf("calls = %d, want 1 (a valid reply must not retry)", fake.count())
	}
	req := fake.request(1)
	if req.Model != ModelID {
		t.Errorf("model = %q, want %q", req.Model, ModelID)
	}
	if req.Thinking == nil || req.Thinking.Type != "enabled" {
		t.Errorf("thinking = %+v, want enabled", req.Thinking)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d, want system + transcript", len(req.Messages))
	}
	if got := req.Messages[0].Role; got != "system" {
		t.Errorf("message 0 role = %q, want system", got)
	}
	user, ok := req.Messages[1].Content.(string)
	if !ok {
		t.Fatalf("user content = %T, want string", req.Messages[1].Content)
	}
	for _, want := range []string{"interviewer: Who is your story about?", "child: a girl called Mira and a dragon!"} {
		if !strings.Contains(user, want) {
			t.Errorf("user turn %q is missing %q", user, want)
		}
	}
}

// TestStructureDeterministicTwiceRunning is the Done-when pin: the
// same transcript against the same scripted answer yields a valid
// story twice running, deep-equal both times.
func TestStructureDeterministicTwiceRunning(t *testing.T) {
	fake := &scriptedChatter{replies: []string{goodReply}}
	ctx := context.Background()
	first, err := Structure(ctx, fake, transcript)
	if err != nil {
		t.Fatalf("first Structure() = %v, want nil", err)
	}
	second, err := Structure(ctx, fake, transcript)
	if err != nil {
		t.Fatalf("second Structure() = %v, want nil", err)
	}
	if !storiesEqual(first, second) {
		t.Errorf("stories differ:\n%+v\n%+v", first, second)
	}
	if fake.count() != 2 {
		t.Errorf("calls = %d, want 2", fake.count())
	}
}

func storiesEqual(a, b Story) bool {
	if a.Title != b.Title || len(a.Cast) != len(b.Cast) || len(a.Pages) != len(b.Pages) {
		return false
	}
	for i := range a.Cast {
		if a.Cast[i] != b.Cast[i] {
			return false
		}
	}
	for i := range a.Pages {
		if a.Pages[i].N != b.Pages[i].N || a.Pages[i].Text != b.Pages[i].Text ||
			a.Pages[i].Prompt != b.Pages[i].Prompt || a.Pages[i].Emotion != b.Pages[i].Emotion {
			return false
		}
		if strings.Join(a.Pages[i].Characters, ",") != strings.Join(b.Pages[i].Characters, ",") {
			return false
		}
		for j := range a.Pages[i].Lines {
			if a.Pages[i].Lines[j] != b.Pages[i].Lines[j] {
				return false
			}
		}
	}
	return true
}

// TestStructureRetriesOnceWithCorrection is the retry contract: a
// malformed first answer is retried exactly once, with the validation
// failure appended as a corrective system message after the original
// system and user turns.
func TestStructureRetriesOnceWithCorrection(t *testing.T) {
	fake := &scriptedChatter{replies: []string{badReply, goodReply}}
	s, err := Structure(context.Background(), fake, transcript)
	if err != nil {
		t.Fatalf("Structure(malformed then valid) = %v, want nil", err)
	}
	if s.Title != "Mira and the Rain Dragon" {
		t.Errorf("Title = %q, want the retried story", s.Title)
	}
	if fake.count() != 2 {
		t.Fatalf("calls = %d, want exactly one retry", fake.count())
	}
	if got := len(fake.request(1).Messages); got != 2 {
		t.Errorf("first call messages = %d, want 2", got)
	}
	msgs := fake.request(2).Messages
	if len(msgs) != 3 {
		t.Fatalf("retry messages = %d, want original two plus correction", len(msgs))
	}
	correction, ok := msgs[2].Content.(string)
	if !ok {
		t.Fatalf("correction content = %T, want string", msgs[2].Content)
	}
	if msgs[2].Role != "system" {
		t.Errorf("correction role = %q, want system", msgs[2].Role)
	}
	for _, want := range []string{"not a valid story", "curious", ErrInvalidStory.Error()} {
		if !strings.Contains(correction, want) {
			t.Errorf("correction %q is missing %q", correction, want)
		}
	}
}

// TestStructureTwiceMalformedFailsLoudly: a second invalid reply must
// surface as ErrInvalidStory after exactly one corrective retry —
// never a half-valid book.
func TestStructureTwiceMalformedFailsLoudly(t *testing.T) {
	fake := &scriptedChatter{replies: []string{badReply}}
	_, err := Structure(context.Background(), fake, transcript)
	if !isInvalidStory(err) {
		t.Fatalf("Structure(twice malformed) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if fake.count() != 2 {
		t.Errorf("calls = %d, want 2 (one corrective retry, no more)", fake.count())
	}
	if !strings.Contains(err.Error(), "curious") {
		t.Errorf("error %q should name the second failure's reason", err)
	}
}

// TestStructureReplyWithoutJSON: a reply with no JSON object at all
// is a malformed structure and takes the same retry path.
func TestStructureReplyWithoutJSON(t *testing.T) {
	fake := &scriptedChatter{replies: []string{"I am sorry, I cannot help with that."}}
	_, err := Structure(context.Background(), fake, transcript)
	if !isInvalidStory(err) {
		t.Fatalf("Structure(no JSON) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if fake.count() != 2 {
		t.Errorf("calls = %d, want 2", fake.count())
	}
	if !strings.Contains(err.Error(), "no JSON object") {
		t.Errorf("error %q should name the missing JSON", err)
	}
}

// TestStructureUndecodableJSON: a well-formed JSON object that is not
// a story (a number where the title belongs) fails the decode branch.
func TestStructureUndecodableJSON(t *testing.T) {
	fake := &scriptedChatter{replies: []string{`{"title": 42, "cast": [], "pages": []}`}}
	_, err := Structure(context.Background(), fake, transcript)
	if !isInvalidStory(err) {
		t.Fatalf("Structure(non-story JSON) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if !strings.Contains(err.Error(), "did not decode") {
		t.Errorf("error %q should name the decode failure", err)
	}
}

// TestStructureDegenerateReplies covers the reply-extraction error
// branches: no choices, a nil response, and a textless message — each
// invalid, each retried once, each loud on the second miss.
func TestStructureDegenerateReplies(t *testing.T) {
	tests := []struct {
		name   string
		fake   *scriptedChatter
		wantIn string
	}{
		{
			name:   "choiceless response",
			fake:   &scriptedChatter{empty: true},
			wantIn: "no choices",
		},
		{
			name:   "textless choice",
			fake:   &scriptedChatter{replies: []string{""}},
			wantIn: "no text",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Structure(context.Background(), tt.fake, transcript)
			if !isInvalidStory(err) {
				t.Fatalf("Structure() = %v, want errors.Is(ErrInvalidStory)", err)
			}
			if tt.fake.count() != 2 {
				t.Errorf("calls = %d, want 2", tt.fake.count())
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q is missing %q", err, tt.wantIn)
			}
		})
	}
}

// TestStructureRunawayReplyExcerpted: a long JSON-free reply still
// fails as invalid, and the echoed reason is bounded, not the whole
// runaway text.
func TestStructureRunawayReplyExcerpted(t *testing.T) {
	runaway := strings.Repeat("no json here ", 100) // 1300 bytes
	fake := &scriptedChatter{replies: []string{runaway}}
	_, err := Structure(context.Background(), fake, transcript)
	if !isInvalidStory(err) {
		t.Fatalf("Structure(runaway) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if strings.Contains(err.Error(), runaway) {
		t.Error("error echoes the whole runaway reply; want it excerpted")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error %q should mark the truncation", err)
	}
}

// TestStructureExcerptUTF8Boundary: the excerpt cut lands mid-rune
// when the reply has a multibyte character across the 512-byte
// boundary; the echoed reason must stay valid UTF-8.
func TestStructureExcerptUTF8Boundary(t *testing.T) {
	split := strings.Repeat("a", 511) + "é" + strings.Repeat("b", 100)
	fake := &scriptedChatter{replies: []string{split}}
	_, err := Structure(context.Background(), fake, transcript)
	if !isInvalidStory(err) {
		t.Fatalf("Structure(split rune) = %v, want errors.Is(ErrInvalidStory)", err)
	}
	if !utf8.ValidString(err.Error()) {
		t.Errorf("error %q is not valid UTF-8; the excerpt split a multibyte rune", err)
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Errorf("error %q should mark the truncation", err)
	}
}

// TestStructureTransportErrorNotRetried: transport failures are the
// text client's contract — they surface untouched, without a story
// retry, and never as ErrInvalidStory. The Story that travels with
// the error is the zero value: a caller branching on the sentinel
// must never see a book alongside it.
func TestStructureTransportErrorNotRetried(t *testing.T) {
	fake := &scriptedChatter{err: gmi.ErrRateLimited}
	s, err := Structure(context.Background(), fake, transcript)
	if !errors.Is(err, gmi.ErrRateLimited) {
		t.Fatalf("Structure(transport failure) = %v, want errors.Is(gmi.ErrRateLimited): transport errors must surface untouched", err)
	}
	if s.Title != "" || len(s.Cast) != 0 || len(s.Pages) != 0 {
		t.Errorf("Structure(transport failure) returned %+v alongside the error, want the zero Story", s)
	}
	if isInvalidStory(err) {
		t.Errorf("transport error classified ErrInvalidStory: %v", err)
	}
	if fake.count() != 1 {
		t.Errorf("calls = %d, want 1 (the retry is the text client's, not story's)", fake.count())
	}
}

// TestStructureTransportSentinelsPreserved is the round-1 M1 probe,
// permanent: every text-client transport sentinel survives Structure
// intact through the %w wrap — T6 and T8 branch on this seam with
// errors.Is, so no sentinel may be reclassified or dropped.
func TestStructureTransportSentinelsPreserved(t *testing.T) {
	for _, sentinel := range []error{
		gmi.ErrBadRequest,
		gmi.ErrPaymentRequired,
		gmi.ErrTransient,
		gmi.ErrUnauthorized,
		gmi.ErrRateLimited,
		gmi.ErrModelNotFound,
	} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			fake := &scriptedChatter{err: sentinel}
			_, err := Structure(context.Background(), fake, transcript)
			if !errors.Is(err, sentinel) {
				t.Errorf("Structure(%v) = %v, want the sentinel preserved untouched", sentinel, err)
			}
			if isInvalidStory(err) {
				t.Errorf("Structure(%v) = %v, want transport errors never as ErrInvalidStory", sentinel, err)
			}
		})
	}
}

// TestStructureEmptyTranscript: nothing to structure is its own
// sentinel, raised before any model call.
func TestStructureEmptyTranscript(t *testing.T) {
	fake := &scriptedChatter{replies: []string{goodReply}}
	_, err := Structure(context.Background(), fake, nil)
	if !errors.Is(err, ErrEmptyTranscript) {
		t.Fatalf("Structure(nil) = %v, want errors.Is(ErrEmptyTranscript)", err)
	}
	if fake.count() != 0 {
		t.Errorf("calls = %d, want 0", fake.count())
	}
}

// decoyBook is the round-1 L1 tail-tier decoy: a complete,
// schema-shaped story — title, cast, real pages — that fails only
// Validate's page-count rule. In the round-1 tree (no count rule) a
// book exactly like this validated and was taken silently from the
// prose ahead of the real one.
func decoyBook(t *testing.T) string {
	t.Helper()
	decoy := validStory()
	decoy.Title = "The Decoy Book"
	decoy.Pages = decoy.Pages[:2]
	b, err := json.Marshal(decoy)
	if err != nil {
		t.Fatalf("marshal decoy: %v", err)
	}
	return string(b)
}

// TestStructureFullStoryDecoyNotTaken is the round-1 L1 tail-tier
// pin: a complete story-shaped decoy sitting in the prose BEFORE the
// real book is skipped by selection — the real book wins, in one
// call, with no retry burned on the decoy's failure.
func TestStructureFullStoryDecoyNotTaken(t *testing.T) {
	reply := "You asked for a book like " + decoyBook(t) + " - here it is: " + goodReply
	fake := &scriptedChatter{replies: []string{reply}}
	s, err := Structure(context.Background(), fake, transcript)
	if err != nil {
		t.Fatalf("Structure(decoy then real book) = %v, want the real book without a retry", err)
	}
	if s.Title != "Mira and the Rain Dragon" {
		t.Errorf("Title = %q, want the real book, not the decoy", s.Title)
	}
	if fake.count() != 1 {
		t.Errorf("calls = %d, want 1 (selection must not burn the corrective retry)", fake.count())
	}
}

// TestStructureProseDecoyDoesNotBurnRetry is the round-1 L1
// common-tier pin: a small prose-embedded decoy before the real book
// ("You asked for a book like {"title":"x"} — here it is: …") must
// not win extraction nor burn the corrective retry on its misleading
// reason — the real book validates on the first call.
func TestStructureProseDecoyDoesNotBurnRetry(t *testing.T) {
	reply := `You asked for a book like {"title":"x"} - here it is: ` + goodReply
	fake := &scriptedChatter{replies: []string{reply}}
	s, err := Structure(context.Background(), fake, transcript)
	if err != nil {
		t.Fatalf("Structure(prose decoy then real book) = %v, want nil", err)
	}
	if s.Title != "Mira and the Rain Dragon" {
		t.Errorf("Title = %q, want the real book, not the prose decoy", s.Title)
	}
	if fake.count() != 1 {
		t.Errorf("calls = %d, want 1 (the real book validates on the first call)", fake.count())
	}
}
