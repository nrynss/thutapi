// Request-queue polling (PLAN.md §T3b, invariant 4): the POST that opens a
// request-queue call answers {request_id, status}; while the answer names
// a request id and no terminal status — queued or processing, or a status
// this client does not know — the queue may still hold the work, so the
// client polls GET .../requests/{request_id} until the record is terminal
// and hands the completed body to the caller as raw bytes — the same
// contract as every other response in this package. A submit answer with
// no request_id cannot be polled: it passes through as the caller's raw
// bytes, exactly as T2 delivered every body before polling existed.
//
// Wire facts and their provenance:
//
//   - Submit path and {model, payload} envelope: PLAN.md §T2 (pinned by
//     the T2 suite).
//   - Poll path .../requests/{request_id}: the documented submit path plus
//     the request id; the same shape the one live consumer of this API on
//     this workstation drives successfully.
//   - Statuses queued/processing/completed/failed: PLAN.md §T3b.
//   - success (a synonym of completed) and cancelled: live-observed status
//     strings from that consumer — accepted here so a real queue response
//     cannot wedge the poll loop. Cancellation is a distinct condition
//     with its own sentinel; it is not a failure of the payload and not
//     transient, so it is never resubmitted.
//
// Retry interaction, decided and pinned (TestPoll_*): each HTTP call site
// keeps T2's one-retry budget. The submit POST retries once inside post
// (pinned by TestRetry_5xxHitTwice). A poll GET that fails transiently is
// tolerated once — the next tick is its single retry; a second consecutive
// transient poll failure surfaces gmi.ErrTransient. A failed terminal
// status mid-poll gets exactly one resubmit through the same post path (a
// resubmit is the only way a request-queue failure can heal — polling the
// same request id again returns the same terminal record); a second failed
// surfaces gmi.ErrTransient. Everything else surfaces immediately with its
// own sentinel.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"thutapi/internal/gmi"
)

// DefaultPollInterval is the gap between poll GETs for a Client built with
// the zero PollConfig. project.md states no interval (its request-queue
// sections document the envelope, not the pacing); 2s is the documented
// default — comfortably inside the ~15s a request-queue image call takes,
// and cheap for the four or five polls that means.
const DefaultPollInterval = 2 * time.Second

// DefaultPollTimeout bounds the whole poll loop for a caller that passed a
// context without a deadline (PLAN.md §T2: every call carries a context
// deadline). Generous on purpose: the submit call has already spent its
// own defaultCallTimeout budget by the time polling starts.
const DefaultPollTimeout = 120 * time.Second

// PollConfig paces and bounds the poll loop. The zero value is usable and
// means DefaultPollInterval and DefaultPollTimeout. Timeout is expected to
// exceed Interval: an Interval at or past the Timeout polls never and
// surfaces ErrPollDeadline — misconfiguration reported, not silently
// repaired.
type PollConfig struct {
	// Interval is the gap between poll GETs. Zero or negative means
	// DefaultPollInterval.
	Interval time.Duration

	// Timeout bounds the whole loop. Zero or negative means
	// DefaultPollTimeout. A caller context with a shorter deadline
	// wins — the loop stops at whichever fires first.
	Timeout time.Duration
}

// withDefaults substitutes the package defaults for zero (and negative)
// fields.
func (pc PollConfig) withDefaults() PollConfig {
	if pc.Interval <= 0 {
		pc.Interval = DefaultPollInterval
	}
	if pc.Timeout <= 0 {
		pc.Timeout = DefaultPollTimeout
	}
	return pc
}

// Sentinel errors declared by the polling layer. internal/gmi's sentinels
// are untouched — these are media-local conditions (PLAN.md invariant 8
// allows a track-declared sentinel).
var (
	// ErrPollDeadline is returned when the poll loop is cut short by
	// its own Timeout budget or by the caller's context deadline,
	// whichever fires first. errors.Is matches this sentinel, and the
	// wrapping keeps the underlying context error in the chain — a
	// caller distinguishing user-cancel from budget-exhausted matches
	// context.Canceled / context.DeadlineExceeded.
	ErrPollDeadline = errors.New("media: request-queue poll deadline exceeded")

	// ErrCancelled is returned when the queue reports the request
	// cancelled upstream. Distinct from failed: it is not the
	// payload's fault and not transient, so it is surfaced as-is and
	// never resubmitted.
	ErrCancelled = errors.New("media: request queue cancelled the request")
)

