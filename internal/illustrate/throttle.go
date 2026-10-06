package illustrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"thutapi/internal/gmi"
)

// Throttled image calls, and why this file exists (nrynss/thutapi#1).
//
// GMI caps requests per minute per model, and the default image model
// (seedream-5.0-lite) allows two per sixty seconds. Reference sheets used
// to fan out in parallel and nothing waited on the cap, so the first
// gmi.ErrRateLimited aborted the whole book — twice on 2026-10-06, and a
// re-POST hit the same wall. internal/gmi/media's one resubmit on
// gmi.ErrTransient is immediate, which against a per-minute cap is a second
// failure a few milliseconds later.
//
// Two layers, in this order:
//
//  1. pacer spaces image submissions so that, provided internal/gmi/media
//     submits once per slot, they stay inside the per-minute cap.
//     Concurrency cannot do that — Limit bounds requests in flight, not
//     requests per minute, and a request slower than its slot never limits
//     itself — so every submission, from reference sheets, pages and T7's
//     regenerations alike, first reserves a slot one interval after the
//     previous one. GMI's cap is per model per account, not per book, so
//     the pacer is process-wide: one per (model, requests-per-minute),
//     shared by every Illustrate run in the process (see pacerRegistry),
//     which is what keeps concurrent books and a "Try again" re-POST after
//     a failed run from each assuming an empty window.
//
//     That is a bound for this process only, and it has one known
//     exception: internal/gmi/media resubmits immediately, inside the slot,
//     on gmi.ErrTransient (and once on a failed poll status), so a transient
//     failure can put two POSTs in one slot. The next slot may then meet a
//     429, which layer 2 absorbs. Nor can a pacer know about another process
//     or another host on the same account.
//  2. retryThrottled is the backstop for what pacing cannot know about (the
//     window was already part-spent by another process, or by that
//     immediate resubmit): wait, honouring what the provider says, and try
//     again a bounded number of times. It is the equivalent of
//     internal/audio's retryThrottled.

// DefaultThrottleAttempts is how many times an image call is tried in total,
// including the first, when the provider says it is rate limited. Pacing
// makes a 429 the exception, so six spends at most five waits on a window
// that something else filled.
const DefaultThrottleAttempts = 6

// DefaultTransientAttempts is how many times an image call is tried in
// total when it fails with gmi.ErrTransient. It is far smaller than
// DefaultThrottleAttempts because a transient failure is not a rate-limit
// wave that a wait clears: it includes a render the model rejected, which
// the media client has already resubmitted once, and every further attempt
// is another paid submission.
const DefaultTransientAttempts = 2

// DefaultThrottleBackoff is the wait before the second attempt when the
// provider did not say how long to wait; each further wait doubles it. It is
// chosen with DefaultThrottleAttempts, MaxThrottleBackoff and
// DefaultThrottleWait so that the longest jittered schedule the defaults can
// produce (15, 30, 60, 90 and 90 s, 285 s) fits the wait budget: every
// default attempt is reachable, whatever the jitter draws.
const DefaultThrottleBackoff = 15 * time.Second

// MaxThrottleBackoff caps one wait, whether it came from the backoff or from
// the provider's own retry_after_ms.
const MaxThrottleBackoff = 90 * time.Second

// DefaultThrottleWait is the most one image call may spend in retry waits
// altogether, across all its attempts. It bounds the worst case that the
// attempt counts alone leave open (five waits at MaxThrottleBackoff is
// 450 s), which only provider-named waits can reach: the default backoff
// schedule's worst case is 285 s and fits. Waits spent in the pacer's queue
// do not count against it.
const DefaultThrottleWait = 5 * time.Minute

// DefaultRequestsPerMinute is the per-minute cap of the default image model
// (seedream-5.0-lite: two requests per sixty seconds). Another model or host
// sets ThrottleConfig.RequestsPerMinute.
const DefaultRequestsPerMinute = 2

// DefaultReferenceLimit is how many reference sheets render at once when
// Config.ReferenceLimit is unset. It bounds work in flight and has nothing
// to do with the rate cap, which the pacer enforces for every model from
// ThrottleConfig.RequestsPerMinute.
const DefaultReferenceLimit = 2

