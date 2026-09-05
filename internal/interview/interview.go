// Package interview runs Thutapi's Phase-A interview loop (PLAN.md
// §T4): M3 asks a child short questions, one at a time, and the loop
// ends itself when the story checklist fills or the child tires.
//
// # The turn protocol
//
// The house style (one concrete question per turn; binary chips when
// the child stalls; accept everything) is a system-prompt concern —
// see prompt.go. What the loop needs beyond prose is a way for the
// model to report checklist progress and chips. project.md does not
// specify a wire shape for that, so this package defines one, the
// control line: the model ends every reply with ONE final line of the
// form
//
//	[[filled: <slot, slot>; chips: <option, option>; end]]
//
// Fields are semicolon-separated, labels case-insensitive, all
// optional. "filled" lists checklist slots the model now considers
// filled (cumulative — ALL filled slots every turn, not just new
// ones); "chips" lists the tap-options offered with this question
// (T9 renders them as buttons); the bare token "end" says this reply
// is a goodbye, not a question. parseReply strips the line from the
// child-visible text and parses it leniently: a missing or malformed
// control line degrades to "plain text, no state change", never to an
// error. The system prompt pins the format; the reporting is
// cumulative so checklist state survives a process restart with no
// store schema change — the very next model reply re-asserts the full
// filled set.
//
// The checklist itself lives server-side (session, below): the loop
// unions each turn's report into persistent state and ends the
// interview when all six slots — hero, companion, want, obstacle,
// turn, ending — are filled, when the model itself says "end", or
// when the stall rules fire. The child's answers going short or stuck
// raise a streak that is signalled to the model with the next Chat
// call (stallDirective) so the model offers binary chips to tap — a
// short answer is a cue for chips, never an end by itself. The
// interview ends early only on no-progress: two consecutive
// "i dunno"-class stall answers, or the same one-word answer repeated
// ("no" then "no") — PLAN.md §T4's "a stall or a repeated one-word
// answer". An answer matching the chips the question offered counts
// as a real answer. Config.MaxTurns (default 12) backstops the
// "~6-10 exchanges" budget. Endings decided without a just-arrived
// model goodbye (stall, limit) run ONE final Chat call with a wrap-up
// directive so the child still gets a model-authored goodbye; the
// interview accepts no further answers from the moment the decision
// is made.
//
// # Architecture
//
// Each turn — including the opening question — is one job on the
// internal/job Runner: no request blocks on M3 (PLAN.md invariant 6),
// and job.Runner bounds concurrency and recovers panics. The child's
// answer is persisted BEFORE the Chat call, so a crash mid-turn loses
// no typing. The interviewer's question is persisted as a Turn and
// published to the interview's SSE topic. The job's own topic
// (job.Topic) carries the job terminal event for infrastructure; the
// PRODUCT surface — what T9 subscribes to — is this package's topic
// (Topic) alone.
//
// SSE topic: interview.Topic(id). Events (data is JSON):
//
//	event: question
//	{"turn":3,"text":"...","chips":["brave","sneaky"],
//	 "filled":["hero","companion"],"exchanges":2}
//	event: question_audio
//	{"turn":3,"audio_url":"/media/<id>"}
//	event: ended
//	{"reason":"checklist|stall|limit","text":"<goodbye>",
//	 "filled":["hero",...]}
//	event: error
//	{"error":"internal"}

// "question" carries the chips and the checklist for T9's progress
// display; "ended" is terminal — no question follows it, and its text
// is always a spoken goodbye (the model's, or fallbackGoodbye when an
// ending path arrived without model text). The "error" payload's
// error field is exactly one of the machine classes "invalid",
// "ended", "busy", "not_found", "internal" — never prose (the full
// error chain goes to the log); T9 branches on the class and renders
// the failure warmly (PLAN.md §T9: errors are never a dead end).
//
// The stream is from-now-on (internal/stream Subscribe), so a client
// that needs catch-up subscribes FIRST, then GETs the transcript
// (Transcript/GET handler) and deduplicates on "turn". The same
// catch-up read carries a turn failure nothing has yet retried in its
// "error" field — see "# Turn failures" below. An answer that arrives
// while a turn is in flight is rejected with ErrBusy; the client
// simply waits for the question event.
//
// # Turn failures
//
// A turn whose Chat call or persist step fails publishes an "error"
// event, logs the prose, and leaves the interview open — the child's
// answer is already in the transcript, and answering again re-runs
// the turn. The failure is also recorded on the catch-up state (the
// transcript route's "error" field, a machine class) until the next
// turn starts, because the event alone cannot be reliable: the
// opening question can fail before any client has been ABLE to
// subscribe — the start response returns first, and the id it carries
// is the only way to find the topic. A client that reads the recorded
// failure tells the child warmly that the turn stumbled and offers
// the answer box again; nothing else needs to recover.
//
// Restart semantics: transcripts and book links persist (internal/
// store). In-process state (streak counters, in-flight flags, the
// checklist union, a recorded turn failure) does not; a restarted
// Handler rebuilds what it can — "ended" is detected from the
// persisted transcript (the closing turn's role), the checklist
// recovers on the model's next cumulative report, and stall counters
// reset. Every end path — the model's own "end", the stall and limit
// wrap-ups, the server-side checklist enforcement — persists a
// closing turn (fallbackGoodbye when the model supplied no text), so
// an ended interview stays ended across a restart.
package interview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"thutapi/internal/gmi/text"
	"thutapi/internal/job"
	"thutapi/internal/store"
	"thutapi/internal/stream"
)

