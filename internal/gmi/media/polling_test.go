package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"thutapi/internal/gmi"
)

// queueHits counts the requests a fake queue saw and captures the last
// GET's wire details, so the tests can pin the poll path, its auth header
// and its Accept header — the wire-level assertions AGENTS.md asks for.
type queueHits struct {
	mu        sync.Mutex
	post, get int
	getMethod string
	getPath   string
	getAuth   string
	getAccept string
}

// tally returns the counts and the last GET's details under the lock.
func (h *queueHits) tally() (posts, gets int, path, auth, accept, method string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.post, h.get, h.getPath, h.getAuth, h.getAccept, h.getMethod
}

// fakeQueue stands in for the request-queue host: post(n)/get(n) return
// the (status, body) for the n-th POST/GET. It wires GMI_API_KEY and
// GMI_MEDIA_BASE_URL the same way the T2 helpers do.
func fakeQueue(t *testing.T, post, get func(n int) (int, string)) *queueHits {
	t.Helper()
	h := &queueHits{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var code int
		var body string
		switch r.Method {
		case http.MethodPost:
			h.mu.Lock()
			h.post++
			n := h.post
			h.mu.Unlock()
			code, body = post(n)
		case http.MethodGet:
			h.mu.Lock()
			h.get++
			n := h.get
			h.mu.Unlock()
			code, body = get(n)
			h.mu.Lock()
			h.getMethod = r.Method
			h.getPath = r.URL.Path
			h.getAuth = r.Header.Get("Authorization")
			h.getAccept = r.Header.Get("Accept")
			h.mu.Unlock()
		default:
			code, body = http.StatusMethodNotAllowed, "unexpected method"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GMI_API_KEY", "k")
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)
	return h
}

// fastClient is a client with an injected 1ms poll interval; tests that
// assert pacing defaults use New() instead.
func fastClient() *Client {
	return NewWithPoll(PollConfig{Interval: time.Millisecond})
}

// TestPoll_QueuedProcessingCompleted is the done-when probe: the poller
// takes a queued submit through processing to completed and hands the
// completed body back as raw bytes — pinned at the wire, including the
// GET path, bearer auth and JSON Accept.
func TestPoll_QueuedProcessingCompleted(t *testing.T) {
	completed := `{"request_id":"req-1","status":"completed","result":{"b64_json":"ZmluaXNoZWQ="}}`
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			switch n {
			case 1:
				return http.StatusOK, `{"request_id":"req-1","status":"processing"}`
			default:
				return http.StatusOK, completed
			}
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if string(raw) != completed {
		t.Errorf("raw = %q, want the completed body as raw bytes", raw)
	}

	posts, gets, path, auth, accept, method := h.tally()
	if posts != 1 || gets != 2 {
		t.Errorf("hits: %d POST(s), %d GET(s), want 1 POST and 2 GETs", posts, gets)
	}
	if want := pathRequestQueue + "/req-1"; path != want {
		t.Errorf("GET path = %q, want %q", path, want)
	}
	if method != http.MethodGet {
		t.Errorf("poll method = %q, want GET", method)
	}
	if auth != "Bearer k" {
		t.Errorf("GET auth = %q, want the bearer key", auth)
	}
	if accept != acceptJSON {
		t.Errorf("GET accept = %q, want %q", accept, acceptJSON)
	}
}

// TestPoll_FailedMidPollResubmitsOnce pins the failed-terminal retry
// interaction: a failed status mid-poll is answered with exactly one
// resubmit through the same post path — the one way a request-queue
// failure can heal — and the resubmitted call drives to completion.
func TestPoll_FailedMidPollResubmitsOnce(t *testing.T) {
	completed := `{"request_id":"req-2","status":"completed","result":{}}`
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-` + strconv.Itoa(n) + `","status":"queued"}`
		},
		func(n int) (int, string) {
			if n == 1 {
				return http.StatusOK, `{"request_id":"req-1","status":"failed","error":"scheduling failed"}`
			}
			return http.StatusOK, completed
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if string(raw) != completed {
		t.Errorf("raw = %q, want the resubmitted call's completed body", raw)
	}
	if posts, gets, _, _, _, _ := h.tally(); posts != 2 || gets != 2 {
		t.Errorf("hits: %d POST(s), %d GET(s), want exactly one resubmit (2/2)", posts, gets)
	}
}

// TestPoll_FailedBudgetSpent: a second failed terminal surfaces
// gmi.ErrTransient with a nil body — the budget is one resubmit, no more.
func TestPoll_FailedBudgetSpent(t *testing.T) {
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"failed","error":"scheduling failed"}`
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if raw != nil {
		t.Errorf("body = %q alongside a non-nil error, want nil", raw)
	}
	if posts, gets, _, _, _, _ := h.tally(); posts != 2 || gets != 2 {
		t.Errorf("hits: %d POST(s), %d GET(s), want 2/2 (one failed poll per submit)", posts, gets)
	}
}

// TestPoll_TransientGETToleratedOnce pins the poll-GET retry budget: one
// 5xx is tolerated — the next tick is its single retry — and the poll
// completes on the following GET.
func TestPoll_TransientGETToleratedOnce(t *testing.T) {
	completed := `{"request_id":"req-1","status":"completed","result":{}}`
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			if n == 1 {
				return http.StatusInternalServerError, "boom"
			}
			return http.StatusOK, completed
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if string(raw) != completed {
		t.Errorf("raw = %q, want the completed body after the tolerated 5xx", raw)
	}
	if _, gets, _, _, _, _ := h.tally(); gets != 2 {
		t.Errorf("GETs = %d, want 2 (the failed one plus its single retry)", gets)
	}
}

// TestPoll_TwoConsecutiveTransientGETs: a second consecutive 5xx spends
// the budget and surfaces gmi.ErrTransient.
func TestPoll_TwoConsecutiveTransientGETs(t *testing.T) {
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusInternalServerError, "boom"
		})

	_, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if _, gets, _, _, _, _ := h.tally(); gets != 2 {
		t.Errorf("GETs = %d, want 2 (one failure plus its single retry)", gets)
	}
}

// TestPoll_GET404SurfacesModelNotFound: a non-transient poll error
// surfaces immediately with its own sentinel and is never retried.
func TestPoll_GET404SurfacesModelNotFound(t *testing.T) {
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusNotFound, "no such request"
		})

	_, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if !errors.Is(err, gmi.ErrModelNotFound) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrModelNotFound)", err)
	}
	if _, gets, _, _, _, _ := h.tally(); gets != 1 {
		t.Errorf("GETs = %d, want 1 (a 4xx is never retried)", gets)
	}
}

// TestPoll_CancelledSurfacesSentinel pins the live-observed `cancelled`
// status: its own sentinel, not transient, and no resubmit — a cancelled
// request is not a failure that trying again can heal.
func TestPoll_CancelledSurfacesSentinel(t *testing.T) {
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"cancelled"}`
		})

	_, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v, want errors.Is(.., ErrCancelled)", err)
	}
	if errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, must not be transient — a cancelled request is never resubmitted", err)
	}
	if posts, gets, _, _, _, _ := h.tally(); posts != 1 || gets != 1 {
		t.Errorf("hits: %d POST(s), %d GET(s), want 1/1 (no resubmit)", posts, gets)
	}
}

