package interview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/text"
	"thutapi/internal/store"
	"thutapi/internal/stream"
)

// questionEvent is the data of the "question" SSE event — the
// interviewer's next question plus everything T9 needs to render it
// (the chips to tap, the checklist for the progress display, and the
// turn number to deduplicate catch-up against).
type questionEvent struct {
	Turn      int      `json:"turn"`
	Text      string   `json:"text"`
	Chips     []string `json:"chips"`
	Filled    []string `json:"filled"`
	Exchanges int      `json:"exchanges"`
}

// questionAudioEvent is the data of the "question_audio" SSE event
// published when TTS synthesis for a question finishes in the background.
type questionAudioEvent struct {
	Turn     int    `json:"turn"`
	AudioURL string `json:"audio_url"`
}

// endedEvent is the data of the terminal "ended" SSE event. Reason is
// one of the Reason constants; Text is always a spoken goodbye — the
// model's, or fallbackGoodbye when the ending path arrived without
// model text.
type endedEvent struct {
	Reason string   `json:"reason"`
	Text   string   `json:"text,omitempty"`
	Filled []string `json:"filled"`
}

// Machine classes — the only error strings this package ever puts on
// the wire. Every "error" SSE event and every error response body
// carries exactly one of these tokens (PLAN.md §T9: T9 branches on
// the class and renders the failure warmly; freeform prose gives it
// nothing to branch on). The full internal error chain goes to the
// log and never to a client.
const (
	classInvalid  = "invalid"   // the request was malformed or empty
	classEnded    = "ended"     // the interview has ended
	classBusy     = "busy"      // a turn is already in flight
	classNotFound = "not_found" // unknown interview id
	classInternal = "internal"  // store, model or job failure — anything else
)

// errClass maps err to one of the machine classes above, by sentinel
// (PLAN.md invariant 8). It is the single source of both the wire
// token and — via writeError — the HTTP status.
func errClass(err error) string {
	switch {
	case errors.Is(err, ErrEmptyAnswer), errors.Is(err, ErrBadBody):
		return classInvalid
	case errors.Is(err, ErrEnded):
		return classEnded
	case errors.Is(err, ErrBusy):
		return classBusy
	case errors.Is(err, store.ErrNotFound):
		return classNotFound
	default:
		return classInternal
	}
}

// errorEvent is the data of the "error" SSE event: a turn failed, the
// interview is still open, and the child may answer again (their
// answer is already in the transcript). Error is one of the machine
// classes above — "invalid", "ended", "busy", "not_found",
// "internal" — never prose; the human-readable chain is logged
// instead. Rendering the failure warmly is T9's job.
type errorEvent struct {
	Error string `json:"error"`
}

// publish marshals v and publishes it as one SSE event on the
// interview's topic. Every payload here is a struct of JSON-safe
// fields, so a marshal failure is unreachable; if it ever fired, the
// failure is logged rather than published as an empty event.
func (h *Handler) publish(id, name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		h.log.Error("interview: marshal event", "id", id, "event", name, "err", err)
		return
	}
	h.broad.Publish(Topic(id), stream.Event{Name: name, Data: string(data)})
}

// fail publishes an "error" event and logs the full prose. The
// interview stays open on every path that calls this; the event
// carries only the machine class (errClass).
func (h *Handler) fail(id string, err error) {
	h.log.Error("interview: turn failed", "id", id, "err", err)
	h.publish(id, "error", errorEvent{Error: errClass(err)})
}

