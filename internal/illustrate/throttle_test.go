package illustrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/media"
	"thutapi/internal/story"
)

// rateLimitJSON is the 429 body GMI sent on 2026-10-06 (nrynss/thutapi#1).
const rateLimitJSON = `{"error":"Rate limit exceeded for model seedream-5.0-lite: 2 requests per 60 seconds. Retry in 29970 ms.","rate_limit":{"limit_type":"RPM","limit":2,"interval_seconds":60,"retry_after_ms":29970}}`

// sleepRecorder is an injectable clock: it records each wait and returns at
// once, so nothing in this file sleeps.
type sleepRecorder struct {
	mu sync.Mutex
	ds []time.Duration
}

func (s *sleepRecorder) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.ds = append(s.ds, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleepRecorder) waits() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.ds...)
}

func TestThrottleConfig_Defaults(t *testing.T) {
	got := ThrottleConfig{}.withDefaults()
	if got.RequestsPerMinute != DefaultRequestsPerMinute || got.Attempts != DefaultThrottleAttempts ||
		got.TransientAttempts != DefaultTransientAttempts || got.Backoff != DefaultThrottleBackoff ||
		got.MaxWait != DefaultThrottleWait || got.Sleep == nil || got.Now == nil {
		t.Fatalf("zero ThrottleConfig = %+v, want every default and a clock", got)
	}
	neg := ThrottleConfig{Attempts: -1, TransientAttempts: -1, Backoff: -time.Second, MaxWait: -time.Second}.withDefaults()
	if neg.Attempts != DefaultThrottleAttempts || neg.TransientAttempts != DefaultTransientAttempts ||
		neg.Backoff != DefaultThrottleBackoff || neg.MaxWait != DefaultThrottleWait {
		t.Fatalf("negative ThrottleConfig = %+v, want the defaults", neg)
	}
	exp := ThrottleConfig{Attempts: 7, TransientAttempts: 4, Backoff: time.Minute, MaxWait: time.Hour, RequestsPerMinute: 9}.withDefaults()
	if exp.Attempts != 7 || exp.TransientAttempts != 4 || exp.Backoff != time.Minute || exp.MaxWait != time.Hour || exp.RequestsPerMinute != 9 {
		t.Fatalf("explicit ThrottleConfig = %+v, want it left alone", exp)
	}
	if off := (ThrottleConfig{RequestsPerMinute: -1}).withDefaults(); off.RequestsPerMinute != -1 {
		t.Fatalf("RequestsPerMinute -1 = %d, want it kept: negative means unpaced", off.RequestsPerMinute)
	}
}

// TestRetryThrottled_DefaultBudgetsAndBackoff is AGENTS Testing rule 2 for
// the retry defaults: nothing but the clock is configured, and what counts
// is how many calls reached the wire and how long the first wait was.
// DefaultThrottleAttempts = 1, DefaultTransientAttempts = 6 or
// DefaultThrottleBackoff = time.Millisecond each fail it.
func TestRetryThrottled_DefaultBudgetsAndBackoff(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCalls int
	}{
		{"rate limited gets the long budget", gmi.ErrRateLimited, 6},
		{"transient gets the short budget", gmi.ErrTransient, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &sleepRecorder{}
			calls := 0
			_, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: rec.sleep}, func() ([]byte, error) {
				calls++
				return nil, tc.err
			})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if calls != tc.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tc.wantCalls)
			}
			w := rec.waits()
			if len(w) != tc.wantCalls-1 {
				t.Fatalf("waits = %v, want %d", w, tc.wantCalls-1)
			}
			// Default backoff is 15 s, jittered across [7.5 s, 15 s].
			if w[0] < 7500*time.Millisecond || w[0] > 15*time.Second {
				t.Errorf("first wait = %s, want within [7.5s, 15s]", w[0])
			}
		})
	}
}