// ThrottleConfig paces image calls and the retry a throttled one gets. The
// zero value is usable and means the defaults above and a real clock.
type ThrottleConfig struct {
	// RequestsPerMinute is the model's per-minute cap. Image submissions
	// are spaced 60s/RequestsPerMinute apart (plus a 5% margin for clock
	// and network skew), whichever of sheets, pages or regenerations makes
	// them, and across every Illustrate run in the process that uses the
	// same model and rate: the cap is per account, so the pacer is
	// process-wide. A config that injects Sleep or Now gets a private
	// pacer instead, so a test's clock never leaks into production's.
	//
	// Zero means DefaultRequestsPerMinute; negative disables pacing,
	// leaving only the retry below.
	RequestsPerMinute int

	// Attempts is the total number of tries per call, including the first,
	// when the failure is gmi.ErrRateLimited. Zero or negative means
	// DefaultThrottleAttempts; one disables retrying.
	Attempts int

	// TransientAttempts is the same for gmi.ErrTransient. Zero or negative
	// means DefaultTransientAttempts; one disables retrying.
	TransientAttempts int

	// Backoff is the wait before the second attempt when the provider gave
	// no retry_after_ms, doubled for each attempt after it and capped at
	// MaxThrottleBackoff. Zero or negative means DefaultThrottleBackoff.
	Backoff time.Duration

	// MaxWait bounds the total retry waiting of one call; a wait that would
	// pass it ends the call with the last error instead. Zero or negative
	// means DefaultThrottleWait.
	MaxWait time.Duration

	// Sleep waits for d and returns early with ctx's error if ctx ends
	// first. Nil means a real timer. It is the injectable clock: tests
	// supply one that records d and returns at once.
	Sleep func(ctx context.Context, d time.Duration) error

	// Now reads the clock the pacer schedules against. Nil means time.Now.
	Now func() time.Time

	// randN draws a uniform integer in [0, n) for retry jitter. Nil means
	// math/rand/v2's global source. Unexported: only this package's tests
	// need a deterministic draw, so a simulation's outcome is a fixed count
	// rather than a sample.
	randN func(n int64) int64

	// registry, when set, is where the pacer is looked up; tests use it to
	// share a pacer between runs on an injected clock, or to isolate one
	// from the process-wide registry. Unexported: nothing outside this
	// package needs it.
	registry *pacerRegistry
}

// withDefaults substitutes the package defaults for zero and negative fields
// (RequestsPerMinute keeps a negative value: that means unpaced).
func (tc ThrottleConfig) withDefaults() ThrottleConfig {
	if tc.RequestsPerMinute == 0 {
		tc.RequestsPerMinute = DefaultRequestsPerMinute
	}
	if tc.Attempts <= 0 {
		tc.Attempts = DefaultThrottleAttempts
	}
	if tc.TransientAttempts <= 0 {
		tc.TransientAttempts = DefaultTransientAttempts
	}
	if tc.Backoff <= 0 {
		tc.Backoff = DefaultThrottleBackoff
	}
	if tc.MaxWait <= 0 {
		tc.MaxWait = DefaultThrottleWait
	}
	if tc.Sleep == nil {
		tc.Sleep = defaultSleep
	}
	if tc.Now == nil {
		tc.Now = time.Now
	}
	if tc.randN == nil {
		tc.randN = rand.Int64N
	}
	return tc
}

// defaultSleep is what an un-configured ThrottleConfig sleeps with. It is a
// variable only so this package's own test binary can make every
// un-configured throttle instant; production code never assigns it, and the
// real clock it points at, realSleep, is tested directly.
var defaultSleep = realSleep

// realSleep waits for d on a real timer, or returns ctx's error as soon as
// ctx ends.
func realSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pacer hands out submission slots one interval apart. It is shared by every
// image call that resolves to it — sheets, pages and regenerations of one
// run, and (through pacerRegistry) every other run of the process — so all
// of them draw on one budget.
//
// A slot belongs to a waiter only until it uses it: a waiter whose context
// ends before its slot arrives gives the slot back (release), and the next
// caller may take it. Slots that arrived were spent on a real submission and
// still count, so the slots of real submissions are never closer than the
// pacer's interval, but a failed or cancelled run does not leave its queue
// behind to delay the next one. The guarantee is about slots, not about the
// instants of submission: a waiter submits after its sleep returns and any
// scheduling delay, which is what the 5% margin absorbs.
type pacer struct {
	interval time.Duration // zero means unpaced
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error

	mu    sync.Mutex
	slots []time.Time // slots handed out and still relevant, ascending
}

// pacerKey identifies one provider-side rate window: GMI's cap is per model.
type pacerKey struct {
	model string
	rpm   int
}