// TestPoll_SuccessSynonymTerminal pins the other live-observed status:
// `success` terminates the poll exactly like `completed`. A poller that
// only knew the documented four would spin to the deadline on a real
// queue response.
func TestPoll_SuccessSynonymTerminal(t *testing.T) {
	success := `{"request_id":"req-1","status":"success","outcome":{"media_urls":["https://x/y.png"]}}`
	fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusOK, success
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if string(raw) != success {
		t.Errorf("raw = %q, want the success body as raw bytes", raw)
	}
}

// TestPoll_DeadlineCutsPoll pins the sentinel: the poll's own Timeout
// budget cuts the loop, ErrPollDeadline is in the chain, and so is the
// underlying context error.
func TestPoll_DeadlineCutsPoll(t *testing.T) {
	fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"processing"}`
		})

	c := NewWithPoll(PollConfig{Interval: time.Millisecond, Timeout: 40 * time.Millisecond})
	_, err := c.GenerateImage(context.Background(), "x", "Z-Image")
	if !errors.Is(err, ErrPollDeadline) {
		t.Errorf("err = %v, want errors.Is(.., ErrPollDeadline)", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the underlying context.DeadlineExceeded in the chain", err)
	}
}

// TestPoll_CallerDeadlineCutsPoll: the caller's own context deadline cuts
// the poll too — every iteration and the loop as a whole respect it.
func TestPoll_CallerDeadlineCutsPoll(t *testing.T) {
	fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"processing"}`
		})

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	c := NewWithPoll(PollConfig{Interval: time.Millisecond, Timeout: 30 * time.Second})
	_, err := c.GenerateImage(ctx, "x", "Z-Image")
	if !errors.Is(err, ErrPollDeadline) {
		t.Errorf("err = %v, want errors.Is(.., ErrPollDeadline)", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the caller's context.DeadlineExceeded in the chain", err)
	}
}

