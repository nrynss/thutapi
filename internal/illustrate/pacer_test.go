package illustrate

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/media"
	"thutapi/internal/story"
)

// fixedClock is a clock the test moves by hand.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestPacer_SpacesSlotsOneIntervalApart(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	p := newPacer(ThrottleConfig{RequestsPerMinute: 2, Sleep: rec.sleep, Now: clk.now}.withDefaults())
	for range 4 {
		if err := p.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// 30 s plus the 5% margin, from a quiet start: the first goes at once.
	const iv = 31500 * time.Millisecond
	want := []time.Duration{iv, 2 * iv, 3 * iv}
	got := rec.waits()
	if len(got) != len(want) {
		t.Fatalf("waits = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("wait %d = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestPacer_QuietSpellResetsTheQueue(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	p := newPacer(ThrottleConfig{Sleep: rec.sleep, Now: clk.now}.withDefaults())
	if err := p.wait(context.Background()); err != nil { // slot now
		t.Fatal(err)
	}
	clk.advance(10 * time.Minute)
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.waits(); len(got) != 0 {
		t.Fatalf("waits = %v, want none: the queue drained while nobody asked", got)
	}
}

// TestPacer_RateComesFromConfig pins that the per-minute figure is the
// caller's: another model or host sets it.
func TestPacer_RateComesFromConfig(t *testing.T) {
	for _, tc := range []struct {
		rpm  int
		want time.Duration // the second slot's wait
	}{
		{0, 31500 * time.Millisecond}, // default: 2/min
		{2, 31500 * time.Millisecond},
		{6, 10500 * time.Millisecond},
		{60, 1050 * time.Millisecond},
	} {
		t.Run(fmt.Sprint(tc.rpm), func(t *testing.T) {
			clk := &fixedClock{t: time.Unix(1_000_000, 0)}
			rec := &sleepRecorder{}
			p := newPacer(ThrottleConfig{RequestsPerMinute: tc.rpm, Sleep: rec.sleep, Now: clk.now}.withDefaults())
			for range 2 {
				if err := p.wait(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if got := rec.waits(); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("rpm %d: waits = %v, want [%s]", tc.rpm, got, tc.want)
			}
		})
	}
}

func TestPacer_NegativeRateIsUnpaced(t *testing.T) {
	rec := &sleepRecorder{}
	p := newPacer(ThrottleConfig{RequestsPerMinute: -1, Sleep: rec.sleep}.withDefaults())
	for range 5 {
		if err := p.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := rec.waits(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
}

func TestPacer_CancelledWaitReturnsTheContextError(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	p := newPacer(ThrottleConfig{Sleep: realSleep, Now: clk.now}.withDefaults())
	if err := p.wait(context.Background()); err != nil { // the first slot is free; its result is not what is under test
		t.Fatalf("first wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// gateSleep is a Sleep that records each wait and, while block is set, parks
// until ctx ends, so a test can hold waiters queued and then cancel them.
type gateSleep struct {
	rec   sleepRecorder
	block atomic.Bool
}

func (g *gateSleep) sleep(ctx context.Context, d time.Duration) error {
	g.rec.mu.Lock()
	g.rec.ds = append(g.rec.ds, d)
	g.rec.mu.Unlock()
	if g.block.Load() {
		<-ctx.Done()
	}
	return ctx.Err()
}

// slotCount is how many slots the pacer is holding.
func (p *pacer) slotCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.slots)
}

// queueCancelled parks n waiters on p, each with its own context, until every
// one holds a slot, and returns their cancel funcs and a wait group over them.
func queueCancelled(t *testing.T, p *pacer, g *gateSleep, n int) (cancels []context.CancelFunc, wg *sync.WaitGroup) {
	t.Helper()
	g.block.Store(true)
	wg = &sync.WaitGroup{}
	base := p.slotCount()
	for i := range n {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.wait(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("queued waiter %d: err = %v, want context.Canceled", i, err)
			}
		}()
		// One at a time, so slots are handed out in a known order.
		for deadline := time.Now().Add(5 * time.Second); p.slotCount() < base+i+1; {
			if time.Now().After(deadline) {
				t.Fatalf("waiter %d never took a slot", i)
			}
			time.Sleep(time.Millisecond)
		}
	}
	return cancels, wg
}

// TestPacer_CancelledWaitersDoNotStrandSlots is round-3 M2, the reviewer's
// probe: one free slot, four queued waiters, all cancelled, then the next
// book's first image. It must wait one interval behind the slot that was
// really used, not five behind the four that never were.
func TestPacer_CancelledWaitersDoNotStrandSlots(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	g := &gateSleep{}
	p := newPacer(ThrottleConfig{Sleep: g.sleep, Now: clk.now}.withDefaults())
	if err := p.wait(context.Background()); err != nil { // the used slot
		t.Fatal(err)
	}
	cancels, wg := queueCancelled(t, p, g, 4)
	for _, cancel := range cancels {
		cancel()
	}
	wg.Wait()
	g.block.Store(false)
	before := len(g.rec.waits())
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	const iv = 31500 * time.Millisecond
	w := g.rec.waits()
	if len(w) != before+1 || w[before] != iv {
		t.Fatalf("next wait after four cancelled = %v, want exactly one interval %s (stranded slots would make it %s)", w[before:], iv, 5*iv)
	}
}

// TestPacer_CancelledMiddleWaiterLeavesAReusableGap: cancelling a waiter in
// the middle of the queue frees its slot for the next caller rather than only
// the tail's.
func TestPacer_CancelledMiddleWaiterLeavesAReusableGap(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	g := &gateSleep{}
	p := newPacer(ThrottleConfig{Sleep: g.sleep, Now: clk.now}.withDefaults())
	if err := p.wait(context.Background()); err != nil { // slot 0, used
		t.Fatal(err)
	}
	cancels, wg := queueCancelled(t, p, g, 3) // slots 1, 2, 3
	cancels[1]()                              // slot 2 goes back
	deadline := time.Now().Add(5 * time.Second)
	for p.slotCount() != 3 {
		if time.Now().After(deadline) {
			t.Fatal("the cancelled middle waiter never released its slot")
		}
		time.Sleep(time.Millisecond)
	}
	g.block.Store(false)
	before := len(g.rec.waits())
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	const iv = 31500 * time.Millisecond
	if w := g.rec.waits(); w[before] != 2*iv {
		t.Fatalf("wait = %s, want the freed slot 2 (%s), not the end of the queue (%s)", w[before], 2*iv, 4*iv)
	}
	for _, i := range []int{0, 2} {
		cancels[i]()
	}
	wg.Wait()
}

// TestPacer_UsedSlotsStillSpaceTheNextCaller is the other direction of M2: a
// slot whose owner got to use it (the sleep ended, or its time had already
// come when the context ended) is spent, and the next caller waits behind it.
func TestPacer_UsedSlotsStillSpaceTheNextCaller(t *testing.T) {
	const iv = 31500 * time.Millisecond
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	p := newPacer(ThrottleConfig{Sleep: rec.sleep, Now: clk.now}.withDefaults())
	for range 2 {
		if err := p.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if w := rec.waits(); len(w) != 1 || w[0] != iv {
		t.Fatalf("second caller waited %v, want [%s] behind the used slot", w, iv)
	}

	// A waiter whose context ends only after its slot arrived has spent it.
	late := &fixedClock{t: time.Unix(2_000_000, 0)}
	arrives := func(ctx context.Context, d time.Duration) error {
		late.advance(d)
		return context.Canceled
	}
	q := newPacer(ThrottleConfig{Sleep: arrives, Now: late.now}.withDefaults())
	if err := q.wait(context.Background()); err != nil { // free, now
		t.Fatal(err)
	}
	if err := q.wait(context.Background()); !errors.Is(err, context.Canceled) { // slot +iv arrives, then ends
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	rec2 := &sleepRecorder{}
	q.sleep = rec2.sleep
	if err := q.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w := rec2.waits(); len(w) != 1 || w[0] != iv {
		t.Fatalf("waited %v after a slot that had arrived, want [%s]: an arrived slot is spent", w, iv)
	}
}

// TestPacer_NoTwoSlotsCloserThanTheInterval is the safety half: whatever mix
// of reservations and releases happens, handed-out slots stay a full interval
// apart, so real submissions can never exceed the cap.
func TestPacer_NoTwoSlotsCloserThanTheInterval(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	p := newPacer(ThrottleConfig{Now: clk.now}.withDefaults())
	var held []time.Time
	for i := range 2000 {
		switch rng.IntN(4) {
		case 0:
			clk.advance(time.Duration(rng.Int64N(int64(2 * p.interval))))
		case 1:
			if len(held) > 0 {
				k := rng.IntN(len(held))
				p.release(held[k])
				held = append(held[:k], held[k+1:]...)
			}
		default:
			slot, _ := p.reserve()
			held = append(held, slot)
		}
		p.mu.Lock()
		for j := 1; j < len(p.slots); j++ {
			if gap := p.slots[j].Sub(p.slots[j-1]); gap < p.interval {
				p.mu.Unlock()
				t.Fatalf("step %d: slots %v and %v are %s apart, want at least %s", i, p.slots[j-1], p.slots[j], gap, p.interval)
			}
		}
		p.mu.Unlock()
	}
}

// TestIllustrate_DefaultPacingOnTheWire is rule 2 for the pacing default:
// Config is empty, a fixed clock never advances, and the recorded sleeps are
// the pacer's own. Three sheets and three pages is six submissions, so five
// waits, growing by 31.5 s (30 s plus margin, the default model's two per
// minute) each.
func TestIllustrate_DefaultPacingOnTheWire(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	fake := &fakeImager{}
	cfg := Config{Imager: fake, Throttle: ThrottleConfig{Sleep: rec.sleep, Now: clk.now}}
	if _, err := Illustrate(context.Background(), cfg, threeCastStory()); err != nil {
		t.Fatal(err)
	}
	got := rec.waits()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(got) != 5 {
		t.Fatalf("waits = %v, want 5 (six submissions, the first immediate)", got)
	}
	for i, w := range got {
		if want := time.Duration(i+1) * 31500 * time.Millisecond; w != want {
			t.Errorf("wait %d = %s, want %s", i, w, want)
		}
	}
}

// TestIllustrate_PacingIsSharedByRegenerations pins that T7's regenerations
// draw on the same budget as sheets and pages.
func TestIllustrate_PacingIsSharedByRegenerations(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	var edits atomic.Int32
	fake := &fakeImager{edit: func(ctx context.Context, prompt, model string, refs []string, opts media.ImageOptions) ([]byte, error) {
		return queueEnvelope(fixtureMedia.url(fmt.Sprintf("paced-regen-%d", edits.Add(1)))), nil
	}}
	judge := &scriptedJudge{out: []bool{false, true}}
	cfg := Config{Imager: fake, Judge: judge, Throttle: ThrottleConfig{Sleep: rec.sleep, Now: clk.now}}
	if _, err := Illustrate(context.Background(), cfg, oneCastStory()); err != nil {
		t.Fatal(err)
	}
	// One sheet, the page, one regeneration: three submissions, two waits.
	if got := rec.waits(); len(got) != 2 {
		t.Fatalf("waits = %v, want 2", got)
	}
}

// TestIllustrate_ReferenceLimitIsIndependentOfTheModel pins L2(d): the sheet
// bound is not a model's rate cap, so naming another model does not change it.
func TestIllustrate_ReferenceLimitIsIndependentOfTheModel(t *testing.T) {
	var inFlight, peak atomic.Int32
	fake := &fakeImager{generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		return queueEnvelope(fixtureMedia.serve("sheet", pngBytes("sheet:"+prompt))), nil
	}}
	cfg := Config{Imager: fake, Model: "bytedance/seedream-other", Throttle: ThrottleConfig{RequestsPerMinute: -1}}
	if _, err := Illustrate(context.Background(), cfg, threeCastStory()); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != DefaultReferenceLimit {
		t.Errorf("peak sheets in flight = %d, want %d regardless of model", got, DefaultReferenceLimit)
	}
}

// TestIllustrate_CancelDuringAThrottleWaitIsACancellation pins M2 through the
// whole flow: job.Runner classifies a run as cancelled only when the error
// chain holds context.Canceled.
func TestIllustrate_CancelDuringAThrottleWaitIsACancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeImager{generate: func(context.Context, string, string, media.ImageOptions) ([]byte, error) {
		return nil, fmt.Errorf("%w: %s", gmi.ErrRateLimited, rateLimitJSON)
	}}
	cfg := Config{Imager: fake, Throttle: ThrottleConfig{RequestsPerMinute: -1, Sleep: func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}}}
	book, err := Illustrate(ctx, cfg, oneCastStory())
	if !errors.Is(err, context.Canceled) || !errors.Is(err, gmi.ErrRateLimited) {
		t.Fatalf("err = %v, want both context.Canceled and gmi.ErrRateLimited", err)
	}
	assertZeroBook(t, book)
}

// vclock is a virtual clock for the rate-window simulation. Sleepers park on
// it; a driver advances it to the earliest wake-up once every goroutine has
// been quiet for a few real milliseconds and no other goroutine is running or
// runnable (otherGoroutinesRunnable).
//
// The second condition is what makes the simulation honest under load. The
// pacer reads its clock and reserves under one lock
// (TestPacer_ReadsItsClockUnderItsLock), so slots are handed out in clock
// order; but a woken goroutine that the OS or the Go scheduler has not yet run
// would, with only the quiet heuristic, be left behind while virtual time
// jumped to the next wake-up, and its submission would reach the window late
// and too close to its neighbour's (seen as a spurious "refused from a quiet
// window" with 24 busy loops on 12 cores and -test.cpu 1,3, accepted times
// seconds off the slot grid). A late goroutine can therefore make requests
// denser, not only sparser; the driver must not let it be late.
type vclock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*vwaiter
	busy    atomic.Int32 // goroutines inside a fake render
}

type vwaiter struct {
	wake time.Time
	ch   chan struct{}
}

func (c *vclock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *vclock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	c.mu.Lock()
	w := &vwaiter{wake: c.now.Add(d), ch: make(chan struct{})}
	c.waiters = append(c.waiters, w)
	c.mu.Unlock()
	select {
	case <-w.ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// drive runs until stop is closed.
func (c *vclock) drive(stop <-chan struct{}) {
	lastN, quiet := -1, 0
	for {
		select {
		case <-stop:
			return
		case <-time.After(time.Millisecond):
		}
		c.mu.Lock()
		n := len(c.waiters)
		if n == lastN && n > 0 && c.busy.Load() == 0 && !otherGoroutinesRunnable() {
			quiet++
		} else {
			quiet = 0
		}
		lastN = n
		if quiet >= 2 {
			sort.Slice(c.waiters, func(i, j int) bool { return c.waiters[i].wake.Before(c.waiters[j].wake) })
			if c.waiters[0].wake.After(c.now) {
				c.now = c.waiters[0].wake
			}
			rest := c.waiters[:0]
			for _, w := range c.waiters {
				if w.wake.After(c.now) {
					rest = append(rest, w)
				} else {
					close(w.ch)
				}
			}
			c.waiters = rest
			quiet, lastN = 0, -1
		}
		c.mu.Unlock()
	}
}

// rateWindow is a fake GMI image endpoint enforcing "limit requests per 60 s,
// sliding": a request is refused when limit accepted requests already sit in
// the trailing minute, and a refusal is not counted against the window.
type rateWindow struct {
	clk     *vclock
	limit   int
	latency func() time.Duration

	mu       sync.Mutex
	accepted []time.Time
	refused  int
	maxIn    int // most accepted requests ever seen inside one trailing minute
}

func (w *rateWindow) submit(ctx context.Context, prompt string) ([]byte, error) {
	w.clk.busy.Add(1)
	now := w.clk.Now()
	w.mu.Lock()
	var in []time.Time
	for _, a := range w.accepted {
		if now.Sub(a) < time.Minute {
			in = append(in, a)
		}
	}
	if len(in) >= w.limit {
		w.refused++
		oldest := in[0]
		for _, a := range in {
			if a.Before(oldest) {
				oldest = a
			}
		}
		retry := time.Minute - now.Sub(oldest)
		w.mu.Unlock()
		w.clk.busy.Add(-1)
		return nil, fmt.Errorf("%w: {\"error\":\"Rate limit exceeded\",\"rate_limit\":{\"limit_type\":\"RPM\",\"limit\":%d,\"interval_seconds\":60,\"retry_after_ms\":%d}}",
			gmi.ErrRateLimited, w.limit, retry.Milliseconds())
	}
	w.accepted = append(w.accepted, now)
	w.maxIn = max(w.maxIn, len(in)+1)
	w.mu.Unlock()
	w.clk.busy.Add(-1)
	if err := w.clk.Sleep(ctx, w.latency()); err != nil { // the render itself
		return nil, err
	}
	return queueEnvelope(fixtureMedia.serve("sim", pngBytes("sim:"+prompt))), nil
}

func eightPageStory() story.Story {
	s := threeCastStory()
	s.Pages = nil
	for n := 1; n <= 8; n++ {
		s.Pages = append(s.Pages, page(n, fmt.Sprintf("Scene %d with Mira and Pip.", n), "Mira", "Pip", "Bramble"))
	}
	return s
}

// simulate runs the full Illustrate flow for an 8-page, 3-cast book against a
// 2-per-60 s sliding window on a virtual clock, with the window already
// holding `prior` requests at random ages (the state after an earlier book),
// and a render latency of 5-40 s.
func simulate(t *testing.T, seed uint64, prior int, rpm int, randN func(int64) int64) (refused, maxIn int, err error) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, 0x7261746521))
	clk := &vclock{now: time.Unix(2_000_000, 0)}
	stop := make(chan struct{})
	go clk.drive(stop)
	defer close(stop)

	win := &rateWindow{clk: clk, limit: 2, latency: func() time.Duration {
		return 5*time.Second + time.Duration(rng.Int64N(int64(35*time.Second)))
	}}
	// rng is shared by concurrent renders; guard it.
	var rmu sync.Mutex
	inner := win.latency
	win.latency = func() time.Duration { rmu.Lock(); defer rmu.Unlock(); return inner() }
	for range prior {
		win.accepted = append(win.accepted, clk.now.Add(-time.Duration(rng.Int64N(int64(time.Minute)))))
	}
	fake := &fakeImager{
		generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
			return win.submit(ctx, prompt)
		},
		edit: func(ctx context.Context, prompt, model string, refs []string, opts media.ImageOptions) ([]byte, error) {
			return win.submit(ctx, prompt)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg := Config{Imager: fake, Throttle: ThrottleConfig{RequestsPerMinute: rpm, Sleep: clk.Sleep, Now: clk.Now, randN: randN}}
	_, err = Illustrate(ctx, cfg, eightPageStory())
	win.mu.Lock()
	defer win.mu.Unlock()
	return win.refused, win.maxIn, err
}

// TestIllustrate_PacingKeepsAnEightPageBookInsideASlidingWindow is the
// reviewer's H1 simulation made a test. The default Config (4 pages wide,
// 2 sheets wide, the model's 2-per-60 s window) renders an 8-page, 3-cast
// book against a sliding window that may already be part-spent, over many
// seeds, and never exhausts its attempts; from a quiet window it never even
// draws a 429, and the window is never exceeded.
func TestIllustrate_PacingKeepsAnEightPageBookInsideASlidingWindow(t *testing.T) {
	const seeds = 24
	for seed := uint64(1); seed <= seeds; seed++ {
		prior := int(seed % 3) // 0, 1 or 2 requests already in the window
		refused, maxIn, err := simulate(t, seed, prior, 0, nil)
		if err != nil {
			t.Fatalf("seed %d (prior %d): %v", seed, prior, err)
		}
		if prior == 0 && refused != 0 {
			t.Errorf("seed %d: %d requests were refused from a quiet window; pacing should keep every submission inside the cap", seed, refused)
		}
		if maxIn > 2 {
			t.Errorf("seed %d: %d accepted requests inside one minute, want at most 2", seed, maxIn)
		}
	}
}

// TestIllustrate_TheSimulationFailsWithoutPacing proves the simulation above
// is load-bearing: with pacing off, the same flow against a full window
// exhausts its attempts on a rate limit. Retry jitter is the one source of
// chance in that flow (it used the global RNG, so the count of failing seeds
// was a sample and the old threshold of six of twelve flaked about 1.7% of
// runs on an idle machine); here the draw is fixed at zero, so the result is
// the same on every run. Every seed must fail; the test asserts all of them,
// and that the paired run with pacing on and the same fixed jitter does not.
func TestIllustrate_TheSimulationFailsWithoutPacing(t *testing.T) {
	const seeds = 12
	zero := func(int64) int64 { return 0 }
	failed := 0
	for seed := uint64(1); seed <= seeds; seed++ {
		_, _, err := simulate(t, seed, 2, -1, zero)
		if err != nil {
			if !errors.Is(err, gmi.ErrRateLimited) {
				t.Fatalf("seed %d: err = %v, want a rate-limit failure", seed, err)
			}
			failed++
		}
		if _, _, perr := simulate(t, seed, 2, 0, zero); perr != nil {
			t.Errorf("seed %d: paced run with the same fixed jitter failed: %v", seed, perr)
		}
	}
	if failed != seeds {
		t.Errorf("%d of %d unpaced runs failed with fixed jitter; want all %d, or the simulation does not reproduce H1", failed, seeds, seeds)
	}
}

// TestPacerFor_SharingRules pins which runs share a pacer: GMI's cap is per
// model per account, so same model and rate share one; a different model or
// rate does not; an injected clock without a registry stays private (a
// test's clock must never leak into production's); the zero value is the
// process-wide one; an unpaced rate bypasses every registry.
func TestPacerFor_SharingRules(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	reg := &pacerRegistry{}
	inj := ThrottleConfig{Sleep: rec.sleep, Now: clk.now, registry: reg}
	resolved := func(tc ThrottleConfig) ThrottleConfig { return tc.withDefaults() }

	a := pacerFor("m", inj, resolved(inj))
	if b := pacerFor("m", inj, resolved(inj)); a != b {
		t.Error("same model and rate in one registry got two pacers; concurrent books would each assume an empty window")
	}
	if b := pacerFor("other", inj, resolved(inj)); a == b {
		t.Error("a different model shares a pacer; the cap is per model")
	}
	fast := inj
	fast.RequestsPerMinute = 6
	if b := pacerFor("m", fast, resolved(fast)); a == b {
		t.Error("a different rate shares a pacer")
	}

	private := ThrottleConfig{Sleep: rec.sleep, Now: clk.now}
	if x, y := pacerFor("m", private, resolved(private)), pacerFor("m", private, resolved(private)); x == y {
		t.Error("an injected clock without a registry was shared; it would leak into every other caller")
	}

	zero := ThrottleConfig{}
	z := pacerFor("zero-test-model", zero, resolved(zero))
	if z != processPacers.get("zero-test-model", resolved(zero)) || z != pacerFor("zero-test-model", zero, resolved(zero)) {
		t.Error("the zero ThrottleConfig did not resolve to the process-wide pacer")
	}

	off := ThrottleConfig{RequestsPerMinute: -1, Sleep: rec.sleep, Now: clk.now, registry: reg}
	if x, y := pacerFor("m", off, resolved(off)), pacerFor("m", off, resolved(off)); x == y || x.interval != 0 {
		t.Error("an unpaced config went through the registry or kept an interval")
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if len(reg.m) != 3 { // m@2, other@2, m@6
		t.Errorf("registry holds %d pacers, want 3 (unpaced must not register)", len(reg.m))
	}
}

// TestResolve_ZeroThrottleSharesTheProcessPacer is round-3 M1: the production
// shape (no ThrottleConfig at all) must reach the one process-wide pacer
// through resolve, the path Illustrate takes. Passing the resolved config
// where the raw one belongs would hand every run a private pacer and let
// concurrent books collide on GMI's shared cap again. It never sleeps, and it
// removes the registry entries it created.
func TestResolve_ZeroThrottleSharesTheProcessPacer(t *testing.T) {
	const model = "resolve-test/zero-throttle-model"
	t.Cleanup(func() {
		processPacers.mu.Lock()
		defer processPacers.mu.Unlock()
		for k := range processPacers.m {
			if k.model == model {
				delete(processPacers.m, k)
			}
		}
	})
	f := &fakeImager{}
	a, err := Config{Imager: f, Model: model}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Config{Imager: f, Model: model}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if a.pacer != b.pacer {
		t.Fatal("two zero-ThrottleConfig runs got different pacers: concurrent books would each assume an empty window")
	}
	if want := processPacers.get(model, ThrottleConfig{}.withDefaults()); a.pacer != want {
		t.Fatal("the zero ThrottleConfig did not resolve to the process-wide registry's pacer")
	}
	other, err := Config{Imager: f, Model: model + "-other"}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if other.pacer == a.pacer {
		t.Error("a different model shares a pacer; the cap is per model")
	}
	rec := &sleepRecorder{}
	injected, err := Config{Imager: f, Model: model, Throttle: ThrottleConfig{Sleep: rec.sleep}}.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if injected.pacer == a.pacer {
		t.Error("a run with an injected clock reached the process-wide pacer; a test clock would leak into production")
	}
}

// TestPacerRegistry_ConcurrentGetIsOnePacer hammers get from many
// goroutines under -race: every caller must see the same pacer.
func TestPacerRegistry_ConcurrentGetIsOnePacer(t *testing.T) {
	reg := &pacerRegistry{}
	tc := ThrottleConfig{}.withDefaults()
	var wg sync.WaitGroup
	got := make([]*pacer, 32)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = reg.get("m", tc)
		}()
	}
	wg.Wait()
	for i, p := range got {
		if p != got[0] {
			t.Fatalf("goroutine %d got a different pacer", i)
		}
	}
}

// TestPacer_SharedWaitHonoursCancellation: a caller queued behind others on
// a shared pacer returns the context error when its own context ends, and
// does not block the callers that follow.
func TestPacer_SharedWaitHonoursCancellation(t *testing.T) {
	reg := &pacerRegistry{}
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	tc := ThrottleConfig{Sleep: realSleep, Now: clk.now, registry: reg}
	p := pacerFor("m", tc, tc.withDefaults())
	if err := p.wait(context.Background()); err != nil { // first slot is free
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.wait(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled queued wait did not return")
	}
}

// sim holds one virtual world: a clock, a fake endpoint enforcing a sliding
// 2-per-60 s window, and an Imager over it.
type sim struct {
	clk  *vclock
	win  *rateWindow
	fake *fakeImager
	stop chan struct{}
}

func newSim(seed uint64) *sim {
	rng := rand.New(rand.NewPCG(seed, 0x7261746521))
	var rmu sync.Mutex // the rng is shared by concurrent renders
	clk := &vclock{now: time.Unix(2_000_000, 0)}
	win := &rateWindow{clk: clk, limit: 2, latency: func() time.Duration {
		rmu.Lock()
		defer rmu.Unlock()
		return 5*time.Second + time.Duration(rng.Int64N(int64(35*time.Second)))
	}}
	s := &sim{clk: clk, win: win, stop: make(chan struct{})}
	s.fake = &fakeImager{
		generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
			return win.submit(ctx, prompt)
		},
		edit: func(ctx context.Context, prompt, model string, refs []string, opts media.ImageOptions) ([]byte, error) {
			return win.submit(ctx, prompt)
		},
	}
	go clk.drive(s.stop)
	return s
}

func (s *sim) close() { close(s.stop) }

func (s *sim) counts() (refused, accepted int) {
	s.win.mu.Lock()
	defer s.win.mu.Unlock()
	return s.win.refused, len(s.win.accepted)
}

// throttle is the default Config's throttle on the sim's clock. reg is the
// pacer registry the runs share; nil gives every run a private pacer, which
// is what a per-run pacer is.
func (s *sim) throttle(reg *pacerRegistry) ThrottleConfig {
	return ThrottleConfig{Sleep: s.clk.Sleep, Now: s.clk.Now, registry: reg}
}

// concurrentBooks runs n default-config 8-page 3-cast books at once against
// one window and returns every run's error and how many 429s were drawn.
func concurrentBooks(t *testing.T, seed uint64, n int, shared bool) (errs []error, refused int) {
	t.Helper()
	s := newSim(seed)
	defer s.close()
	var reg *pacerRegistry
	if shared {
		reg = &pacerRegistry{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	errs = make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Illustrate(ctx, Config{Imager: s.fake, Throttle: s.throttle(reg)}, eightPageStory())
		}()
	}
	wg.Wait()
	refused, _ = s.counts()
	return errs, refused
}

// TestIllustrate_ConcurrentBooksShareOneRateWindow is round-2 H1. GMI's
// 2-per-60 s cap is per model per account, so books running at once must
// queue behind a single pacer. With the process-wide pacer 1, 2, 3 and 4
// concurrent default-config books all finish with no attempts-exhaustion and
// not one 429 from a quiet window.
func TestIllustrate_ConcurrentBooksShareOneRateWindow(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4} {
		for seed := uint64(1); seed <= 6; seed++ {
			errs, refused := concurrentBooks(t, seed, n, true)
			for i, err := range errs {
				if err != nil {
					t.Errorf("n=%d seed=%d book %d: %v", n, seed, i, err)
				}
			}
			if refused != 0 {
				t.Errorf("n=%d seed=%d: %d requests refused from a quiet window; one shared pacer should keep every submission inside the cap", n, seed, refused)
			}
		}
	}
}

// TestIllustrate_ConcurrentBooksFailWithAPacerPerRun proves the pin above is
// load-bearing: the same four books with a private pacer each (the shape
// before the registry) exhaust their attempts on a rate limit.
func TestIllustrate_ConcurrentBooksFailWithAPacerPerRun(t *testing.T) {
	const seeds = 6
	failed := 0
	for seed := uint64(1); seed <= seeds; seed++ {
		errs, _ := concurrentBooks(t, seed, 4, false)
		for _, err := range errs {
			if err != nil {
				if !errors.Is(err, gmi.ErrRateLimited) {
					t.Fatalf("seed %d: err = %v, want a rate-limit failure", seed, err)
				}
				failed++
				break
			}
		}
	}
	if failed < seeds/2 {
		t.Errorf("only %d of %d four-book runs failed with a pacer per run; the simulation does not reproduce H1", failed, seeds)
	}
	t.Logf("per-run pacers, 4 concurrent books: %d of %d seeds failed", failed, seeds)
}

// rePost runs a book that is cancelled once `after` requests have been
// accepted (a failed run that leaves the window part-spent), then at once a
// second book on the same window. It returns the second book's error and how
// many 429s that second book drew.
func rePost(t *testing.T, seed uint64, after int, shared bool) (retryErr error, retryRefused int) {
	t.Helper()
	s := newSim(seed)
	defer s.close()
	var reg *pacerRegistry
	if shared {
		reg = &pacerRegistry{}
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	go func() {
		for {
			if _, accepted := s.counts(); accepted >= after {
				cancel1()
				return
			}
			select {
			case <-s.stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	if _, err := Illustrate(ctx1, Config{Imager: s.fake, Throttle: s.throttle(reg)}, eightPageStory()); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run err = %v, want it cancelled", err)
	}
	if shared {
		// Every waiter of the dead run has returned, so none is asleep: every
		// slot still held must already have arrived (been used). A slot in
		// the future is one the failed run stranded for the next book.
		for _, p := range reg.m {
			p.mu.Lock()
			for _, slot := range p.slots {
				if slot.After(s.clk.Now()) {
					t.Errorf("seed %d: the cancelled run left a slot %s in the future; it was never used", seed, slot.Sub(s.clk.Now()))
				}
			}
			p.mu.Unlock()
		}
	}
	before, _ := s.counts() // 429s drawn by the first run
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel2()
	_, retryErr = Illustrate(ctx2, Config{Imager: s.fake, Throttle: s.throttle(reg)}, eightPageStory())
	refusedAll, _ := s.counts()
	return retryErr, refusedAll - before
}

// TestIllustrate_RePostAfterAFailedRunQueuesBehindIt: the issue's own second
// occurrence. A run dies with the window part-spent and the child re-POSTs at
// once. With the process-wide pacer the retry queues behind the slots the
// dead run really used and draws no 429; with a private pacer its first
// submissions land inside the leftover minute and are refused. The slots the
// dead run reserved but never used are given back (round-3 M2: they are not
// stranded in the shared queue), which rePost asserts on the pacer itself.
func TestIllustrate_RePostAfterAFailedRunQueuesBehindIt(t *testing.T) {
	privateRefused := 0
	for seed := uint64(1); seed <= 6; seed++ {
		if err, refused := rePost(t, seed, 5, true); err != nil || refused != 0 {
			t.Errorf("seed %d, shared pacer: re-POST err = %v, %d refusals; want none", seed, err, refused)
		}
		_, refused := rePost(t, seed, 5, false)
		privateRefused += refused
	}
	if privateRefused == 0 {
		t.Error("a private pacer drew no 429 on the re-POST; the scenario does not reproduce the leftover-window collision")
	}
	t.Logf("private pacer: %d refusals across 6 re-POSTs", privateRefused)
}

// TestPacer_ReadsItsClockUnderItsLock pins nrynss/thutapi#1 round-4 M1: the
// clock is read in the same critical section as the reservation. A pacer that
// samples it before taking its lock lets two callers reserve with out-of-order
// times, and two submissions can then land closer than the interval. The
// injected clock asserts the pacer's mutex is held whenever it is read.
func TestPacer_ReadsItsClockUnderItsLock(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	var p *pacer
	var reads, unlocked int
	now := func() time.Time {
		reads++
		if p.mu.TryLock() { // it succeeded, so the caller did not hold the lock
			p.mu.Unlock()
			unlocked++
		}
		return clk.now()
	}
	p = newPacer(ThrottleConfig{Sleep: (&sleepRecorder{}).sleep, Now: now}.withDefaults())
	ctx, cancel := context.WithCancel(context.Background())
	for range 3 {
		if err := p.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.wait(ctx); err != nil { // a queued slot, so release has something to do
		t.Fatal(err)
	}
	cancel()
	p.release(time.Unix(2_000_000, 0)) // reads the clock too, and returns early
	if reads == 0 {
		t.Fatal("the pacer never read its clock")
	}
	if unlocked != 0 {
		t.Fatalf("%d of %d clock reads happened without the pacer's lock held", unlocked, reads)
	}
}

// TestPacer_AnAlreadyCancelledContextGetsNoSlot pins the pre-check in wait
// (round-4 L1): a context that has already ended meets a free slot, so
// without the check wait would return nil, spend the slot and let the caller
// submit with a dead context.
func TestPacer_AnAlreadyCancelledContextGetsNoSlot(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	rec := &sleepRecorder{}
	p := newPacer(ThrottleConfig{Sleep: rec.sleep, Now: clk.now}.withDefaults())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := p.slotCount(); n != 0 {
		t.Fatalf("slotCount = %d, want 0: a dead context must not spend a slot", n)
	}
	if got := rec.waits(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
	// The next, live caller goes at once: nothing was reserved ahead of it.
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.waits(); len(got) != 0 {
		t.Fatalf("waits = %v, want none after a refused caller", got)
	}
}

// TestPacer_ExpiredSlotsArePruned pins round-4 L2: slots a full interval
// behind the clock are dropped on the next reserve, so the list is bounded by
// live waiters rather than growing with every image the process ever made.
func TestPacer_ExpiredSlotsArePruned(t *testing.T) {
	clk := &fixedClock{t: time.Unix(1_000_000, 0)}
	p := newPacer(ThrottleConfig{Sleep: (&sleepRecorder{}).sleep, Now: clk.now}.withDefaults())
	for i := range 1000 {
		if err := p.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := p.slotCount(); n > 2 {
			t.Fatalf("after %d waits slotCount = %d, want at most 2", i+1, n)
		}
		clk.advance(p.interval)
	}
	clk.advance(10 * time.Minute)
	if err := p.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := p.slotCount(); n != 1 {
		t.Fatalf("after a long quiet spell slotCount = %d, want exactly 1", n)
	}
}

// otherGoroutinesRunnable reports whether any goroutine besides the caller is
// running or runnable, read from a stop-the-world stack dump. The virtual
// clock may only advance when everyone else is parked: a goroutine that was
// woken but has not yet been scheduled (heavy CPU contention, or the race
// detector) would otherwise have virtual time jump past it, and its
// submission would reach the window late and too close to its neighbour.
func otherGoroutinesRunnable() bool {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	blocks := strings.Split(string(buf), "\n\n")
	for _, b := range blocks[1:] { // the first block is the calling goroutine
		head, _, _ := strings.Cut(b, "\n")
		if strings.Contains(head, "[runnable") || strings.Contains(head, "[running") {
			return true
		}
	}
	return false
}