// TestDefaultRetryBudget_WorstCaseFitsTheWaitBudget is round-3 M3: the
// longest schedule the default backoff can produce (every wait drawing the
// top of its jitter range) must fit DefaultThrottleWait, or the sixth attempt
// DefaultThrottleAttempts promises is only reachable when the dice are kind.
// The check is arithmetic, not sampling, so it cannot flake.
func TestDefaultRetryBudget_WorstCaseFitsTheWaitBudget(t *testing.T) {
	var worst time.Duration
	backoff := DefaultThrottleBackoff
	for range DefaultThrottleAttempts - 1 {
		worst += backoff // jitter never exceeds its base
		backoff = min(2*backoff, MaxThrottleBackoff)
	}
	if worst > DefaultThrottleWait {
		t.Fatalf("worst-case default backoff total %s exceeds DefaultThrottleWait %s: the last attempt is unreachable under high jitter", worst, DefaultThrottleWait)
	}
}

// TestRetryThrottled_DefaultAttemptsAreNeverCutShort runs the default
// schedule many times: whatever the jitter draws, all six attempts happen and
// the budget annotation never appears. Before M3 it failed about 0.3% of runs.
func TestRetryThrottled_DefaultAttemptsAreNeverCutShort(t *testing.T) {
	for i := range 5000 {
		rec := &sleepRecorder{}
		calls := 0
		_, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: rec.sleep}, func() ([]byte, error) {
			calls++
			return nil, gmi.ErrRateLimited
		})
		if calls != DefaultThrottleAttempts || !errors.Is(err, gmi.ErrRateLimited) ||
			bytes.Contains([]byte(err.Error()), []byte("giving up")) {
			t.Fatalf("run %d: calls = %d, err = %v, waits = %v; want all %d attempts", i, calls, err, rec.waits(), DefaultThrottleAttempts)
		}
	}
}

// TestRetryThrottled_TransientBudgetIsSeparate pins M4: the two classes are
// counted independently, in both directions.
func TestRetryThrottled_TransientBudgetIsSeparate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		cfg       ThrottleConfig
		wantCalls int
	}{
		{"transient ignores a large Attempts", gmi.ErrTransient, ThrottleConfig{Attempts: 9}, DefaultTransientAttempts},
		{"rate limited ignores a large TransientAttempts", gmi.ErrRateLimited, ThrottleConfig{TransientAttempts: 9}, DefaultThrottleAttempts},
		{"explicit TransientAttempts", gmi.ErrTransient, ThrottleConfig{TransientAttempts: 4}, 4},
		{"TransientAttempts of one disables transient retry", gmi.ErrTransient, ThrottleConfig{TransientAttempts: 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.Sleep = (&sleepRecorder{}).sleep
			calls := 0
			_, err := retryThrottled(context.Background(), tc.cfg, func() ([]byte, error) {
				calls++
				return nil, tc.err
			})
			if !errors.Is(err, tc.err) || calls != tc.wantCalls {
				t.Fatalf("calls = %d err = %v, want %d calls and %v", calls, err, tc.wantCalls, tc.err)
			}
		})
	}
}

// TestRetryThrottled_WaitBudgetBoundsTheTotal pins M4's second half: with a
// provider that keeps saying "retry in 80 s", the attempts alone would allow
// five waits (~400 s); MaxWait ends the call after three (240 s + the next
// 80 s would pass 300 s), so four calls reach the wire.
func TestRetryThrottled_WaitBudgetBoundsTheTotal(t *testing.T) {
	rec := &sleepRecorder{}
	calls := 0
	_, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: rec.sleep}, func() ([]byte, error) {
		calls++
		return nil, fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":80000}}", gmi.ErrRateLimited)
	})
	if !errors.Is(err, gmi.ErrRateLimited) {
		t.Fatalf("err = %v, want gmi.ErrRateLimited", err)
	}
	if calls != 4 || len(rec.waits()) != 3 {
		t.Fatalf("calls = %d, waits = %v, want 4 calls and 3 waits", calls, rec.waits())
	}
	var total time.Duration
	for _, w := range rec.waits() {
		total += w
	}
	if total > DefaultThrottleWait {
		t.Errorf("waited %s in total, want at most %s", total, DefaultThrottleWait)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("giving up after")) {
		t.Errorf("err = %q, want the budget annotation", err)
	}
}