// TestPoll_DefaultIntervalOnWire exercises the default pacing through its
// default path: a New() client polls with the documented 2s gap between
// GETs — the timing is the wire assertion. This costs ~2s of wall clock.
func TestPoll_DefaultIntervalOnWire(t *testing.T) {
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			if n == 1 {
				return http.StatusOK, `{"request_id":"req-1","status":"processing"}`
			}
			return http.StatusOK, `{"request_id":"req-1","status":"completed","result":{}}`
		})

	start := time.Now()
	if _, err := New().GenerateImage(context.Background(), "x", "Z-Image"); err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if elapsed := time.Since(start); elapsed < DefaultPollInterval {
		t.Errorf("two GETs completed in %v, want >= %v (the default interval on the wire)", elapsed, DefaultPollInterval)
	}
	if _, gets, _, _, _, _ := h.tally(); gets != 2 {
		t.Errorf("GETs = %d, want 2", gets)
	}
}

// TestPollDefaultsSubstituted pins the zero-PollConfig substitution: the
// New() client carries the package defaults, an explicit NewWithPoll
// config keeps its Interval and inherits the default Timeout.
func TestPollDefaultsSubstituted(t *testing.T) {
	want := PollConfig{Interval: DefaultPollInterval, Timeout: DefaultPollTimeout}
	if got := (PollConfig{}).withDefaults(); got != want {
		t.Errorf("withDefaults() = %+v, want the documented defaults", got)
	}
	if got := New().poll.withDefaults(); got != want {
		t.Errorf("New() client defaults = %+v, want the documented defaults", got)
	}
	if got := NewWithPoll(PollConfig{Interval: 5 * time.Millisecond}).poll.withDefaults(); got.Interval != 5*time.Millisecond || got.Timeout != DefaultPollTimeout {
		t.Errorf("NewWithPoll defaults = %+v, want the explicit Interval kept and the default Timeout", got)
	}
}

// TestSubmitCompletedNoPoll is the regression companion: a submit body
// that is already terminal reaches the caller as raw bytes with no poll
// traffic — the T2 passthrough, unchanged.
func TestSubmitCompletedNoPoll(t *testing.T) {
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, fakeResponse
		},
		func(n int) (int, string) {
			t.Error("the queue was polled for an already-completed submit")
			return http.StatusOK, fakeResponse
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if !strings.Contains(string(raw), `"status":"completed"`) {
		t.Errorf("raw = %q, want the submit body passed through", raw)
	}
	if posts, gets, _, _, _, _ := h.tally(); posts != 1 || gets != 0 {
		t.Errorf("hits: %d POST(s), %d GET(s), want 1/0", posts, gets)
	}
}

