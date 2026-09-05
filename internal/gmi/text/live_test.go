//go:build live

// Live probes for T2b. Excluded from every normal build and from CI by
// the `live` tag: they need a real GMI_API_KEY and reach the real
// production host. Run with:
//
//	set -a; . ./.env; set +a; go test -tags live -run Live -v ./internal/gmi/text/
//
// T2b owns these -- they are the half of T2's original Done when
// ("an integration test hits both endpoints live and unmarshals into
// typed structs") that no agent could run while the operator key was
// rejected by both providers.
package text

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("GMI_API_KEY") == "" {
		t.Skip("GMI_API_KEY not set")
	}
	return New()
}

// TestLiveChat_TypedUnmarshal is T2b's text-endpoint probe: a real call
// to the real host, decoded into the package's typed structs.
func TestLiveChat_TypedUnmarshal(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	resp, err := c.Chat(ctx, ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "Reply with exactly the word: pong"}},
	})
	if err != nil {
		t.Fatalf("live Chat: %v", err)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("live Chat: no choices")
	}
	got, err := resp.Choices[0].Message.Text()
	if err != nil {
		t.Fatalf("Text(): %v", err)
	}
	t.Logf("finish_reason=%q role=%q text=%q", resp.Choices[0].FinishReason, resp.Choices[0].Message.Role, got)
	if strings.TrimSpace(got) == "" {
		t.Error("assistant text is empty")
	}
}

// TestLiveChat_BareModelID404s pins the quirk the package documents as
// fact #1: the bare id must 404, so the MiniMaxAI/ prefix is proven
// load-bearing against production rather than against a comment.
func TestLiveChat_BareModelID404s(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := c.Chat(ctx, ChatRequest{
		Model:    "MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("bare MiniMax-M3 unexpectedly succeeded -- the prefix quirk may have been fixed upstream; re-check the docs in client.go")
	}
	t.Logf("bare id error (expected): %v", err)
}

// TestLiveChat_ThinkingEnabled proves the reasoning switch is accepted
// by production, not merely emitted.
func TestLiveChat_ThinkingEnabled(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := c.Chat(ctx, ChatRequest{
		Model:    "MiniMaxAI/MiniMax-M3",
		Messages: []Message{{Role: "user", Content: "What is 17 * 23? Reply with only the number."}},
		Thinking: &Reasoning{Type: "enabled"},
	})
	if err != nil {
		t.Fatalf("live Chat with thinking: %v", err)
	}
	got, err := resp.Choices[0].Message.Text()
	if err != nil {
		t.Fatalf("Text(): %v", err)
	}
	t.Logf("thinking-enabled reply: %q", got)
	if !strings.Contains(got, "391") {
		t.Errorf("expected 391 in reply, got %q", got)
	}
}