func TestRetryThrottled(t *testing.T) {
	cases := []struct {
		name      string
		failures  int
		err       error
		attempts  int
		wantCalls int
		wantErrIs error // nil means success expected
	}{
		{"rate limit clears on second try", 1, gmi.ErrRateLimited, 3, 2, nil},
		{"rate limit clears on last try", 2, gmi.ErrRateLimited, 3, 3, nil},
		{"rate limit never clears", 9, gmi.ErrRateLimited, 3, 3, gmi.ErrRateLimited},
		{"transient never clears", 9, gmi.ErrTransient, 3, 3, gmi.ErrTransient},
		{"wrapped sentinel still counts", 1, fmt.Errorf("page 3: %w", gmi.ErrRateLimited), 3, 2, nil},
		{"attempts of one disables retry", 9, gmi.ErrRateLimited, 1, 1, gmi.ErrRateLimited},
		{"bad request is never retried", 9, gmi.ErrBadRequest, 3, 1, gmi.ErrBadRequest},
		{"unauthorized is never retried", 9, gmi.ErrUnauthorized, 3, 1, gmi.ErrUnauthorized},
		{"payment required is never retried", 9, gmi.ErrPaymentRequired, 3, 1, gmi.ErrPaymentRequired},
		{"model not found is never retried", 9, gmi.ErrModelNotFound, 3, 1, gmi.ErrModelNotFound},
		{"unclassified error is never retried", 9, errors.New("boom"), 3, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &sleepRecorder{}
			calls := 0
			raw, err := retryThrottled(context.Background(), ThrottleConfig{Attempts: tc.attempts, TransientAttempts: tc.attempts, Sleep: rec.sleep}, func() ([]byte, error) {
				calls++
				if calls <= tc.failures {
					return nil, tc.err
				}
				return []byte("ok"), nil
			})
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if got := len(rec.waits()); got != tc.wantCalls-1 && throttled(tc.err) {
				t.Fatalf("waits = %d, want %d", got, tc.wantCalls-1)
			}
			switch {
			case tc.failures >= tc.wantCalls && tc.wantErrIs != nil:
				if !errors.Is(err, tc.wantErrIs) || raw != nil {
					t.Fatalf("raw=%q err=%v, want nil bytes and %v", raw, err, tc.wantErrIs)
				}
			case tc.failures >= tc.wantCalls:
				if err == nil || raw != nil {
					t.Fatalf("raw=%q err=%v, want an error and nil bytes", raw, err)
				}
			default:
				if err != nil || string(raw) != "ok" {
					t.Fatalf("raw=%q err=%v, want ok", raw, err)
				}
			}
		})
	}
}

func TestRetryThrottled_ExhaustionIsAnnotatedAndKeepsTheSentinel(t *testing.T) {
	_, err := retryThrottled(context.Background(), ThrottleConfig{Attempts: 2, Sleep: (&sleepRecorder{}).sleep}, func() ([]byte, error) {
		return nil, fmt.Errorf("%w: %s", gmi.ErrRateLimited, rateLimitJSON)
	})
	if !errors.Is(err, gmi.ErrRateLimited) {
		t.Fatalf("err = %v, want gmi.ErrRateLimited", err)
	}
	if want := "still rate limited after 2 attempts"; !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Fatalf("err = %q, want it to say %q", err, want)
	}
}

