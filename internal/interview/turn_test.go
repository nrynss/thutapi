package interview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"thutapi/internal/job"
	"thutapi/internal/store"
	"thutapi/internal/stream"
)

// errUpstream503 simulates a 503 Service Unavailable error from GMI TTS.
var errUpstream503 = errors.New("upstream 503 service unavailable")

// fakeSpeaker records calls to SynthesizeQuestion and returns configured results.
type fakeSpeaker struct {
	mu       sync.Mutex
	calls    []string
	mediaID  string
	err      error
	delay    time.Duration
	deadline time.Time
	hasDead  bool
}

func (f *fakeSpeaker) SynthesizeQuestion(ctx context.Context, text string) (string, error) {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, text)
	f.deadline, f.hasDead = ctx.Deadline()
	return f.mediaID, f.err
}

func (f *fakeSpeaker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeSpeaker) call(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func (f *fakeSpeaker) getDeadlineInfo() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deadline, f.hasDead
}

// newHarnessWithSpeaker builds a Handler over a real store and broker with
// a configured QuestionSpeaker.
func newHarnessWithSpeaker(t *testing.T, replies []string, speaker QuestionSpeaker) (*Handler, *scriptedChatter, *testStore, *stream.Broker) {
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
	h := newHandler(t, Config{
		Chat:    chat,
		Speaker: speaker,
		Store:   ts,
		Broker:  broker,
		Jobs:    job.New(broker),
	})
	return h, chat, ts, broker
}

// assertNoEvent asserts that no SSE event is received on subscription within d.
func assertNoEvent(t *testing.T, s *stream.Subscription, d time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-s.Events:
		if ok {
			t.Fatalf("unexpected event on stream: name=%q data=%q", ev.Name, ev.Data)
		}
	case <-time.After(d):
		// Expected: no event arrived.
	}
}

// TestQuestionSpeakerFunc exercises the QuestionSpeakerFunc adapter.
func TestQuestionSpeakerFunc(t *testing.T) {
	var calledWith string
	fn := QuestionSpeakerFunc(func(ctx context.Context, text string) (string, error) {
		calledWith = text
		return "media-abc-123", nil
	})
	var qs QuestionSpeaker = fn
	id, err := qs.SynthesizeQuestion(t.Context(), "Who is your best friend?")
	if err != nil {
		t.Fatalf("SynthesizeQuestion error = %v, want nil", err)
	}
	if id != "media-abc-123" {
		t.Fatalf("SynthesizeQuestion id = %q, want %q", id, "media-abc-123")
	}
	if calledWith != "Who is your best friend?" {
		t.Fatalf("calledWith = %q, want %q", calledWith, "Who is your best friend?")
	}
}

