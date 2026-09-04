# T3b round 1 — remediation

| | |
|---|---|
| **Target** | All 4 findings (2 × M, 2 × L) in `dev-diary/adversarial-review/t3b-round1.md`. Verdict was REMEDIATE (0C/0H/2M/2L). |
| **Date** | 2026-09-05 |
| **Commit** | Uncommitted at remediation time; the working tree stays uncommitted — the orchestrator commits on APPROVE (AGENTS.md §Process step 5). |
| **Round file** | `dev-diary/adversarial-review/t3b-round1.md` is unchanged. PLAN.md is untouched. |
| **Verification methodology** | Every pin re-run under `-race -count=1` against the remediated tree. Each of the four reviewer-prescribed mutations applied by hand, observed RED for the recorded reason, tree restored byte-identical (`cmp` against a pre-mutation snapshot), pin re-run green. Gate outputs in the aggregate section below; full suite green twice back-to-back as the flake check. Everything ran locally against `httptest` surfaces; no live GMI or network calls. |
| **Scope note** | `internal/job/**`, `internal/gmi/media/polling.go` + its tests, `internal/stream/sse.go` + its tests, and this file. Nothing else moved: `client.go` needed no change (both media fixes are inside polling.go's seam), `main.go` untouched. The T2 regression net (`TestRetry_*`, raw-wire pins) ran green inside the media suite, unchanged. |

## Rows

### J1 — A running job has no cancellation path; a hung fn leaks its limit slot forever

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/job/job.go` — the cancel registry on `Runner` (now :135), `Start`'s job-context construction (now :193), the new `Cancel` (:210-232), `run`'s exit path and terminal classification (now :236-280), the new `StatusCancelled` (:58). |
| **What was done** | The one-way `context.WithoutCancel` gained a handle. `Start` now builds the job context as `context.WithCancel(context.WithoutCancel(ctx))` — request-value inheritance kept exactly as pinned by `TestJobOutlivesRequestContext` (still green), plus a Runner-owned cancel handle — and registers it in a new `cancels map[string]context.CancelFunc` guarded by the Runner's existing mutex. The new `Runner.Cancel(id)` cancels that internal context; cancellation is cooperative and documented as such (Go cannot kill a goroutine): an fn that returns promptly on `ctx.Done()` ends the job and frees its limit slot, one that ignores its context keeps going until it returns on its own. **Recorded choice (the round's "pick one"):** the cancelled terminal is a **Status value**, not a sentinel — `run` classifies fn's error with `errors.Is(err, context.Canceled)` (only `Cancel` holds a cancel handle for the job context, so that chain is precisely a cancelled job) into `StatusCancelled` (`"cancelled"`), published through run's single terminal site exactly once; `Result.Err` keeps the context error chained, `Data` is whatever fn returned. `Cancel` is safe to call twice on a running job (second call is a no-op returning nil) and reports `ErrUnknownJob` — the same sentinel `Result` uses — for an id never started, already terminated, or from another Runner. The registry entry is deleted in `run`'s exit path (after the terminal is published, before the slot releases), so no stale cancel handle outlives its job. `Func`/`Start`/`Status` doc comments updated to match the behaviour. |
| **Test added** | `TestCancelRunningJobRecoversSlot` — the round-1 probe, suite-ised: a `Limit: 2` runner, two idiomatic fns blocked on `ctx.Done()`, the capacity probe returns `ErrLimit`, then `Cancel` on both → the `"cancelled"` terminal is observed on each topic exactly once (subscriptions made before the cancel, so no terminal can outrun them), `Result` reports `StatusCancelled` with `context.Canceled` in the chain and nil `Data`, and a fresh `Start` succeeds (bounded retry for the defer-ordered slot release). `TestCancelIdempotentAndUnknown` — unknown id → `ErrUnknownJob`; a second `Cancel` while the job is still registered (fn held past its `ctx.Done()`) → nil; after the terminal, `Cancel` reports `ErrUnknownJob` again — the entry is provably gone. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked: deleting the registration line `r.cancels[id] = cancel` in `Start` (the round's re-introduction — the handle exists but nothing can reach it) turns both Cancel pins red with `Cancel(..): job: unknown id`; restored byte-identical (`cmp`), green again. |

### G1 — A queue record with a request_id but an unknown/absent status is handed to the caller as the result

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/gmi/media/polling.go` — `settle`'s default branch (now :196-206) and the rationale the finding contradicted: the file header (now :1-9) and `settle`'s doc comment (now :169-180). |
| **What was done** | The default branch now treats a request_id-bearing record as non-terminal and hands it to the poll loop — the same wait `pollOnce` already gives the very same unknown statuses mid-poll, bounded by the poll deadline (min of `PollConfig.Timeout` and the caller's deadline), so an unknown status can no longer terminate the call as a silent wrong answer. A body with **no** `request_id` keeps T2's raw passthrough byte-identically — it cannot be polled, and it may not even be a queue record. Both the file-header rationale ("while the answer names a request id and no terminal status — queued or processing, or a status this client does not know — the queue may still hold the work") and `settle`'s branch rationale now state this contract; the contradiction the finding cited is gone. |
| **Test added** | `TestSubmitNonTerminalSubmitBodiesPoll` — table-driven over the finding's two bodies (`status:"started"`, status absent): poll traffic observed on the pinned path (`.../requests/req-1`), and only the terminal body reaches the caller. `TestSubmitUnknownStatusWithoutRequestIDPassesThrough` — the unchanged half: no-request-id body passes through untouched with 1 POST / 0 GETs. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked: the default branch reverted to unconditional `return raw, nil` (the round's re-introduction — exactly the pre-fix behaviour) turns both poll subtests red with the reviewer's exact symptom — the submit body returned as the result, 0 GETs — while the no-request-id passthrough pin stays green, proving it is the untouched half; restored byte-identical (`cmp`), green again. |

### S1 — Event.Name is written onto the wire without newline normalisation

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/stream/sse.go` — `writeEvent`'s Name path (now :81-87) and the new `oneLine` helper (:104-110). |
| **What was done** | `Event.Name` now passes through `oneLine` — `normalizeNewlines` (CR/CRLF → LF) followed by LF → space — before it is written, the Name-side counterpart of the treatment `Data` already gets: a name frames as exactly one `event:` field line, so no publisher-supplied name can inject a field line into the frame. `Data`'s path is untouched. `writeEvent`'s doc comment states the hardening and why (a newline inside a name would be read as payload by every SSE client). |
| **Test added** | `TestServeTopicEventNameNewlineSanitized` — the round's wire pin, suite-ised: `Event{Name: "done\ndata: injected", Data: "real"}` frames as exactly `"event: done data: injected\ndata: real\n\n"` (two field lines, payload untouched), plus the CRLF spelling (`"a\r\nb"` → `event: a b`). |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked: writing `event.Name` verbatim again turns the pin red with the reviewer's exact wire — `"event: done\ndata: injected\ndata: real\n\n"` — every client reading `data: injected` as payload; restored byte-identical (`cmp`), green again. |

### G2 — A deadline expiring mid-GET surfaces gmi.ErrTransient instead of ErrPollDeadline

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/media/polling.go` — `await`'s select tick path (now :240-264). |
| **What was done** | Inside the transient branch, `pollCtx.Err()` is checked **before** a transient is counted: when the budget expired while the GET was in flight, the loop returns `ErrPollDeadline` wrapping the live context error — matching the sentinel's own doc — instead of counting the deadline abort as transient, spending the budget on an instantly-failing retry GET, and mislabelling budget-out as retry-now. Everything between ticks is untouched: a genuine transient still gets its one-tick tolerance (`TestPoll_TransientGETToleratedOnce`, `TestPoll_TwoConsecutiveTransientGETs` green, unchanged), a non-transient sentinel still surfaces immediately, and a terminal that lands inside its tick still wins (the `done` path precedes the guard's reach). |
| **Test added** | `TestPoll_DeadlineDuringInflightGETSurfacesPollDeadline` — deterministic: the poll GET is blocked past the 40 ms budget while a 5 ms ticker ticks, then asserts `errors.Is(err, ErrPollDeadline)`, the underlying `context.DeadlineExceeded` in the chain, **no** `gmi.ErrTransient` in the chain, and exactly 1 POST / 1 GET — the deadline-aborted GET is the last. |
| **Pin re-run status** | Green 8/8 under `-race -count=8`. Mutation-checked: the guard deleted (the deadline-aborted GET counted as transient again) goes red 6/8 with the reviewer's exact misclassification (`gmi: transient error: polling request req-1: … context deadline exceeded`); the 2 green runs are the select's own coin-flip between the ready `Done` and tick channels on the mutation's re-entry — the very nondeterminism the finding documents — and the fixed tree is deterministic because the guard returns without re-entering the select. Restored byte-identical (`cmp`), green 8/8 again. |