// TestRetryThrottled_TransientExhaustionDoesNotClaimARateLimit pins L2(b):
// a 5xx or a failed render is not a throttle, and the operator reading a
// failed-book log must not be told it was.
func TestRetryThrottled_TransientExhaustionDoesNotClaimARateLimit(t *testing.T) {
	_, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: (&sleepRecorder{}).sleep}, func() ([]byte, error) {
		return nil, fmt.Errorf("%w: render failed", gmi.ErrTransient)
	})
	if !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want gmi.ErrTransient", err)
	}
	msg := err.Error()
	if bytes.Contains([]byte(msg), []byte("throttled")) || bytes.Contains([]byte(msg), []byte("rate limited after")) {
		t.Errorf("err = %q, must not describe a transient failure as a throttle", msg)
	}
	if !bytes.Contains([]byte(msg), []byte("still failing after 2 attempts")) {
		t.Errorf("err = %q, want it to say %q", msg, "still failing after 2 attempts")
	}
}

func TestRetryThrottled_BackoffDoublesAndIsCapped(t *testing.T) {
	rec := &sleepRecorder{}
	if _, err := retryThrottled(context.Background(), ThrottleConfig{TransientAttempts: 6, MaxWait: time.Hour, Backoff: 20 * time.Second, Sleep: rec.sleep}, func() ([]byte, error) {
		return nil, gmi.ErrTransient
	}); !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want the exhausted call to wrap gmi.ErrTransient", err)
	}
	// Each wait is jittered across [b/2, b] for b = 20, 40, 80, 90, 90.
	bases := []time.Duration{20 * time.Second, 40 * time.Second, 80 * time.Second, MaxThrottleBackoff, MaxThrottleBackoff}
	got := rec.waits()
	if len(got) != len(bases) {
		t.Fatalf("waits = %v, want %d of them", got, len(bases))
	}
	for i, b := range bases {
		if got[i] < b/2 || got[i] > b {
			t.Errorf("wait %d = %s, want within [%s, %s]", i, got[i], b/2, b)
		}
	}
}

func TestRetryThrottled_HonoursRetryAfter(t *testing.T) {
	rec := &sleepRecorder{}
	calls := 0
	_, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: rec.sleep}, func() ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("media: %w: %s", gmi.ErrRateLimited, rateLimitJSON)
		}
		return []byte("ok"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	w := rec.waits()
	if len(w) != 1 || w[0] < 29970*time.Millisecond || w[0] > 29970*time.Millisecond+time.Second {
		t.Fatalf("waits = %v, want one wait of retry_after_ms (29.97s) plus under a second of spread", w)
	}
}

func TestRetryThrottled_RetryAfterIsCapped(t *testing.T) {
	rec := &sleepRecorder{}
	calls := 0
	if _, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: rec.sleep}, func() ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, fmt.Errorf("%w: %s", gmi.ErrRateLimited, `{"rate_limit":{"retry_after_ms":3600000}}`)
		}
		return nil, nil
	}); err != nil {
		t.Fatalf("err = %v, want the second attempt to succeed", err)
	}
	if w := rec.waits(); len(w) != 1 || w[0] != MaxThrottleBackoff {
		t.Fatalf("waits = %v, want one wait capped at %s", w, MaxThrottleBackoff)
	}
}

