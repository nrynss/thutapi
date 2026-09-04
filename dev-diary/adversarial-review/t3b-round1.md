# T3b — round 1 adversarial review

**Target tree:** uncommitted working tree on `main`, base `26b2b1d`
(delta: `M internal/gmi/media/client.go` +15/−8; new
`internal/gmi/media/polling.go`, `internal/gmi/media/polling_test.go`,
`internal/job/`, `internal/stream/`).
**Date:** 2026-09-05.
**Evidence:** AGENTS.md; PLAN.md §T3b, §T4, §T6, §T8, invariants 4/5/6/8,
§Unowned seams; project.md §Architecture consequences, §Stack, §Lift from
Mosaic; adversarial-review/README.md schema; the code and tests of
`internal/stream/**`, `internal/job/**`, `internal/gmi/media/**`; the T2
suite as regression net.
**Method:** probe, don't trust prose. Every claim below was exercised with
throwaway `zz_probe_test.go` probes (one per package: stream, job, media)
under `-race -count=1`, on this machine, against a fake queue / real
httptest SSE surface — no live network. Probe outputs are quoted in the
Pins table. Probe files were removed after the run; `git status` at close
shows the tree byte-identical to the reviewed delta except this file.
No fixes were applied by this review.

## Verdict: REMEDIATE — 0 C / 0 H / 2 M / 2 L

| Severity | Count |
| --- | --- |
| C | 0 |
| H | 0 |
| M | 2 |
| L | 2 |

