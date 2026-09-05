# T10b remediation round 2

The two C4 lifecycle findings and the one ownership finding from
`t10b-round2.md` are remediated below. No verdict or PLAN status is changed.

The C4 protocol now lives in T10b's `static/book/catchup.js`: it owns the
subscription-ready barrier and the single state read. The existing T9
interview shell is retained only as the narrow consumer adapter because it
already owns screen 5 and its race widget. Its adapter supplies the existing
interview-derived event URL and Preact callbacks; it does not define or change
the C4 protocol. This is the minimal integration seam for the already-recorded
C4 consumer, and replaces the round-1 inline protocol implementation in
`static/app.js`.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `static/book/catchup.js`; `static/app.js` C4 adapter; `static/browser-test.js:testC4WaitsForOpenAndKeepsBoundaryEvent` | Constructing an `EventSource` did not establish the server subscription before C4 read state, leaving an event-loss interval. | `subscribeBook` installs handlers and exposes an `opened` barrier; `readBookState` waits for that `open` acknowledgement before the one C4 read. The adapter still deduplicates page numbers, so a page received at the boundary and a later three-page snapshot produce four real markers. | The controllable fake holds `open`, asserts no `/book/book/state` fetch, injects page 4, opens the source, resolves a three-page snapshot, and requires four markers with the running stream still open. | Replace the open barrier with constructor-followed-by-fetch. The test observes the state read before `open`, recreating the missed-event interval. |
| H2 | `static/book/catchup.js`; `static/app.js`; `static/browser-test.js:testC4StateFailureKeepsLiveStream` | A failed C4 state read falsely rendered the terminal failure screen and closed the authoritative SSE stream. | `readBookState` returns a read error without closing its subscription. The adapter renders the non-terminal, recoverable “We’re checking on your book” state; page events restore drawing, and only a stream `failed` event creates retry doors. | A synchronized C4 read returns 500. The test requires the checking state, no retry door, and an open stream; a later `book_ready` must still be handled and close that source. | Restore `close()` plus `setBookState("failed")` on the C4 read error. The source-close and no-failure-door assertions fail. |
| M1 | `static/book/catchup.js`; `static/app.js`; `static/browser-test.js` | Round 1 put the complete C4 consumer inside T9-owned `static/app.js` without an owned protocol seam. | Moved the reusable C4 subscription, ready barrier, terminal handling, and snapshot result contract into T10b-owned `static/book/catchup.js`. The remaining `static/app.js` code is the constrained T9 adapter described above, preserving screen-5 state and the T9a race bridge. | The existing ended-reload pin still proves the adapter restores C4 state without a repeat generate POST; the two new C4 lifecycle pins exercise the module through that adapter. | Inline `subscribeBook`/`readBookState` back into `static/app.js` or remove the module import. The ownership seam disappears and the open-barrier lifecycle pin no longer has a T10b-owned implementation. |

## Checks

* `node --check static/app.js static/book/catchup.js static/browser-test.js` — PASS
* Node C4 module contract (held `open`, boundary page, failed state read, then `book_ready`) — PASS
* `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1` — PASS
* `go vet ./...` — PASS
* `go test ./... -race -count=1` — only `internal/job.TestStartAtLimit/explicit-limit` failed, timing out after zero events. `go test ./internal/job -race -count=5` — PASS. No T10b path imports or changes `internal/job`; this is recorded as the known unrelated timing flake.
* `test -z "$(gofmt -l .)"` and `git diff --check` — PASS

The deterministic browser contract cases are present and syntax-checked. They
were not executed in a browser here: CUA reported no available browsers or
apps, and its native-app entry point was unavailable. No browser execution is
claimed.
