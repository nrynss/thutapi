# T-IllustrateThrottle remediation, round 5

| | |
|---|---|
| **Remediates** | `t-illustrate-throttle-round5.md` (REMEDIATE, 0C / 0H / 2M / 2L) |
| **Agent** | Remediation Agent, round 5 (fresh; did not implement, review, or do rounds 1-4 remediation). 2026-10-06. |
| **Scope** | `internal/illustrate/{throttle.go,pacer_test.go,throttle_test.go}`; `dev-diary/PLAN.md` (Contract row C1 heading, status row). Nothing committed, nothing deployed, no live or paid call, review files and verdict untouched. |

## Findings

| # | Sev | Change | Where | Pinning test | Mutation |
|---|---|---|---|---|---|
| M1 | M | The only chance in the simulation is retry jitter, which drew from the global `math/rand/v2` source. `ThrottleConfig` gains an unexported `randN func(n int64) int64` (nil means `rand.Int64N`, set in `withDefaults`); `jitter` and the `retry_after_ms` spread draw through it. `simulate` takes a `randN` argument. `TestIllustrate_TheSimulationFailsWithoutPacing` now runs every one of the 12 seeds with a fixed zero draw and asserts that **all 12** unpaced runs fail with `gmi.ErrRateLimited` (the old `>= seeds/2` margin on a random count is gone), and, as the paired control, that the same 12 seeds **paced**, with the same fixed jitter, all succeed. The comment no longer claims "472 of 500" or "7-12 on a quiet machine"; it says what the test measures. The positive pin and the concurrent-books pins keep the real jitter source (their pass condition does not depend on it). | `throttle.go` (`ThrottleConfig.randN`, `withDefaults`, `retryThrottled`, `jitter`); `pacer_test.go` (`simulate`, the control) | `TestIllustrate_TheSimulationFailsWithoutPacing` (deterministic: 12 of 12 unpaced fail, 12 of 12 paced pass). Stability runs are in the section below. | On a scratch copy, `p.interval = 0 * base` in `newPacer` (pacing off everywhere): the paired paced half goes **red** for the seeds ("paced run with the same fixed jitter failed ... still rate limited after 6 attempts"). |
| M2 | M | `productionDefaultSleep = defaultSleep` is captured by a package-level initialiser, which runs before `TestMain` replaces `defaultSleep` with the instant stub, so the test sees the value the production binary starts with. `TestDefaultSleep_ProductionDefaultReallyWaits` calls it with 60 ms and asserts at least 60 ms elapsed, then with a cancelled context and an hour and asserts `errors.Is(err, context.Canceled)`. Cost: 60 ms. | `throttle_test.go` | `TestDefaultSleep_ProductionDefaultReallyWaits` | On a scratch copy, `var defaultSleep = func(ctx context.Context, d time.Duration) error { return nil }`: **red** ("returned after 4.37µs: it does not wait" and "cancelled ctx = <nil>"). Reviewer's identical mutation was green before this change. |
| L1 | L | Pacer doc now guarantees only that the slots of real submissions are never closer than the interval, and says the guarantee is about slots, not the instants of submission (the 5% margin absorbs a late wake-up). Registry doc now says a pacer holds a slice of the slots still relevant, and that its mutex covers one read of the injected clock plus the slot arithmetic, never a sleep. | `throttle.go` (docs of `type pacer`, `type pacerRegistry`) | Doc only; read against `reserve`, `release`, `pacer.slots`. | Restore the old sentences. |
| L2 | L | Contract row C1 heading is now "orchestrator-made widening of `Owns` onto `internal/bookgen` ... operator ratification pending", consistent with its own "Sanction: none recorded" sentence. The T-IllustrateThrottle status row stays **IN PROGRESS** and lists round 5 (REMEDIATE 0C/0H/2M/2L), remediation 5 (this file), and a pending round-6 review. | `dev-diary/PLAN.md` | `grep -n "sanctioned widening" dev-diary/PLAN.md` is empty; `grep -n "round-6" dev-diary/PLAN.md` matches the status row. | Restore the old heading. |

## Stability runs (M1)

See the report that accompanies this file for the counts; the file cannot attest to runs made after it is written. Method: `go test -race -c`, then the binary with `-test.count`, bounded hogs only (`timeout N sh -c 'while :; do :; done' &`, no trailing kill), run in the foreground under `timeout`.

## Residual, stated plainly

`TestIllustrate_ConcurrentBooksFailWithAPacerPerRun` (4 concurrent books with a private pacer each, threshold 3 of 6 seeds) was not a round-5 finding and is unchanged. It also depends on retry jitter and goroutine order; its stability is measured in the accompanying report rather than assumed.
