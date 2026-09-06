package interview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/gmi/text"
	"thutapi/internal/job"
	"thutapi/internal/store"
	"thutapi/internal/stream"
)

// ---------------------------------------------------------------------------

// errChat is the failure scriptedChatter returns on its errAt call.
var errChat = errors.New("interview test: chat exploded")

// errStoreTest is the failure testStore injects on UpdateInterview.
var errStoreTest = errors.New("interview test: store write failed")

// scriptedChatter is the fake M3: each Chat call consumes the next
// scripted reply (the last one repeats, so a runaway loop still
// terminates). It records every request so tests can assert what the
// loop actually sent. blockFirst gates the first N calls on `gate`:
// each blocked call waits for one token (a send) or for the gate to
// close — the deterministic handle for "a turn is in flight".
type scriptedChatter struct {
	mu         sync.Mutex
	replies    []string
	i          int
	requests   []text.ChatRequest
	errAt      int // 1-based call index that fails with errChat; 0 = never
	blockFirst int
	gate       chan struct{}
}

func (f *scriptedChatter) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	f.mu.Lock()
	i := f.i + 1
	f.i++
	f.requests = append(f.requests, req)
	gate, blockFirst := f.gate, f.blockFirst
	fail := f.errAt == i
	reply := f.replies[min(i-1, len(f.replies)-1)]
	f.mu.Unlock()

	if gate != nil && i <= blockFirst {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail {
		return nil, errChat
	}
	return &text.ChatResponse{Choices: []text.Choice{{
		Message: text.AssistantMessage{TextBody: reply},
	}}}, nil
}

// count reports how many Chat calls have been made.
func (f *scriptedChatter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.i
}

// request returns the i-th (1-based) recorded request.
func (f *scriptedChatter) request(i int) text.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i-1]
}

// failRunner is a jobRunner that never has a free slot.
type failRunner struct{}

func (failRunner) Start(context.Context, job.Func) (string, error) {
	return "", job.ErrLimit
}

// testStore wraps *store.DB so a test can fail UpdateInterview
// deterministically through errStoreTest.
type testStore struct {
	*store.DB
	failUpd bool
}

func (s *testStore) UpdateInterview(ctx context.Context, iv store.Interview) error {
	if s.failUpd {
		return errStoreTest
	}
	return s.DB.UpdateInterview(ctx, iv)
}

// newHarness builds a Handler over a real store and broker with the
// given scripted replies. Config{} semantics throughout — only the
// knobs a test names are set — so the default paths run for real.
func newHarness(t *testing.T, replies []string) (*Handler, *scriptedChatter, *testStore, *stream.Broker) {
	t.Helper()
	db, err := store.Open(t.Context(), store.Config{
		Path: filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	broker := stream.New(stream.Config{Buffer: 64})
	chat := &scriptedChatter{replies: replies}
	ts := &testStore{DB: db}
	h := newHandler(t, Config{Chat: chat, Store: ts, Broker: broker, Jobs: job.New(broker)})
	return h, chat, ts, broker
}

// newHandler is New with a discarded logger; nil deps stay an error.
func newHandler(t *testing.T, cfg Config) *Handler {
	t.Helper()
	cfg.Log = slog.New(slog.NewTextHandler(testSink{}, nil))
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return h
}

// testSink discards log output.
type testSink struct{}

func (testSink) Write(p []byte) (int, error) { return len(p), nil }

// sub subscribes to an interview topic and guarantees cleanup.
func sub(t *testing.T, broker *stream.Broker, id string) *stream.Subscription {
	t.Helper()
	s := broker.Subscribe(t.Context(), Topic(id))
	t.Cleanup(s.Cancel)
	return s
}

// waitEvent reads the next event and requires it to be `name` — a
// strict expectation that turns a surprise event (an unexpected
// "error", say) into the test failure that names it.
func waitEvent(t *testing.T, s *stream.Subscription, name string) map[string]any {
	t.Helper()
	select {
	case ev, ok := <-s.Events:
		if !ok {
			t.Fatalf("subscription closed while waiting for %q", name)
		}
		if ev.Name != name {
			t.Fatalf("event = %q (%s), want %q", ev.Name, ev.Data, name)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("decode %s event %q: %v", name, ev.Data, err)
		}
		return payload
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q event", name)
		return nil
	}
}

// postJSON sends one JSON POST and returns the status plus decoded
// body.
func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response from %s: %v", url, err)
	}
	return resp.StatusCode, out
}

// waitTranscriptTurn polls the store until the interview has n turns,
// the deterministic stand-in for an event the test has not subscribed
// to (the documented catch-up path for the opening question).
func waitTranscriptTurn(t *testing.T, h *Handler, id string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr, err := h.transcript(t.Context(), id)
		if err == nil && len(tr.Turns) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d transcript turns", n)
}