// pacerRegistry hands out one pacer per (model, requests-per-minute). The
// set of keys is bounded by the models and rates the process is configured
// with (in production, one), so entries are never evicted; a pacer holds no
// goroutine, timer or file, only a slice of the slots still relevant, and its
// mutex covers one read of the injected clock plus the slot arithmetic (never
// a sleep), so nothing can leak or deadlock through it.
type pacerRegistry struct {
	mu sync.Mutex
	m  map[pacerKey]*pacer
}

// processPacers is the production registry: the pacers every Illustrate run
// of the process shares. The zero ThrottleConfig resolves here.
var processPacers = &pacerRegistry{}

// get returns the pacer for model at tc's rate, creating it from tc (clock
// and sleep included) on first use. The first creator's clock wins, which is
// harmless in production (everyone uses the real one) and is why tests that
// inject a clock must share a registry only with callers injecting the same.
func (g *pacerRegistry) get(model string, tc ThrottleConfig) *pacer {
	k := pacerKey{model: model, rpm: tc.RequestsPerMinute}
	g.mu.Lock()
	defer g.mu.Unlock()
	if p, ok := g.m[k]; ok {
		return p
	}
	if g.m == nil {
		g.m = map[pacerKey]*pacer{}
	}
	p := newPacer(tc)
	g.m[k] = p
	return p
}

// pacerFor picks the pacer one run draws on. An unpaced rate bypasses every
// registry. A config with an injected clock (Sleep or Now) and no explicit
// registry gets a private pacer; everything else — the zero value, i.e.
// production — shares processPacers.
func pacerFor(model string, raw, tc ThrottleConfig) *pacer {
	if tc.RequestsPerMinute <= 0 {
		return newPacer(tc)
	}
	reg := raw.registry
	if reg == nil && raw.Sleep == nil && raw.Now == nil {
		reg = processPacers
	}
	if reg == nil {
		return newPacer(tc)
	}
	return reg.get(model, tc)
}

// newPacer builds the pacer for a resolved ThrottleConfig.
func newPacer(tc ThrottleConfig) *pacer {
	p := &pacer{now: tc.Now, sleep: tc.Sleep}
	if tc.RequestsPerMinute > 0 {
		base := time.Minute / time.Duration(tc.RequestsPerMinute)
		p.interval = base + base/20
	}
	return p
}

// wait reserves the earliest free slot and sleeps until it. The first caller
// after a quiet spell goes at once. If ctx ends before the slot arrives, the
// slot is released for the next caller and the sleep's own error is returned;
// a slot that has arrived stays spent. A context that has already ended gets
// no slot at all.
func (p *pacer) wait(ctx context.Context) error {
	if p.interval <= 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	slot, now := p.reserve()
	if d := slot.Sub(now); d > 0 {
		if err := p.sleep(ctx, d); err != nil {
			p.release(slot)
			return err
		}
	}
	return nil
}

// reserve takes the earliest slot at or after now that is a full interval
// clear of every slot already handed out, which may be a gap a cancelled
// waiter left rather than the end of the queue. It returns the slot and the
// instant it was reckoned from. The clock is read inside the lock, in the
// same critical section as the reservation: callers then reserve in the
// order they sampled the clock, so the pruning and the spacing below never
// see a time older than one a previous caller already used.
func (p *pacer) reserve() (slot, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now = p.now()
	// Slots a full interval behind now can no longer conflict with anything.
	keep := p.slots[:0]
	for _, s := range p.slots {
		if s.Add(p.interval).After(now) {
			keep = append(keep, s)
		}
	}
	p.slots = keep
	slot = now
	at := len(p.slots)
	for i, s := range p.slots {
		if !s.Add(p.interval).After(slot) {
			continue // wholly before the candidate
		}
		if !s.Before(slot.Add(p.interval)) {
			at = i // the candidate fits before s
			break
		}
		slot = s.Add(p.interval)
	}
	p.slots = append(p.slots, time.Time{})
	copy(p.slots[at+1:], p.slots[at:])
	p.slots[at] = slot
	return slot, now
}

// release returns slot to the pacer if it has not arrived yet. A slot that
// has arrived is left spent: its owner may already have submitted.
func (p *pacer) release(slot time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slot.After(p.now()) {
		return
	}
	for i, s := range p.slots {
		if s.Equal(slot) {
			p.slots = append(p.slots[:i], p.slots[i+1:]...)
			return
		}
	}
}