// TestRetryThrottled_CancelledContextStopsWaiting pins M2: a context that
// ends during a wait surfaces as a cancellation (job.Runner classifies on
// errors.Is(err, context.Canceled)) and still carries what the provider said.
func TestRetryThrottled_CancelledContextStopsWaiting(t *testing.T) {
	for _, tc := range []struct {
		name string
		stop error
		mk   func() (context.Context, context.CancelFunc)
	}{
		{"cancel during the wait", context.Canceled, func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }},
		{"deadline already passed", context.DeadlineExceeded, func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.mk()
			defer cancel()
			calls := 0
			_, err := retryThrottled(ctx, ThrottleConfig{Sleep: func(ctx context.Context, d time.Duration) error {
				cancel()
				return tc.stop
			}}, func() ([]byte, error) {
				calls++
				if calls > 1 {
					t.Error("a second attempt ran after the wait was interrupted")
				}
				return nil, gmi.ErrRateLimited
			})
			if tc.stop == context.DeadlineExceeded {
				// The context is already expired when the first attempt
				// fails, so the loop ends before it ever sleeps.
				if calls != 1 || !errors.Is(err, tc.stop) || !errors.Is(err, gmi.ErrRateLimited) {
					t.Fatalf("calls=%d err=%v, want one call wrapping the deadline and gmi.ErrRateLimited", calls, err)
				}
				return
			}
			if calls != 1 || !errors.Is(err, context.Canceled) || !errors.Is(err, gmi.ErrRateLimited) {
				t.Fatalf("calls=%d err=%v, want one call wrapping both context.Canceled and gmi.ErrRateLimited", calls, err)
			}
		})
	}
	t.Run("cancelled while the attempt was failing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		slept := false
		_, err := retryThrottled(ctx, ThrottleConfig{Sleep: func(context.Context, time.Duration) error { slept = true; return nil }}, func() ([]byte, error) {
			cancel()
			return nil, gmi.ErrTransient
		})
		if slept || !errors.Is(err, context.Canceled) || !errors.Is(err, gmi.ErrTransient) {
			t.Fatalf("slept=%v err=%v, want no sleep and both context.Canceled and gmi.ErrTransient", slept, err)
		}
	})
}

// productionDefaultSleep is defaultSleep as the production binary has it.
// A package-level initialiser runs before TestMain, which replaces
// defaultSleep with an instant stub for every other test, so this is the
// only place the un-stubbed value can be seen.
var productionDefaultSleep = defaultSleep

// TestDefaultSleep_ProductionDefaultReallyWaits is round-5 M2. Every test of
// the zero-config path runs against TestMain's stub, so nothing else notices
// if the production default stops sleeping, which would leave the pacer
// spacing nothing and reintroduce nrynss/thutapi#1. This calls the value the
// production binary starts with: it must wait the interval it is given, and
// it must give up early on a cancelled context.
func TestDefaultSleep_ProductionDefaultReallyWaits(t *testing.T) {
	const d = 60 * time.Millisecond
	start := time.Now()
	if err := productionDefaultSleep(context.Background(), d); err != nil {
		t.Fatalf("production default sleep: %v", err)
	}
	if el := time.Since(start); el < d {
		t.Errorf("production default sleep(%s) returned after %s: it does not wait, so the pacer would not space anything", d, el)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := productionDefaultSleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("production default sleep on a cancelled ctx = %v, want context.Canceled", err)
	}
}

// TestRealSleep pins M3 against the production clock itself, so replacing
// its body with `return nil` or dropping its ctx.Done arm goes red.
func TestRealSleep(t *testing.T) {
	start := time.Now()
	if err := realSleep(context.Background(), 40*time.Millisecond); err != nil {
		t.Fatalf("realSleep: %v", err)
	}
	if el := time.Since(start); el < 40*time.Millisecond {
		t.Errorf("realSleep(40ms) returned after %s, want it to wait", el)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Run the hour-long sleeps off-thread so a missing ctx.Done arm fails
	// this test in seconds instead of hanging it.
	sleepsFor := func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() { done <- realSleep(ctx, time.Hour) }()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			return errors.New("realSleep ignored its context for 5s")
		}
	}
	if err := sleepsFor(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("realSleep on a cancelled ctx = %v, want context.Canceled", err)
	}

	// A context that ends mid-wait ends the wait.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if err := sleepsFor(ctx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("realSleep past its deadline = %v, want context.DeadlineExceeded", err)
	}
}