// waitTranscriptRole polls the store until the interview's LAST turn
// has the given role.
func waitTranscriptRole(t *testing.T, ts *testStore, id, role string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		iv, err := ts.DB.Interview(t.Context(), id)
		if err == nil && len(iv.Turns) > 0 && iv.Turns[len(iv.Turns)-1].Role == role {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a %q closing turn", role)
}

// ---------------------------------------------------------------------------
// Scripted material shared by the run tests.
// ---------------------------------------------------------------------------

// checklistScript is a full, legal interview: the opening question, one
// question per slot, then the model's own goodbye with the filled list
// complete. Every reply carries a cumulative control line.
var checklistScript = []string{
	"Once upon a time — who is your hero?\n[[filled:]]",
	"And who tags along?\n[[filled: hero]]",
	"What does your hero want more than anything?\n[[filled: hero, companion]]",
	"Oh no — what stands in the way?\n[[filled: hero, companion, want]]",
	"How does your hero turn it around?\n[[filled: hero, companion, want, obstacle]]",
	"How does it all end?\n[[filled: hero, companion, want, obstacle, turn]]",
	"What a story — let me make your book!\n[[filled: hero, companion, want, obstacle, turn, ending; end]]",
}

var checklistAnswers = []string{
	"a girl called Mira",
	"a dragon named Spark",
	"to find the lost star",
	"a big grumpy storm cloud",
	"she sings until the cloud cries",
	"everyone eats cake in the garden",
}

// ---------------------------------------------------------------------------
// Checklist-filled run: start → six answers → the model says end.
// ---------------------------------------------------------------------------

func TestInterviewRunsToEndOnChecklist(t *testing.T) {
	// Gate the opening call so the first question event publishes only
	// after the test has subscribed — every turn of this interview is
	// then observed over SSE, deterministically.
	h, chat, ts, broker := newHarness(t, checklistScript)
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	srv := serve(t, muxFor(t, h))

	// Start. The opening turn is in flight (gated) when the response
	// lands — the POST never waited on M3.
	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id, _ := body["id"].(string)
	bookID, _ := body["book_id"].(string)
	topic, _ := body["topic"].(string)
	if id == "" || bookID == "" || topic != Topic(id) {
		t.Fatalf("start body = %v, want id, book_id, topic=interview:<id>", body)
	}

	// Subscribe before releasing the gate: the opening question is
	// observable over SSE.
	s := sub(t, broker, id)
	close(gate)
	q := waitEvent(t, s, "question")
	if got := int(q["turn"].(float64)); got != 1 {
		t.Fatalf("opening question turn = %v, want 1", got)
	}
	if got := q["text"].(string); got != "Once upon a time — who is your hero?" {
		t.Fatalf("opening question text = %q — the control line must be stripped", got)
	}
	if got := fmt.Sprint(q["filled"]); got != "[]" {
		t.Fatalf("opening filled = %v, want []", got)
	}

	// Six answers. Each gets exactly one question event; the last
	// additionally gets the terminal ended event.
	for i, answer := range checklistAnswers {
		last := i == len(checklistAnswers)-1
		code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": answer})
		if code != 202 {
			t.Fatalf("answer %d status = %d (%v), want 202", i+1, code, body)
		}
		if !last && body["status"] != statusOpen {
			t.Fatalf("answer %d status = %v, want %q", i+1, body["status"], statusOpen)
		}
		if last {
			ended := waitEvent(t, s, "ended")
			if got := ended["reason"]; got != ReasonChecklist {
				t.Fatalf("ended reason = %v, want %q", got, ReasonChecklist)
			}
			if got := ended["text"].(string); got != "What a story — let me make your book!" {
				t.Fatalf("ended text = %q, want the goodbye", got)
			}
			wantFilled := `[hero companion want obstacle turn ending]`
			if got := fmt.Sprint(ended["filled"]); got != wantFilled {
				t.Fatalf("ended filled = %v, want %s", got, wantFilled)
			}
			break
		}
		q := waitEvent(t, s, "question")
		// The event's turn is the question's position in the transcript:
		// opening, child, question, ... — 2i+3 for the 0-based i-th answer.
		if got := int(q["turn"].(float64)); got != 2*i+3 {
			t.Fatalf("answer %d: question turn = %d, want %d", i+1, got, 2*i+3)
		}
	}

	// The interview is terminal: a seventh answer is a conflict.
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "one more"})
	if code != 409 {
		t.Fatalf("answer after end status = %d (%v), want 409", code, body)
	}

	// The transcript is complete and ordered: question, answer pairs,
	// then the closing turn.
	tr, err := h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if tr.Status != statusEnded {
		t.Fatalf("transcript status = %q, want %q", tr.Status, statusEnded)
	}
	wantRoles := []string{
		RoleInterviewer, RoleChild,
		RoleInterviewer, RoleChild,
		RoleInterviewer, RoleChild,
		RoleInterviewer, RoleChild,
		RoleInterviewer, RoleChild,
		RoleInterviewer, RoleChild,
		RoleClosing,
	}
	if len(tr.Turns) != len(wantRoles) {
		t.Fatalf("transcript has %d turns, want %d: %+v", len(tr.Turns), len(wantRoles), tr.Turns)
	}
	for i, want := range wantRoles {
		if tr.Turns[i].Role != want {
			t.Fatalf("turn %d role = %q, want %q (turns: %+v)", i, tr.Turns[i].Role, want, tr.Turns)
		}
	}
	if got, want := tr.Turns[0].Text, "Once upon a time — who is your hero?"; got != want {
		t.Fatalf("turn 1 text = %q, want %q", got, want)
	}
	if got, want := tr.Turns[12].Text, "What a story — let me make your book!"; got != want {
		t.Fatalf("closing text = %q, want %q", got, want)
	}

	// The book row exists and the interview links to it.
	book, err := ts.DB.Book(t.Context(), bookID)
	if err != nil {
		t.Fatalf("book %s not in store: %v", bookID, err)
	}
	if book.Title != WorkingTitle {
		t.Fatalf("book title = %q, want the working title %q (Phase B authors the real one)", book.Title, WorkingTitle)
	}
	if tr.BookID != bookID {
		t.Fatalf("transcript book_id = %q, want %q", tr.BookID, bookID)
	}

	// One Chat call per turn: opening + six answers = seven. Each call
	// carried the growing transcript, no thinking, and the system
	// prompt first.
	if got := chat.count(); got != 7 {
		t.Fatalf("chat calls = %d, want 7", got)
	}
	for i := 1; i <= 7; i++ {
		req := chat.request(i)
		if req.Model != ModelID {
			t.Fatalf("call %d model = %q, want %q", i, req.Model, ModelID)
		}
		if req.Thinking != nil {
			t.Fatalf("call %d carried Thinking %+v, want nil (thinking is OFF for every interview call)", i, req.Thinking)
		}
		// system + the transcript so far: call i carries 1+(i-1)*2 turns
		// (opening, then child+question per exchange). Call 1 has no
		// transcript yet and carries the opening directive instead —
		// MiniMax rejects a system-only conversation as empty.
		want := 1 + (i-1)*2
		if i == 1 {
			want++
		}
		if got := len(req.Messages); got != want {
			t.Fatalf("call %d has %d messages, want %d (system + one per transcript turn)", i, got, want)
		}
		if req.Messages[0].Role != "system" {
			t.Fatalf("call %d message[0] role = %q, want system", i, req.Messages[0].Role)
		}
	}
	if got := chat.request(7).Messages[12].Content; got != checklistAnswers[5] {
		t.Fatalf("last call's last message = %v, want the child's final answer", got)
	}

	// Closing state via GET: the JSON catch-up surface.
	code, body = getJSON(t, srv.URL+"/interviews/"+id)
	if code != 200 {
		t.Fatalf("transcript status = %d, want 200", code)
	}
	if body["status"] != statusEnded {
		t.Fatalf("GET status = %v, want %q", body["status"], statusEnded)
	}
	if got, want := fmt.Sprint(body["filled"]), `[hero companion want obstacle turn ending]`; got != want {
		t.Fatalf("GET filled = %v, want %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// Stall-early run: two low-effort answers end the interview, the chip
// question carries its options, the goodbye is one last Chat call.
// ---------------------------------------------------------------------------

var stallScript = []string{
	"Who is your hero?\n[[filled:]]",
	"Is she brave, or is she sneaky?\n[[chips: brave, sneaky]]",
	"That's okay — I loved it! Bye!\n[[filled:; end]]",
}

func TestInterviewEndsEarlyOnStall(t *testing.T) {
	h, chat, _, broker := newHarness(t, stallScript)
	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d, want 201", code)
	}
	id := body["id"].(string)

	// The opening question lands before the test subscribes — the
	// documented race; catch-up reads it from the store.
	waitTranscriptTurn(t, h, id, 1)
	s := sub(t, broker, id)

	code, _ = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "i dunno"})
	if code != 202 {
		t.Fatalf("first stall answer status = %d, want 202 (one stall gets the chip question)", code)
	}
	q := waitEvent(t, s, "question")
	chips, _ := q["chips"].([]any)
	if len(chips) != 2 || chips[0] != "brave" || chips[1] != "sneaky" {
		t.Fatalf("chip question chips = %v, want [brave sneaky]", chips)
	}

	// The second consecutive low-effort answer ends it.
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "idk"})
	if code != 202 {
		t.Fatalf("second stall answer status = %d, want 202", code)
	}
	if body["status"] != statusEnding {
		t.Fatalf("answer status = %v, want %q (the goodbye is in flight)", body["status"], statusEnding)
	}
	ended := waitEvent(t, s, "ended")
	if got := ended["reason"]; got != ReasonStall {
		t.Fatalf("ended reason = %v, want %q", got, ReasonStall)
	}
	if got := ended["text"]; got != "That's okay — I loved it! Bye!" {
		t.Fatalf("ended text = %v, want the model goodbye", got)
	}

	// Three Chat calls: opening, the chip question, the goodbye. The
	// goodbye call carried the ending directive as its last message.
	if got := chat.count(); got != 3 {
		t.Fatalf("chat calls = %d, want 3", got)
	}
	goodbye := chat.request(3)
	n := len(goodbye.Messages)
	if n < 2 || goodbye.Messages[n-1].Role != "system" || goodbye.Messages[n-1].Content != endingDirective {
		t.Fatalf("goodbye call's last message = %+v, want the ending directive", goodbye.Messages[n-1])
	}

	// Terminal: no further answers.
	code, _ = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "hello?"})
	if code != 409 {
		t.Fatalf("answer after stall-end status = %d, want 409", code)
	}
}

