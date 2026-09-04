# T3b — review round 2

- **Target:** the uncommitted working tree on `main`, base `26b2b1d`,
  after the round-1 remediation (`t3b-remediation-round1.md`). The T3b
  delta is unchanged since round 1 closed: `M internal/gmi/media/client.go`
  (+15/−8), new `internal/gmi/media/polling.go` + `polling_test.go`,
  `internal/job/`, `internal/stream/` — the orchestrator commits on
  APPROVE (AGENTS.md §Process step 5).
- **Date:** 2026-09-05.
- **Reviewer:** round-2 review agent — a fresh role per AGENTS.md
  §Agentic development; not the round-1 reviewer, not the implementer,
  not the remediator.
- **Evidence read in full:** `AGENTS.md`;
  `dev-diary/adversarial-review/README.md`; `t3b-round1.md` (all four
  finding rows, the pins table, the exit criteria);
  `t3b-remediation-round1.md`; the whole remediated code:
  `internal/job/job.go` + `job_test.go`,
  `internal/gmi/media/polling.go` + `polling_test.go` (+ the T2 net in
  `client_test.go`), `internal/stream/sse.go` + `sse_test.go` (+ the
  broker and its tests), `client.go` as the untouched T2 base.
- **Method:** probe, don't trust prose. (1) The four remediation pins and
  the core round-1 pins re-run with recorded output under `-race`;
  (2) the two contract mutations (J1 registry deletion, G1 unconditional
  passthrough) applied by hand, observed red for the recorded reason,
  restored byte-identical (`cmp`), re-run green; (3) fresh-eyes probes
  against the new corners the remediation created — Cancel-vs-completion,
  Cancel-on-done, cancelled Result chains, concurrent Cancel, the G1
  request_id-plus-terminal paths, the G2 terminal-inside-tick edge;
  (4) gates re-run by this reviewer on the final tree. Probe files ran
  once, output recorded verbatim below, then removed; `diff -r` against
  a pre-review snapshot of `internal/**` reports the tree byte-identical,
  and `git status` at close shows only the reviewed delta plus the review
  files. No fixes were applied by this review; no live GMI or network
  calls.

## Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L

| Severity | Count |
| --- | --- |
| C | 0 |
| H | 0 |
| M | 0 |
| L | 0 |

**T3b can close.**

## Findings

None. (Empty table — recorded for schema completeness.)

| # | Severity | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| — | — | — | — | — | — |

## The new terminal path, judged adversarially (J1)

`run` remains the single place terminal state is written and the single
publish site; `Cancel` only fires the job context's cancel func. Three
questions the fix had to answer, each probed:

