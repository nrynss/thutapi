package audio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"thutapi/internal/gmi"
)

// fastThrottle is every retry test's pacing: the real waits are tens of
// seconds and the decision under test is which errors wait at all, not how
// long. Backoff is a real timer, so the retry path runs exactly as it does
// in production — just quickly.
var fastThrottle = ThrottleConfig{Backoff: time.Microsecond}

func TestThrottleConfig_Defaults(t *testing.T) {
	got := ThrottleConfig{}.withDefaults()
	if got.Attempts != DefaultThrottleAttempts || got.Backoff != DefaultThrottleBackoff {
		t.Fatalf("zero ThrottleConfig = %+v, want %d attempts and %s", got, DefaultThrottleAttempts, DefaultThrottleBackoff)
	}
	negative := ThrottleConfig{Attempts: -3, Backoff: -time.Second}.withDefaults()
	if negative.Attempts != DefaultThrottleAttempts || negative.Backoff != DefaultThrottleBackoff {
		t.Fatalf("negative ThrottleConfig = %+v, want the defaults", negative)
	}
	explicit := ThrottleConfig{Attempts: 7, Backoff: time.Minute}.withDefaults()
	if explicit.Attempts != 7 || explicit.Backoff != time.Minute {
		t.Fatalf("explicit ThrottleConfig = %+v, want it left alone", explicit)
	}
}

// TestRetryThrottled_WaitsOutACapAndSurrenders is the behaviour that the
// silent book of 2026-09-06 needed and did not have: a per-minute cap is
// waited out, and the error that survives every attempt is still the
// provider's own sentinel so the caller can degrade on it.
func TestRetryThrottled_WaitsOutACapAndSurrenders(t *testing.T) {
	cases := []struct {
		name       string
		failures   int
		err        error
		wantCalls  int
		wantErrIs  error
		wantErrNil bool
	}{
		{name: "rate limit clears on the second try", failures: 1, err: gmi.ErrRateLimited, wantCalls: 2, wantErrNil: true},
		{name: "rate limit clears on the last try", failures: 2, err: gmi.ErrRateLimited, wantCalls: 3, wantErrNil: true},
		{name: "rate limit never clears", failures: 9, err: gmi.ErrRateLimited, wantCalls: 3, wantErrIs: gmi.ErrRateLimited},
		{name: "transient is waited on too", failures: 9, err: gmi.ErrTransient, wantCalls: 3, wantErrIs: gmi.ErrTransient},
		{name: "wrapped sentinel still counts", failures: 1, err: fmt.Errorf("page 3: %w", gmi.ErrRateLimited), wantCalls: 2, wantErrNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			err := retryThrottled(context.Background(), fastThrottle, func() error {
				calls++
				if calls <= tc.failures {
					return tc.err
				}
				return nil
			})
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if tc.wantErrNil && err != nil {
				t.Fatalf("err = %v, want nil once the cap cleared", err)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("err = %v, want %v to survive the retry", err, tc.wantErrIs)
			}
		})
	}
}

// TestRetryThrottled_SurfacesEverythingElseAtOnce is the other half of the
// classification: waiting on an error a wait cannot fix would spend the
// budget of the calls that could still have succeeded.
func TestRetryThrottled_SurfacesEverythingElseAtOnce(t *testing.T) {
	for _, err := range []error{gmi.ErrModelNotFound, gmi.ErrBadRequest, gmi.ErrUnauthorized, gmi.ErrPaymentRequired, errors.New("audio: measure clip")} {
		calls := 0
		got := retryThrottled(context.Background(), fastThrottle, func() error {
			calls++
			return err
		})
		if calls != 1 {
			t.Errorf("%v was retried %d times, want 1 attempt", err, calls)
		}
		if !errors.Is(got, err) {
			t.Errorf("err = %v, want %v untouched", got, err)
		}
	}
}

func TestRetryThrottled_AttemptsOfOneNeverRetries(t *testing.T) {
	calls := 0
	err := retryThrottled(context.Background(), ThrottleConfig{Attempts: 1, Backoff: time.Microsecond}, func() error {
		calls++
		return gmi.ErrRateLimited
	})
	if calls != 1 || !errors.Is(err, gmi.ErrRateLimited) {
		t.Fatalf("calls = %d, err = %v; want one attempt and the cap surfaced", calls, err)
	}
}

// TestRetryThrottled_CancelledContextStopsTheWait pins that a run being
// torn down does not sit in a backoff, and that the caller still gets the
// PROVIDER's error rather than the context's — the caller degrades on what
// the provider said.
func TestRetryThrottled_CancelledContextStopsTheWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retryThrottled(ctx, ThrottleConfig{Attempts: 5, Backoff: time.Hour}, func() error {
		calls++
		cancel()
		return gmi.ErrRateLimited
	})
	if calls != 1 {
		t.Fatalf("calls = %d, want the cancelled context to stop after the first", calls)
	}
	if !errors.Is(err, gmi.ErrRateLimited) {
		t.Fatalf("err = %v, want the provider's own error", err)
	}
}

func TestThrottleNote(t *testing.T) {
	capped := throttleNote(gmi.ErrRateLimited, ThrottleConfig{Attempts: 3})
	if !errors.Is(capped, gmi.ErrRateLimited) || !strings.Contains(capped.Error(), "still throttled after 3 attempts") {
		t.Fatalf("throttleNote(rate limited) = %v, want the attempt count and the sentinel", capped)
	}
	plain := errors.New("audio: something else")
	if got := throttleNote(plain, ThrottleConfig{}); got != plain {
		t.Fatalf("throttleNote(%v) = %v, want it returned untouched", plain, got)
	}
}

func TestJitter_StaysInTheUpperHalf(t *testing.T) {
	const d = 20 * time.Second
	for i := 0; i < 200; i++ {
		got := jitter(d)
		if got < d/2 || got > d {
			t.Fatalf("jitter(%s) = %s, want it inside [%s, %s]", d, got, d/2, d)
		}
	}
	if got := jitter(0); got != 0 {
		t.Fatalf("jitter(0) = %s, want 0", got)
	}
}