// ---------------------------------------------------------------------------
// Chip taps are real answers: one low-effort answer, then a chip tap,
// keeps the interview going.
// ---------------------------------------------------------------------------

var chipScript = []string{
	"Who is your hero?\n[[filled:]]",
	"Is she brave, or is she sneaky?\n[[chips: brave, sneaky]]",
	"Brave it is! What does she want?\n[[filled: hero]]",
}

func TestChipTapResetsStallStreak(t *testing.T) {
	h, _, _, broker := newHarness(t, chipScript)
	srv := serve(t, muxFor(t, h))

	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	waitTranscriptTurn(t, h, id, 1)
	s := sub(t, broker, id)

	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "hmm"}); code != 202 {
		t.Fatalf("stall answer status, want 202")
	}
	waitEvent(t, s, "question")

	// "Brave!" is one word — but it is one of the offered chips, so
	// the streak resets and the interview continues (question 3).
	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Brave!"}); code != 202 {
		t.Fatalf("chip answer status, want 202")
	}
	q := waitEvent(t, s, "question")
	// transcript position: opening, child, chip question, child, q3.
	if got := int(q["turn"].(float64)); got != 5 {
		t.Fatalf("turn = %d, want 5 — the chip tap must not have ended the interview", got)
	}
}

// ---------------------------------------------------------------------------
// Server-enforced ending: the model reports every slot filled but
// never says end — the checklist rule ends it anyway.
// ---------------------------------------------------------------------------

func TestChecklistFullWithoutModelEndStillEnds(t *testing.T) {
	replies := []string{
		"Who is your hero?\n[[filled: hero]]",
		"Everything at once then!\n[[filled: hero, companion, want, obstacle, turn, ending]]",
	}
	h, _, _, broker := newHarness(t, replies)
	srv := serve(t, muxFor(t, h))

	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	waitTranscriptTurn(t, h, id, 1)
	s := sub(t, broker, id)

	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "the whole story happens"}); code != 202 {
		t.Fatalf("answer status, want 202")
	}
	// The answer turn's reply filled everything without saying end. It is
	// terminal before a question event can offer the child another reply box.
	ended := waitEvent(t, s, "ended")
	if got := ended["reason"]; got != ReasonChecklist {
		t.Fatalf("ended reason = %v, want %q", got, ReasonChecklist)
	}
	code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "more"})
	if code != 409 {
		t.Fatalf("answer after enforcement status = %d, want 409", code)
	}
}

