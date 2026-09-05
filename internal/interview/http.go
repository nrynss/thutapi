package interview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Interview lifecycle states surfaced in HTTP bodies. The SSE "ended"
// event is the terminal signal on the stream; these strings are their
// request/response counterparts.
const (
	statusOpen   = "open"
	statusEnding = "ending"
	statusEnded  = "ended"
)

// startResponse is the body of a 201 answer to POST /interviews.
type startResponse struct {
	ID     string `json:"id"`
	BookID string `json:"book_id"`
	Topic  string `json:"topic"`
	Events string `json:"events_url"`
	Status string `json:"status"`
}

// answerResponse is the body of a 202 answer to POST
// /interviews/{id}/answers. Status "open" means a question turn is
// running; "ending" means the goodbye is. Either way the next event on
// the topic is what the client waits for.
type answerResponse struct {
	Topic  string `json:"topic"`
	Status string `json:"status"`
}

// answerRequest is the body of POST /interviews/{id}/answers.
type answerRequest struct {
	Text string `json:"text"`
}

// turnJSON is one transcript turn as the transcript route serves it.
type turnJSON struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// transcriptResponse is the body of GET /interviews/{id} — the
// authoritative catch-up state (subscribe to the events topic first,
// then read this; deduplicate "question" events on Turn). Error is
// the machine class of the last turn failure nothing has yet retried
// ("" when the last turn succeeded): the SSE "error" event cannot
// reach a client that had no way to subscribe when it fired — the
// opening question can fail before the start response has delivered
// the id — so the catch-up state carries the failure until the next
// turn starts. Status stays "open": the interview is recoverable, the
// child may simply answer again.
type transcriptResponse struct {
	ID        string     `json:"id"`
	BookID    string     `json:"book_id"`
	CreatedAt time.Time  `json:"created_at"`
	Status    string     `json:"status"`
	Filled    []string   `json:"filled"`
	Error     string     `json:"error,omitempty"`
	Turns     []turnJSON `json:"turns"`
}

// start runs the start path: create the book row (working title —
// Phase B authors the real one), the interview row, link them, and
// launch the opening-question turn. The request returns before M3
// answers; the question arrives as the first event on the topic.
func (h *Handler) start(ctx context.Context) (startResponse, error) {
	book, err := h.store.CreateBook(ctx, WorkingTitle)
	if err != nil {
		return startResponse{}, fmt.Errorf("interview: start: create book: %w", err)
	}
	iv, err := h.store.CreateInterview(ctx)
	if err != nil {
		return startResponse{}, fmt.Errorf("interview: start: create interview: %w", err)
	}
	iv.BookID = book.ID
	if err := h.store.UpdateInterview(ctx, iv); err != nil {
		return startResponse{}, fmt.Errorf("interview: start %s: link book: %w", iv.ID, err)
	}

	s := h.sessionFor(iv)
	s.mu.Lock()
	s.inFlight = true
	s.mu.Unlock()

	err = h.startTurn(ctx, iv.ID, func(tctx context.Context) {
		h.runTurn(tctx, iv.ID, false, "")
	})
	if err != nil {
		s.mu.Lock()
		s.inFlight = false
		s.mu.Unlock()
		return startResponse{}, fmt.Errorf("interview: start %s: opening turn: %w", iv.ID, err)
	}
	return startResponse{
		ID:     iv.ID,
		BookID: book.ID,
		Topic:  Topic(iv.ID),
		Events: "/interviews/" + iv.ID + "/events",
		Status: statusOpen,
	}, nil
}

