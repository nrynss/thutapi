package story

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"thutapi/internal/gmi/text"
)

// ErrEmptyTranscript reports a Structure call with no turns: there is
// nothing to structure. It is distinct from ErrInvalidStory — the
// input is empty before any model call, so no retry can help.
var ErrEmptyTranscript = errors.New("story: empty transcript")

// Chatter is story's view of the text client (PLAN.md invariant 3:
// consumers declare the interface). It is satisfied by *text.Client;
// tests substitute a scripted fake.
type Chatter interface {
	Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error)
}

// systemPrompt instructs the model to reply with exactly the Phase-B
// JSON object. The page count and the emotion vocabulary are
// interpolated from PageCount and Emotions so the prompt and the
// validator cannot drift apart.
var systemPrompt = fmt.Sprintf(`You are turning a child's interview into a picture book. Read the whole transcript below, then reply with ONLY one JSON object - no prose, no code fences - exactly this shape:

{"title": string, "cast": [{"name": string, "visual": string, "voice": {"pitch": number, "sound_effects": string}}], "pages": [{"n": number, "text": string, "prompt": string, "characters": [string], "emotion": string, "lines": [{"character": string, "text": string}]}]}

Rules:
- Exactly %[1]d pages numbered n = 1 to %[1]d in order, with a beginning, a turn and a warm ending.
- title: short and child-friendly.
- cast: one entry per distinct character. visual: a self-contained appearance description - hair, clothing, glasses, size - because it is the description every illustration must match; never refer to other cast members inside it.
- voice.pitch: an integer semitone offset from the child's recorded voice: 0 for a narrator, negative for a big deep character, positive for a small high one. voice.sound_effects: "" unless the character has a signature effect, e.g. "spacious_echo" or "robotic".
- page.text: the narration, in short sentences a parent reads aloud. Keep the child's own logic and wording; never correct it.
- page.prompt: the illustration prompt for the page. Describe the scene; name every character that appears.
- page.characters: the cast names appearing on the page. page.lines: what each of them says aloud; every "character" must be a cast name.
- page.emotion: exactly one of %s, lowercase.`, PageCount, strings.Join(Emotions, ", "))

// Structure turns a finished Phase-A interview transcript into a
// validated Story in a single Chat call over the whole transcript
// (PLAN.md §T5: M3's 1M context means no summarisation and no state
// to marshal), with thinking enabled on the wire.
//
// The model's reply is mined for the first candidate that decodes as
// a Story and passes Validate — leniently, tolerating prose and code
// fences, so a well-formed decoy in front of the real book cannot win
// its slot. A reply yielding no valid story fails with an error
// wrapping ErrInvalidStory, and the call is
// retried exactly once with that failure appended as a corrective
// system message; a second invalid reply fails loudly with
// ErrInvalidStory rather than returning a half-valid book. Transport
// failures are the text client's contract — it classifies and retries
// ErrTransient itself — and are returned untouched, without a story
// retry.
func Structure(ctx context.Context, chat Chatter, transcript []Turn) (Story, error) {
	if len(transcript) == 0 {
		return Story{}, ErrEmptyTranscript
	}
	messages := []text.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: renderTranscript(transcript)},
	}
	s, err := attempt(ctx, chat, messages)
	if err == nil || !errors.Is(err, ErrInvalidStory) {
		return s, err
	}
	messages = append(messages, text.Message{
		Role: "system",
		Content: fmt.Sprintf("Your previous reply was not a valid story: %v\n"+
			"Reply again with ONLY the corrected JSON object - same shape, no prose, no code fences.",
			err),
	})
	return attempt(ctx, chat, messages)
}

// attempt runs one Chat call with thinking enabled, then extracts and
// validates the story from the reply (extractStory). Every failure of
// the reply itself — no choices, no text, no JSON object, a decode
// failure, a validation failure — wraps ErrInvalidStory, so the
// corrective retry in Structure has exactly one trigger; transport
// errors pass through as the text client's sentinels.
func attempt(ctx context.Context, chat Chatter, messages []text.Message) (Story, error) {
	resp, err := chat.Chat(ctx, text.ChatRequest{
		Model:    ModelID,
		Messages: messages,
		// Reasoning is ON for the one Phase-B call (project.md §M3
		// phase settings: one call, quality matters, a progress bar is
		// already showing). Pinned on the raw wire by
		// TestStructureThinkingOnRawWire.
		Thinking: &text.Reasoning{Type: "enabled"},
	})
	if err != nil {
		return Story{}, fmt.Errorf("story: chat: %w", err)
	}
	reply, err := replyText(resp)
	if err != nil {
		return Story{}, err // already ErrInvalidStory-wrapped
	}
	return extractStory(reply)
}

// replyText pulls the assistant's flat text out of a ChatResponse. A
// nil response, an empty choice list or a textless message is a
// malformed structure — the model answered but produced nothing to
// structure — so it wraps ErrInvalidStory rather than a transport
// sentinel: the corrective retry is the right recovery, and the text
// client's retry contract stays about transport.
func replyText(resp *text.ChatResponse) (string, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return "", invalidf("reply carried no choices")
	}
	reply, err := resp.Choices[0].Message.Text()
	if err != nil {
		return "", invalidf("reply carried no text: %v", err)
	}
	return reply, nil
}

// renderTranscript formats the transcript as the user turn: one line
// per exchange, prefixed by the opaque role, so the model sees who
// said what. Role vocabulary is T4's; Phase B only reads it.
func renderTranscript(turns []Turn) string {
	var b strings.Builder
	b.WriteString("Here is the interview transcript. Turn it into the picture book JSON.\n\n")
	for _, t := range turns {
		fmt.Fprintf(&b, "%s: %s\n", t.Role, t.Text)
	}
	return b.String()
}

// excerpt bounds a reply echoed inside an error message, so a runaway
// reply cannot bloat the corrective message or the log. The cut backs
// off to a valid UTF-8 boundary.
func excerpt(s string) string {
	const maxExcerpt = 512
	if len(s) <= maxExcerpt {
		return s
	}
	cut := s[:maxExcerpt]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "...[truncated]"
}