// ---------------------------------------------------------------------------
// MaxTurns default path: Config{} budgets defaultMaxTurns exchanges,
// then the interview ends itself (reason "limit").
// ---------------------------------------------------------------------------

func TestDefaultMaxTurnsEndsTheInterview(t *testing.T) {
	replies := make([]string, 0, defaultMaxTurns+2)
	replies = append(replies, "Who is your hero?\n[[filled:]]")
	for i := 1; i <= defaultMaxTurns; i++ {
		replies = append(replies, fmt.Sprintf("Tell me more, part %d.\n[[filled: hero]]", i))
	}
	replies = append(replies, "Time to make the book!\n[[filled: hero; end]]")
	h, chat, _, broker := newHarness(t, replies)
	// newHarness never sets MaxTurns, so this run exercises the
	// defaultMaxTurns path end to end.
	srv := serve(t, muxFor(t, h))

	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	waitTranscriptTurn(t, h, id, 1)
	s := sub(t, broker, id)

	for i := 1; i <= defaultMaxTurns; i++ {
		code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{
			"text": fmt.Sprintf("my answer number %d has several words", i),
		})
		if code != 202 {
			t.Fatalf("answer %d status = %d, want 202", i, code)
		}
		waitEvent(t, s, "question")
	}
	// One answer past the budget: the interview ends itself.
	code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{
		"text": "one answer past the budget with words",
	})
	if code != 202 {
		t.Fatalf("over-budget answer status = %d, want 202", code)
	}
	if body["status"] != statusEnding {
		t.Fatalf("over-budget answer status field = %v, want %q", body["status"], statusEnding)
	}
	ended := waitEvent(t, s, "ended")
	if got := ended["reason"]; got != ReasonLimit {
		t.Fatalf("ended reason = %v, want %q", got, ReasonLimit)
	}
	if got := chat.count(); got != defaultMaxTurns+2 {
		t.Fatalf("chat calls = %d, want %d (opening + %d turns + goodbye)", got, defaultMaxTurns+2, defaultMaxTurns)
	}
}

// ---------------------------------------------------------------------------
// Concurrency and failure corners.
// ---------------------------------------------------------------------------

func TestAnswerWhileTurnInFlightIsBusy(t *testing.T) {
	h, chat, _, broker := newHarness(t, []string{
		"Who is your hero?\n[[filled:]]",
		"And who else?\n[[filled: hero]]",
	})
	// Gate the first TWO calls: the opening, and the first answer's
	// turn. Each blocked call waits for one token.
	chat.mu.Lock()
	chat.blockFirst = 2
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate
	srv := serve(t, muxFor(t, h))

	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	s := sub(t, broker, id)

	// Release the opening question.
	gate <- struct{}{}
	waitTranscriptTurn(t, h, id, 1)

	// Answer 1 goes in flight (its Chat call is gated).
	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Mira and nobody else"}); code != 202 {
		t.Fatalf("first answer status, want 202")
	}
	// Answer 2 arrives while the turn runs: busy, not queued.
	code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "and also a dog"})
	if code != 409 {
		t.Fatalf("racing answer status = %d (%v), want 409", code, body)
	}
	gate <- struct{}{}
	waitEvent(t, s, "question")

	// The busy answer must not have been persisted.
	tr, err := h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	var child int
	for _, turn := range tr.Turns {
		if turn.Role == RoleChild {
			child++
			if turn.Text == "and also a dog" {
				t.Fatalf("the busy-rejected answer was persisted: %+v", tr.Turns)
			}
		}
	}
	if child != 1 {
		t.Fatalf("child turns = %d, want 1", child)
	}
}

func TestChatFailurePublishesErrorAndStaysOpen(t *testing.T) {
	replies := []string{
		"Who is your hero?\n[[filled:]]",
		"", // the second call returns no text at all
		"And who else?\n[[filled: hero]]",
		"More?\n[[filled: hero, companion]]",
	}
	h, chat, _, broker := newHarness(t, replies)
	chat.errAt = 3 // the call after the empty reply fails outright
	srv := serve(t, muxFor(t, h))

	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	waitTranscriptTurn(t, h, id, 1)
	s := sub(t, broker, id)

	// Answer 1 → the second call returns an empty reply: error event,
	// interview still open.
	postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Mira of the hills"})
	waitEvent(t, s, "error")

	// Answer 2 → the third call fails outright: error event again.
	postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Spark the dragon"})
	waitEvent(t, s, "error")

	// Answer 3 → the fourth call succeeds: the interview recovered
	// without any operator step.
	postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "the shiny star"})
	q := waitEvent(t, s, "question")
	// The question's transcript position: opening plus three persisted
	// child answers (persist-first: failed turns keep the child's
	// words), then the recovered question.
	if got := int(q["turn"].(float64)); got != 5 {
		t.Fatalf("recovered question turn = %d, want 5", got)
	}
}

func TestStoreFailuresSurfaceAsSentinels(t *testing.T) {
	h, _, ts, _ := newHarness(t, []string{"Who is your hero?\n[[filled:]]"})

	// Start: the book-link write fails → start fails wrapping the
	// store error.
	ts.failUpd = true
	if _, err := h.start(t.Context()); !errors.Is(err, errStoreTest) {
		t.Fatalf("start err = %v, want it to wrap errStoreTest", err)
	}
	ts.failUpd = false

	res, err := h.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTranscriptTurn(t, h, res.ID, 1)

	// Answer: the transcript write fails → the answer is rejected and
	// nothing was persisted.
	ts.failUpd = true
	if _, err := h.answer(t.Context(), res.ID, "Mira of the hills"); !errors.Is(err, errStoreTest) {
		t.Fatalf("answer err = %v, want it to wrap errStoreTest", err)
	}
	tr, err := h.transcript(t.Context(), res.ID)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if got := len(tr.Turns); got != 1 {
		t.Fatalf("transcript has %d turns after the failed answer, want 1 (nothing persisted)", got)
	}
	ts.failUpd = false
}

