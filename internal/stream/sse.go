package stream

import (
	"io"
	"net/http"
	"strings"
	"time"
)

// sseHeaders are the response headers every SSE response carries, per
// PLAN.md §T1: no intermediary caching, and no proxy buffering in front
// of the stream.
func sseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
}

// ServeTopic writes topic to w as a server-sent-events stream and returns
// when the response ends: client disconnect (the request context is done),
// a write error, or the subscription ending. It is the one HTTP surface
// the package provides — a track adds its route line in newServer and
// calls this with the topic it chose (PLAN.md invariant 5).
//
// Headers are set once, up front, before the first byte. Every event is
// flushed as it is written. During quiet periods a `: ping` comment every
// b.cfg.Heartbeat keeps intermediaries from reaping the connection; the
// heartbeat timer resets on every real event, so pings only appear between
// events.
//
// There is deliberately no Last-Event-ID resume: the stream is
// from-now-on (see Subscribe) and catch-up after a page reload reads the
// authoritative state (for jobs, job.Result), so replay would duplicate
// that mechanism, not replace it.
func (b *Broker) ServeTopic(w http.ResponseWriter, r *http.Request, topic string) {
	sub := b.Subscribe(r.Context(), topic)
	defer sub.Cancel()

	sseHeaders(w)
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	rc.Flush()

	heartbeat := time.NewTicker(b.cfg.Heartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-sub.Events:
			if !ok {
				// The broker removed the subscription (ctx
				// watcher or Cancel). Nothing more to write.
				return
			}
			if err := writeEvent(w, ev); err != nil {
				return
			}
			rc.Flush()
			heartbeat.Reset(b.cfg.Heartbeat)
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			rc.Flush()
		}
	}
}

// writeEvent frames one event onto the SSE wire: the `event:` field (when
// named), then Data split on line breaks into `data:` lines, then the
// blank line that terminates the event. Carriage returns are normalised
// to line breaks first — a bare CR inside Data would otherwise be read as
// a line terminator by clients but not by the split, corrupting the
// framing. The Name gets the same hardening from the other side: every
// newline in it is flattened to a space, because a name is one `event:`
// field — a newline inside it would put a spurious field line inside the
// frame, and every client would read that line as payload. A nil writer
// error surfaces to ServeTopic, which ends the response.
func writeEvent(w io.Writer, event Event) error {
	var b strings.Builder
	if event.Name != "" {
		b.WriteString("event: ")
		b.WriteString(oneLine(event.Name))
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(normalizeNewlines(event.Data), "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}

// normalizeNewlines turns CR and CRLF into LF so a single split("\n")
// covers every newline spelling.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// oneLine flattens every newline spelling in s to a single space — the
// Name-side counterpart of normalizeNewlines plus the data split: an
// event name frames as exactly one field line, so an embedded newline
// must never reach the wire.
func oneLine(s string) string {
	return strings.ReplaceAll(normalizeNewlines(s), "\n", " ")
}
