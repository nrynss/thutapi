package interview

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"thutapi/internal/store"
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
	// Opening is a best-effort replay when the opening job completed before
	// this response was encoded. GET /interviews carries the same replay for
	// the normal asynchronous ordering.
	Opening *currentQuestionJSON `json:"opening,omitempty"`
}

// startRequest is question zero, the optional book byline collected by the
// UI. It is attribution rather than story material, so M3 never receives it.
type startRequest struct {
	Byline string `json:"byline"`
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

// currentQuestionJSON is the latest interviewer question, including the
// metadata that a late subscriber needs to render the tap-first opening.
// It is separate from transcript turns because chips and question audio are
// event metadata rather than model conversation content.
type currentQuestionJSON struct {
	Turn     int      `json:"turn"`
	Text     string   `json:"text"`
	Chips    []string `json:"chips"`
	AudioURL string   `json:"audio_url,omitempty"`
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
	ID        string               `json:"id"`
	BookID    string               `json:"book_id"`
	CreatedAt time.Time            `json:"created_at"`
	Status    string               `json:"status"`
	Filled    []string             `json:"filled"`
	Error     string               `json:"error,omitempty"`
	Turns     []turnJSON           `json:"turns"`
	Current   *currentQuestionJSON `json:"current,omitempty"`
}

// start runs the start path: create the book row (working title —
// Phase B authors the real one), the interview row, link them, and
// launch the opening-question turn. The request returns before M3
// answers; the question arrives as the first event on the topic.
func (h *Handler) start(ctx context.Context) (startResponse, error) {
	return h.startWithByline(ctx, "")
}

// startWithByline records question zero on the book before the opening turn
// begins. An empty byline is normal and leaves the title card un-attributed.
func (h *Handler) startWithByline(ctx context.Context, byline string) (startResponse, error) {
	book, err := h.store.CreateBook(ctx, WorkingTitle)
	if err != nil {
		return startResponse{}, fmt.Errorf("interview: start: create book: %w", err)
	}
	if byline != "" {
		book.Byline = byline
		if err := h.store.UpdateBook(ctx, book); err != nil {
			return startResponse{}, fmt.Errorf("interview: start: set byline: %w", err)
		}
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
	s.mu.Lock()
	opening := currentQuestionFor(s)
	s.mu.Unlock()
	return startResponse{
		ID:      iv.ID,
		BookID:  book.ID,
		Topic:   Topic(iv.ID),
		Events:  "/interviews/" + iv.ID + "/events",
		Status:  statusOpen,
		Opening: opening,
	}, nil
}

// askedTheSameQuestionTwice reports whether the interviewer's last two
// questions were the same. turns is the transcript as it stands BEFORE the
// answer being judged is appended, so its last turn is the question that
// answer replies to and the one two questions back sits two turns earlier.
//
// It exists because the repeat rule reads a repeated answer as a child who
// has stopped trying. That is only true when the child was asked something
// new. When the model asks "what is your name?" twice — which it does — the
// child types the same name twice, which is not a stall but a correct
// answer given twice, and ending the interview for it is the defect this
// guards.
func askedTheSameQuestionTwice(turns []store.Turn) bool {
	var questions []string
	for i := len(turns) - 1; i >= 0 && len(questions) < 2; i-- {
		if turns[i].Role == RoleInterviewer {
			questions = append(questions, normalizeChip(turns[i].Text))
		}
	}
	return len(questions) == 2 && questions[0] != "" && questions[0] == questions[1]
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
	// A repeated answer is only no-progress when the QUESTION moved on. When
	// the interviewer asks the same thing twice — which it does — answering
	// it the same way twice is the correct thing for a child to do, not a
	// stall, and ending the interview for it is how a real session died on
	// 2026-09-06 after two exchanges. See askedTheSameQuestionTwice.
	repeated := low && !chip && norm != "" && norm == s.lastAnswer && !askedTheSameQuestionTwice(iv.Turns)
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
	current := currentQuestionFor(s)
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
		Current:   current,
	}, nil
}

func currentQuestionFor(s *session) *currentQuestionJSON {
	if s.current == nil {
		return nil
	}
	return &currentQuestionJSON{
		Turn:     s.current.turn,
		Text:     s.current.text,
		Chips:    append([]string(nil), s.current.chips...),
		AudioURL: s.current.audioURL,
	}
}

// Start handles POST /interviews.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		h.writeError(w, fmt.Errorf("interview: start: %w: %w", ErrBadBody, err))
		return
	}
	res, err := h.startWithByline(r.Context(), strings.TrimSpace(req.Byline))
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