## Mutation check (applied by hand, observed red, reverted byte-identical, re-run green)

| Mutation | Pin that goes red |
|---|---|
| J1: `r.cancels[id] = cancel` deleted from `Start` | `TestCancelRunningJobRecoversSlot`, `TestCancelIdempotentAndUnknown` (both: `Cancel` → `ErrUnknownJob`; slot recovery impossible) |
| G1: `settle` default branch reverts to unconditional `return raw, nil` | `TestSubmitNonTerminalSubmitBodiesPoll/unknown-status`, `/absent-status` (submit body passed as result, 0 GETs; the no-request-id passthrough pin correctly stays green) |
| S1: `event.Name` written verbatim (no `oneLine`) | `TestServeTopicEventNameNewlineSanitized` (wire shows the injected `data: injected` field line) |
| G2: `pollCtx.Err()` guard deleted from `await`'s transient branch | `TestPoll_DeadlineDuringInflightGETSurfacesPollDeadline` (6/8: `gmi.ErrTransient` misclassification, 2/8 the select coin-flip the finding itself describes) |

## Residue against round 1

**Zero.** Each of the four findings has a row above following its mutation's letter; the contract's recorded choices are stated in place (J1: Status value over sentinel; G1: wait like pollOnce, deadline-bounded, no-request-id passthrough byte-identical). The round-1 pins that already passed run green inside the full suite below, untouched except where a row says otherwise; the T2 regression net (`TestRetry_*`, raw-wire pins, `TestSubmitCompletedNoPoll`) is green with `c.post → c.drive` unchanged.