func TestJobStartFailureRollsBackTheAnswer(t *testing.T) {
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	broker := stream.New(stream.Config{})
	h := newHandler(t, Config{
		Chat:   &scriptedChatter{replies: []string{"Who?\n[[filled:]]"}},
		Store:  &testStore{DB: db},
		Broker: broker,
		Jobs:   failRunner{},
	})

	// Start: the opening turn cannot start at all.
	if _, err := h.start(t.Context()); !errors.Is(err, job.ErrLimit) {
		t.Fatalf("start err = %v, want it to wrap job.ErrLimit", err)
	}

	// A working start so the answer path has an interview to fail on.
	h2 := newHandler(t, Config{Chat: h.chat, Store: h.store, Broker: broker, Jobs: job.New(broker)})
	res, err := h2.start(t.Context())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitTranscriptTurn(t, h2, res.ID, 1)

	// The answer path rolls the transcript back when the job Runner
	// refuses the turn.
	h3 := newHandler(t, Config{Chat: h.chat, Store: h.store, Broker: broker, Jobs: failRunner{}})
	if _, err := h3.answer(t.Context(), res.ID, "Mira of the hills"); !errors.Is(err, job.ErrLimit) {
		t.Fatalf("answer err = %v, want it to wrap job.ErrLimit", err)
	}
	// The rollback put the transcript back: one turn, the opening.
	iv, err := db.Interview(t.Context(), res.ID)
	if err != nil {
		t.Fatalf("interview after rollback: %v", err)
	}
	if got := len(iv.Turns); got != 1 {
		t.Fatalf("transcript has %d turns after rollback, want 1: %+v", got, iv.Turns)
	}
}

// ---------------------------------------------------------------------------
// Restart semantics.
// ---------------------------------------------------------------------------

func TestRestartedHandlerSeesEndedInterview(t *testing.T) {
	h, _, ts, _ := newHarness(t, checklistScript)
	srv := serve(t, muxFor(t, h))

	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	waitTranscriptTurn(t, h, id, 1)
	// One answer at a time. A turn is in flight from the POST that
	// starts it until it has persisted its reply, and answer() refuses
	// an answer that arrives meanwhile with ErrBusy (409) — so posting
	// the six blind drops whichever ones land inside a turn's window,
	// the script never reaches its "end" reply, and the wait below
	// hangs for a closing turn nothing will ever write. Waiting for
	// each turn to land is the same gate the SSE-observing tests get
	// from their per-answer question event; the store is the
	// authoritative source, and the turn is persisted before s.mu is
	// released, so a visible turn means the next answer is accepted.
	for i, answer := range checklistAnswers {
		code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": answer})
		if code != 202 {
			t.Fatalf("answer %d status = %d (%v), want 202", i+1, code, body)
		}
		// The opening question plus a child turn and a reply per answer.
		waitTranscriptTurn(t, h, id, 1+2*(i+1))
	}
	waitTranscriptRole(t, ts, id, RoleClosing)

	// A fresh Handler — the restart: same store, no in-process state.
	rebornH := newHandler(t, Config{
		Chat:   &scriptedChatter{replies: checklistScript},
		Store:  &testStore{DB: ts.DB},
		Broker: stream.New(stream.Config{}),
		Jobs:   job.New(stream.New(stream.Config{})),
	})

	tr, err := rebornH.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript after restart: %v", err)
	}
	if tr.Status != statusEnded {
		t.Fatalf("status after restart = %q, want %q (last turn is the closing turn)", tr.Status, statusEnded)
	}
	if _, err := rebornH.answer(t.Context(), id, "again"); !errors.Is(err, ErrEnded) {
		t.Fatalf("answer after restart err = %v, want it to wrap ErrEnded", err)
	}
	// The pre-restart handler agrees, of course.
	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "again"}); code != 409 {
		t.Fatalf("answer after restart status = %d, want 409", code)
	}
}

// ---------------------------------------------------------------------------
// Round-1 remediation pins (adversarial-review/t4-round1.md). Each
// test converts the reviewer's failing probe into a permanent
// regression test; the mutation that re-breaks each pin is recorded
// in t4-remediation-round1.md.
// ---------------------------------------------------------------------------

// flakyRunner refuses the nth Start and delegates to real afterwards:
// the deterministic handle for a job Runner that rejects one turn.
type flakyRunner struct {
	mu     sync.Mutex
	n      int
	refuse int
	real   jobRunner
}

func (r *flakyRunner) Start(ctx context.Context, fn job.Func) (string, error) {
	r.mu.Lock()
	r.n++
	refuse := r.n == r.refuse
	real := r.real
	r.mu.Unlock()
	if refuse {
		return "", job.ErrLimit
	}
	return real.Start(ctx, fn)
}