// Internal markers: errTransientPoll marks a poll GET whose failure the
// one-tick budget tolerates once; errQueueFailed marks a failed terminal
// status, which drive() answers with exactly one resubmit.
var (
	errTransientPoll = errors.New("media: poll attempt failed transiently")
	errQueueFailed   = errors.New("media: request queue reported failed")
)

// NewWithPoll returns a Client like New, with an explicit poll pacing —
// config flows down from main (PLAN.md invariant 2) for callers that must
// not inherit the package defaults. The base URL still comes from
// GMI_MEDIA_BASE_URL, the package's recorded env seam.
func NewWithPoll(poll PollConfig) *Client {
	c := New()
	c.poll = poll
	return c
}

// drive runs one request-queue call to a terminal state: the submit POST
// (post, with its own untouched one-retry contract), then — when the queue
// answers queued or processing — the poll loop. A failed terminal status
// mid-poll is answered with exactly one resubmit; the second failure
// surfaces gmi.ErrTransient. Every other error surfaces as-is with its own
// sentinel.
func (c *Client) drive(ctx context.Context, model, accept string, payload map[string]any) ([]byte, error) {
	raw, err := c.post(ctx, model, accept, payload)
	if err != nil {
		return nil, err
	}
	body, err := c.settle(ctx, raw)
	if err == nil {
		return body, nil
	}
	if !errors.Is(err, errQueueFailed) {
		return nil, err
	}

	// The queue failed the request. Polling the same id again returns
	// the same terminal record, so the one internal retry is a
	// resubmit — the same answer T2's contract gives a failed SUBMIT
	// body, and the only way this call can still succeed.
	raw, err = c.post(ctx, model, accept, payload)
	if err != nil {
		if errors.Is(err, gmi.ErrTransient) {
			// Dual wrap: the resubmit's failure is transient,
			// and its own chain (e.g. a deadline) stays intact.
			return nil, fmt.Errorf("%w: resubmit after failed status: %w", gmi.ErrTransient, err)
		}
		return nil, err
	}
	body, err = c.settle(ctx, raw)
	if err != nil {
		if errors.Is(err, errQueueFailed) {
			return nil, fmt.Errorf("%w: still failing after one resubmit: %w", gmi.ErrTransient, err)
		}
		return nil, err
	}
	return body, nil
}

// settle routes one submit response body: terminal-good or not-a-queue-
// record bodies go back to the caller as raw bytes (the T2 passthrough,
// unchanged); queued/processing enter the poll loop; an unknown or
// absent status is not a terminal answer — pollOnce waits out the same
// statuses mid-poll — so a record that names a request_id enters the
// poll loop too and the deadline bounds the wait, while a body with no
// request_id cannot be polled and passes through untouched. post() has
// already converted a failed submit body to an error, so settle never
// sees one.
func (c *Client) settle(ctx context.Context, raw []byte) ([]byte, error) {
	var st queueStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		// Not a queue record — TTS audio bytes and friends. The
		// peek no-ops, exactly as in attempt.
		return raw, nil
	}
	switch strings.ToLower(st.Status) {
	case "completed", "success":
		return raw, nil
	case "queued", "processing":
		if st.RequestID == "" {
			return nil, fmt.Errorf("%w: queue accepted the request but returned no request_id: %s", gmi.ErrTransient, strings.TrimSpace(string(raw)))
		}
		return c.await(ctx, st.RequestID)
	default:
		// Unknown or absent status: not a terminal answer. With a
		// request id the record may still be in flight — wait it
		// out like any non-terminal status; the poll deadline is
		// the bound. Without one there is nothing to poll: the
		// caller's bytes, as T2 handed them down before polling
		// existed.
		if st.RequestID == "" {
			return raw, nil
		}
		return c.await(ctx, st.RequestID)
	}
}