// startTurn launches one turn on the job Runner: body is the turn work
// proper, run with the Runner-owned context (not cancelled when the
// starting request ends — invariant 6). The bool result of the job is
// always nil: the turn's product is the persisted turn and the SSE
// event, not a job payload.
func (h *Handler) startTurn(ctx context.Context, id string, body func(ctx context.Context)) error {
	_, err := h.jobs.Start(ctx, func(jobCtx context.Context, _ func(string)) ([]byte, error) {
		body(jobCtx)
		return nil, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// runTurn is one Chat exchange: read the transcript (which already
// ends with the child's answer when this is an answer turn), call M3
// with no thinking, parse the control line, persist the interviewer's
// turn, publish the event, and run the end decision. ending/reason
// carry the server's prior decision (stall and limit endings set
// them; the ordinary path passes false/""). Every failure path
// publishes an error event and leaves the interview open — a failed
// goodbye also reopens `ending`, so the child's next answer can
// re-trigger the close.
func (h *Handler) runTurn(ctx context.Context, id string, ending bool, reason string) {
	iv, err := h.store.Interview(ctx, id)
	if err != nil {
		h.fail(id, fmt.Errorf("interview: turn %s: read transcript: %w", id, err))
		return
	}
	s := h.sessionFor(iv)
	// The low-effort streak rides with this call (stallDirective) so
	// the model can offer chips while the child is still answering.
	// answer() raised it under s.mu before starting this turn, so the
	// lock read below sees the answer's own contribution.
	s.mu.Lock()
	streak := s.streak
	s.mu.Unlock()

	resp, err := h.chat.Chat(ctx, text.ChatRequest{
		Model:    ModelID,
		Messages: buildMessages(iv.Turns, ending, streak),
		// Thinking stays nil: reasoning is OFF for every interview
		// call (project.md §M3 phase settings) — pinned on the raw
		// wire by TestInterviewTurnsOmitThinkingOnRawWire.
	})
	if err != nil {
		h.turnFailed(s, id, err)
		return
	}
	txt, err := assistantText(resp)
	if err != nil {
		h.turnFailed(s, id, fmt.Errorf("interview: turn %s: %w", id, err))
		return
	}
	rep := parseReply(txt)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.filled.union(rep.Slots)
	s.inFlight = false

	// A full checklist is deliberately NOT an end condition here. A
	// reply that fills the last slot without the model saying "end"
	// still goes through questionTurn below, which publishes the
	// question and then enforces the close — the documented behaviour
	// (the goodbye path is for a model "end" or a server decision
	// made in answer).
	endNow := ending || rep.End
	if !endNow {
		if rep.Text == "" {
			// A reply that is only a control line gives the child
			// nothing — treat it like a failed turn.
			h.failLocked(s, id, fmt.Errorf("interview: turn %s: %w: reply had no text", id, gmi.ErrTransient))
			return
		}
		h.questionTurn(ctx, s, iv, rep)
		return
	}
	// Which reason: a server decision names itself; a model "end"
	// (with or without a full checklist) is the checklist rule — the
	// model is instructed to end only when the list is full or the
	// child is tiring.
	if !ending {
		reason = ReasonChecklist
	}
	h.closeTurn(ctx, s, iv, rep, reason)
}

// questionTurn persists and publishes an ordinary question. Caller
// holds s.mu.
func (h *Handler) questionTurn(ctx context.Context, s *session, iv store.Interview, rep reply) {
	iv.Turns = append(iv.Turns, store.Turn{Role: RoleInterviewer, Text: rep.Text})
	if err := h.store.UpdateInterview(ctx, iv); err != nil {
		h.failLocked(s, iv.ID, fmt.Errorf("interview: turn %s: persist question: %w", iv.ID, err))
		return
	}
	s.chips = rep.Chips
	turnN := len(iv.Turns)
	h.publish(iv.ID, "question", questionEvent{
		Turn:      turnN,
		Text:      rep.Text,
		Chips:     emptyWhenNil(rep.Chips),
		Filled:    s.filled.filled(),
		Exchanges: s.exchanges,
	})
	if h.speaker != nil {
		speaker := h.speaker
		id := iv.ID
		text := rep.Text
		go func() {
			synthCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			mediaID, err := speaker.SynthesizeQuestion(synthCtx, text)
			if err != nil {
				h.log.Warn("interview: synthesize question audio failed", "id", id, "turn", turnN, "err", err)
				return
			}
			if mediaID == "" {
				h.log.Warn("interview: synthesize question audio returned empty media ID", "id", id, "turn", turnN)
				return
			}
			h.publish(id, "question_audio", questionAudioEvent{
				Turn:     turnN,
				AudioURL: "/media/" + mediaID,
			})
		}()
	}
	// Server-side enforcement: the checklist filled but the model did
	// not say "end". The question still lands (it is good transcript
	// content and often a confirmation); the interview ends right
	// behind it with no further answer accepted. The close goes
	// through closeTurn with the model's text absent, so the closing
	// turn (and with it the restart-stable end) is persisted exactly
	// as on every other end path.
	if s.filled.full() {
		h.closeTurn(ctx, s, iv, reply{}, ReasonChecklist)
	}
}

// closeTurn persists and publishes the goodbye of an ended interview.
// An ending path that arrived without model text — an end-only
// control line, a wrap-up call that returned none, the server-side
// checklist enforcement — still persists a closing turn, carrying
// fallbackGoodbye so the ended interview is restart-stable and the
// child always gets a spoken goodbye, never a bare stop. Caller holds
// s.mu.
func (h *Handler) closeTurn(ctx context.Context, s *session, iv store.Interview, rep reply, reason string) {
	text := rep.Text
	if text == "" {
		text = fallbackGoodbye
	}
	iv.Turns = append(iv.Turns, store.Turn{Role: RoleClosing, Text: text})
	if err := h.store.UpdateInterview(ctx, iv); err != nil {
		h.failLocked(s, iv.ID, fmt.Errorf("interview: turn %s: persist closing: %w", iv.ID, err))
		return
	}
	s.ended = true
	s.ending = false
	h.publish(iv.ID, "ended", endedEvent{Reason: reason, Text: text, Filled: s.filled.filled()})
}

// turnFailed records a failed turn: release the turn slot, reopen a
// decided-but-undelivered goodbye (the child's next answer re-triggers
// the close — see the package doc), record the failure on the
// catch-up state (a late subscriber reads it from the transcript
// route; the SSE event cannot reach a client that was never able to
// subscribe), and publish the error event.
func (h *Handler) turnFailed(s *session, id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFlight = false
	s.ending = false
	s.turnErr = errClass(err)
	h.fail(id, err)
}

// failLocked is turnFailed's recording plus fail, for callers already
// holding s.mu.
func (h *Handler) failLocked(s *session, id string, err error) {
	s.ending = false
	s.turnErr = errClass(err)
	h.fail(id, err)
}

// emptyWhenNil normalises an absent chip list for the wire: T9 gets []
// rather than null.
func emptyWhenNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// appendChildTurn persists the child's answer onto the transcript
// before the Chat call runs (crash-safe: the typing is never lost).
func appendChildTurn(ctx context.Context, st interviewStore, iv store.Interview, answer string) (store.Interview, error) {
	iv.Turns = append(iv.Turns, store.Turn{Role: RoleChild, Text: answer})
	if err := st.UpdateInterview(ctx, iv); err != nil {
		return iv, fmt.Errorf("interview: persist answer: %w", err)
	}
	return iv, nil
}

// dropLastTurn undoes appendChildTurn when the turn job could not be
// started at all (the store must not keep an answer nothing will ever
// respond to). Only ever called with the session lock held, so there
// is no concurrent writer to race.
func dropLastTurn(ctx context.Context, st interviewStore, iv store.Interview) error {
	iv.Turns = iv.Turns[:len(iv.Turns)-1]
	if err := st.UpdateInterview(ctx, iv); err != nil {
		return fmt.Errorf("interview: rollback answer: %w", err)
	}
	return nil
}
