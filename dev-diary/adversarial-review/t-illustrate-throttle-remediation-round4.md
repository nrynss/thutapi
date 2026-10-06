# T-IllustrateThrottle remediation, round 4

| | |
|---|---|
| **Remediates** | `t-illustrate-throttle-round4.md` (REMEDIATE, 0C / 0H / 1M / 3L) |
| **Agent** | Remediation Agent, round 4 (fresh; did not implement, review, or do rounds 1-3 remediation). 2026-10-06. |
| **Scope** | `internal/illustrate/{throttle.go,pacer_test.go}` (incl. the `vclock` driver); `dev-diary/PLAN.md` (status row and section heading). Nothing committed, nothing deployed, no live or paid call, review files and verdict untouched. |

## Findings

| # | Sev | Change | Where | Pinning test | Mutation |
|---|---|---|---|---|---|
| M1 | M | `pacer.reserve` no longer takes `now` from the caller: it locks, reads `p.now()` and reserves in one critical section, and returns `(slot, now)`; `wait` uses both for its sleep duration. Callers therefore reserve in the order they sampled the clock, and the pruning never sees a time older than one a previous caller used. The `vclock` doc comment is rewritten: the old claim ("waking a sleeper late can only make requests sparser, never denser") was wrong, and the new text states what is guaranteed (slots handed out in clock order) and what is not (the driver's quiet heuristic can leave a woken goroutine behind, see the load note below). `TestPacer_NoTwoSlotsCloserThanTheInterval` follows the new `reserve` signature. | `throttle.go` (`wait`, `reserve`); `pacer_test.go` | New `TestPacer_ReadsItsClockUnderItsLock`: the injected `Now` calls `p.mu.TryLock()`; success means the caller did not hold the lock, and any such read fails the test. Covers `wait` (free and queued slots) and `release`. | In `reserve`, move `now = p.now()` above `p.mu.Lock()` (scratch copy): `TestPacer_ReadsItsClockUnderItsLock` **red**, and only it among `TestPacer_*`. |
| L1 | L | Test only. | `pacer_test.go` | New `TestPacer_AnAlreadyCancelledContextGetsNoSlot`: cancelled ctx on a fresh pacer returns `context.Canceled` (`errors.Is`), `slotCount() == 0`, `Sleep` never called, and the next live caller goes at once. | Delete the `ctx.Err()` pre-check in `wait` (scratch copy): **red** (the free slot is taken and `wait` returns nil). |
| L2 | L | Test only. | `pacer_test.go` | New `TestPacer_ExpiredSlotsArePruned`: 1,000 waits, each one interval after the last, assert `slotCount() <= 2` after every one; then a 10-minute quiet spell and one wait leaves exactly one slot. | Replace the pruning condition in `reserve` with `true` (scratch copy): **red**. |
| L3 | L | PLAN status row and section heading now say: round 3 REMEDIATE 0C/0H/3M/1L remediated in `t-illustrate-throttle-remediation-round3.md`; round 4 REMEDIATE 0C/0H/1M/3L remediated in this file; status **IN PROGRESS**, awaiting a fresh round-5 review; not committed. | `dev-diary/PLAN.md` | `grep -n "round-3 review" dev-diary/PLAN.md` is empty; `grep -n "round-5" dev-diary/PLAN.md` matches the row and the heading. | Restore the old sentences: the first grep matches again. |

## Load reproduction (M1), honestly

The round-4 reviewer's recipe: 24 `timeout`-bounded busy loops on 12 cores, `go test -race -run TestIllustrate_ConcurrentBooksShareOneRateWindow -test.cpu 1,3 -test.count=15`.

1. **Moving the clock read under the lock was not enough.** With it in place, the pin still failed on load (1 of 3 runs twice; 3 of 3 on a debug build) and once in a quiet full `go test ./... -race` (n=3 seed=4 each time). Printed accepted times showed the pacer's slots a clean 31.5 s apart but one accepted submission 12-19 s *off* its slot grid, late. Cause: the test's virtual clock (`vclock.drive`) advanced after a few quiet milliseconds even while a woken goroutine had not yet been scheduled, so its submission reached the window late and too close to its neighbour. The `vclock` comment I had first written ("late can only make requests sparser") was therefore wrong; the final comment says so.
2. **Harness fix, in the same seam, needed for the pin to be a stable gate:** `vclock.drive` now also requires that no other goroutine is running or runnable, read from a stop-the-world `runtime.Stack(all)` dump (`otherGoroutinesRunnable`, `pacer_test.go`). Woken goroutines are runnable until they park again, so virtual time cannot jump past one.
3. **Result under the same load** (24 hogs, `-race`, `-test.cpu 1,3 -test.count=15`, three runs, pin plus the negative control): `TestIllustrate_ConcurrentBooksShareOneRateWindow` **passed in all three runs** (90 executions). Run 3 failed on a different test, see below.

**Residual, not fixed (pre-existing, outside round 4's findings): `TestIllustrate_TheSimulationFailsWithoutPacing`** requires at least 6 of 12 unpaced seeds to exhaust their retries. Quiet-machine rate is 7-12 of 12 (mean about 9.5, identical with the old and new driver, so inherent: the backoff jitter uses the global RNG), but under load it dropped to 4-5 of 12 (it failed once in a loaded `go test ./...` and once in the loaded loop above). It is a negative control, so a flake does not hide a bug, but it can redden CI. A fix (seed the jitter, or lower the threshold, or count more seeds) is for the orchestrator or round-5 reviewer to assign.

## Gates

Recorded in the report that accompanies this file; the file cannot attest to runs made after it is written.