// retryThrottled runs call until it succeeds, fails with something a wait
// cannot fix, or its attempts or wait budget are spent. It returns call's
// own error, so every sentinel a caller matches on survives; an error that
// outlived the retries is annotated (still wrapping the original).
//
// Only gmi.ErrRateLimited and gmi.ErrTransient are waited on, with separate
// attempt budgets (Attempts and TransientAttempts). Everything else — a
// missing model, a bad request, a rejected key, billing — surfaces on the
// first attempt. A context that ends during a wait, or while an attempt
// was failing, ends the loop at once and the error wraps both the
// context's error and the last attempt's, so a caller can tell a cancelled
// run from a failed one and an operator still sees what the provider said.
func retryThrottled(ctx context.Context, tc ThrottleConfig, call func() ([]byte, error)) ([]byte, error) {
	tc = tc.withDefaults()
	backoff := tc.Backoff
	var waited time.Duration
	for attempt := 1; ; attempt++ {
		raw, err := call()
		if err == nil {
			return raw, nil
		}
		if !throttled(err) {
			return nil, err
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, interrupted(cerr, err)
		}
		limited := errors.Is(err, gmi.ErrRateLimited)
		budget := tc.TransientAttempts
		if limited {
			budget = tc.Attempts
		}
		if attempt >= budget {
			if limited {
				return nil, fmt.Errorf("still rate limited after %d attempts: %w", attempt, err)
			}
			return nil, fmt.Errorf("still failing after %d attempts: %w", attempt, err)
		}
		d := jitter(tc.randN, backoff)
		if ra, ok := retryAfter(err); ok {
			// The provider named the moment a slot frees. Wait that long
			// plus a little spread, so a throttled fan-out does not retry
			// in lockstep and re-trip the same window.
			d = min(ra+time.Duration(tc.randN(int64(time.Second)+1)), MaxThrottleBackoff)
		}
		if waited+d > tc.MaxWait {
			return nil, fmt.Errorf("giving up after %s of waiting (limit %s) and %d attempts: %w", waited, tc.MaxWait, attempt, err)
		}
		if serr := tc.Sleep(ctx, d); serr != nil {
			return nil, interrupted(serr, err)
		}
		waited += d
		if backoff = 2 * backoff; backoff > MaxThrottleBackoff {
			backoff = MaxThrottleBackoff
		}
	}
}

// interrupted joins the reason a wait ended with the failure that caused it,
// keeping both matchable with errors.Is.
func interrupted(cause, last error) error {
	return fmt.Errorf("wait interrupted: %w; last attempt: %w", cause, last)
}

// throttled reports whether err is a condition that waiting can clear.
func throttled(err error) bool {
	return errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient)
}

// rateLimitBody is the part of GMI's 429 body this package reads:
//
//	{"error":"...","rate_limit":{"limit_type":"RPM","limit":2,
//	 "interval_seconds":60,"retry_after_ms":29970}}
type rateLimitBody struct {
	RateLimit struct {
		RetryAfterMS int64 `json:"retry_after_ms"`
	} `json:"rate_limit"`
}

// retryAfter extracts the provider's retry_after_ms from a rate-limit error.
//
// internal/gmi carries the 429 body only as text after the sentinel
// (fmt.Errorf("%w: %s", gmi.ErrRateLimited, body)) and has no typed field,
// so this decodes the JSON object that follows the first '{' — it reads one
// named field of the body, never the message wording, and never feeds a
// control-flow decision other than "how long". It reports false for any
// error that is not gmi.ErrRateLimited, has no JSON body, or carries a
// non-positive or non-integer value. A value above MaxThrottleBackoff is
// clamped to it. A typed accessor on the gmi error would replace this
// (contract-change request in the track report).
func retryAfter(err error) (time.Duration, bool) {
	if !errors.Is(err, gmi.ErrRateLimited) {
		return 0, false
	}
	msg := []byte(err.Error())
	i := bytes.IndexByte(msg, '{')
	if i < 0 {
		return 0, false
	}
	var body rateLimitBody
	if json.NewDecoder(bytes.NewReader(msg[i:])).Decode(&body) != nil {
		return 0, false
	}
	if body.RateLimit.RetryAfterMS <= 0 {
		return 0, false
	}
	// Clamp before multiplying: a huge value would wrap to a negative
	// Duration, and a negative wait is a retry hammer.
	ms := min(body.RateLimit.RetryAfterMS, int64(MaxThrottleBackoff/time.Millisecond))
	return time.Duration(ms) * time.Millisecond, true
}

// jitter spreads a wait across the upper half of d.
func jitter(randN func(n int64) int64, d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(randN(int64(d/2)+1))
}
