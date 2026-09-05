//go:build live

// Live probes for T5b -- the half of T5's original Done when that needs
// a real M3 call. Excluded from every normal build and from CI by the
// `live` tag. Run with:
//
//	set -a; . ./.env; set +a; go test -tags live -run Live -v ./internal/story/
//
// Free: M3 is free during the campaign window. Phase B runs with
// thinking ON (project.md §M3 phase settings), which is what these
// exercise against production.
package story

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"thutapi/internal/gmi/text"
)

// realTranscript is a plausible Phase-A interview: short child answers,
// one question at a time, the checklist filled -- hero, companion,
// want, obstacle, turn, ending.
var realTranscript = []Turn{
	{Role: "interviewer", Text: "Hello! Who is the hero of your story?"},
	{Role: "child", Text: "a girl called Mira"},
	{Role: "interviewer", Text: "Lovely. What does Mira look like?"},
	{Role: "child", Text: "she has a red raincoat and black hair and round glasses"},
	{Role: "interviewer", Text: "Does Mira have a friend who goes with her?"},
	{Role: "child", Text: "a tiny dragon named Pip who is green"},
	{Role: "interviewer", Text: "What does Mira want more than anything?"},
	{Role: "child", Text: "she wants to find her lost shoe"},
	{Role: "interviewer", Text: "Oh no! What makes it hard to find?"},
	{Role: "child", Text: "a big grumpy river is in the way"},
	{Role: "interviewer", Text: "How do Mira and Pip get past the grumpy river?"},
	{Role: "child", Text: "Pip breathes warm air and makes a bridge of steam"},
	{Role: "interviewer", Text: "And how does the story end?"},
	{Role: "child", Text: "she finds the shoe and the river laughs and they have cake"},
}

func liveChatter(t *testing.T) Chatter {
	t.Helper()
	if os.Getenv("GMI_API_KEY") == "" {
		t.Skip("GMI_API_KEY not set")
	}
	return text.New()
}

// TestLiveStructure_TwiceRunning is T5b's core probe: the Done when
// says "a transcript yields valid JSON that validates against the
// schema, twice running". Two independent live calls, both validated.
func TestLiveStructure_TwiceRunning(t *testing.T) {
	chat := liveChatter(t)

	for run := 1; run <= 2; run++ {
		ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
		start := time.Now()
		s, err := Structure(ctx, chat, realTranscript)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			t.Fatalf("run %d: Structure: %v", run, err)
		}
		if err := Validate(s); err != nil {
			t.Fatalf("run %d: Validate: %v", run, err)
		}
		if len(s.Pages) != PageCount {
			t.Errorf("run %d: got %d pages, want %d", run, len(s.Pages), PageCount)
		}

		// Every page emotion must be in the taught vocabulary.
		for _, p := range s.Pages {
			if !isEmotion(p.Emotion) {
				t.Errorf("run %d: page %d emotion %q not in taught vocabulary %v", run, p.N, p.Emotion, Emotions)
			}
		}

		// Every character named on a page must exist in the cast --
		// the T6 image lock depends on this being exact.
		known := map[string]bool{}
		for _, c := range s.Cast {
			known[strings.ToLower(c.Name)] = true
		}
		for _, p := range s.Pages {
			for _, ch := range p.Characters {
				if !known[strings.ToLower(ch)] {
					t.Errorf("run %d: page %d names %q, absent from cast", run, p.N, ch)
				}
			}
		}

		names := make([]string, 0, len(s.Cast))
		for _, c := range s.Cast {
			names = append(names, c.Name)
		}
		t.Logf("run %d OK in %s: title=%q pages=%d cast=%v", run, elapsed.Round(time.Second), s.Title, len(s.Pages), names)
		for _, c := range s.Cast {
			t.Logf("  cast %q visual=%q", c.Name, c.Visual)
		}
		t.Logf("  page 1 text=%q emotion=%q", s.Pages[0].Text, s.Pages[0].Emotion)
		t.Logf("  page 1 prompt=%q", s.Pages[0].Prompt)
	}
}

// TestLiveCorrectiveSystemMessage pins the acceptance T5b names: that
// MiniMax accepts a system-role corrective message mid-conversation,
// which is the mechanism Structure's retry depends on.
func TestLiveCorrectiveSystemMessage(t *testing.T) {
	chat := liveChatter(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := chat.Chat(ctx, text.ChatRequest{
		Model: ModelID,
		Messages: []text.Message{
			{Role: "system", Content: "You reply with one word only."},
			{Role: "user", Content: "Name a colour."},
			{Role: "assistant", Content: "I would be delighted to help you with that request."},
			{Role: "system", Content: "That reply broke the rule. Reply with ONE word only, nothing else."},
		},
	})
	if err != nil {
		t.Fatalf("corrective system message rejected: %v", err)
	}
	got, err := resp.Choices[0].Message.Text()
	if err != nil {
		t.Fatalf("Text(): %v", err)
	}
	t.Logf("reply after mid-conversation corrective system message: %q", got)
	if strings.TrimSpace(got) == "" {
		t.Error("empty reply")
	}
}