// Roles the package writes into store.Turn.Role. The store treats the
// role as an opaque string; the vocabulary is this package's (PLAN.md
// §T3), and internal/story (T5) reads it to know who said what.
const (
	// RoleInterviewer marks a question the interviewer asked.
	RoleInterviewer = "interviewer"
	// RoleChild marks an answer the child typed.
	RoleChild = "child"
	// RoleClosing marks the interviewer's goodbye — the final turn of
	// an interview that ended through the primary path (a model "end"
	// or a stall/limit goodbye). A transcript whose last turn carries
	// this role is ended, which is how a restarted Handler detects a
	// finished interview.
	RoleClosing = "closing"
)

// ModelID is the chat model every interview call uses: the full
// "MiniMaxAI/" prefix is required or api.gmi-serving.com 404s (PLAN.md
// §T2). Reasoning stays OFF for interview turns — short conversational
// questions, and a child watching a spinner is a usability failure
// (project.md §M3 phase settings) — so requests carry Thinking: nil
// and the field is absent on the wire (pinned by
// TestInterviewTurnsOmitThinkingOnRawWire).
const ModelID = "MiniMaxAI/MiniMax-M3"

// WorkingTitle is the title of the book row created at start, before
// Phase B (T5) authors the real one via the store's UpdateBook.
const WorkingTitle = "Our story"

// fallbackGoodbye is the closing turn's text when an ending path
// arrived without model text — an end-only control line, a wrap-up
// call that returned none, the server-side checklist enforcement.
// Every end persists a closing turn (the restart-stable end marker),
// and the child always gets a spoken goodbye, never a bare stop.
// Child-facing per the T0 conventions: warm, short, no jargon.
const fallbackGoodbye = "What a lovely story! Let's make your book."

// TopicPrefix namespaces the interview SSE topics, the same convention
// job.TopicPrefix uses for jobs.
const TopicPrefix = "interview:"

// Reason values carried by the "ended" SSE event's "reason" field.
const (
	// ReasonChecklist: the checklist filled (or the model declared the
	// interview complete under the same rule).
	ReasonChecklist = "checklist"
	// ReasonStall: the child's answers stopped making progress — two
	// consecutive stall answers ("i dunno" twice), or the same
	// one-word answer repeated ("no" then "no"). A short answer on
	// its own never fires this; see the package doc.
	ReasonStall = "stall"
	// ReasonLimit: Config.MaxTurns exchanges were processed.
	ReasonLimit = "limit"
)

// defaultMaxTurns is the exchange budget Config{} gets: the spec's
// "roughly 6-10 exchanges" (PLAN.md §T4) with headroom for chip taps.
const defaultMaxTurns = 12

// maxChips caps the chips carried on a question event — a rendering
// bound for T9's tap targets, applied while parsing so every consumer
// sees the same shape.
const maxChips = 4

// QuestionSpeaker is the interview's view of the question audio synthesizer
// (PLAN.md invariant 3: the consumer declares the interface).
type QuestionSpeaker interface {
	SynthesizeQuestion(ctx context.Context, text string) (mediaID string, err error)
}

// QuestionSpeakerFunc adapts a function to the QuestionSpeaker interface.
type QuestionSpeakerFunc func(ctx context.Context, text string) (string, error)

// SynthesizeQuestion calls f(ctx, text).
func (f QuestionSpeakerFunc) SynthesizeQuestion(ctx context.Context, text string) (string, error) {
	return f(ctx, text)
}

// Chatter is the interview's view of the text client (PLAN.md
// invariant 3: consumers declare the interface). It is satisfied by
// *text.Client; tests substitute a fake.
type Chatter interface {
	Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error)
}

// interviewStore is the interview's view of the book store: the
// narrowest surface that can create the book and interview rows and
// keep the transcript ordered. Satisfied by *store.DB.
type interviewStore interface {
	CreateBook(ctx context.Context, title string) (store.Book, error)
	CreateInterview(ctx context.Context) (store.Interview, error)
	Interview(ctx context.Context, id string) (store.Interview, error)
	UpdateInterview(ctx context.Context, iv store.Interview) error
}

// broadcaster is the interview's view of the SSE broker: publish turn
// events to a topic, serve a topic as a stream. Satisfied by
// *stream.Broker.
type broadcaster interface {
	Publish(topic string, event stream.Event)
	ServeTopic(w http.ResponseWriter, r *http.Request, topic string)
}

// jobRunner is the interview's view of the job Runner: runs a turn off
// the request path. Satisfied by *job.Runner.
type jobRunner interface {
	Start(ctx context.Context, fn job.Func) (string, error)
}