// poll drives GET .../requests/{request_id} every cfg.Interval until the
// record is terminal-good (raw bytes returned), a terminal-bad sentinel
// fires, or the budget runs out. The budget is min(PollConfig.Timeout,
// the caller's context deadline): the select below and the
// NewRequestWithContext inside pollOnce both honour it, so neither an
// iteration nor the loop as a whole can outlive the deadline.
func (c *Client) await(ctx context.Context, requestID string) ([]byte, error) {
	cfg := c.poll.withDefaults()
	pollCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	apiKey := os.Getenv("GMI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("%w: GMI_API_KEY not set", gmi.ErrUnauthorized)
	}

	endpoint, err := url.JoinPath(c.baseURL, pathRequestQueue, requestID)
	if err != nil {
		return nil, fmt.Errorf("media: join path: %w", err)
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	last := "no response yet"
	transients := 0
	for {
		select {
		case <-pollCtx.Done():
			return nil, fmt.Errorf("%w: polling request %s (last status: %s): %w", ErrPollDeadline, requestID, last, pollCtx.Err())
		case <-ticker.C:
			raw, status, done, err := c.pollOnce(pollCtx, requestID, endpoint, apiKey)
			if err != nil {
				if errors.Is(err, errTransientPoll) {
					if pollCtx.Err() != nil {
						// The budget ran out while
						// this GET was in flight:
						// the deadline, not the
						// queue, aborted it.
						// Counting it as transient
						// would spend the budget on
						// an instantly-failing next
						// GET and mislabel a
						// deadline as retry-now.
						return nil, fmt.Errorf("%w: polling request %s (last status: %s): %w", ErrPollDeadline, requestID, last, pollCtx.Err())
					}
					// The next tick is this GET's one
					// retry; a second consecutive
					// transient spends the budget.
					transients++
					if transients > 1 {
						return nil, fmt.Errorf("%w: polling request %s: %w", gmi.ErrTransient, requestID, err)
					}
					continue
				}
				return nil, err
			}
			transients = 0
			if done {
				return raw, nil
			}
			last = status
		}
	}
}

// pollOnce performs one poll GET. It returns the body and its status when
// the record is terminal-good (done), a nil error with done=false for
// every non-terminal status (queued, processing, and any unknown status —
// the deadline bounds how long an unknown status is waited out), and an
// error otherwise: errTransientPoll for the failures the tick budget
// tolerates, the classified sentinel for everything else.
func (c *Client) pollOnce(ctx context.Context, requestID, endpoint, apiKey string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("%w: build request: %w", errTransientPoll, err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", acceptJSON)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("%w: %w", errTransientPoll, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", false, fmt.Errorf("%w: read body: %w", errTransientPoll, err)
	}

	if resp.StatusCode != http.StatusOK {
		cerr := classifyStatus(resp.StatusCode, raw)
		if errors.Is(cerr, gmi.ErrTransient) {
			return nil, "", false, fmt.Errorf("%w: %w", errTransientPoll, cerr)
		}
		return nil, "", false, cerr
	}

	var st queueStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, "", false, fmt.Errorf("%w: poll body is not a queue record: %.200s", errTransientPoll, raw)
	}

	status := strings.ToLower(st.Status)
	switch status {
	case "completed", "success":
		return raw, status, true, nil
	case "failed":
		return nil, status, false, fmt.Errorf("%w: %s", errQueueFailed, strings.TrimSpace(string(raw)))
	case "cancelled":
		return nil, status, false, fmt.Errorf("%w: request %s was cancelled upstream: %s", ErrCancelled, requestID, strings.TrimSpace(string(raw)))
	default:
		// queued, processing, and anything undocumented: keep
		// waiting; the deadline is the bound.
		return nil, status, false, nil
	}
}