// waitTranscriptError polls the catch-up state until a recorded turn
// failure appears — the deterministic handle on an async turn failure.
func waitTranscriptError(t *testing.T, h *Handler, id string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tr, err := h.transcript(t.Context(), id)
		if err == nil && tr.Error != "" {
			return tr.Error
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for the recorded turn failure")
	return ""
}

// TestStallEndRequiresNoProgress pins F1: a short answer raises the
// chip signal, never an end. Two substantive one-word answers ("Mira"
// then "red") keep the interview open, with the streak signalled to
// the model on every call so it can offer chips; the end fires only
// on no-progress — a repeated one-word answer (below) or two stall
// answers (TestInterviewEndsEarlyOnStall).
//
// Mutation: re-widen the end condition in answer() to the raw streak
// (case s.streak >= 2:) — the first subtest goes red on "red".
func TestStallEndRequiresNoProgress(t *testing.T) {
	t.Run("substantive one-word answers stay open with the chip signal", func(t *testing.T) {
		h, chat, _, broker := newHarness(t, []string{
			"What is your hero called?\n[[filled:]]",
			"What colour is the dragon?\n[[filled:]]",
			"What does the dragon do all day?\n[[filled:]]",
		})
		srv := serve(t, muxFor(t, h))

		_, body := postJSON(t, srv.URL+"/interviews", nil)
		id := body["id"].(string)
		waitTranscriptTurn(t, h, id, 1)
		// The opening call carries system + the opening directive and
		// nothing else: no STALL directive before any answer exists.
		opening := chat.request(1).Messages
		if got := len(opening); got != 2 {
			t.Fatalf("opening call carries %d messages, want 2 (system + opening directive)", got)
		}
		if opening[1].Role != "user" || opening[1].Content != openingDirective {
			t.Fatalf("opening call message[1] = %q/%q, want user/%q (a stall directive must not precede any answer)",
				opening[1].Role, opening[1].Content, openingDirective)
		}
		s := sub(t, broker, id)

		// "Mira" is one word: the streak rises and the NEXT call
		// carries the chip signal — but nothing ends.
		code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Mira"})
		if code != 202 || body["status"] != statusOpen {
			t.Fatalf("answer 1 = %d %v, want 202 %q", code, body, statusOpen)
		}
		waitEvent(t, s, "question")
		req := chat.request(2)
		if got := req.Messages[len(req.Messages)-1]; got.Role != "system" || got.Content != stallDirective(1) {
			t.Fatalf("call 2's last message = %+v, want the stall directive for streak 1", got)
		}

		// "red" is a different one-word answer: still open, still
		// signalled, never ended.
		code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "red"})
		if code != 202 || body["status"] != statusOpen {
			t.Fatalf("answer 2 = %d %v, want 202 %q (a substantive one-word answer never ends)", code, body, statusOpen)
		}
		waitEvent(t, s, "question")
		req = chat.request(3)
		if got := req.Messages[len(req.Messages)-1]; got.Role != "system" || got.Content != stallDirective(2) {
			t.Fatalf("call 3's last message = %+v, want the stall directive for streak 2", got)
		}
		tr, err := h.transcript(t.Context(), id)
		if err != nil {
			t.Fatalf("transcript: %v", err)
		}
		if tr.Status != statusOpen {
			t.Fatalf("transcript status = %q, want %q", tr.Status, statusOpen)
		}
	})

	t.Run("the same one-word answer repeated ends it", func(t *testing.T) {
		h, _, _, broker := newHarness(t, []string{
			"What colour is the dragon?\n[[filled:]]",
			"Are you sure?\n[[filled:]]",
			"That's okay — bye!\n[[filled:; end]]",
		})
		srv := serve(t, muxFor(t, h))
		_, body := postJSON(t, srv.URL+"/interviews", nil)
		id := body["id"].(string)
		waitTranscriptTurn(t, h, id, 1)
		s := sub(t, broker, id)

		if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "idk"}); code != 202 {
			t.Fatalf("first idk status = %d, want 202", code)
		}
		waitEvent(t, s, "question")
		code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "idk"})
		if code != 202 || body["status"] != statusEnding {
			t.Fatalf("repeated idk = %d %v, want 202 %q", code, body, statusEnding)
		}
		ended := waitEvent(t, s, "ended")
		if ended["reason"] != ReasonStall {
			t.Fatalf("ended reason = %v, want %q", ended["reason"], ReasonStall)
		}
	})

	t.Run("three distinct one-word answers stay open and signalled", func(t *testing.T) {
		h, chat, _, broker := newHarness(t, []string{
			"What is your hero called?\n[[filled:]]",
			"What colour is the dragon?\n[[filled:]]",
			"What does the dragon eat?\n[[filled:]]",
			"How big is the dragon?\n[[filled:]]",
		})
		srv := serve(t, muxFor(t, h))
		_, body := postJSON(t, srv.URL+"/interviews", nil)
		id := body["id"].(string)
		waitTranscriptTurn(t, h, id, 1)
		s := sub(t, broker, id)

		for i, a := range []string{"red", "blue", "green"} {
			code, body := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": a})
			if code != 202 || body["status"] != statusOpen {
				t.Fatalf("answer %d (%q) = %d %v, want 202 %q", i+1, a, code, body, statusOpen)
			}
			waitEvent(t, s, "question")
		}
		req := chat.request(4)
		if got := req.Messages[len(req.Messages)-1]; got.Content != stallDirective(3) {
			t.Fatalf("call 4's last message = %+v, want the stall directive for streak 3", got)
		}
		tr, err := h.transcript(t.Context(), id)
		if err != nil {
			t.Fatalf("transcript: %v", err)
		}
		if tr.Status != statusOpen {
			t.Fatalf("transcript status = %q, want %q", tr.Status, statusOpen)
		}
	})
}