## Gates (run by this reviewer on the final tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race -count=1` | ok, all packages |
| `gofmt -l .` | clean |
| `go test ./... -cover` | stream 94.4 %, job 94.6 %, media 92.3 %, text 90.4 %, store 86.9 %, mediastore 95.6 %, cmd 81.5 % — floors met (85 % gmi, 75 % others) |
| `CGO_ENABLED=0 go build ./...` | ok |
| `go test ./internal/stream ./internal/job -race -count=3` | ok ×3 (flake shake; the probe-induced failure below is the reviewer's own probe, removed before close) |

## Done-when coverage (PLAN §T3b)

Every line of the `Done when` was executed, not just read:

- *httptest SSE client subscribes, fake long job streams progress + terminal*
  — `TestProgressAndTerminalExactlyOnce`, `TestStartReturnsImmediately`,
  corroborated by probe P2/P3.
- *heartbeat observed between events* —
  `TestServeTopicHeartbeatBetweenEvents` (content-predicate), plus
  byte-level frame-integrity probe P4 (25+ frames, zero torn frames).
- *second subscriber joining mid-job catches up* —
  `TestMidJobSubscriberCatchesUp` (mid-job via stream, post-terminal via
  `Result`), dedup contract documented at `job.Result`.
- *poll drives queued → processing → completed; failed → ErrTransient;
  context deadline cuts the poll* — `TestPoll_QueuedProcessingCompleted`,
  `TestPoll_FailedMidPollResubmitsOnce`, `TestPoll_FailedBudgetSpent`,
  `TestPoll_DeadlineCutsPoll`, `TestPoll_CallerDeadlineCutsPoll`.
- T2 regression net with `c.post` → `c.drive` in place: full media suite
  green (`TestRetry_5xxHitTwice`, `TestRetry_FailedStatusThenSuccess`,
  `TestRetry_FailedStatusBudgetSpent`, all raw-wire pins unchanged).

## Judged design decisions (recorded claims, probed)

1. **Drop-oldest slow-subscriber policy** — sound for its purpose. A
   stalled reader never blocks `Publish` (P2: 5000 publishes against a
   dead reader, instant); the terminal event — published last, therefore
   newest — survives a full buffer and is the last event drained (P3). A
   subscriber can only miss the terminal by being removed before it is
   published, and that case is exactly what the documented
   subscribe-then-Result dedup covers ("may see the terminal event AND
   Result; it deduplicates on the job id"), pinned by
   `TestMidJobSubscriberCatchesUp`. No finding.
2. **`context.WithoutCancel` on jobs** — the policy (a client leaving must
   not kill a generation) is right and pinned by
   `TestJobOutlivesRequestContext`. But it is one-way and the Runner offers
   no substitute handle: **finding J1 (M)**.
3. **Panic-at-boundary recovery** — sound, including the harder case: an
   fn that returns and then panics in its own deferred call is still
   caught by the boundary recover; exactly one `error` terminal,
   `errors.Is(.., ErrPanic)`, `Data == nil`, runner reusable (P7). No
   finding.
4. **Poll/resubmit interaction** — the resubmit is honestly recorded
   (polling.go header: "a resubmit is the only way a request-queue failure
   can heal") and pinned (`TestPoll_FailedMidPollResubmitsOnce`: exactly
   2 POSTs). Probe M2 shows what it means at the provider: the second POST
   gets a **new** request id (`req-aaa` → `req-bbb`) and every subsequent
   GET targets the new id — the dead id is never polled again. So yes, a
   resubmit creates a second provider request; that is the recorded
   design, not a hidden double-bill. The one nuance the docs under-state:
   the resubmit rides through `post`, which keeps T2's own one-retry
   budget, so a resubmit whose *submit body* reports failed fans into two
   POSTs — 3 POSTs / 2 GETs observed end to end (M3). The header's "each
   HTTP call site keeps T2's one-retry budget" states this, so it is
   recorded, not a finding. Whether a failed request bills is a live-API
   truth owned by T2b.
5. **Status casing normalisation** — works end to end: submit `QUEUED`,
   polls `Processing`/`CoMpLeTeD` drive to the completed body, and the
   submit-side `FAILED` peek keeps its one T2 retry (M1). Behaviour is
   identical for every pinned T2 input (all use lowercase; uppercase is a
   superset that now retries), and the full T2 suite is green.
6. **The poll GET path is derived, not documented** — polling.go records
   the provenance honestly ("the documented submit path plus the request
   id; the same shape the one live consumer … drives successfully") and a
   wrong path fails loudly: the GET carries bearer auth to
   `.../requests/{id}` (wire-pinned by `TestPoll_QueuedProcessingCompleted`),
   a 404 mid-poll surfaces `gmi.ErrModelNotFound` immediately with no
   retry (`TestPoll_GET404SurfacesModelNotFound`), and a queue that names
   no request id refuses to poll (`gmi.ErrTransient`,
   `TestPoll_MissingRequestID`). No silent wedge exists. Judged safely
   gated.
7. **Invariant 6 fitness** — nothing in T3b blocks an HTTP response:
   `ServeTopic` writes headers and first bytes immediately, flushes per
   event, heartbeats at the pinned 15 s default
   (`TestDefaultHeartbeatReachesTheWire`). The media budgets (120 s submit
   + 120 s poll) exceed 100 s but execute in job goroutines and honour any
   caller deadline (`TestPoll_CallerDeadlineCutsPoll`); the request-path
   wiring that must respect the 100 s budget is T4/T6/T8's, per invariant
   6's own text. Judged fit.
8. **The readUntil flake fix** — sound. Predicates are content-based
   ("a ping before and after the event"), never count-based; timeouts are
   5 s against 25 ms ticks; the result channel is buffered so a late reader
   cannot deadlock; cleanup closes bodies and servers. The heartbeat test's
   60 ms publish against 25 ms ticks is proven by content, not luck. Three
   runs under `-race` are clean. Side observation (not counted): three
   instant-fn tests (`TestPanicBecomesErrorTerminal`,
   `TestFnErrorSurfacesSentinel`, `TestJobOutlivesRequestContext`) do not
   gate the Func against the subscribe, contrary to `subscribeAfter`'s own
   doc; a 2000-run census produced 0 terminal-before-subscribe misses, so
   there is no observable failure to pin — flagged for round 2's
   attention only.
9. **Start after stop / lifetime** — the Runner has no Stop, by doc
   ("One per process"); every goroutine's exit path is defined (run exits
   when fn returns; the Subscribe watcher exits on `gone` or ctx). The
   absence of any *forced* exit is finding J1.
10. **Job ids** — 500 starts: every id 32 lowercase hex, zero collisions
    (P9); `ErrUnknownJob`, `ErrNoBroker`, the ErrLimit storm at
    `DefaultLimit`, and `TopicPrefix` are all pinned. Result registry is
    unbounded for the process lifetime, but that is the recorded decision
    ("results live for the process lifetime"; books persist via T3) —
    noted, not counted.

## Findings

| # | Severity | Where | What | Pin | Mutation |
| --- | --- | --- | --- | --- | --- |
| J1 | M | `internal/job/job.go:186` (with `Start` 162–189, `Runner` 118–124) | A running job has no cancellation path. `Start` hands fn `context.WithoutCancel(ctx)` — a context nothing in the process can ever cancel — and the Runner exposes no `Cancel`/`Shutdown`. An fn written in the idiomatic "return promptly on `ctx.Done()`" shape never returns; its limit slot is released only by `run`'s defer, so it leaks forever. Probed: with `Limit: 2`, two idiomatic fns whose starting requests all get cancelled, after 2 s of grace every further `Start` still returns `ErrLimit`, and `Result` still reports `StatusRunning` — recovery is impossible without a process restart. Sixteen such fns wedge every future `Start` at the default limit. "Fn bounds its own lifetime" is recorded, but the seam gives the operator — and T11's cap/gate work — no handle to stop a runaway generation that is actively spending the wallet. | `TestProbe_RunningJobMustBeCancellable` (job zz_probe): fails today — `no slot recovers after every origin context is cancelled: job: runner at capacity`; corroborated by `TestProbe_ResultStaysRunningAfterOriginCancel` (Result stays `running` with the origin ctx cancelled). | After remediation adds the cancel registry + `Runner.Cancel(id)`: delete the registration line in `Start` (revert to bare `context.WithoutCancel(ctx)` on line 186) — the Pin fails again. |
| G1 | M | `internal/gmi/media/polling.go:190-193` (`settle` default branch) | A queue record with a `request_id` but an unknown (or absent) status on the submit body is handed to the caller **as the result** — `err == nil`, zero poll traffic. Observed: submit `{"request_id":"req-1","status":"started"}` → `GenerateImage` returns that queue record as the image bytes. The caller (T6/T8) receives a non-result as media with no sentinel — a silent wrong answer. The rationale on the branch ("only the four documented statuses may drive polling") is contradicted twice in the same file: the header admits the documented set was already incomplete live (`success`, `cancelled` were observed in the wild and added), and `pollOnce` treats the very same unknown statuses as non-terminal and waits. At submit the unknown status terminates the call instead. | `TestProbe_UnknownSubmitStatusMustNotPassAsResult` (media zz_probe): fails today — `passed through as a result (…status":"started"…) with no poll`. Covers both `status:"started"` and status-absent bodies. | Restore the defect by deleting the unknown-status handling in `settle` (make the `default` branch return `raw, nil` even when `st.RequestID != ""` — i.e. exactly today's behaviour) — the Pin fails again. |
| S1 | L | `internal/stream/sse.go:80-84` (`writeEvent`) | `Event.Name` is written onto the wire without the newline normalisation `Event.Data` gets (`normalizeNewlines`, lines 85, 95–100). A Name containing `\n` produces a spurious field line inside the event frame. Observed wire for `Event{Name: "done\ndata: injected", Data: "real"}`: `event: done\ndata: injected\ndata: real\n\n` — every SSE client reads `data: injected` as part of the event payload. All current publishers pass constants (job `Status` values), so nothing triggers today; but this seam exists so T4/T9 publishers can use it, and the asymmetric treatment (Data hardened, Name not) is the oversight. | `TestProbe_EventNameNewlineMustNotCorruptFraming` (stream zz_probe): fails today with the wire quoted above. | Re-introduce by removing the newline stripping on the Name path (write `event.Name` verbatim again) — the Pin fails again. |
| G2 | L | `internal/gmi/media/polling.go:223-247` (`await` select), contract at 94–100 | A deadline that expires while a poll GET is in flight surfaces `gmi.ErrTransient`, not `ErrPollDeadline`, whenever a ticker tick is pending at the moment the aborted GET returns: the select sees both channels ready, the tick path counts the deadline abort as a transient, the next (instantly-failing) GET spends the budget, and the caller gets `gmi: transient error: polling request req-1: … context deadline exceeded`. Probed deterministically (GET blocked past the budget, 5 ms ticker, 40 ms budget): 8/8 runs returned `gmi.ErrTransient`, 0/8 `ErrPollDeadline` — contradicting the sentinel's own doc ("ErrPollDeadline is returned when the poll loop is cut short by its own Timeout budget or by the caller's context deadline"). The documented fallback advice still holds (both chains carry `context.DeadlineExceeded`, pinned by `TestPoll_DeadlineCutsPoll`), which is why this is L: only a caller matching the sentinel instead of the context error misclassifies a budget-out as retry-now. | `TestProbe_DeadlineBeatsLateTerminal` (media zz_probe): fails today — `err = gmi: transient error: … context deadline exceeded, want ErrPollDeadline wrapping DeadlineExceeded` (8/8 deterministic). | Re-introduce by letting the transient counter consume the deadline-aborted GET (revert any fix that checks `pollCtx.Err()` before counting, or that drains the ticker when `pollCtx` is done) — the Pin fails again. |

## Pins run (probes; removed after the run)

| Probe | Package | Asserts | Result on this tree |
| --- | --- | --- | --- |
| `TestProbe_SubscriberLeakOnCtxCancel` | stream | cancelled ctx removes subscriber; post-cancel publish leaks nothing; Events closed | PASS |
| `TestProbe_PublisherNeverBlockedBySlowSubscriber` | stream | 5000 publishes vs stalled reader; newest survives | PASS |
| `TestProbe_TerminalSurvivesFullBuffer` | stream | terminal (published last) lands in a full buffer, drained last | PASS |
| `TestProbe_HeartbeatByteFraming` | stream | 25+ interleaved ping/event frames, all well-formed, none torn | PASS |
| `TestProbe_TwoBrokersIsolated` | stream | cross-broker silence; nil-broker publish no-op | PASS |
| `TestProbe_EventNameNewlineMustNotCorruptFraming` | stream | **S1** — Name newline must not inject a data line | **FAIL** (finding) |
| `TestProbe_DeferPanicAfterReturn` | job | fn-returns-then-defer-panics → exactly one error terminal, ErrPanic, Data nil | PASS |
| `TestProbe_UngatedSubscribeRaceCensus` | job | 2000 instant fns: terminal never beat the subscribe (0 misses) | PASS |
| `TestProbe_RunningJobMustBeCancellable` | job | **J1** — slots must recover when every origin ctx is cancelled | **FAIL** (finding) |
| `TestProbe_IDsUniqueAndShaped` | job | 500 ids: 32 lowercase hex, unique, all settle | PASS |
| `TestProbe_ResultStaysRunningAfterOriginCancel` | job | WithoutCancel one-way: fn never sees cancellation, Result stays running | PASS (corroborates J1) |
| `TestProbe_StatusCasingNormalised` | media | QUEUED/Processing/CoMpLeTeD end-to-end; FAILED submit peek retried once | PASS |
| `TestProbe_ResubmitCreatesNewProviderRequest` | media | resubmit gets a NEW request id; polls target only the new id | PASS |
| `TestProbe_ResubmitFansThroughPostRetry` | media | one resubmit with a failed submit body = 3 POSTs / 2 GETs, T2 budget intact | PASS |
| `TestProbe_UnknownSubmitStatusMustNotPassAsResult` | media | **G1** — unknown/absent status with request_id must not pass as result | **FAIL** (finding) |
| `TestProbe_DeadlineBeatsLateTerminal` | media | **G2** — late terminal must surface ErrPollDeadline wrapping DeadlineExceeded | **FAIL** (finding, 8/8) |
| `TestProbe_TerminalInsideItsTickIsNotLost` | media | a completed GET that straddles ticks is never abandoned | PASS |
| `TestProbe_BudgetConstants` | media | DefaultPollTimeout/Interval/defaultCallTimeout as documented | PASS |

## Boundary

This round judged and probed the whole of §T3b's `Done when` on the
working tree: the broker (lifecycle, drop-oldest, framing, heartbeats,
isolation), the job runner (contract, panic boundary, capacity, ids,
catch-up), the polling addition (drive/settle/await/pollOnce, the
client.go delta's behaviour-identity for pinned inputs via the full T2
suite), the recorded design decisions listed above, invariant 4's
placement (one polling layer inside `internal/gmi/media` — respected; no
second polling implementation exists) and invariant 6 fitness. Not judged,
by ownership: live-API truths the operator owns (T2b — real poll-path
shape, real status vocabulary beyond the two live-observed additions,
billing of failed requests); T4/T6/T8 request-path wiring against the
100 s budget; the unrecorded-in-PLAN prose of project.md (the "223 lines"
figure for Mosaic's broker.go is approximate provenance, not a pin).
False-positive candidates considered and rejected as recorded design:
nil-Broker no-op publish, empty-progress drop, unknown-status *mid-poll*
wait, 404-on-poll mapping to `ErrModelNotFound` (pre-existing T2
classifier), unbounded result registry, and the ignored `rc.Flush()`
error (write errors still terminate `ServeTopic`).

## Exit criteria for round 2 (REMEDIATE)

1. Remediation file `t3b-remediation-round1.md` with one row per finding
   J1, G1, S1, G2 — no severity exempt.
2. J1: a cancellation handle exists for running jobs (e.g. `Runner.Cancel`
   or a bounded-lifetime job context) with a regression test that fails on
   the pre-fix tree (the J1 probe, suite-ised). "Fn must self-bound" alone
   does not close J1 — the finding is the missing handle, not the docs.
3. G1: an unknown/absent status on a submit body that carries a
   `request_id` either polls or errors with a sentinel — never passes a
   queue record through as media. Decide the recorded contract either way
   and pin it; if the decision is "wait like pollOnce", the poll deadline
   bounds it.
4. S1: Name framing is hardened (or the Event contract explicitly
   documents and *enforces* publisher-side Name encoding) with the wire
   pin from S1 in the suite.
5. G2: the sentinel classification for a deadline that fires mid-GET is
   deterministic and matches the sentinel doc (or the doc is corrected to
   match the behaviour, with the context-error matching advice pinned).
6. All gates re-run green (`vet`, `-race`, `gofmt -l .`, coverage floors,
   `CGO_ENABLED=0 go build`), T2 regression net still green, and a fresh
   reviewer re-opens the full `Done when`, not just the diff.
