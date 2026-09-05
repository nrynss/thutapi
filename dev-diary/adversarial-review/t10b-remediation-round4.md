# T10b remediation round 4

The H1 finding from `t10b-round4.md` is addressed below. This record does not
change the verdict, PLAN status, or commit state.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `static/book/catchup.js`; `static/app.js`; `static/browser-test.js` | C4 read state ran only after the first EventSource `open`; reconnects and fresh generation had an event-loss interval, so a missed approval or `book_ready` could leave the player drawing forever. | The catch-up subscription now signals every establishment, while the app performs one synchronized `/book/{id}/state` read after the initial open and after each reconnect. Fresh `POST /generate` also awaits its first synchronized read. Reads are scoped per subscription and deduplicated while in flight; state-read failures retain the live stream and terminal events still control navigation/failure. | `testC4ReconnectRepeatsSnapshot` holds a running initial snapshot, dispatches a second `open`, returns `ready`, and requires the second state read and book navigation. `testFreshGenerationSynchronizesAfterOpen` requires a fresh generation's state read and restores its approved page marker. `node --check static/app.js static/book/catchup.js static/browser-test.js`; focused Go tests pass. | Restore the `{ once: true }` open listener or remove the fresh-generation synchronization. The reconnect pin then sees one state request and remains on the drawing screen; the fresh-start pin loses the approved marker. |

## Checks

* `node --check static/app.js static/book/catchup.js static/browser-test.js` — PASS
* `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1` — PASS
* `git diff --check` — PASS

The browser harness cases are deterministic and syntax-checked; no browser
surface was available for execution in this environment.
