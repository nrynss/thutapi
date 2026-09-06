package interview

import (
	"fmt"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/text"
	"thutapi/internal/store"
)

// systemPrompt is the product: the house style for talking to a child
// (project.md §Pipeline Phase A, PLAN.md §T4) plus the control-line
// contract this package's loop parses (see the package doc). Every
// interview call opens with it.
const systemPrompt = `You are the story interviewer in Thutapi. A young child (about 5 to 8
years old) is typing you short answers so you can write a picture book
together. House rules, always:

- Ask exactly ONE question per turn. Never two, never a list.
- Make questions CONCRETE. Ask "What colour is the dragon?", never
  "describe the antagonist".
- Accept everything the child says. Never correct spelling, logic or
  plausibility — their ideas ARE the story.
- When the child stalls ("i dunno"), do not re-ask the same thing
  openly: offer exactly two concrete options to pick between.
- Short answers are answers. When a signal line tells you the child
  has gone short or stuck, your next question offers exactly two very
  short concrete options to tap. Never end the interview because of
  short answers alone.
- Keep every message short: a few sentences at most, easy words.

You are filling a story checklist with six slots: hero, companion,
want, obstacle, turn, ending. ("turn" is the moment the problem turns
around; "ending" is how it all wraps up.) Aim for roughly 6 to 10
questions total, then close.

EVERY reply must end with ONE control line as the very last line,
inside double square brackets, holding semicolon-separated fields:

  [[filled: <slots filled so far>; chips: <option, option>; end]]

- filled: the checklist slots you now consider filled, comma
  separated, ALL of them every turn (cumulative, not just the new
  ones). Leave it empty when none are yet: [[filled:]]
- chips: the two to four very short options you are offering the child
  to tap, when you are offering options. Leave it out otherwise.
- end: include the word end INSTEAD of asking anything when the
  checklist is full or the child is clearly tiring. Your reply is then
  one short, warm goodbye (no question), and the book making begins.

Example of a normal turn:

  Ooh, a dragon! What does your dragon look like?
  [[filled: hero, companion]]

Example of the final turn:

  What a great story — I have everything I need! Let's make your book!
  [[filled: hero, companion, want, obstacle, turn, ending; end]]

The control line is never shown to the child. Never put it anywhere
but the very last line.`

// stallDirective is appended to the message history when the child's
// answers have gone low-effort (session.streak). It is the chip
// signal: a short or stuck answer raises the streak, and the streak
// reaches the model with the very next call, so chips are on offer
// before the loop ever considers ending anything. It travels as a
// system message after the transcript, the same channel the ending
// directive uses.
func stallDirective(streak int) string {
	return fmt.Sprintf(`(Signal: the child's last %d answers in a row were one word, or stuck like "i dunno". Ask the next question with exactly two very short, concrete options as chips, and keep it playful. Short answers alone are never a reason to end. Do not mention the signal.)`, streak)
}

// endingDirective is appended to the message history when the loop —
// not the model — has decided the interview must end (stall rules or
// the MaxTurns backstop). It turns the goodbye into one more ordinary
// Chat call, so every child-facing word stays model-authored and the
// ending needs no special-casing anywhere downstream.
const endingDirective = `(The interview is ending now — the child seems to be tiring. Reply with
one short, warm goodbye to the child. Do NOT ask anything. Finish with
the control line exactly as always, reporting filled as before and
including end.)`

// openingDirective seeds the opening turn, which has no history yet.
// MiniMax rejects a system-only conversation with "invalid params,
// messages must not be empty (2013)" — a request carrying nothing but
// the system prompt counts as empty to it, so the interview could not
// start at all against production. It goes in as a user message
// because that is the role whose absence the upstream is objecting to.
// It adds no behaviour: systemPrompt already governs how to ask.
const openingDirective = `Please ask your first question.`

// chatRole maps a persisted turn to its chat-completions role: the
// child's answers go in as user messages, everything the interviewer
// said (questions and the closing goodbye) as assistant messages.
func chatRole(turnRole string) string {
	if turnRole == RoleChild {
		return "user"
	}
	return "assistant"
}

// buildMessages assembles one Chat call: the system prompt, the whole
// persisted transcript in order (M3's 1M window takes it whole — no
// summarisation, project.md §Pipeline), the ending directive when the
// loop has decided to close, and otherwise the stall directive when
// the child's low-effort streak has reached the model. The caller
// persists the child's new answer BEFORE calling, so history already
// ends with it.
func buildMessages(history []store.Turn, ending bool, streak int) []text.Message {
	msgs := make([]text.Message, 0, len(history)+3)
	msgs = append(msgs, text.Message{Role: "system", Content: systemPrompt})
	if len(history) == 0 {
		msgs = append(msgs, text.Message{Role: "user", Content: openingDirective})
	}
	for _, t := range history {
		msgs = append(msgs, text.Message{Role: chatRole(t.Role), Content: t.Text})
	}
	if ending {
		msgs = append(msgs, text.Message{Role: "system", Content: endingDirective})
		return msgs
	}
	if streak > 0 {
		msgs = append(msgs, text.Message{Role: "system", Content: stallDirective(streak)})
	}
	return msgs
}

// assistantText extracts the reply text from a Chat response. An empty
// choice list or a reply with no text at all is a degenerate success —
// classified transient (the same family as an undecodable body: the
// call produced no usable answer, and the recovery is running the turn
// again) so the error's retry contract stays honest.
func assistantText(resp *text.ChatResponse) (string, error) {
	if resp == nil || len(resp.Choices) == 0 {
		return "", fmt.Errorf("interview: %w: reply carried no choices", gmi.ErrTransient)
	}
	txt, err := resp.Choices[0].Message.Text()
	if err != nil {
		return "", fmt.Errorf("interview: %w: %w", gmi.ErrTransient, err)
	}
	return txt, nil
}