// TestQuestionAudioRawWire is this track's wire pin test (AGENTS.md §Testing rule 4).
// It pins the exact JSON field names ("turn" and "audio_url") and values on the raw wire.
func TestQuestionAudioRawWire(t *testing.T) {
	ev := questionAudioEvent{
		Turn:     1,
		AudioURL: "/media/media-uuid-987",
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"turn":1,"audio_url":"/media/media-uuid-987"}`
	if string(b) != want {
		t.Fatalf("marshalled wire JSON = %s, want %s", string(b), want)
	}

	var decoded questionAudioEvent
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Turn != 1 {
		t.Errorf("decoded.Turn = %d, want 1", decoded.Turn)
	}
	if decoded.AudioURL != "/media/media-uuid-987" {
		t.Errorf("decoded.AudioURL = %q, want %q", decoded.AudioURL, "/media/media-uuid-987")
	}
}

// TestQuestionSpeaker_NilSpeakerPreservesBehavior asserts that when Speaker is nil
// (the default configuration), no question_audio event is ever emitted, and the interview
// proceeds purely on text as before.
func TestQuestionSpeaker_NilSpeakerPreservesBehavior(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"Where do they live?\n[[filled: hero]]",
	}
	h, chat, _, broker := newHarness(t, script)
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	close(gate)

	q1 := waitEvent(t, s, "question")
	if got := int(q1["turn"].(float64)); got != 1 {
		t.Fatalf("q1 turn = %d, want 1", got)
	}

	// Assert no question_audio event follows when Speaker is nil.
	assertNoEvent(t, s, 50*time.Millisecond)

	// Answering also works without question_audio.
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "A friendly dragon"})
	if code != 202 {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}

	q2 := waitEvent(t, s, "question")
	if got := int(q2["turn"].(float64)); got != 3 {
		t.Fatalf("q2 turn = %d, want 3", got)
	}

	assertNoEvent(t, s, 50*time.Millisecond)
}

// TestQuestionSpeaker_SuccessEmitsQuestionAudio asserts that when Speaker succeeds:
// 1. question event arrives immediately (text never waits on audio).
// 2. question_audio event arrives in parallel with {"turn": N, "audio_url": "/media/<id>"}.
// 3. The context passed to SynthesizeQuestion has a detached timeout.
func TestQuestionSpeaker_SuccessEmitsQuestionAudio(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"What do they want?\n[[filled: hero]]",
	}
	speaker := &fakeSpeaker{mediaID: "media-clip-001"}
	h, chat, _, broker := newHarnessWithSpeaker(t, script, speaker)
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	close(gate)

	// 1. question arrives first.
	q1 := waitEvent(t, s, "question")
	if got := int(q1["turn"].(float64)); got != 1 {
		t.Fatalf("q1 turn = %d, want 1", got)
	}
	if got := q1["text"].(string); got != "Who is your hero?" {
		t.Fatalf("q1 text = %q, want %q", got, "Who is your hero?")
	}

	// 2. question_audio arrives next.
	qa1 := waitEvent(t, s, "question_audio")
	if got := int(qa1["turn"].(float64)); got != 1 {
		t.Fatalf("qa1 turn = %d, want 1", got)
	}
	if got := qa1["audio_url"].(string); got != "/media/media-clip-001" {
		t.Fatalf("qa1 audio_url = %q, want /media/media-clip-001", got)
	}

	// Verify detached timeout on speaker context.
	deadline, hasDead := speaker.getDeadlineInfo()
	if !hasDead {
		t.Fatal("expected SynthesizeQuestion context to have a deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 10*time.Second || remaining > 31*time.Second {
		t.Fatalf("expected ~30s timeout on detached context, got remaining = %v", remaining)
	}

	// Answer the first question.
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "A girl named Alice"})
	if code != 202 {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}

	q2 := waitEvent(t, s, "question")
	if got := int(q2["turn"].(float64)); got != 3 {
		t.Fatalf("q2 turn = %d, want 3", got)
	}

	qa2 := waitEvent(t, s, "question_audio")
	if got := int(qa2["turn"].(float64)); got != 3 {
		t.Fatalf("qa2 turn = %d, want 3", got)
	}
	if got := qa2["audio_url"].(string); got != "/media/media-clip-001" {
		t.Fatalf("qa2 audio_url = %q, want /media/media-clip-001", got)
	}

	if n := speaker.callCount(); n != 2 {
		t.Fatalf("speaker called %d times, want 2", n)
	}
	if call0 := speaker.call(0); call0 != "Who is your hero?" {
		t.Errorf("speaker call 0 text = %q, want %q", call0, "Who is your hero?")
	}
	if call1 := speaker.call(1); call1 != "What do they want?" {
		t.Errorf("speaker call 1 text = %q, want %q", call1, "What do they want?")
	}
}

// TestQuestionSpeaker_ErrorEmitsNothingAndInterviewStaysHealthy asserts that when Speaker
// returns an error (e.g. upstream 503), question arrives immediately, NO question_audio
// event is emitted, NO error event is emitted, and the interview remains open and healthy.
func TestQuestionSpeaker_ErrorEmitsNothingAndInterviewStaysHealthy(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"Where are they going?\n[[filled: hero]]",
	}
	speaker := &fakeSpeaker{err: errUpstream503}
	h, chat, _, broker := newHarnessWithSpeaker(t, script, speaker)
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	close(gate)

	// Question arrives immediately.
	q1 := waitEvent(t, s, "question")
	if got := int(q1["turn"].(float64)); got != 1 {
		t.Fatalf("q1 turn = %d, want 1", got)
	}

	// No question_audio or error event emitted.
	assertNoEvent(t, s, 60*time.Millisecond)

	// Interview remains healthy: answer can be posted successfully.
	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "A bold explorer"})
	if code != 202 {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}
	if body["status"] != statusOpen {
		t.Fatalf("interview status = %v, want open", body["status"])
	}

	// Next question still arrives.
	q2 := waitEvent(t, s, "question")
	if got := int(q2["turn"].(float64)); got != 3 {
		t.Fatalf("q2 turn = %d, want 3", got)
	}

	// Still no question_audio or error event.
	assertNoEvent(t, s, 60*time.Millisecond)
}

// TestQuestionSpeaker_EmptyMediaIDEmitsNothing asserts that when Speaker returns
// an empty media ID without an error, no question_audio event is published.
func TestQuestionSpeaker_EmptyMediaIDEmitsNothing(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
	}
	speaker := &fakeSpeaker{mediaID: ""}
	h, chat, _, broker := newHarnessWithSpeaker(t, script, speaker)
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	close(gate)

	waitEvent(t, s, "question")
	assertNoEvent(t, s, 60*time.Millisecond)
}

// TestTurn_FullChecklistPublishesEndedWithoutQuestion pins the terminal
// transition: an ordinary model sentence that fills the last checklist slot
// must not briefly publish an answerable question or synthesize its audio.
func TestTurn_FullChecklistPublishesEndedWithoutQuestion(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"Do you think Pip should celebrate at sunset.\n[[filled: hero, companion, want, obstacle, turn, ending]]",
	}
	speaker := &fakeSpeaker{mediaID: "media-final-q"}
	h, chat, _, broker := newHarnessWithSpeaker(t, script, speaker)
	chat.mu.Lock()
	chat.blockFirst = 1
	chat.gate = make(chan struct{})
	chat.mu.Unlock()
	gate := chat.gate

	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	close(gate)
	waitEvent(t, s, "question")
	waitEvent(t, s, "question_audio")

	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Pip"})
	if code != http.StatusAccepted {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}

	ended := waitEvent(t, s, "ended")
	if got := ended["reason"]; got != ReasonChecklist {
		t.Fatalf("ended reason = %v, want %q", got, ReasonChecklist)
	}
	assertNoEvent(t, s, 60*time.Millisecond)
	if got := speaker.callCount(); got != 1 {
		t.Fatalf("speaker calls = %d, want only the initial question", got)
	}
}

// TestTurn_ClosingPhrasingWithoutModelEndFinishesCleanly verifies that when the model
// outputs farewell/closing phrasing without a bare "end" token in the control line,
// the server treats it as an ended interview cleanly, publishing "ended" and clearing
// the active question.
func TestTurn_ClosingPhrasingWithoutModelEndFinishesCleanly(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"Thanks for the great story, Leopold is going to be such a special character. I'll go make your book now!\n[[filled: hero]]",
	}
	h, _, _, broker := newHarness(t, script)
	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	q1 := waitEvent(t, s, "question")
	if int(q1["turn"].(float64)) != 1 {
		t.Fatalf("q1 turn = %v, want 1", q1["turn"])
	}

	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Leopold"})
	if code != 202 {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}

	endedEv := waitEvent(t, s, "ended")
	if got := endedEv["reason"]; got != ReasonChecklist {
		t.Fatalf("ended reason = %v, want %q", got, ReasonChecklist)
	}
	wantText := "Thanks for the great story, Leopold is going to be such a special character. I'll go make your book now!"
	if got := endedEv["text"]; got != wantText {
		t.Fatalf("ended text = %q, want %q", got, wantText)
	}

	tr, err := h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if tr.Status != statusEnded {
		t.Fatalf("transcript status = %q, want ended", tr.Status)
	}
	if tr.Current != nil {
		t.Fatalf("transcript Current = %+v, want nil on ended interview", tr.Current)
	}
	lastTurn := tr.Turns[len(tr.Turns)-1]
	if lastTurn.Role != RoleClosing || lastTurn.Text != wantText {
		t.Fatalf("last turn = %+v, want RoleClosing with farewell text", lastTurn)
	}

	code, _ = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "more"})
	if code != 409 {
		t.Fatalf("subsequent answer status = %d, want 409 conflict", code)
	}
}

// TestTurn_QuestionContainingFarewellWordDoesNotEnd pins that a farewell word
// inside an ordinary question does not suppress the answer the child owes.
func TestTurn_QuestionContainingFarewellWordDoesNotEnd(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"Tell me what Pip says when waving goodbye.\n[[filled:]]",
	}
	h, _, _, broker := newHarness(t, script)
	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != http.StatusCreated {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)
	s := sub(t, broker, id)
	waitEvent(t, s, "question")

	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Pip"})
	if code != http.StatusAccepted {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}
	question := waitEvent(t, s, "question")
	if got := question["text"]; got != "Tell me what Pip says when waving goodbye." {
		t.Fatalf("question text = %q, want the farewell-word question", got)
	}

	tr, err := h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if tr.Status != statusOpen || tr.Current == nil {
		t.Fatalf("transcript = %+v, want open interview with an answerable question", tr)
	}
}

// TestTurn_ModelEndFinishesCleanly verifies that when the model includes "; end" in the
// control line, the turn finishes cleanly, emitting "ended" and clearing Current.
func TestTurn_ModelEndFinishesCleanly(t *testing.T) {
	script := []string{
		"Who is your hero?\n[[filled:]]",
		"That was wonderful!\n[[filled: hero; end]]",
	}
	h, _, _, broker := newHarness(t, script)
	srv := serve(t, muxFor(t, h))

	code, body := postJSON(t, srv.URL+"/interviews", nil)
	if code != 201 {
		t.Fatalf("start status = %d (%v), want 201", code, body)
	}
	id := body["id"].(string)

	s := sub(t, broker, id)
	waitEvent(t, s, "question")

	code, body = postJSON(t, srv.URL+"/interviews/"+id+"/answers", map[string]string{"text": "Alice"})
	if code != 202 {
		t.Fatalf("answer status = %d (%v), want 202", code, body)
	}

	endedEv := waitEvent(t, s, "ended")
	if got := endedEv["reason"]; got != ReasonChecklist {
		t.Fatalf("ended reason = %v, want %q", got, ReasonChecklist)
	}

	tr, err := h.transcript(t.Context(), id)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if tr.Status != statusEnded {
		t.Fatalf("transcript status = %q, want ended", tr.Status)
	}
	if tr.Current != nil {
		t.Fatalf("transcript Current = %+v, want nil on ended interview", tr.Current)
	}
}