// TestRetryThrottled_OverflowingRetryAfterStillWaits pins M1 end to end: a
// garbled 429 body must never turn "wait" into "retry now".
func TestRetryThrottled_OverflowingRetryAfterStillWaits(t *testing.T) {
	for _, ms := range []string{"9223372036854775807", "9223372036855", "-1", "0"} {
		rec := &sleepRecorder{}
		calls := 0
		if _, err := retryThrottled(context.Background(), ThrottleConfig{Sleep: rec.sleep}, func() ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":%s}}", gmi.ErrRateLimited, ms)
			}
			return nil, nil
		}); err != nil {
			t.Fatalf("retry_after_ms %s: err = %v, want the second attempt to succeed", ms, err)
		}
		w := rec.waits()
		if len(w) != 1 || w[0] <= 0 || w[0] > MaxThrottleBackoff {
			t.Errorf("retry_after_ms=%s: waits = %v, want one wait in (0, %s]", ms, w, MaxThrottleBackoff)
		}
		if ms == "9223372036854775807" && w[0] < MaxThrottleBackoff {
			t.Errorf("retry_after_ms=%s: wait = %s, want the cap %s", ms, w[0], MaxThrottleBackoff)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want time.Duration
		ok   bool
	}{
		{"live 429 body", fmt.Errorf("%w: %s", gmi.ErrRateLimited, rateLimitJSON), 29970 * time.Millisecond, true},
		{"wrapped and trailing text", fmt.Errorf("page 2: %w", fmt.Errorf("%w: %s (x)", gmi.ErrRateLimited, rateLimitJSON)), 29970 * time.Millisecond, true},
		{"no body", gmi.ErrRateLimited, 0, false},
		{"body without rate_limit", fmt.Errorf("%w: {\"error\":\"slow down\"}", gmi.ErrRateLimited), 0, false},
		{"malformed json", fmt.Errorf("%w: {not json", gmi.ErrRateLimited), 0, false},
		{"zero value", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":0}}", gmi.ErrRateLimited), 0, false},
		{"negative value", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":-5}}", gmi.ErrRateLimited), 0, false},
		{"huge value is clamped, not wrapped", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":9223372036854775807}}", gmi.ErrRateLimited), MaxThrottleBackoff, true},
		{"value that wraps negative when multiplied", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":9223372036855}}", gmi.ErrRateLimited), MaxThrottleBackoff, true},
		{"just over the cap", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":90001}}", gmi.ErrRateLimited), MaxThrottleBackoff, true},
		{"exactly the cap", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":90000}}", gmi.ErrRateLimited), MaxThrottleBackoff, true},
		{"minimum int64", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":-9223372036854775808}}", gmi.ErrRateLimited), 0, false},
		{"fractional value does not decode", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":1.5}}", gmi.ErrRateLimited), 0, false},
		{"string value does not decode", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":\"soon\"}}", gmi.ErrRateLimited), 0, false},
		{"beyond int64 does not decode", fmt.Errorf("%w: {\"rate_limit\":{\"retry_after_ms\":99999999999999999999}}", gmi.ErrRateLimited), 0, false},
		{"not a rate limit", fmt.Errorf("%w: %s", gmi.ErrTransient, rateLimitJSON), 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := retryAfter(tc.err)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("retryAfter = (%s, %v), want (%s, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestIllustrate_DefaultThrottleRetriesOnTheWire is rule 2 for the defaults:
// Config is empty, so Attempts, Backoff and the clock all come from the
// default path, and the assertion is on what reached the imager.
func TestIllustrate_DefaultThrottleRetriesOnTheWire(t *testing.T) {
	rec := &sleepRecorder{}
	prev := defaultSleep
	defaultSleep = rec.sleep
	t.Cleanup(func() { defaultSleep = prev })

	var gens, edits atomic.Int32
	fake := &fakeImager{
		generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
			if gens.Add(1) == 1 {
				return nil, fmt.Errorf("%w: %s", gmi.ErrRateLimited, rateLimitJSON)
			}
			return queueEnvelope(fixtureMedia.serve("sheet", pngBytes("sheet:"+prompt))), nil
		},
		edit: func(ctx context.Context, prompt, model string, refs []string, opts media.ImageOptions) ([]byte, error) {
			if edits.Add(1) == 1 {
				return nil, fmt.Errorf("%w: boom", gmi.ErrTransient)
			}
			return queueEnvelope(fixtureMedia.serve("page", pngBytes("page:"+prompt))), nil
		},
	}
	if _, err := Illustrate(context.Background(), Config{Imager: fake, Throttle: ThrottleConfig{RequestsPerMinute: -1}}, twoCastStory()); err != nil {
		t.Fatalf("Illustrate with one 429 and one 5xx: %v", err)
	}
	calls, _ := fake.snapshot()
	var gen, edit int
	for _, c := range calls {
		if c.model != DefaultModel {
			t.Errorf("%s call carried model %q, want the default %q", c.kind, c.model, DefaultModel)
		}
		if c.kind == "generate" {
			gen++
		} else {
			edit++
		}
	}
	if gen != 3 || edit != 3 {
		t.Errorf("calls: %d generate, %d edit, want 3 and 3 (2 renders each plus one retry)", gen, edit)
	}
	if got := rec.waits(); len(got) != 2 {
		t.Errorf("waits = %v, want two (one per throttled call)", got)
	}
}

func TestIllustrate_ThrottleExhaustion(t *testing.T) {
	for _, sent := range []error{gmi.ErrRateLimited, gmi.ErrTransient} {
		t.Run("reference/"+sent.Error(), func(t *testing.T) {
			var gens atomic.Int32
			fake := &fakeImager{generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
				if ctx.Err() != nil {
					return nil, ctx.Err() // a sibling already failed the group
				}
				gens.Add(1)
				return nil, fmt.Errorf("%w: upstream", sent)
			}}
			rec := &sleepRecorder{}
			book, err := Illustrate(context.Background(), Config{Imager: fake, Limit: 1, Throttle: ThrottleConfig{Attempts: 3, TransientAttempts: 3, Sleep: rec.sleep}}, twoCastStory())
			if !errors.Is(err, sent) {
				t.Fatalf("err = %v, want %v", err, sent)
			}
			assertZeroBook(t, book)
			if got := gens.Load(); got != 3 {
				t.Errorf("GenerateImage called %d times, want 3 (Attempts) before the run fails", got)
			}
		})
		t.Run("page/"+sent.Error(), func(t *testing.T) {
			var edits atomic.Int32
			fake := &fakeImager{edit: func(ctx context.Context, prompt, model string, refs []string, opts media.ImageOptions) ([]byte, error) {
				if ctx.Err() != nil {
					return nil, ctx.Err() // a sibling already failed the group
				}
				edits.Add(1)
				return nil, fmt.Errorf("%w: upstream", sent)
			}}
			book, err := Illustrate(context.Background(), Config{Imager: fake, Limit: 1, Throttle: ThrottleConfig{Attempts: 3, TransientAttempts: 3, Sleep: (&sleepRecorder{}).sleep}}, twoCastStory())
			if !errors.Is(err, sent) {
				t.Fatalf("err = %v, want %v", err, sent)
			}
			assertZeroBook(t, book)
			if got := edits.Load(); got != 3 {
				t.Errorf("EditImage called %d times, want 3", got)
			}
		})
	}
}