// Sentinel errors. Every failure this package produces wraps one of
// these (or one of the store/text sentinels it is only carrying
// through); branch with errors.Is (PLAN.md invariant 8).
var (
	// ErrNotConfigured is returned by New when a required dependency
	// is nil. The error message names the missing one.
	ErrNotConfigured = errors.New("interview: handler not configured")
	// ErrBadBody is returned when an answer request body is not valid
	// JSON. HTTP 400.
	ErrBadBody = errors.New("interview: request body is not valid JSON")
	// ErrEmptyAnswer is returned when an answer body decodes but
	// carries no non-blank text. HTTP 400.
	ErrEmptyAnswer = errors.New("interview: answer text is empty")
	// ErrEnded is returned when an answer arrives for an interview
	// that has ended. HTTP 409.
	ErrEnded = errors.New("interview: interview has ended")
	// ErrBusy is returned when an answer arrives while a turn is
	// already in flight for the same interview. HTTP 409; the client
	// waits for the question event instead of retrying.
	ErrBusy = errors.New("interview: a turn is already in progress")
)

// Config carries the Handler's dependencies and knobs (PLAN.md
// invariant 2: new packages take a config struct; nothing here reads
// the environment). Chat, Store, Broker and Jobs are required; New
// refuses to build a Handler without them.
type Config struct {
	// Chat is the M3 text client used for every turn.
	Chat Chatter
	// Speaker, when non-nil, synthesizes audio for each question in the
	// background and publishes question_audio on the interview topic.
	// Nil leaves the interview purely text-based (the current behavior).
	Speaker QuestionSpeaker
	// Store persists books, interviews and the transcript.
	Store interviewStore
	// Broker publishes the turn events and serves the SSE topic.
	Broker broadcaster
	// Jobs runs each turn off the request path.
	Jobs jobRunner
	// MaxTurns is the exchange budget after which the interview ends
	// itself (reason "limit"). Zero or negative means defaultMaxTurns.
	MaxTurns int
	// Log receives turn failures. Nil means logs are discarded.
	Log *slog.Logger
}

// withDefaults returns cfg with zero values substituted: MaxTurns from
// defaultMaxTurns, Log from a discarded handler.
func (cfg Config) withDefaults() Config {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = defaultMaxTurns
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return cfg
}

// Handler serves the interview routes (see http.go) and owns the
// loop. Build it with New; the zero value is not usable.
type Handler struct {
	chat     Chatter
	speaker  QuestionSpeaker
	store    interviewStore
	broad    broadcaster
	jobs     jobRunner
	maxTurns int
	log      *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
}

// New returns a Handler with the given dependencies. It returns an
// error wrapping ErrNotConfigured when Chat, Store, Broker or Jobs is
// nil — the Handler cannot serve anything useful without all four.
func New(cfg Config) (*Handler, error) {
	required := []struct {
		name string
		dep  any
	}{
		{"Chat", cfg.Chat},
		{"Store", cfg.Store},
		{"Broker", cfg.Broker},
		{"Jobs", cfg.Jobs},
	}
	for _, d := range required {
		if d.dep == nil {
			return nil, fmt.Errorf("%w: nil %s", ErrNotConfigured, d.name)
		}
	}
	cfg = cfg.withDefaults()
	return &Handler{
		chat:     cfg.Chat,
		speaker:  cfg.Speaker,
		store:    cfg.Store,
		broad:    cfg.Broker,
		jobs:     cfg.Jobs,
		maxTurns: cfg.MaxTurns,
		log:      cfg.Log,
		sessions: make(map[string]*session),
	}, nil
}

// Topic is the broker topic an interview publishes on: TopicPrefix
// plus the interview id.
func Topic(id string) string { return TopicPrefix + id }

// session is the in-process state of one interview: the server-side
// checklist, the stall counter, and the turn lifecycle. Everything
// here is reconstructible or deliberately volatile — see the package
// doc's restart semantics.
type session struct {
	mu sync.Mutex

	filled      checklist // slots the model has reported so far
	streak      int       // consecutive low-effort child answers — the chip signal
	stallStreak int       // consecutive stall-word child answers ("i dunno")
	lastAnswer  string    // the previous child answer, normalised — the repetition check
	exchanges   int       // child answers processed so far
	inFlight    bool      // a turn (including the opening question) is running
	ending      bool      // end decided; goodbye turn is running
	ended       bool      // terminal; no further answers accepted
	chips       []string  // options the last published question offered
	turnErr     string    // machine class of the last failed turn; "" when none
}

// sessionFor returns the session for iv, creating it on first touch.
// After a process restart the first touch rebuilds from the persisted
// transcript: an interview whose last turn is the closing turn is
// already ended (see RoleClosing); everything else starts open.
func (h *Handler) sessionFor(iv store.Interview) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[iv.ID]
	if ok {
		return s
	}
	s = &session{filled: checklist{}}
	if n := len(iv.Turns); n > 0 && iv.Turns[n-1].Role == RoleClosing {
		s.ended = true
	}
	h.sessions[iv.ID] = s
	return s
}
