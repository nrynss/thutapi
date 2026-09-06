package job

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"thutapi/internal/stream"
)

// newBrokerAndRunner returns a broker and a Runner on it — the shape every
// consumer (T4+) wires: one broker, one runner, topics namespaced by
// TopicPrefix.
func newBrokerAndRunner(t *testing.T) (*stream.Broker, *Runner) {
	t.Helper()
	b := stream.New(stream.Config{Heartbeat: time.Hour})
	return b, New(b)
}

// subscribeAfter returns the subscription on the job's topic made after
// Start returned — the race every real client faces. Deterministic tests
// gate the Func so events cannot outrun the subscribe.
func subscribeAfter(t *testing.T, b *stream.Broker, id string) *stream.Subscription {
	t.Helper()
	return b.Subscribe(context.Background(), Topic(id))
}

// startGated starts fn behind a gate, subscribes to its topic, and only
// then releases it — so every event fn publishes, the first progress and
// the terminal included, lands in the subscriber's buffer.
//
// Without the gate a test racing Start against Subscribe is asserting on
// a stream whose opening events the broker legitimately never delivered:
// Subscribe is from-now-on with no replay, so an fn that publishes on its
// goroutine's first instruction can beat the subscribe and the assertion
// waits forever for an event that was never owed to it. Under -race, with
// other packages loading the scheduler, that goroutine wins often enough
// to turn the suite intermittently red.
//
// A test that wants the racy shape on purpose — TestMidJobSubscriberCatchesUp
// pins exactly the missed-early-event contract — does not use this.
func startGated(t *testing.T, b *stream.Broker, r *Runner, fn Func) (string, *stream.Subscription) {
	t.Helper()
	gate := make(chan struct{})
	id, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		<-gate
		return fn(ctx, progress)
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	sub := subscribeAfter(t, b, id)
	close(gate)
	return id, sub
}

// drain collects the next n events from sub, failing the test on timeout.
func drain(t *testing.T, sub *stream.Subscription, n int) []stream.Event {
	t.Helper()
	events := make([]stream.Event, 0, n)
	for len(events) < n {
		select {
		case ev, ok := <-sub.Events:
			if !ok {
				t.Fatalf("stream closed after %d event(s), want %d", len(events), n)
			}
			events = append(events, ev)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out after %d event(s): %+v", len(events), events)
		}
	}
	return events
}

// TestTopicFormat pins the topic convention: the broker topic for a job is
// TopicPrefix + id. T4's routes and T9's EventSource hardcode it.
func TestTopicFormat(t *testing.T) {
	if got, want := Topic("abc"), "job:abc"; got != want {
		t.Errorf("Topic = %q, want %q", got, want)
	}
}

// TestStartReturnsImmediately: Start hands back an id while fn is still
// running — the short-POST contract invariant 6 is built on.
func TestStartReturnsImmediately(t *testing.T) {
	b := stream.New(stream.Config{Heartbeat: time.Hour})
	r := New(b)
	release := make(chan struct{})
	started := make(chan struct{})

	id, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		close(started)
		<-release
		return []byte("late"), nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id == "" {
		t.Fatal("Start returned an empty id")
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fn never started")
	}
	close(release)

	if _, err := r.Result(id); err != nil {
		t.Errorf("Result: %v", err)
	}
}