// TestEmptyGoodbyeStillClosesTheInterview pins F2: an ending path
// that arrives without model text — an end-only control line, or a
// wrap-up call that returned none — still persists a closing turn
// carrying fallbackGoodbye, so the ended interview is restart-stable
// and the child hears a goodbye, never a bare stop.
//
// Mutation: re-shorten closeTurn to skip persistence when the reply
// text is empty — both subtests go red (no closing turn; the
// restarted Handler accepts a new answer).
func TestEmptyGoodbyeStillClosesTheInterview(t *testing.T) {
	t.Run("end-only control line", func(t *testing.T) {
		h, _, ts, broker := newHarness(t, []string{
			"What is your hero called?\n[[filled:]]",
			"What does she want?\n[[filled: hero]]",
			"[[filled: hero; end]]", // the model says end, says nothing else
		})
		srv := serve(t, muxFor(t, h))
		_, body := postJSON(t, srv.URL+"/interviews", nil)
		id := body["id"].(string)
		waitTranscriptTurn(t, h, id, 1)
		s := sub(t, broker, id)

		postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "a girl called Mira"})
		waitEvent(t, s, "question")
		if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "to find the lost star"}); code != 202 {
			t.Fatalf("answer 2 status = %d, want 202", code)
		}
		ended := waitEvent(t, s, "ended")
		if got := ended["text"]; got != fallbackGoodbye {
			t.Fatalf("ended text = %v, want the fallback goodbye %q", got, fallbackGoodbye)
		}
		waitTranscriptRole(t, ts, id, RoleClosing)

		reborn := newHandler(t, Config{
			Chat:   &scriptedChatter{replies: []string{"x\n[[filled:]]"}},
			Store:  &testStore{DB: ts.DB},
			Broker: stream.New(stream.Config{}),
			Jobs:   job.New(stream.New(stream.Config{})),
		})
		if _, err := reborn.answer(t.Context(), id, "again"); !errors.Is(err, ErrEnded) {
			t.Fatalf("answer after restart err = %v, want it to wrap ErrEnded", err)
		}
	})

	t.Run("stall wrap-up without text", func(t *testing.T) {
		h, _, ts, broker := newHarness(t, []string{
			"What is your hero called?\n[[filled:]]",
			"Is she brave or sneaky?\n[[chips: brave, sneaky]]",
			"[[filled:; end]]", // the wrap-up call returns only the control line
		})
		srv := serve(t, muxFor(t, h))
		_, body := postJSON(t, srv.URL+"/interviews", nil)
		id := body["id"].(string)
		waitTranscriptTurn(t, h, id, 1)
		s := sub(t, broker, id)

		postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "hmm"})
		waitEvent(t, s, "question")
		if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "i dunno"}); code != 202 {
			t.Fatalf("second stall answer status = %d, want 202", code)
		}
		ended := waitEvent(t, s, "ended")
		if got := ended["text"]; got != fallbackGoodbye {
			t.Fatalf("ended text = %v, want the fallback goodbye %q", got, fallbackGoodbye)
		}
		waitTranscriptRole(t, ts, id, RoleClosing)
	})
}

// TestEnforcedEndSurvivesRestart pins F6: the server-side checklist
// enforcement — the checklist filled without a model "end" — persists
// a closing turn like every other end path, so the interview is still
// ended after a restart.
//
// Mutation: revert the enforcement branch to publishing ended without
// persisting (s.ended = true + publish only) — this test goes red on
// the restart answer.
func TestEnforcedEndSurvivesRestart(t *testing.T) {
	replies := []string{
		"Who is your hero?\n[[filled: hero]]",
		"Everything at once then!\n[[filled: hero, companion, want, obstacle, turn, ending]]",
	}
	h, _, ts, broker := newHarness(t, replies)
	srv := serve(t, muxFor(t, h))
	_, body := postJSON(t, srv.URL+"/interviews", nil)
	id := body["id"].(string)
	waitTranscriptTurn(t, h, id, 1)
	s := sub(t, broker, id)

	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "the whole story happens"}); code != 202 {
		t.Fatalf("answer status, want 202")
	}
	ended := waitEvent(t, s, "ended")
	if ended["reason"] != ReasonChecklist {
		t.Fatalf("ended reason = %v, want %q", ended["reason"], ReasonChecklist)
	}
	if got := ended["text"]; got != "Everything at once then!" {
		t.Fatalf("ended text = %v, want the terminal model text", got)
	}
	waitTranscriptRole(t, ts, id, RoleClosing)

	reborn := newHandler(t, Config{
		Chat:   &scriptedChatter{replies: replies},
		Store:  &testStore{DB: ts.DB},
		Broker: stream.New(stream.Config{}),
		Jobs:   job.New(stream.New(stream.Config{})),
	})
	tr, err := reborn.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript after restart: %v", err)
	}
	if tr.Status != statusEnded {
		t.Fatalf("status after restart = %q, want %q", tr.Status, statusEnded)
	}
	if _, err := reborn.answer(t.Context(), id, "again"); !errors.Is(err, ErrEnded) {
		t.Fatalf("answer after restart err = %v, want it to wrap ErrEnded", err)
	}
}

// TestOpeningTurnFailureIsObservable pins F3: the opening question's
// Chat call can fail before any client has been able to subscribe
// (the id only exists once the start response is gone). The failure
// is recorded on the catch-up state until the next turn starts, so
// the client's post-start catch-up read reports it, and the recovery
// — answering again — works and clears the record.
//
// Mutation: stop recording the failure (drop the s.turnErr assignment
// from turnFailed) — this test goes red on the catch-up read.
func TestOpeningTurnFailureIsObservable(t *testing.T) {
	h, chat, _, broker := newHarness(t, []string{
		"Who is your hero?\n[[filled:]]",
		"And who tags along?\n[[filled:]]",
	})
	chat.errAt = 1 // the opening call fails outright
	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d, want 201 (the turn runs off the request)", code)
	}
	id := body["id"].(string)

	// The catch-up state reports the failure even though no client
	// could have been subscribed when it fired.
	if got := waitTranscriptError(t, h, id); got != "internal" {
		t.Fatalf("recorded error = %q, want the machine class %q", got, "internal")
	}
	tr, err := h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if tr.Status != statusOpen {
		t.Fatalf("status = %q, want %q (the interview is recoverable)", tr.Status, statusOpen)
	}
	if len(tr.Turns) != 0 {
		t.Fatalf("transcript has %d turns, want 0 (the failure is the only fact)", len(tr.Turns))
	}

	// The recovery: answer again. The record clears the moment the
	// turn re-starts, and the question lands.
	s := sub(t, broker, id)
	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "a girl called Mira"}); code != 202 {
		t.Fatalf("recovery answer status, want 202")
	}
	tr, err = h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if tr.Error != "" {
		t.Fatalf("error field = %q after the re-start, want it cleared", tr.Error)
	}
	waitEvent(t, s, "question")
}

