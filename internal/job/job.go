// Package job runs long work off the request path (PLAN.md §T3b).
//
// The shape invariant 6 demands: a short POST starts the work and returns
// a job id at once; the work itself runs on a goroutine the Runner owns;
// progress streams over SSE on the job's topic; and the terminal state is
// retrievable by id, so a page reload can catch up. A Handler (T4, T6,
// T8) starts a job and hands the client its id; the client subscribes to
// stream.Topic via the broker and fetches job results by id.
//
// Panic policy: library code must not panic (AGENTS.md §Go style), but a
// job's function is caller-supplied and may anyway. The Runner recovers
// at the job boundary — the one place a panic is a data point rather than
// a corrupted process — and converts it to an error terminal with
// ErrPanic in its chain. The process keeps serving.
package job

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"thutapi/internal/stream"
)

// TopicPrefix is the broker topic namespace every job publishes on: a
// job's topic is TopicPrefix + its id. Front-end code (T9) and any
// subscriber hardcode the convention, so it is pinned here and by
// TestTopicFormat.
const TopicPrefix = "job:"

// DefaultLimit is the number of jobs a Runner built with the zero Config
// runs concurrently. Generation is gated upstream (PLAN.md §T11 caps
// per-IP); the bound here keeps a bug or a burst from forking unbounded
// goroutines and unbounded GMI spend.
const DefaultLimit = 16

// Status is a job's terminal (or current) state.
type Status string

// Status values published as the SSE event name of the terminal event and
// carried in Result.
const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusError   Status = "error"

	// StatusCancelled marks a job that ended because its context was
	// cancelled: fn returned an error whose chain carries
	// context.Canceled. Only Cancel holds a cancel handle for a job's
	// context, so this is the cancelled terminal, not a failure. It is
	// published as the event name like any other terminal; Result.Err
	// keeps the context error in its chain and Data is whatever fn
	// returned, normally nil.
	StatusCancelled Status = "cancelled"
)

// Sentinel errors. Every failure path this package can produce wraps one
// of these; callers branch with errors.Is.
var (
	// ErrUnknownJob is returned by Result for an id this Runner never
	// started (or that predates the process — the registry is
	// in-memory, see Result).
	ErrUnknownJob = errors.New("job: unknown id")

	// ErrLimit is returned by Start when the Runner is already at
	// Config.Limit jobs. Start never blocks waiting for a slot — the
	// short POST must return at once — so saturation surfaces here.
	ErrLimit = errors.New("job: runner at capacity")

	// ErrPanic wraps the value a job function panicked with. The
	// terminal status is StatusError like any other failure.
	ErrPanic = errors.New("job: panicked")

	// ErrNoBroker is returned by Start when the Runner was built
	// without a broker: progress and terminal events are the product,
	// so a Runner that cannot publish them refuses to start jobs.
	ErrNoBroker = errors.New("job: nil broker")
)

// Func is the work one job runs. It receives a context that is NOT
// cancelled when the starting HTTP request ends — a job outlives its
// request by design (invariant 6) — and carries whatever values the
// caller's context held; Runner.Cancel(id) is what cancels it, so an fn
// that returns promptly on ctx.Done() frees its slot and lands the
// cancelled terminal. progress publishes a progress event to the job's
// topic; data is the publisher-encoded payload (the same raw-bytes rule
// as the GMI clients: the publisher knows the schema). An empty progress
// payload is dropped, not published.
//
// Func owns its own deadlines: a long job must bound itself (the media
// poller, for one, carries a per-call deadline) — the Runner does not
// second-guess how long the work may take.
type Func func(ctx context.Context, progress func(data string)) ([]byte, error)

// Result is the terminal state of one job, retrievable by id for
// catch-up. Data carries the function's return bytes; Err is nil exactly
// when Status is StatusDone.
type Result struct {
	ID     string
	Status Status
	Data   []byte
	Err    error
}