// TestProgressAndTerminalExactlyOnce is the core streaming contract: both
// progress events flow to the topic, the terminal "done" event carries the
// completion envelope, and exactly one terminal event is published — this
// test would catch a second one from any path.
func TestProgressAndTerminalExactlyOnce(t *testing.T) {
	b, r := newBrokerAndRunner(t)

	id, sub := startGated(t, b, r, func(ctx context.Context, progress func(string)) ([]byte, error) {
		progress(`{"step":1}`)
		progress(`{"step":2}`)
		return []byte(`{"book":1}`), nil
	})

	events := drain(t, sub, 3)
	if events[0].Name != "progress" || events[0].Data != `{"step":1}` {
		t.Errorf("first event = %+v, want progress step 1", events[0])
	}
	if events[1].Name != "progress" || events[1].Data != `{"step":2}` {
		t.Errorf("second event = %+v, want progress step 2", events[1])
	}
	if events[2].Name != "done" {
		t.Errorf("terminal event = %+v, want name \"done\"", events[2])
	}
	if !strings.Contains(events[2].Data, `"id":"`+id+`"`) || !strings.Contains(events[2].Data, `"status":"done"`) {
		t.Errorf("terminal payload = %q, want the completion envelope for %s", events[2].Data, id)
	}

	// Exactly once: after the terminal, nothing else arrives.
	select {
	case ev := <-sub.Events:
		t.Errorf("unexpected event after terminal: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}

	// Catch-up: the result by id carries the returned bytes.
	res, err := r.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.Status != StatusDone || string(res.Data) != `{"book":1}` || res.Err != nil {
		t.Errorf("Result = %+v, want done with the returned bytes", res)
	}
}

// TestEmptyProgressDropped pins the Func contract: an empty progress
// payload is not published — an eventless `data:` line would be wire noise.
func TestEmptyProgressDropped(t *testing.T) {
	b, r := newBrokerAndRunner(t)
	gate := make(chan struct{})

	id, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		progress("")
		<-gate
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	sub := subscribeAfter(t, b, id)
	close(gate)
	events := drain(t, sub, 1)
	if events[0].Name != "done" {
		t.Errorf("first event = %+v, want the done event with no progress before it", events[0])
	}
}

// TestPanicBecomesErrorTerminal pins the panic policy: a panicking Func is
// recovered at the job boundary, surfaces as exactly one "error" terminal,
// and the Result carries ErrPanic in its chain. The process — and this
// Runner — keeps working afterwards.
func TestPanicBecomesErrorTerminal(t *testing.T) {
	b, r := newBrokerAndRunner(t)

	id, sub := startGated(t, b, r, func(ctx context.Context, progress func(string)) ([]byte, error) {
		panic("illustrator exploded")
	})
	events := drain(t, sub, 1)
	if events[0].Name != "error" {
		t.Fatalf("terminal event = %+v, want \"error\"", events[0])
	}
	if !strings.Contains(events[0].Data, "illustrator exploded") {
		t.Errorf("terminal payload = %q, want the panic value verbatim", events[0].Data)
	}

	res, err := r.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.Status != StatusError {
		t.Errorf("Status = %q, want %q", res.Status, StatusError)
	}
	if !errors.Is(res.Err, ErrPanic) {
		t.Errorf("Err = %v, want errors.Is(.., ErrPanic)", res.Err)
	}
	if res.Data != nil {
		t.Errorf("Data = %q alongside an error, want nil", res.Data)
	}

	// The runner survives the panic: the next job runs normally.
	_, sub2 := startGated(t, b, r, func(ctx context.Context, progress func(string)) ([]byte, error) {
		return []byte("ok"), nil
	})
	drain(t, sub2, 1)
}

// TestFnErrorSurfacesSentinel: an error returned by fn — not a panic —
// becomes the "error" terminal and keeps its sentinel chain for
// errors.Is at the catch-up site.
func TestFnErrorSurfacesSentinel(t *testing.T) {
	b, r := newBrokerAndRunner(t)
	sentinel := errors.New("media: transient")

	id, sub := startGated(t, b, r, func(ctx context.Context, progress func(string)) ([]byte, error) {
		return nil, sentinel
	})
	events := drain(t, sub, 1)
	if events[0].Name != "error" {
		t.Fatalf("terminal event = %+v, want \"error\"", events[0])
	}
	res, err := r.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if !errors.Is(res.Err, sentinel) {
		t.Errorf("Err = %v, want the fn's own error", res.Err)
	}
}

// TestJobOutlivesRequestContext pins the context policy: cancelling the
// STARTING request's context must not kill the job — a page navigation
// must not abort a generation (invariant 6). Only values are inherited.
func TestJobOutlivesRequestContext(t *testing.T) {
	b, r := newBrokerAndRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	gate := make(chan struct{})

	id, err := r.Start(ctx, func(jobCtx context.Context, progress func(string)) ([]byte, error) {
		<-gate
		if jobCtx.Err() != nil {
			return nil, jobCtx.Err()
		}
		return []byte("survived"), nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Subscribe before the job may publish, then cancel the STARTING
	// context while the job is still gated: the terminal it publishes
	// after the release is observed in full.
	sub := subscribeAfter(t, b, id)
	cancel()
	close(gate)

	events := drain(t, sub, 1)
	if events[0].Name != "done" {
		t.Errorf("terminal event = %+v, want \"done\" despite the cancelled start context", events[0])
	}
}

// TestResultUnknownJob pins the catch-up error path: an id this Runner
// never started yields ErrUnknownJob, not a zero Result.
func TestResultUnknownJob(t *testing.T) {
	_, r := newBrokerAndRunner(t)
	res, err := r.Result("no-such-job")
	if !errors.Is(err, ErrUnknownJob) {
		t.Errorf("err = %v, want errors.Is(.., ErrUnknownJob)", err)
	}
	if res.ID != "" || res.Status != "" {
		t.Errorf("res = %+v alongside an error, want the zero Result", res)
	}
}

// TestStartNilBroker: a Runner that cannot publish progress or terminal
// events refuses to start jobs rather than running them in silence.
func TestStartNilBroker(t *testing.T) {
	r := NewWithConfig(nil, Config{})
	if _, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		return nil, nil
	}); !errors.Is(err, ErrNoBroker) {
		t.Errorf("err = %v, want errors.Is(.., ErrNoBroker)", err)
	}
}

// TestStartAtLimit pins the capacity contract: the default path included,
// Start returns ErrLimit without starting the job once slots are full, and
// a finished job frees its slot.
func TestStartAtLimit(t *testing.T) {
	t.Run("explicit-limit", func(t *testing.T) {
		b := stream.New(stream.Config{})
		r := NewWithConfig(b, Config{Limit: 1})
		release := make(chan struct{})
		if _, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
			<-release
			return nil, nil
		}); err != nil {
			t.Fatalf("first Start: %v", err)
		}
		if _, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
			return nil, nil
		}); !errors.Is(err, ErrLimit) {
			t.Errorf("err = %v, want errors.Is(.., ErrLimit)", err)
		}
		close(release)

		// The slot frees when the job's goroutine has fully exited;
		// wait for the terminal event, then give Start a bounded
		// window to observe the release.
		// Gated, so the terminal this drains cannot be published
		// before the subscribe below. A failed Start begins nothing,
		// so the retry loop leaves exactly one job on the gate.
		gate := make(chan struct{})
		startGatedJob := func() (string, error) {
			return r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
				<-gate
				return nil, nil
			})
		}
		id, err := startGatedJob()
		deadline := time.Now().Add(2 * time.Second)
		for err != nil {
			if !errors.Is(err, ErrLimit) || time.Now().After(deadline) {
				t.Fatalf("Start after release: %v", err)
			}
			time.Sleep(time.Millisecond)
			id, err = startGatedJob()
		}
		sub := subscribeAfter(t, b, id)
		close(gate)
		drain(t, sub, 1)
	})

	t.Run("default-limit", func(t *testing.T) {
		if DefaultLimit <= 0 {
			t.Fatalf("DefaultLimit = %d, want positive", DefaultLimit)
		}
		b := stream.New(stream.Config{})
		r := New(b) // the default path: Config{} must mean DefaultLimit slots
		release := make(chan struct{})
		for i := 0; i < DefaultLimit; i++ {
			if _, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
				<-release
				return nil, nil
			}); err != nil {
				t.Fatalf("Start %d of %d: %v", i+1, DefaultLimit, err)
			}
		}
		if _, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
			return nil, nil
		}); !errors.Is(err, ErrLimit) {
			t.Errorf("job %d: err = %v, want ErrLimit at the default limit", DefaultLimit+1, err)
		}
		close(release)
	})
}