// TestErrorBodiesCarryMachineClass pins F5: every error body and every
// SSE error event carries a stable machine class derived from the
// sentinel — never prose — and the prose goes only to the log.
//
// Mutation: put err.Error() back into writeError's or fail's payload
// — the exact-class assertions go red.
func TestErrorBodiesCarryMachineClass(t *testing.T) {
	var logBuf bytes.Buffer
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	broker := stream.New(stream.Config{Buffer: 64})
	chat := &scriptedChatter{replies: []string{
		"Who is your hero?\n[[filled:]]",
		"Bye! What a story!\n[[filled:; end]]",
	}}
	// New, not newHandler: the test needs the real slog sink (the
	// prose assertions below), not the harness's discarded one.
	h, err := New(Config{
		Chat:   chat,
		Store:  &testStore{DB: db},
		Broker: broker,
		Jobs:   job.New(broker),
		Log:    slog.New(slog.NewTextHandler(&logBuf, nil)),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	srv := serve(t, muxFor(t, h))

	// The opening turn is gated in flight, so a racing answer is busy:
	// 409 with the class, not prose.
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d, want 201", code)
	}
	id := body["id"].(string)
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Mira and her dog"})
	if code != 409 || body["error"] != "busy" {
		t.Fatalf("racing answer = %d %v, want 409 error=busy", code, body)
	}
	gate <- struct{}{}
	waitTranscriptTurn(t, h, id, 1)

	// internal — the SSE error event carries the class; the log
	// carries the prose.
	chat.mu.Lock()
	chat.errAt = 2
	chat.mu.Unlock()
	s := sub(t, broker, id)
	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "a girl called Mira"}); code != 202 {
		t.Fatalf("answer status, want 202")
	}
	ev := waitEvent(t, s, "error")
	if ev["error"] != "internal" {
		t.Fatalf("error event = %v, want error=internal", ev)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("chat exploded")) {
		t.Fatalf("log = %q, want the error prose", logBuf.String())
	}

	// Recovery, then the model's own end, then ended — 409 with the
	// class.
	chat.mu.Lock()
	chat.errAt = 0
	chat.mu.Unlock()
	if code, _ := postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "a dragon called Spark"}); code != 202 {
		t.Fatalf("recovery answer status, want 202")
	}
	waitEvent(t, s, "ended")
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "one more"})
	if code != 409 || body["error"] != "ended" {
		t.Fatalf("answer after end = %d %v, want 409 error=ended", code, body)
	}

	// not_found on the request surface.
	getErr := func(url string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		var b struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.StatusCode, b.Error
	}
	if code, class := getErr(srv.URL + "/interviews/00000000000000000000000000000000"); code != 404 || class != "not_found" {
		t.Fatalf("unknown id = %d %q, want 404 not_found", code, class)
	}

	// invalid — the body carries the class; the log carries the prose.
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/interviews/"+id+"/answers", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var badBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&badBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StatusCode != 400 || badBody.Error != "invalid" {
		t.Fatalf("broken body = %d %q, want 400 invalid", resp.StatusCode, badBody.Error)
	}
	if !bytes.Contains(logBuf.Bytes(), []byte("not valid JSON")) {
		t.Fatalf("log = %q, want the request-error prose", logBuf.String())
	}
}

// TestStreakRollsBackWithTheAnswer pins F4: an answer that is rolled
// back — the job Runner refusing the turn, or the transcript write
// failing — takes its stall-bookkeeping point back with it. After a
// refused "hmm", a first processed "i dunno" is still only the FIRST
// stall.
//
// Mutation: drop the counter restore from the answer failure paths —
// both subtests go red (the second answer reports "ending").
func TestStreakRollsBackWithTheAnswer(t *testing.T) {
	t.Run("job refusal", func(t *testing.T) {
		db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(t.TempDir(), "test.db")})
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		broker := stream.New(stream.Config{})
		chat := &scriptedChatter{replies: []string{
			"Who is your hero?\n[[filled:]]",
			"And who tags along?\n[[filled:]]",
		}}
		h := newHandler(t, Config{
			Chat:   chat,
			Store:  &testStore{DB: db},
			Broker: broker,
			Jobs:   &flakyRunner{refuse: 2, real: job.New(broker)},
		})

		res, err := h.start(t.Context())
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		waitTranscriptTurn(t, h, res.ID, 1)

		// "hmm" is accepted, persisted, and its turn refused: the
		// answer rolls back — its streak point with it.
		if _, err := h.answer(t.Context(), res.ID, "hmm"); !errors.Is(err, job.ErrLimit) {
			t.Fatalf("refused answer err = %v, want it to wrap job.ErrLimit", err)
		}
		iv, err := db.Interview(t.Context(), res.ID)
		if err != nil {
			t.Fatalf("interview after rollback: %v", err)
		}
		if got := len(iv.Turns); got != 1 {
			t.Fatalf("transcript has %d turns after rollback, want 1", got)
		}

		// The first PROCESSED stall: still only the first.
		res2, err := h.answer(t.Context(), res.ID, "i dunno")
		if err != nil {
			t.Fatalf("answer after refusal: %v", err)
		}
		if res2.Status != statusOpen {
			t.Fatalf("status after processed stall = %q, want %q (the refused answer's point rolled back)", res2.Status, statusOpen)
		}
		waitTranscriptTurn(t, h, res.ID, 3)
	})

	t.Run("store failure", func(t *testing.T) {
		h, _, ts, _ := newHarness(t, []string{
			"Who is your hero?\n[[filled:]]",
			"And who tags along?\n[[filled:]]",
		})
		res, err := h.start(t.Context())
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		waitTranscriptTurn(t, h, res.ID, 1)

		ts.failUpd = true
		if _, err := h.answer(t.Context(), res.ID, "hmm"); !errors.Is(err, errStoreTest) {
			t.Fatalf("failed answer err = %v, want it to wrap errStoreTest", err)
		}
		ts.failUpd = false

		res2, err := h.answer(t.Context(), res.ID, "i dunno")
		if err != nil {
			t.Fatalf("answer after store failure: %v", err)
		}
		if res2.Status != statusOpen {
			t.Fatalf("status after processed stall = %q, want %q (the failed answer's point rolled back)", res2.Status, statusOpen)
		}
		waitTranscriptTurn(t, h, res.ID, 3)
	})
}
