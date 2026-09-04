// Package stream provides the topic-based in-process broker behind
// Thutapi's server-sent-events surface (PLAN.md §T3b).
//
// Long work is started by a short POST and observed over SSE — Cloudflare
// kills a proxied request at 100s (PLAN.md invariant 6), so nothing may
// hold a response open while it computes. A Handler publishes progress to
// a topic; every open browser tab holds one Subscription on that topic and
// receives the events framed as SSE.
//
// Wire contract (PLAN.md §T1, carried here so it lives next to the code
// that enforces it): responses set Content-Type text/event-stream,
// Cache-Control no-cache and X-Accel-Buffering no; every event is flushed;
// a `: ping` heartbeat comment keeps intermediaries honest during quiet
// periods.
package stream

import (
	"context"
	"sync"
	"time"
)

// DefaultHeartbeat is the quiet-period length after which ServeTopic writes
// a `: ping` comment. Fifteen seconds, per PLAN.md §T1: short enough that
// Cloudflare and corporate proxies keep the connection open, long enough to
// be invisible next to real event traffic.
const DefaultHeartbeat = 15 * time.Second

// DefaultBuffer is the per-subscriber event buffer a Broker built with the
// zero Config gets. Sixteen covers a book generation's progress stream
// (~8 pages × a few events) with headroom.
const DefaultBuffer = 16

// Config carries the Broker's knobs. The zero value is usable and means
// DefaultHeartbeat and DefaultBuffer — callers pass Config{} unless they
// are tuning for tests.
type Config struct {
	// Heartbeat is the quiet period between `: ping` comments on every
	// open connection. Zero or negative means DefaultHeartbeat.
	Heartbeat time.Duration

	// Buffer is the per-subscriber event buffer. Zero or negative means
	// DefaultBuffer.
	Buffer int
}

// withDefaults returns cfg with zero (and negative) fields substituted.
func (cfg Config) withDefaults() Config {
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = DefaultHeartbeat
	}
	if cfg.Buffer <= 0 {
		cfg.Buffer = DefaultBuffer
	}
	return cfg
}

// Event is one server-sent event. Name is the SSE `event:` field (empty
// means the client-default "message"); Data is the payload after
// `data:` — pre-encoded by the publisher, because the publisher knows the
// schema (the same raw-bytes rule the GMI clients follow). A Data
// containing line breaks is framed as multiple `data:` lines, which is
// what the SSE spec requires clients to rejoin with "\n".
type Event struct {
	Name string
	Data string
}

// subscriber is one registered consumer. ch carries events to the writer
// loop; gone is closed the moment the subscriber is removed, so the
// watcher goroutine and the writer loop both have a defined exit path.
type subscriber struct {
	ch   chan Event
	gone chan struct{}
}

// Broker fans events out to topic subscribers without ever blocking on a
// slow one.
//
// Slow-subscriber policy (a bounded buffer has to pick one): when a
// subscriber's buffer is full, the OLDEST buffered event is dropped to
// make room for the new one. The publisher never blocks and never
// disconnects the subscriber. Newest-state-wins fits progress streams:
// "page 3 of 8" supersedes "page 2 of 8", and the terminal event — the
// newest — always makes it into the buffer. A client that must not miss
// anything polls the authoritative state (for jobs: job.Result) instead of
// relying on the stream; the SSE channel is best-effort by design.
type Broker struct {
	cfg    Config
	mu     sync.Mutex
	nextID uint64
	topics map[string]map[uint64]*subscriber
}

// Subscription is one consumer's registration on one topic. Events is
// closed when the subscription ends (Cancel, ctx done, or removal), so a
// `for ev := range sub.Events` loop terminates. Cancel is safe to call
// more than once and safe to call on a nil Subscription.
type Subscription struct {
	Events <-chan Event

	once   sync.Once
	remove func()
}

// Cancel removes the subscription. Called by ServeTopic when the response
// ends and by the ctx watcher on disconnect; either path removes the
// subscriber exactly once.
func (s *Subscription) Cancel() {
	if s == nil {
		return
	}
	s.once.Do(func() { s.remove() })
}

// New returns an empty Broker. The zero Config means the package
// defaults: DefaultHeartbeat between pings, DefaultBuffer per subscriber.
func New(cfg Config) *Broker {
	return &Broker{
		cfg:    cfg.withDefaults(),
		topics: make(map[string]map[uint64]*subscriber),
	}
}

// Subscribe registers a subscriber on topic. The returned Subscription's
// Events channel receives every event published to the topic from now on —
// there is no replay, so a subscriber that needs the state as of its join
// fetches it from the authoritative source and treats the stream as
// from-now-on (the job package documents the subscribe-then-Result
// ordering for exactly this).
//
// The subscription ends when ctx is done or Cancel is called; both remove
// the subscriber so topics hold no writers for closed connections. ctx
// must carry no deadline the subscriber should outlive — an HTTP handler
// passes its request context, which is done the moment the client goes
// away.
func (b *Broker) Subscribe(ctx context.Context, topic string) *Subscription {
	if ctx == nil {
		ctx = context.Background()
	}

	b.mu.Lock()
	id := b.nextID
	b.nextID++
	sub := &subscriber{
		ch:   make(chan Event, b.cfg.Buffer),
		gone: make(chan struct{}),
	}
	if b.topics[topic] == nil {
		b.topics[topic] = make(map[uint64]*subscriber)
	}
	b.topics[topic][id] = sub
	b.mu.Unlock()

	s := &Subscription{
		Events: sub.ch,
		remove: func() { b.unsubscribe(topic, id) },
	}

	// Watcher: removes the subscriber when the caller's context ends.
	// Exit path: ctx.Done or the subscription ending — whichever first;
	// it never outlives the subscription.
	go func() {
		select {
		case <-ctx.Done():
			s.Cancel()
		case <-sub.gone:
		}
	}()

	return s
}

// Publish delivers event to every current subscriber on topic without
// blocking: a subscriber whose buffer is full has its oldest buffered
// event dropped (the Broker doc comment states the policy and why).
// Publishing to a topic with no subscribers, or on a nil Broker, is a
// no-op. An event with an empty Name is a legal SSE "message" event and is
// delivered like any other.
func (b *Broker) Publish(topic string, event Event) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.topics[topic] {
		select {
		case sub.ch <- event:
		default:
			// Buffer full: drop the oldest buffered event, then
			// enqueue the new one. Under b.mu no other publisher
			// can race the drain-and-send pair.
			select {
			case <-sub.ch:
			default:
			}
			select {
			case sub.ch <- event:
			default:
			}
		}
	}
}

// unsubscribe removes the subscriber and closes its channel. Idempotent:
// the map lookup is the once-guard, so a second call (or the watcher
// racing Cancel) is a no-op. Callers hold b.mu.
func (b *Broker) unsubscribe(topic string, id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs, ok := b.topics[topic]
	if !ok {
		return
	}
	sub, ok := subs[id]
	if !ok {
		return
	}
	delete(subs, id)
	if len(subs) == 0 {
		delete(b.topics, topic)
	}
	close(sub.ch)
	close(sub.gone)
}

// Subscribers reports how many live subscriptions topic has. It exists
// for lifecycle tests and diagnostics; it says nothing about subscriber
// identities.
func (b *Broker) Subscribers(topic string) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.topics[topic])
}