// answer runs the answer path: validate, detect stalls against the
// offered chips, persist the child's turn, and launch the question or
// goodbye turn. The store write happens BEFORE the turn starts, so a
// crash mid-turn never loses the child's words.
func (h *Handler) answer(ctx context.Context, id, answer string) (answerResponse, error) {
	if strings.TrimSpace(answer) == "" {
		return answerResponse{}, ErrEmptyAnswer
	}
	iv, err := h.store.Interview(ctx, id)
	if err != nil {
		return answerResponse{}, fmt.Errorf("interview: answer %s: %w", id, err)
	}
	s := h.sessionFor(iv)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return answerResponse{}, fmt.Errorf("interview: answer %s: %w", id, ErrEnded)
	}
	if s.inFlight || s.ending {
		return answerResponse{}, fmt.Errorf("interview: answer %s: %w", id, ErrBusy)
	}

	// Stall bookkeeping: a low-effort answer advances the streak
	// unless it tapped one of the chips the last question offered —
	// a chip answer is a real answer even though it is one word.
	// The streak alone never ends anything: it is signalled to the
	// model with the next Chat call (stallDirective) so the model can
	// offer chips while the child is still answering. The end fires
	// only on no-progress — the same stall twice in a row, or the
	// same one-word answer repeated (PLAN.md §T4: "a stall or a
	// repeated one-word answer ends it early"). A substantive
	// one-word answer ("Mira", "red") is a real answer: it raises the
	// chip signal and nothing else.
	chip := matchesChip(s.chips, answer)
	low := lowEffort(answer)
	norm := normalizeChip(answer)
	stalled := !chip && stallAnswer(answer)
	repeated := low && !chip && norm != "" && norm == s.lastAnswer
	streak0, stallStreak0, lastAnswer0 := s.streak, s.stallStreak, s.lastAnswer
	if low && !chip {
		s.streak++
	} else {
		s.streak = 0
	}
	if stalled {
		s.stallStreak++
	} else {
		s.stallStreak = 0
	}
	s.lastAnswer = norm

	// The server's end decision, made here so no further answer is
	// accepted from this moment (PLAN.md §T4: a stall or a repeated
	// one-word answer ends it early; MaxTurns backstops the budget).
	endNow, reason := false, ""
	switch {
	case s.stallStreak >= 2 || repeated:
		endNow, reason = true, ReasonStall
	case s.exchanges >= h.maxTurns:
		endNow, reason = true, ReasonLimit
	}

	iv, err = appendChildTurn(ctx, h.store, iv, answer)
	if err != nil {
		// The answer never entered the transcript: its streak point
		// must not either — a rolled-back answer rolls its stall
		// bookkeeping back with it.
		s.streak, s.stallStreak, s.lastAnswer = streak0, stallStreak0, lastAnswer0
		return answerResponse{}, fmt.Errorf("interview: answer %s: %w", id, err)
	}

	s.exchanges++
	s.inFlight = true
	s.ending = endNow
	ending, closeReason := endNow, reason
	err = h.startTurn(ctx, id, func(tctx context.Context) {
		h.runTurn(tctx, id, ending, closeReason)
	})
	if err != nil {
		// No turn will answer this child — undo the transcript write
		// and release the interview. We hold s.mu, so there is no
		// concurrent writer to race the rollback. The answer's
		// streak point rolls back with it.
		s.inFlight = false
		s.ending = false
		s.exchanges--
		s.streak, s.stallStreak, s.lastAnswer = streak0, stallStreak0, lastAnswer0
		if derr := dropLastTurn(ctx, h.store, iv); derr != nil {
			h.log.Error("interview: answer rollback failed", "id", id, "rollback_err", derr)
		}
		return answerResponse{}, fmt.Errorf("interview: answer %s: start turn: %w", id, err)
	}
	// The turn is running again: a recorded earlier failure is stale
	// — the catch-up state reports only a failure nothing has yet
	// retried. A turn that then fails records itself, as ever.
	s.turnErr = ""

	status := statusOpen
	if endNow {
		status = statusEnding
	}
	return answerResponse{Topic: Topic(id), Status: status}, nil
}

// transcript assembles the authoritative catch-up state: the persisted
// transcript plus the in-process status, checklist and recorded turn
// failure.
func (h *Handler) transcript(ctx context.Context, id string) (transcriptResponse, error) {
	iv, err := h.store.Interview(ctx, id)
	if err != nil {
		return transcriptResponse{}, fmt.Errorf("interview: transcript %s: %w", id, err)
	}
	s := h.sessionFor(iv)
	s.mu.Lock()
	ended, filled, turnErr := s.ended, s.filled.filled(), s.turnErr
	s.mu.Unlock()

	status := statusOpen
	if ended {
		status = statusEnded
	}
	turns := make([]turnJSON, len(iv.Turns))
	for i, t := range iv.Turns {
		turns[i] = turnJSON{Role: t.Role, Text: t.Text}
	}
	return transcriptResponse{
		ID:        iv.ID,
		BookID:    iv.BookID,
		CreatedAt: iv.CreatedAt,
		Status:    status,
		Filled:    filled,
		Error:     turnErr,
		Turns:     turns,
	}, nil
}

// Start handles POST /interviews.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	res, err := h.start(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// Answer handles POST /interviews/{id}/answers.
func (h *Handler) Answer(w http.ResponseWriter, r *http.Request) {
	var req answerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, fmt.Errorf("interview: answer: %w: %w", ErrBadBody, err))
		return
	}
	res, err := h.answer(r.Context(), r.PathValue("id"), req.Text)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

// Transcript handles GET /interviews/{id}.
func (h *Handler) Transcript(w http.ResponseWriter, r *http.Request) {
	res, err := h.transcript(r.Context(), r.PathValue("id"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// Events handles GET /interviews/{id}/events: the interview's SSE
// stream, served by internal/stream (headers, flushing and heartbeats
// are its contract). Unknown ids are a 404 before the stream opens.
func (h *Handler) Events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.Interview(r.Context(), id); err != nil {
		h.writeError(w, err)
		return
	}
	h.broad.ServeTopic(w, r, Topic(id))
}

// writeJSON writes one JSON body. An encode failure after the headers
// are gone is a dropped connection, not an application error — there
// is no status left to write.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The request is dead; nothing useful can be reported.
		return
	}
}

// writeError maps an error to its HTTP status by machine class
// (errClass; PLAN.md invariant 8) and writes the class as the body's
// "error" field. The full prose goes to the log and to nothing a
// client sees — T9 renders failures warmly and branches on the class
// (PLAN.md §T9).
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	class := errClass(err)
	h.log.Error("interview: request failed", "class", class, "err", err)
	writeJSON(w, statusForClass(class), errorEvent{Error: class})
}

// statusForClass is the HTTP status of each machine class: 400 for a
// bad request, 409 for ended/busy conflicts, 404 for an unknown id,
// 500 for everything else.
func statusForClass(class string) int {
	switch class {
	case classInvalid:
		return http.StatusBadRequest
	case classEnded, classBusy:
		return http.StatusConflict
	case classNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}