1. **Can Cancel race fn's own completion into a double terminal or a
   wrong winner?** No, and the winner is deterministic: classification
   reads only the error fn *returned* (`job.go:255-268`), so a completed
   fn lands `done` even if `Cancel` fired inside its final microseconds —
   and a cancelled fn lands `cancelled` exactly once. Probed
   `TestProbe_CancelVsCompletionDoneWins`: the canceller fires in a window
   straddling fn's return, 200 interleavings per run, **20 runs under
   `-race` (4000 interleavings) — 20/20 PASS, zero deviations**: always
   exactly one terminal, always `done`, Result untouched, entry cleaned
   up. There is no "cancelled-after-done": `Cancel` never touches
   `results`, so a landed terminal cannot be flipped (probed explicitly:
   `TestProbe_CancelAfterDoneNoResurrection`). The only timing-sensitive
   surface is Cancel's *return value*, not the terminal: in the window
   after the result is written but before `run`'s defer deletes the
   registry entry, `Cancel` returns nil — consistent with the doc's own
   operational definition ("the registry entry goes when the job's
   goroutine exits"), harmless, and the terminal is unaffected.
2. **Can Cancel deadlock with the registry mutex?** No path exists:
   `Cancel` copies the func under `r.mu` and invokes it **after**
   unlocking (`job.go:224-231`); `cancel()` touches no Runner state;
   `run`'s defer takes `r.mu`, releases it, then calls its own captured
   cancel; and `broker.Publish` is never called under `r.mu`, so no
   lock-ordering cycle with the broker's mutex exists. Probed under load:
   `TestProbe_CancelFromManyGoroutines` — 8 concurrent cancellers + 4
   concurrent `Result` readers on one held job, `-race` clean, no
   deadlock, all 8 cancels nil (the documented idempotent no-op), exactly
   one `cancelled` terminal.
3. **What does a caller see from a cancelled job?** `Result.Err` keeps
   `context.Canceled` in its chain with nil `Data`
   (`TestCancelRunningJobRecoversSlot`, re-run green; re-asserted in the
   8-canceller probe). Classification precedence survives the edge cases:
   an fn that panics *after* its context was cancelled classifies as an
   error terminal (`ErrPanic`, no `context.Canceled`), because the
   recovered error does not chain `context.Canceled`
   (`TestProbe_CancelThenPanicIsAnErrorTerminal`) — a panic is a failure,
   not a cancellation, matching the doc.

## The G1 boundary, judged adversarially

- **The no-request-id passthrough is byte-identical, proven twice.** The
  permanent pin `TestSubmitUnknownStatusWithoutRequestIDPassesThrough` is
  green on the fixed tree — and stayed green *under the G1 mutation*
  (below), which proves it really is the untouched half and the default
  branch is the only thing that moved.
- **request_id + terminal status mid-poll: the poll loop still wins.**
  Submit `{"request_id":"req-1","status":"started"}` → GET answers a
  record that carries the request_id **and** `completed` → the loop
  returns the completed body, 1 POST / 1 GET
  (`TestProbe_RequestIDWithTerminalStatusPollWins/mid-poll-terminal-wins`,
  ×3 under `-race`). The submit-side twin also holds: a submit body with
  request_id **and** `completed` is already terminal-good and passes
  through with zero GETs (`/submit-terminal-wins-no-poll`). `settle`
  never sees a *failed* submit body (post converts it first), so the new
  wait-on-unknown branch cannot mask a failed status at submit.

## The G2 guard, judged adversarially

**The guard cannot swallow a genuine terminal.** It lives only inside the
transient branch of `await`'s select (`polling.go:243-255`); a terminal
returns at `if done` (`polling.go:268-270`) before the guard's reach, and
non-transient errors return at `:265`. The remaining adversarial case —
a terminal GET that completes inside its tick while several ticks are
pending and a budget is burning — is probed:
`TestProbe_TerminalInsideTickBeatsDeadline` (25 ms GET against a 5 ms
ticker and a 40 ms budget): the body reaches the caller, no
`ErrPollDeadline` fabricated, 1 POST / 1 GET, ×3 under `-race`. And the
round-1 nondeterminism stays gone for a structural reason: the guard
returns *without re-entering the select*, so the coin-flip the mutation
exposed (two ready channels, two different sentinels across iterations)
no longer has a second iteration to flip — both ready orders converge on
`ErrPollDeadline` wrapping the live context error.

## The S1 hardening, judged adversarially

`oneLine` = `normalizeNewlines` (CR, CRLF → LF) + LF → space
(`sse.go:109-111`), applied to `Event.Name` only. Probed beyond the pin's
two spellings — bare CR, mixed LF/CRLF/CR, and the degenerate
all-newlines name — exact-wire equality in all five cases: exactly one
`event:` field line, one frame terminator, the single `data:` payload
line untouched (`TestProbe_NameNewlineSpellingsOneFieldLine`). A name
cannot inject a field line, whatever spelling it arrives in.

## Mutation check (applied by hand, observed red, restored byte-identical, re-run green)

| Mutation | Pin that went red | Restoration |
|---|---|---|
| J1: `r.cancels[id] = cancel` deleted from `Start` (`job.go:196`) | `TestCancelRunningJobRecoversSlot` — `Cancel(3c5af3ca…): job: unknown id` at the first Cancel; `TestCancelIdempotentAndUnknown` — `first Cancel: job: unknown id`. Exactly the remediation's recorded symptom: the handle exists but nothing can reach it | `cmp` byte-identical; both pins green again |
| G1: `settle` default branch reverted to unconditional `return raw, nil` (`polling.go:196-206`) | `TestSubmitNonTerminalSubmitBodiesPoll` both subtests red with the round-1 symptom verbatim — `raw = "{\"request_id\":\"req-1\",\"status\":\"started\"}", want the completed body — the submit body must not pass as the result`, `hits: 1 POST(s), 0 GET(s)`; `TestSubmitUnknownStatusWithoutRequestIDPassesThrough` **stayed green** (the untouched half) | `cmp` byte-identical; green again |

## Probe transcripts (verbatim; files removed after the run)

Cancel-vs-completion, `-race -count=20` (200 interleavings per run):

```
--- PASS: TestProbe_CancelVsCompletionDoneWins (4.25s)
PASS
ok  	thutapi/internal/job	86.136s
```

Cancel-after-done, 8 concurrent cancellers + 4 Result readers, and
panic-after-cancel, `-race -count=1`:

```
--- PASS: TestProbe_CancelAfterDoneNoResurrection (0.00s)
--- PASS: TestProbe_CancelFromManyGoroutines (0.10s)
--- PASS: TestProbe_CancelThenPanicIsAnErrorTerminal (0.00s)
```

G1 request_id-plus-terminal paths and the G2 terminal-inside-tick edge,
`-race -count=3`:

```
--- PASS: TestProbe_RequestIDWithTerminalStatusPollWins/mid-poll-terminal-wins
--- PASS: TestProbe_RequestIDWithTerminalStatusPollWins/submit-terminal-wins-no-poll
--- PASS: TestProbe_TerminalInsideTickBeatsDeadline (0.03s)   [×3]
```

S1 newline spellings, `-race -count=1` (the one intermediate red was a
bug in the probe's own assertion — it counted the substring `data:`
*inside* the flattened name as a payload line; the wire was correct in
every spelling, the probe was fixed, the product untouched):

```
--- PASS: TestProbe_NameNewlineSpellingsOneFieldLine (0.00s)
```

## Zero residue against round 1, finding by finding

- **J1 (M) — no cancellation handle; hung fn leaks its limit slot.**
  Fixed with the recorded choice honoured: `StatusCancelled` as a Status
  value (event name `cancelled`) classified by
  `errors.Is(err, context.Canceled)` at `run`'s single publish site;
  `Start` builds `WithCancel(WithoutCancel(ctx))` — values kept, as
  `TestJobOutlivesRequestContext` (green, re-run in the suite) still
  pins — and registers the handle; `Cancel` is nil-idempotent while
  registered, `ErrUnknownJob` for unknown/terminated/foreign ids, entry
  deleted on exit (no-leak asserted in both pins). Slot recovery is
  pinned end to end (`ErrLimit` → Cancel → fresh `Start` succeeds). The
  round-1 probe's conditions are now a permanent pin, and this round
  re-proved the pin load-bearing (mutation red, above) and found no new
  defect in the paths the fix created (section above). Residue: zero.
- **G1 (M) — unknown/absent status with request_id passed through as
  media.** Fixed with the recorded choice honoured: such a record waits
  like `pollOnce` waits the same statuses mid-poll, bounded by the poll
  deadline; the no-request-id passthrough is byte-identical (proven
  green on the fixed tree *and* under the mutation). Round-1 exit
  criterion "poll or sentinel, never a silent passthrough" is met.
  Residue: zero.
- **S1 (L) — Name written without newline normalisation.** Fixed:
  `oneLine` on the Name path, `Data` untouched, rationale in
  `writeEvent`'s doc. The round-1 wire symptom
  (`event: done\ndata: injected\ndata: real`) is now unconstructible —
  exact-wire pin plus this round's five-spelling probe. Residue: zero.
- **G2 (L) — deadline mid-GET surfaced `gmi.ErrTransient`.** Fixed:
  `pollCtx.Err()` checked before a transient is counted, returning
  `ErrPollDeadline` wrapping the context error, the aborted GET the last
  (1 POST / 1 GET pinned). The pin is deterministic on the fixed tree
  (green in-suite, in the ×3 shake, and in the remediation's own 8/8);
  the two green runs the remediation's mutation produced are the select
  coin-flip, which the fix eliminates by not re-entering the select.
  Round-1 exit criterion "deterministic and matches the sentinel doc" is
  met. Residue: zero.

Round 1's non-counted side observation (three instant-fn tests do not
gate the Func against the subscribe) was re-examined, not just carried:
the round-1 census (2000 runs, 0 misses) plus the drop-oldest terminal-
survival property and the subscribe-then-Result dedup contract
(`TestMidJobSubscriberCatchesUp`, green in the suite) still hold with the
cancel registry in place — the publish site moved not at all — and my
Cancel-vs-completion probe subscribed after `Start` like a real client
and drained exactly one terminal in 4000 interleavings. Still no
observable failure to pin; still not a finding.

## Gates (run by this reviewer on the final tree, probes removed)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race -count=1` | ok — all packages (`cmd` 10.0s, `gmi/media` 6.0s, `gmi/text` 1.9s, `job` 1.4s, `mediastore` 1.5s, `store` 1.8s, `stream` 16.5s) |
| `gofmt -l .` | empty |
| `go test ./... -cover` | stream 94.4 %, job 95.7 %, media 92.5 %, text 90.4 %, store 86.9 %, mediastore 95.6 %, cmd 81.5 % — floors met (85 % gmi, 75 % others). Isolated `go test ./internal/gmi/media -cover` reproduces the remediation's 93.0 %; the 92.5 % aggregate figure is a build-mode measurement artifact, floor holds either way (recorded, not a finding) |
| `CGO_ENABLED=0 go build ./...` | ok |
| `go test ./internal/stream ./internal/job ./internal/gmi/media -race -count=3` | ok ×3 (flake shake on the concurrency-heavy packages) |

## Pins re-run (recorded, `-race -count=1`, all PASS)

| Pin | Package | Proves |
| --- | --- | --- |
| `TestCancelRunningJobRecoversSlot` | job | J1 — ErrLimit → Cancel → `cancelled` terminal exactly once, Result chains `context.Canceled`, slot recovers |
| `TestCancelIdempotentAndUnknown` | job | J1 handle contract — nil no-op while registered, `ErrUnknownJob` after exit, no entry leak |
| `TestSubmitNonTerminalSubmitBodiesPoll` (+2 subtests) | media | G1 — unknown/absent status with request_id polls, only the terminal body reaches the caller |
| `TestSubmitUnknownStatusWithoutRequestIDPassesThrough` | media | G1 counterpart — no-request-id passthrough byte-identical, 1 POST / 0 GETs |
| `TestServeTopicEventNameNewlineSanitized` | stream | S1 — Name frames as exactly one field line, LF and CRLF spellings |
| `TestPoll_DeadlineDuringInflightGETSurfacesPollDeadline` | media | G2 — deadline mid-GET is `ErrPollDeadline` wrapping `DeadlineExceeded`, no `gmi.ErrTransient`, 1 POST / 1 GET |
| `TestSlowSubscriberDropOldest` | stream | round-1 core — slow subscriber never blocks publish, newest survives |
| `TestProgressAndTerminalExactlyOnce` | job | round-1 core — progress streams, exactly one terminal |
| `TestPanicBecomesErrorTerminal` | job | round-1 core — panic boundary → error terminal, runner reusable |
| `TestPoll_QueuedProcessingCompleted` | media | round-1 core — queued → processing → completed drives to the body |
| `TestSubmitCompletedNoPoll` | media | T2 net — completed submit passes through, zero GETs |
| `TestRetry_5xxHitTwice`, `TestRetry_FailedStatusThenSuccess`, `TestRetry_FailedStatusBudgetSpent` | media | T2 regression net — one-retry budget intact with `c.post → c.drive` |

## Boundary

This round judged the remediation's four fixes and their freshly created
paths, plus regression over the round-1 surface (the pins above, the
full `-race` suite, the round-1 side observation re-examined). The full
`Done when` was re-opened and closed by round 1; nothing in the
remediation touched its surface outside `job.go`/`polling.go`/`sse.go`
and their tests, which this round read in full. Not judged, by
ownership: live-API truths the operator owns (T2b — the real provider
status vocabulary beyond the live-observed additions, real poll-path
shape, billing of failed/resubmitted requests); T4/T6/T8 request-path
wiring against the 100 s budget and their future use of `Cancel`; T9
front-end conventions; the recorded no-`Stop` design ("one per process")
and unbounded result registry (recorded decisions, unchanged); the
aggregate-vs-isolated media coverage figure drift noted in the gates
table (measurement artifact, floor holds, no behaviour behind it).

## Close

All four round-1 pins are green permanent tests; both contract mutations
were re-applied by hand, observed red for the recorded reasons, and
reverted byte-identical (`cmp`); the new terminal path survived
adversarial probing (4000 raced Cancel-vs-completion interleavings under
`-race`, 20/20 deterministic; concurrent Cancel and Result load clean;
no deadlock path exists); the gates are green. Zero residue against
round 1, claimed severity by severity above. **T3b can close.**
