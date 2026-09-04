package stream

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestSubscribeDefaultConfigSubstituted pins the zero-Config defaults:
// New(Config{}) must substitute DefaultHeartbeat and DefaultBuffer, the
// same substitution rule the media client's model defaults follow — a
// default nobody executes is a default nobody tests.
func TestSubscribeDefaultConfigSubstituted(t *testing.T) {
	b := New(Config{})
	if b.cfg.Heartbeat != DefaultHeartbeat {
		t.Errorf("heartbeat = %v, want %v (the default must be substituted, not zero)", b.cfg.Heartbeat, DefaultHeartbeat)
	}
	if b.cfg.Buffer != DefaultBuffer {
		t.Errorf("buffer = %d, want %d", b.cfg.Buffer, DefaultBuffer)
	}
	if got := (Config{Heartbeat: -1, Buffer: -5}).withDefaults(); got.Heartbeat != DefaultHeartbeat || got.Buffer != DefaultBuffer {
		t.Errorf("withDefaults on negative values = %+v, want the defaults", got)
	}
}

// TestPublishReachesTopicSubscriber: a subscriber receives events published
// to its topic, and only its topic.
func TestPublishReachesTopicSubscriber(t *testing.T) {
	b := New(Config{})
	sub := b.Subscribe(context.Background(), "job:1")
	defer sub.Cancel()

	b.Publish("job:2", Event{Name: "elsewhere", Data: "x"})
	b.Publish("job:1", Event{Name: "progress", Data: "page 1"})

	select {
	case ev := <-sub.Events:
		if ev.Name != "progress" || ev.Data != "page 1" {
			t.Errorf("event = %+v, want the job:1 progress event", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the event")
	}
}

// TestSlowSubscriberDropOldest pins the bounded-buffer policy: a
// subscriber that stops reading never blocks Publish, and the oldest
// buffered events are the ones lost — the newest state wins.
func TestSlowSubscriberDropOldest(t *testing.T) {
	b := New(Config{Buffer: 2})
	sub := b.Subscribe(context.Background(), "t")
	defer sub.Cancel()

	// Five events (b..f) into a two-slot buffer nobody reads: the
	// surviving pair must be the two newest, e and f.
	for i := 1; i <= 5; i++ {
		b.Publish("t", Event{Name: "progress", Data: string(rune('a' + i))})
	}

	drain := make([]string, 0, 2)
	for len(drain) < 2 {
		select {
		case ev := <-sub.Events:
			drain = append(drain, ev.Data)
		case <-time.After(time.Second):
			t.Fatalf("timed out after %d events: %v", len(drain), drain)
		}
	}
	if got, want := drain[0]+drain[1], "ef"; got != want {
		t.Errorf("buffered events = %v, want the two newest %q (oldest dropped)", drain, want)
	}
}

// TestCancelRemovesSubscriberAndClosesEvents: Cancel removes the
// subscriber and closes Events, so a range loop — the shape ServeTopic
// and any consumer use — terminates.
func TestCancelRemovesSubscriberAndClosesEvents(t *testing.T) {
	b := New(Config{})
	sub := b.Subscribe(context.Background(), "t")

	sub.Cancel()
	if got := b.Subscribers("t"); got != 0 {
		t.Errorf("Subscribers after Cancel = %d, want 0", got)
	}
	for range sub.Events {
		t.Error("Events received a value after Cancel")
	}

	// Idempotent, and safe on a nil Subscription.
	sub.Cancel()
	var nilSub *Subscription
	nilSub.Cancel()
}

// TestSubscribeCtxDoneRemovesSubscriber pins the disconnect contract: a
// subscriber whose context ends is removed without anyone calling Cancel
// — the watcher goroutine does it, so topics hold no writers for
// connections that went away.
func TestSubscribeCtxDoneRemovesSubscriber(t *testing.T) {
	b := New(Config{})
	ctx, cancel := context.WithCancel(context.Background())
	sub := b.Subscribe(ctx, "t")
	defer sub.Cancel()

	cancel()
	deadline := time.Now().Add(time.Second)
	for b.Subscribers("t") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscriber still registered 1s after its context ended")
		}
		time.Sleep(time.Millisecond)
	}
	for range sub.Events {
		t.Error("Events received a value after the context ended")
	}
}

// TestSubscribeNilCtxUsesBackground: Subscribe documents nil ctx as
// background; the subscription must still be removable via Cancel.
func TestSubscribeNilCtxUsesBackground(t *testing.T) {
	b := New(Config{})
	sub := b.Subscribe(nil, "t")
	sub.Cancel()
	if got := b.Subscribers("t"); got != 0 {
		t.Errorf("Subscribers = %d, want 0", got)
	}
}

// TestPublishConcurrentWithSubscribe shakes the broker's locking under the
// race detector: publishers, subscribers joining and leaving, all at once.
// No assertion on delivery counts — the drop policy makes them
// nondeterministic by design — only that nothing deadlocks or panics.
func TestPublishConcurrentWithSubscribe(t *testing.T) {
	b := New(Config{})
	stop := make(chan struct{})

	// Publishers run until stop closes; they are NOT part of the
	// completion signal, or the wait could never finish (a publisher
	// only exits after the wait it belongs to).
	var publishers sync.WaitGroup
	for i := 0; i < 4; i++ {
		publishers.Add(1)
		go func() {
			defer publishers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b.Publish("t", Event{Name: "progress", Data: "x"})
			}
		}()
	}

	var joiners sync.WaitGroup
	for i := 0; i < 4; i++ {
		joiners.Add(1)
		go func() {
			defer joiners.Done()
			for i := 0; i < 100; i++ {
				sub := b.Subscribe(context.Background(), "t")
				b.Publish("t", Event{Name: "progress", Data: "y"})
				// The just-published event may itself be
				// dropped under load — that is the policy,
				// not a bug — so the receive is bounded.
				select {
				case <-sub.Events:
				case <-time.After(50 * time.Millisecond):
				}
				sub.Cancel()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		joiners.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("joiners did not finish under concurrent publish and subscribe")
	}
	close(stop)
	publishers.Wait()
}