// TestMidJobSubscriberCatchesUp is the done-when probe: a subscriber
// joining mid-job receives the progress published after it joined plus the
// terminal; a subscriber joining AFTER the terminal catches up on the
// result by id — the documented subscribe-then-Result pattern.
func TestMidJobSubscriberCatchesUp(t *testing.T) {
	b, r := newBrokerAndRunner(t)
	joined := make(chan struct{})
	gate := make(chan struct{})

	id, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		progress(`{"step":"early"}`)
		close(joined)
		<-gate
		progress(`{"step":"late"}`)
		return []byte(`{"book":"final"}`), nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	<-joined // the early progress has already fired; this subscriber missed it
	sub := subscribeAfter(t, b, id)
	close(gate)

	events := drain(t, sub, 2)
	if events[0].Name != "progress" || events[0].Data != `{"step":"late"}` {
		t.Errorf("first event = %+v, want the late progress", events[0])
	}
	if events[1].Name != "done" {
		t.Errorf("second event = %+v, want the terminal", events[1])
	}

	// A page reloading after the terminal: no stream traffic, full
	// catch-up by id.
	res, err := r.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.Status != StatusDone || string(res.Data) != `{"book":"final"}` {
		t.Errorf("catch-up Result = %+v, want the done result with bytes", res)
	}

	// The from-now-on stream contract: nothing replays after the
	// terminal event.
	select {
	case ev := <-sub.Events:
		t.Errorf("unexpected replay after terminal: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestRunningStatusBeforeTerminal: Result reports StatusRunning while fn
// is still going — a poller joining mid-job can distinguish the two.
func TestRunningStatusBeforeTerminal(t *testing.T) {
	_, r := newBrokerAndRunner(t)
	gate := make(chan struct{})
	release := make(chan struct{})

	id, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		close(gate)
		<-release
		return nil, nil
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-gate

	res, err := r.Result(id)
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if res.Status != StatusRunning {
		t.Errorf("Status mid-job = %q, want %q", res.Status, StatusRunning)
	}
	close(release)
}

// TestCancelRunningJobRecoversSlot is the J1 pin (suite-ised from the
// round-1 probe): idiomatic fns — the shape that returns on ctx.Done() —
// hold both slots; Cancel ends each job, the cancelled terminal is
// published exactly once through run's single site, and the slots
// recover: a Runner full of hung jobs is no longer wedged until restart.
func TestCancelRunningJobRecoversSlot(t *testing.T) {
	b := stream.New(stream.Config{Heartbeat: time.Hour})
	r := NewWithConfig(b, Config{Limit: 2})
	started := make(chan struct{}, 2)
	blocked := func(ctx context.Context, progress func(string)) ([]byte, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	id1, err := r.Start(context.Background(), blocked)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	id2, err := r.Start(context.Background(), blocked)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	<-started
	<-started
	if _, err := r.Start(context.Background(), blocked); !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit with both slots held", err)
	}

	// Subscribe before cancelling: neither fn can return before
	// Cancel, so no terminal can outrun its subscription.
	subs := map[string]*stream.Subscription{
		id1: subscribeAfter(t, b, id1),
		id2: subscribeAfter(t, b, id2),
	}
	if err := r.Cancel(id1); err != nil {
		t.Fatalf("Cancel(%s): %v", id1, err)
	}
	if err := r.Cancel(id2); err != nil {
		t.Fatalf("Cancel(%s): %v", id2, err)
	}

	for _, id := range []string{id1, id2} {
		events := drain(t, subs[id], 1)
		if events[0].Name != string(StatusCancelled) {
			t.Errorf("terminal event = %+v, want name %q", events[0], StatusCancelled)
		}
		select {
		case ev := <-subs[id].Events:
			t.Errorf("second terminal after Cancel on %s: %+v", id, ev)
		case <-time.After(100 * time.Millisecond):
		}
		res, err := r.Result(id)
		if err != nil {
			t.Fatalf("Result(%s): %v", id, err)
		}
		if res.Status != StatusCancelled || res.Data != nil || !errors.Is(res.Err, context.Canceled) {
			t.Errorf("Result(%s) = %+v, want cancelled with context.Canceled in the chain and nil data", id, res)
		}
	}

	// The slots recovered: a fresh Start succeeds (bounded retry — the
	// slot releases when run's defer fires, just after the terminal).
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
			return []byte("fresh"), nil
		})
		if err == nil {
			break
		}
		if !errors.Is(err, ErrLimit) || time.Now().After(deadline) {
			t.Fatalf("Start after cancelling both jobs: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestCancelIdempotentAndUnknown pins Cancel's handle contract: an
// unknown id reports ErrUnknownJob, a second Cancel on a still-registered
// job is a nil no-op, and once the job has terminated the registry entry
// is gone — Cancel reports ErrUnknownJob instead of leaking the handle.
func TestCancelIdempotentAndUnknown(t *testing.T) {
	b, r := newBrokerAndRunner(t)
	if err := r.Cancel("no-such-job"); !errors.Is(err, ErrUnknownJob) {
		t.Errorf("err = %v, want errors.Is(.., ErrUnknownJob)", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	id, err := r.Start(context.Background(), func(ctx context.Context, progress func(string)) ([]byte, error) {
		close(started)
		<-ctx.Done()
		<-release // hold the job so the second Cancel lands while registered
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started

	if err := r.Cancel(id); err != nil {
		t.Fatalf("first Cancel: %v", err)
	}
	if err := r.Cancel(id); err != nil {
		t.Errorf("second Cancel = %v, want nil (idempotent while the job is registered)", err)
	}
	// Subscribe before releasing the job: the cancelled terminal is
	// published the moment fn returns, so it must not be able to run
	// ahead of the subscription.
	sub := subscribeAfter(t, b, id)
	close(release)
	drain(t, sub, 1) // the cancelled terminal, exactly once

	// After the goroutine exits the entry is gone: bounded wait for
	// ErrUnknownJob, then the no-leak assertion.
	deadline := time.Now().Add(2 * time.Second)
	for err := r.Cancel(id); !errors.Is(err, ErrUnknownJob); err = r.Cancel(id) {
		if time.Now().After(deadline) {
			t.Fatal("Cancel still succeeds after the job terminated; the registry entry leaked")
		}
		time.Sleep(time.Millisecond)
	}
}