## Aggregate verification

```
$ go vet ./...                       clean
$ gofmt -l .                         empty
$ CGO_ENABLED=0 go build ./...       ok
$ go test ./... -race -count=1       ok — all packages, twice back-to-back (flake check)
$ go test ./... -cover
stream       94.4%  (floor 75%; unchanged)
job          95.7%  (floor 75%; was 94.6%)
gmi/media    93.0%  (floor 85%; was 92.3%)
gmi/text     90.4%  (floor 85%)
store        86.9%  (floor 75%)
mediastore   95.6%  (floor 75%)
cmd/thutapi  81.5%  (floor 75%)
```

## Files changed

* `internal/job/job.go` — `StatusCancelled`; `cancels` registry on `Runner`; `Start` builds the job context as `WithCancel(WithoutCancel(ctx))` and registers the handle; new `Runner.Cancel`; `run` takes the cancel, deletes the entry on exit, classifies the cancelled terminal through its single publish site; docs updated.
* `internal/job/job_test.go` — `TestCancelRunningJobRecoversSlot`, `TestCancelIdempotentAndUnknown`.
* `internal/gmi/media/polling.go` — file-header and `settle` rationale rewritten; `settle` default branch waits on request_id-bearing records and passes no-request-id bodies through unchanged; `await` checks `pollCtx.Err()` before counting a transient.
* `internal/gmi/media/polling_test.go` — `TestSubmitNonTerminalSubmitBodiesPoll`, `TestSubmitUnknownStatusWithoutRequestIDPassesThrough`, `TestPoll_DeadlineDuringInflightGETSurfacesPollDeadline`.
* `internal/stream/sse.go` — `oneLine`; `writeEvent` sanitizes `Event.Name` through it; doc updated.
* `internal/stream/sse_test.go` — `TestServeTopicEventNameNewlineSanitized`.
* `dev-diary/adversarial-review/t3b-remediation-round1.md` — this file.