// TestPoll_MissingRequestID: a queue that says queued but names no request
// id cannot be polled — the shape surprise surfaces as gmi.ErrTransient
// (the same class as a 2xx body that fails to decode).
func TestPoll_MissingRequestID(t *testing.T) {
	fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"status":"queued"}`
		},
		func(n int) (int, string) {
			t.Error("the queue was polled without a request id")
			return http.StatusOK, fakeResponse
		})

	_, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if !errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
}

// TestSubmitNonTerminalSubmitBodiesPoll is the G1 pin: a submit body with
// an unknown or absent status that still names a request_id is a record
// in flight, not a result — the poll loop runs, and only the terminal
// body reaches the caller.
func TestSubmitNonTerminalSubmitBodiesPoll(t *testing.T) {
	for name, submit := range map[string]string{
		"unknown-status": `{"request_id":"req-1","status":"started"}`,
		"absent-status":  `{"request_id":"req-1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			completed := `{"request_id":"req-1","status":"completed","result":{"b64_json":"ZmluaXNoZWQ="}}`
			h := fakeQueue(t,
				func(n int) (int, string) {
					return http.StatusOK, submit
				},
				func(n int) (int, string) {
					if n == 1 {
						return http.StatusOK, `{"request_id":"req-1","status":"processing"}`
					}
					return http.StatusOK, completed
				})

			raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
			if err != nil {
				t.Fatalf("GenerateImage: %v", err)
			}
			if string(raw) != completed {
				t.Errorf("raw = %q, want the completed body — the submit body must not pass as the result", raw)
			}
			posts, gets, path, _, _, _ := h.tally()
			if posts != 1 || gets != 2 {
				t.Errorf("hits: %d POST(s), %d GET(s), want 1 POST and 2 GETs", posts, gets)
			}
			if path != pathRequestQueue+"/req-1" {
				t.Errorf("GET path = %q, want %q", path, pathRequestQueue+"/req-1")
			}
		})
	}
}

// TestSubmitUnknownStatusWithoutRequestIDPassesThrough pins the G1
// counterpart: a body that names no request_id cannot be polled, whatever
// its status says — the T2 raw passthrough stands, with no poll traffic.
func TestSubmitUnknownStatusWithoutRequestIDPassesThrough(t *testing.T) {
	submit := `{"status":"weird"}`
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, submit
		},
		func(n int) (int, string) {
			t.Error("the queue was polled without a request id")
			return http.StatusOK, `{"status":"completed"}`
		})

	raw, err := fastClient().GenerateImage(context.Background(), "x", "Z-Image")
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if string(raw) != submit {
		t.Errorf("raw = %q, want the submit body passed through untouched", raw)
	}
	if posts, gets, _, _, _, _ := h.tally(); posts != 1 || gets != 0 {
		t.Errorf("hits: %d POST(s), %d GET(s), want 1/0", posts, gets)
	}
}

// TestPoll_DeadlineDuringInflightGETSurfacesPollDeadline is the G2 pin: a
// deadline that expires while a poll GET is in flight surfaces
// ErrPollDeadline wrapping the context error — never gmi.ErrTransient —
// even though a ticker tick is pending when the aborted GET returns. The
// aborted GET is also the last GET: the budget is not spent retrying a
// dead context.
func TestPoll_DeadlineDuringInflightGETSurfacesPollDeadline(t *testing.T) {
	block := make(chan struct{})
	h := fakeQueue(t,
		func(n int) (int, string) {
			return http.StatusOK, `{"request_id":"req-1","status":"queued"}`
		},
		func(n int) (int, string) {
			<-block // hold the GET past the poll budget
			return http.StatusOK, `{"request_id":"req-1","status":"processing"}`
		})

	c := NewWithPoll(PollConfig{Interval: 5 * time.Millisecond, Timeout: 40 * time.Millisecond})
	_, err := c.GenerateImage(context.Background(), "x", "Z-Image")
	close(block) // free the handler so the test server can shut down

	if !errors.Is(err, ErrPollDeadline) {
		t.Errorf("err = %v, want errors.Is(.., ErrPollDeadline)", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the underlying context.DeadlineExceeded in the chain", err)
	}
	if errors.Is(err, gmi.ErrTransient) {
		t.Errorf("err = %v, want no gmi.ErrTransient in the chain", err)
	}
	if posts, gets, _, _, _, _ := h.tally(); posts != 1 || gets != 1 {
		t.Errorf("hits: %d POST(s), %d GET(s), want 1/1 — the deadline-aborted GET is the last", posts, gets)
	}
}
