package audio

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"thutapi/internal/gmi"
)

// Throttled provider calls, and why this file exists.
//
// GMI caps requests per minute per model. Narration fans out eight speech
// calls and the film asks for one music bed, and both hit that cap on a
// normal book: on 2026-09-06 a live run lost four of eight narration clips
// and its whole music bed to it, and the child was handed a silent film.
//
// Nothing retried any of it. internal/gmi's package doc has always told
// callers to "retry with backoff" on gmi.ErrRateLimited, and no caller did —
// the one retry that existed (internal/gmi/media's single resubmit on
// gmi.ErrTransient) is immediate, which against a per-minute cap is just a
// second failure a few milliseconds later. A per-minute cap needs a wait
// measured in tens of seconds, and that wait belongs to the consumer that
// knows whether it can afford one. Narration and the music bed both can:
// they run after every image is paid for, and their alternative is a book
// nobody can hear.

// DefaultThrottleAttempts is how many times a throttled provider call is
// tried in total, including the first. Three spends at most two waits on a
// clip and still recovers the common case, which is one wave of a fan-out
// arriving inside a minute the previous wave already filled.
const DefaultThrottleAttempts = 3

// DefaultThrottleBackoff is the wait before the second attempt; each further
// wait doubles it. Twenty seconds is sized for a per-minute cap — short
// enough that two waits stay inside the generation budget, long enough that
// the retry lands in a fresh minute rather than the one that just rejected
// it.
const DefaultThrottleBackoff = 20 * time.Second

// MaxThrottleBackoff caps one wait however many attempts are configured, so
// a large Attempts cannot park a page behind a wait longer than the poll
// budget it is competing with.
const MaxThrottleBackoff = 90 * time.Second

// ThrottleConfig paces the retry a throttled provider call gets. The zero
// value is usable and means DefaultThrottleAttempts and
// DefaultThrottleBackoff.
type ThrottleConfig struct {
	// Attempts is the total number of tries, including the first. Zero or
	// negative means DefaultThrottleAttempts; one disables retrying.
	Attempts int

	// Backoff is the wait before the second attempt, doubled for each
	// attempt after it and capped at MaxThrottleBackoff. Zero or negative
	// means DefaultThrottleBackoff.
	Backoff time.Duration
}

// withDefaults substitutes the package defaults for zero and negative fields.
func (tc ThrottleConfig) withDefaults() ThrottleConfig {
	if tc.Attempts <= 0 {
		tc.Attempts = DefaultThrottleAttempts
	}
	if tc.Backoff <= 0 {
		tc.Backoff = DefaultThrottleBackoff
	}
	return tc
}

// retryThrottled runs call until it succeeds, until it fails with something a
// wait cannot fix, or until the attempts are spent. It returns call's own
// error untouched, so every sentinel a caller matches on survives the retry.
//
// Only gmi.ErrRateLimited and gmi.ErrTransient are waited on: a cap and an
// upstream wobble are the two conditions that heal on their own. Everything
// else — a missing model, a bad request, a rejected key — surfaces on the
// first attempt, because retrying it would spend the budget of the calls
// that could still have succeeded.
//
// The wait is full-jittered across the lower half of each backoff so that a
// fan-out throttled together does not retry in lockstep and re-trip the same
// cap. A cancelled context ends the wait immediately and returns the error
// the last attempt produced, never the context's own: the caller degrades on
// what the provider said.
func retryThrottled(ctx context.Context, tc ThrottleConfig, call func() error) error {
	tc = tc.withDefaults()
	backoff := tc.Backoff
	var err error
	for attempt := 1; ; attempt++ {
		err = call()
		if err == nil {
			return nil
		}
		if attempt >= tc.Attempts || !throttled(err) || ctx.Err() != nil {
			return err
		}
		if !wait(ctx, jitter(backoff)) {
			return err
		}
		if backoff = 2 * backoff; backoff > MaxThrottleBackoff {
			backoff = MaxThrottleBackoff
		}
	}
}

// throttled reports whether err is a condition that waiting can clear.
func throttled(err error) bool {
	return errors.Is(err, gmi.ErrRateLimited) || errors.Is(err, gmi.ErrTransient)
}

// jitter spreads a wait across the upper half of d, so concurrent callers
// that were throttled at the same moment come back at different moments
// without any of them waiting less than half the intended gap.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// wait sleeps for d and reports whether it completed. A cancelled context
// stops it early and reports false.
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// throttleNote annotates an error that survived every attempt, so an
// operator reading a degraded book's log can tell a cap that was waited out
// and still refused from one that was never retried at all.
func throttleNote(err error, tc ThrottleConfig) error {
	if !throttled(err) {
		return err
	}
	return fmt.Errorf("still throttled after %d attempts: %w", tc.withDefaults().Attempts, err)
}