func TestIllustrate_NonThrottleErrorsAreNotRetried(t *testing.T) {
	for _, sent := range []error{gmi.ErrBadRequest, gmi.ErrUnauthorized, gmi.ErrPaymentRequired, gmi.ErrModelNotFound} {
		t.Run(sent.Error(), func(t *testing.T) {
			var gens atomic.Int32
			fake := &fakeImager{generate: func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
				if ctx.Err() != nil {
					return nil, ctx.Err() // a sibling already failed the group
				}
				gens.Add(1)
				return nil, fmt.Errorf("%w: no", sent)
			}}
			_, err := Illustrate(context.Background(), Config{Imager: fake, Limit: 1}, twoCastStory())
			if !errors.Is(err, sent) {
				t.Fatalf("err = %v, want %v", err, sent)
			}
			if got := gens.Load(); got != 1 {
				t.Errorf("GenerateImage called %d times, want 1", got)
			}
		})
	}
}

func TestIllustrate_RegenerationRetriesAThrottle(t *testing.T) {
	var edits atomic.Int32
	fake := &fakeImager{edit: func(ctx context.Context, prompt, model string, refs []string, opts media.ImageOptions) ([]byte, error) {
		n := edits.Add(1)
		if n == 2 {
			return nil, fmt.Errorf("%w: slow down", gmi.ErrRateLimited)
		}
		return queueEnvelope(fixtureMedia.url(fmt.Sprintf("regen-throttle-%d", n))), nil
	}}
	judge := &scriptedJudge{out: []bool{false, true}}
	rec := &sleepRecorder{}
	if _, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge, Throttle: ThrottleConfig{RequestsPerMinute: -1, Sleep: rec.sleep}}, oneCastStory()); err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if got := edits.Load(); got != 3 {
		t.Errorf("EditImage calls = %d, want 3 (render, throttled regeneration, retried regeneration)", got)
	}
	if len(rec.waits()) != 1 {
		t.Errorf("waits = %v, want one", rec.waits())
	}
}