// completion is the wire shape of the terminal SSE event: the same
// information as Result minus the payload bytes. A result can be large
// (a book's images) — the event announces the outcome; clients that want
// the bytes fetch Result via the track's route.
type completion struct {
	ID     string `json:"id"`
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Config carries the Runner's knobs. The zero value is usable and means
// DefaultLimit.
type Config struct {
	// Limit is the maximum number of concurrently running jobs. Zero
	// or negative means DefaultLimit.
	Limit int
}

// Runner starts jobs, publishes their progress and terminal events, and
// remembers their terminal results. One per process.
type Runner struct {
	broker *stream.Broker
	limit  chan struct{}

	mu      sync.Mutex
	results map[string]Result
	cancels map[string]context.CancelFunc
}

// New returns a Runner publishing to broker with the default Config.
// broker must be non-nil.
func New(broker *stream.Broker) *Runner {
	return NewWithConfig(broker, Config{})
}

// NewWithConfig returns a Runner publishing to broker with an explicit
// Config. broker must be non-nil.
func NewWithConfig(broker *stream.Broker, cfg Config) *Runner {
	if cfg.Limit <= 0 {
		cfg.Limit = DefaultLimit
	}
	return &Runner{
		broker:  broker,
		limit:   make(chan struct{}, cfg.Limit),
		results: make(map[string]Result),
		cancels: make(map[string]context.CancelFunc),
	}
}

// Topic is the broker topic a job publishes on.
func Topic(id string) string {
	return TopicPrefix + id
}

// Start runs fn on a Runner-owned goroutine and returns the job id at
// once. The id's topic (Topic) carries every progress event fn publishes
// plus exactly one terminal event, named "done" or "error", published
// even if fn panics or its context is cancelled.
//
// ctx is the STARTING request's context. Only its values are inherited —
// cancellation is deliberately dropped (context.WithoutCancel), because
// the client that started a generation going away must not kill it. The
// context fn receives is a Runner-owned child of that value-carrying
// context, and the only handle that cancels it is Runner.Cancel. fn
// bounds its own lifetime with deadlines on the context it receives.
//
// Start returns ErrNoBroker without a broker, and ErrLimit — without
// starting anything — when the Runner is at capacity.
func (r *Runner) Start(ctx context.Context, fn Func) (string, error) {
	if r.broker == nil {
		return "", ErrNoBroker
	}
	select {
	case r.limit <- struct{}{}:
	default:
		return "", ErrLimit
	}

	id, err := newID()
	if err != nil {
		<-r.limit // release the slot: no job exists to hold it
		return "", fmt.Errorf("job: generate id: %w", err)
	}

	jobCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	r.mu.Lock()
	r.results[id] = Result{ID: id, Status: StatusRunning}
	r.cancels[id] = cancel
	r.mu.Unlock()

	// The goroutine's exit path: run returns when fn returns (or
	// panics — recovered inside), publishes the terminal event, and
	// releases the limit slot and the cancel registry entry. Nothing
	// else holds a reference that could keep it alive past that.
	go r.run(jobCtx, cancel, id, fn)

	return id, nil
}

// Cancel asks a running job to stop: it cancels the Runner-owned context
// fn was handed (Start built it from the starting request's values, and
// Cancel is the only handle that can cancel it). Cancellation is
// cooperative — Go cannot kill a goroutine: an fn that returns promptly
// on ctx.Done() ends the job and frees its limit slot, one that ignores
// its context keeps going until it returns on its own. When the error fn
// returned carries context.Canceled, the terminal is StatusCancelled
// (SSE event name "cancelled") instead of StatusError, published through
// the same single site in run; Data is whatever fn returned, normally
// nil.
//
// Cancel is safe to call twice on a running job — the second call is a
// no-op returning nil. An id this Runner never started, one that has
// already terminated (the registry entry goes when the job's goroutine
// exits), or one from another Runner reports ErrUnknownJob.
func (r *Runner) Cancel(id string) error {
	r.mu.Lock()
	cancel, ok := r.cancels[id]
	r.mu.Unlock()
	if !ok {
		return ErrUnknownJob
	}
	cancel()
	return nil
}

// run executes one job and is the only place terminal state is written
// and the terminal event is published — once, on every path.
func (r *Runner) run(ctx context.Context, cancel context.CancelFunc, id string, fn Func) {
	defer func() {
		r.mu.Lock()
		delete(r.cancels, id)
		r.mu.Unlock()
		cancel() // release the job context's resources
		<-r.limit
	}()

	progress := func(data string) {
		if data == "" {
			return
		}
		r.broker.Publish(Topic(id), stream.Event{Name: "progress", Data: data})
	}

	data, err := call(ctx, fn, progress)

	res := Result{ID: id, Data: data}
	switch {
	case err == nil:
		res.Status = StatusDone
	case errors.Is(err, context.Canceled):
		// The only context fn sees is the Runner-owned one and the
		// only handle that cancels it is Cancel, so a
		// context.Canceled in fn's error chain means the job was
		// cancelled — not failed.
		res.Status = StatusCancelled
		res.Err = err
	default:
		res.Status = StatusError
		res.Err = err
	}

	r.mu.Lock()
	r.results[id] = res
	r.mu.Unlock()

	wire := completion{ID: id, Status: res.Status}
	if res.Err != nil {
		wire.Error = res.Err.Error()
	}
	r.broker.Publish(Topic(id), stream.Event{Name: string(res.Status), Data: marshalCompletion(wire)})
}

// call invokes fn with the panic boundary around it: a panic becomes an
// ErrPanic-wrapped error and a nil result, and the deferred recover is the
// only reason the process survives the panicking goroutine.
func call(ctx context.Context, fn Func, progress func(data string)) (data []byte, err error) {
	defer func() {
		if p := recover(); p != nil {
			data = nil
			err = fmt.Errorf("%w: %v", ErrPanic, p)
		}
	}()
	return fn(ctx, progress)
}

// Result returns a job's state by id: StatusRunning while fn is still on
// its goroutine, the terminal Result afterwards. Catch-up after a page
// reload reads this — subscribe to Topic(id) first, then call Result:
// anything terminal before the subscribe is answered by Result, anything
// after flows through the stream (a client that races the window may see
// the terminal event AND Result; it deduplicates on the job id).
//
// The registry is in-memory: results live for the process lifetime, so
// catch-up works across page reloads but not across a restart. Surviving
// a restart is the T3 store's contract for finished artifacts (books do
// not depend on the job registry once persisted); putting raw job results
// in SQLite would need a table in internal/store, which is outside this
// track's seam (PLAN.md §T3b Owns).
func (r *Runner) Result(id string) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, ok := r.results[id]
	if !ok {
		return Result{}, ErrUnknownJob
	}
	return res, nil
}

// newID returns 128 bits of crypto/rand hex-encoded to 32 lowercase
// characters — the same shape the store uses for media paths and book
// links, so a job id is as unguessable as any other handle this process
// hands out.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// marshalCompletion encodes the terminal event payload. A struct of
// string fields cannot fail to marshal, so the error branch exists for
// the compiler — and if it ever fired, the wire would say so rather than
// silently dropping the exactly-once terminal event.
func marshalCompletion(c completion) string {
	b, err := json.Marshal(c)
	if err != nil {
		return `{"status":"error","error":"terminal event marshal failed"}`
	}
	return string(b)
}
