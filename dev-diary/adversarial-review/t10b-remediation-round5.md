# T10b remediation round 5

The H1 finding from `t10b-round5.md` is addressed below. This record does not
change the review verdict, PLAN status, or commit state.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `static/book/catchup.js`; `static/app.js`; `static/browser-test.js` | A reconnect during an in-flight C4 snapshot was discarded by the per-subscription in-flight guard. The old running snapshot could then apply after a `book_ready` event was published during the disconnect, leaving the player drawing forever. | `subscribeBook` assigns a monotonically increasing connection epoch and reports it on every reconnect. `readBookState` returns the epoch captured after the subscription opened. The app tracks the newest requested epoch per subscription; a reconnect queues a follow-up read, and a response from an older epoch is discarded before state application. Only the newest read may apply terminal state or trigger a fresh-generation transition, with one bounded request at a time and no new goroutines or timers. | `testC4ReconnectQueuesSnapshotDuringInflightRead` holds the first state promise, simulates disconnect, drops `book_ready` during the gap, reconnects, resolves the first read as `running`, and requires a second state request that returns `ready` and navigates to `/book/book`. | Restore the one-bit `WeakSet` in-flight guard or remove the epoch check/queued reread. The delayed reconnect pin then records one state request, applies stale `running`, and misses the terminal result. |

## Checks

* `node --check static/app.js static/book/catchup.js static/browser-test.js` — PASS
* `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1` — PASS
* `git diff --check` — PASS

The browser harness is deterministic and syntax-checked; no browser execution
is claimed because no browser surface is available in this environment.