// TestIllustrate_ReferenceLimitBoundsTheOpeningBurst pins the default path:
// ReferenceLimit and Limit are both zero and three sheets are wanted, but at
// most DefaultReferenceLimit are ever in flight.
func TestIllustrate_ReferenceLimitBoundsTheOpeningBurst(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want int32
	}{
		{"default", Config{}, DefaultReferenceLimit},
		{"explicit wider", Config{ReferenceLimit: 3}, 3},
		{"never wider than Limit", Config{ReferenceLimit: 3, Limit: 1}, 1},
		{"negative means default", Config{ReferenceLimit: -1}, DefaultReferenceLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			cfg := tc.cfg
			cfg.Imager = fake
			if _, err := Illustrate(context.Background(), cfg, threeCastStory()); err != nil {
				t.Fatal(err)
			}
			if got := peak.Load(); got != tc.want {
				t.Errorf("peak concurrent reference sheets = %d, want %d", got, tc.want)
			}
		})
	}
}

func threeCastStory() story.Story {
	s := twoCastStory()
	s.Cast = append(s.Cast, story.CastMember{Name: "Pip", Visual: "a tiny grey mouse with a blue scarf"})
	s.Pages = append(s.Pages, page(3, "Pip hides under the gate.", "Pip", "Mira"))
	return s
}

// TestWire_RateLimitedThenSuccess runs the real media client against an
// httptest server that answers the first image POST with GMI's own 429 body
// and everything after it normally: the book completes, the throttled
// request was re-sent byte-identical, and the wait was the provider's.
func TestWire_RateLimitedThenSuccess(t *testing.T) {
	ws := newWireServer(t)
	target, err := url.Parse(ws.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var posts atomic.Int32
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && posts.Add(1) == 1 {
			http.Error(w, rateLimitJSON, http.StatusTooManyRequests)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	t.Setenv("GMI_MEDIA_BASE_URL", front.URL)

	rec := &sleepRecorder{}
	book, err := Illustrate(context.Background(), Config{Imager: wireClient(), Throttle: ThrottleConfig{RequestsPerMinute: -1, Sleep: rec.sleep}}, twoCastStory())
	if err != nil {
		t.Fatalf("Illustrate across a 429: %v", err)
	}
	if len(book.References) != 2 || len(book.Pages) != 2 {
		t.Fatalf("book = %d sheets, %d pages, want 2 and 2", len(book.References), len(book.Pages))
	}
	w := rec.waits()
	if len(w) != 1 || w[0] < 29970*time.Millisecond || w[0] > 29970*time.Millisecond+time.Second {
		t.Fatalf("waits = %v, want the provider's retry_after_ms", w)
	}
	reqs := ws.captured()
	if len(reqs) != 4 {
		t.Fatalf("%d requests reached the model, want 4 (the 429'd one never got past the front)", len(reqs))
	}
	for i, r := range reqs {
		if r.env.Model != DefaultModel {
			t.Errorf("request %d model = %q, want %q", i, r.env.Model, DefaultModel)
		}
	}
}
