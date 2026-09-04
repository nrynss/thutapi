# T0 round 2 — adversarial re-review

| | |
|---|---|
| **Target** | T0 code at `5a7e7169802951e6e05fae29d5bf783366beb2b1` plus remediation record at `1b69486`; remediation diff reviewed against `185b37c`. |
| **Evidence** | `dev-diary/adversarial-review/t0-round1.md`, `dev-diary/adversarial-review/t0-remediation-round1.md`, `cmd/thutapi/main.go`, `cmd/thutapi/main_test.go`, `.gitignore`, `go.mod`, `AGENTS.md`, `dev-diary/project.md`, `dev-diary/PLAN.md`, and `dev-diary/adversarial-review/README.md`; targeted Pin/Mutation runs, two temporary reviewer probes removed after use, full Go validation, and a production-binary smoke run. |
| **Method** | Re-probe every round-1 Pin on the current tree, apply its Mutation, require a red result for the named reason, restore, and require green again. Then inspect the remediation diff, exercise the real `main()` path, and attack the new `run(log, args, sigs)` seam. |
| **Date** | 2026-09-04 |

## Verdict

**REMEDIATE — 0 × C, 1 × H, 0 × M, 1 × L.**

H2, H3, M2, and L1 have zero functional residue, and M1 remains explicitly waived. H1 does **not** have zero residue: the new Pin passes while the production defaults still reproduce the original `context deadline exceeded` failure. One new L-severity test-isolation defect was also introduced by the remediation. T0 therefore cannot make the explicit all-findings zero-residue claim required for APPROVE.

| ID | Severity | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| **H1 (re-opened)** | H | `cmd/thutapi/main.go:61-68`, `cmd/thutapi/main_test.go:151-243` | Production has a 10s graceful-shutdown deadline but a 30s `ReadTimeout`. A request accepted just before SIGTERM can therefore remain blocked on its body until after `Shutdown` has already returned `context deadline exceeded`. The standing Pin encodes the wrong ordering (`ReadTimeout > 10s`) and its behavioural half uses the opposite, safe ordering (80ms read timeout, 500ms shutdown timeout), so it passes without protecting production. | Keep a slow-body handler active under `parseConfig()` defaults and call `Shutdown` with `cfg.timeout`. `TestRound2DefaultSlowBodyShutdown` failed after 10.00s with `Shutdown = context deadline exceeded after 10s; default ReadTimeout 30s outlives the graceful-shutdown deadline (H1)`. The temporary reviewer test was removed after the run. | After fixing the defaults and Pin so active-request timeouts expire strictly before the graceful-shutdown deadline, restore `readTimeout: 30 * time.Second` while `timeout` remains 10s. The production-default slow-body Pin must fail with the named ordering/deadline reason. |
| **L2 (new)** | L | `cmd/thutapi/main_test.go:345-367` | `TestShutdownLogRecordsSignalName` calls `run(log, nil, sigs)` without overriding `ADDR`, so it binds the public default `0.0.0.0:8080`. If any process already owns port 8080, the test returns on the listen error before consuming the synthetic signal and fails for an unrelated environmental reason. | Hold `127.0.0.1:8080`, then run `go test ./cmd/thutapi -run TestShutdownLogRecordsSignalName -count=1 -v`. It failed with `listen tcp 0.0.0.0:8080: bind: address already in use` and `no shutdown signal received log line`. | After isolating the test with `t.Setenv("ADDR", "127.0.0.1:0")`, remove that line. Re-running the occupied-port Pin must reproduce the bind error and missing-log failure. |

## Severity key

| Level | Meaning |
|---|---|
| **C** | Breaks the demo. Cannot ship. |
| **H** | Real defect the demo survives. Must fix before the track closes. |
| **M** | Real defect with a workaround. Must fix before the track closes. |
| **L** | Polish / hygiene. Must fix before the track closes. |

No severity is exempt under `dev-diary/adversarial-review/README.md`.

## Per-finding re-verification

### H1 — re-opened; Pin is not load-bearing against production defaults

**Pin:** `TestShutdownDeadlineIsHonouredAgainstSlowBody`

1. **Current commit:**
   ```text
   === RUN   TestShutdownDeadlineIsHonouredAgainstSlowBody
   --- PASS: TestShutdownDeadlineIsHonouredAgainstSlowBody (0.61s)
   PASS
   ok  thutapi/cmd/thutapi  0.615s
   ```
2. **Round-1 Mutation:** deleting `ReadTimeout`, `WriteTimeout`, and `IdleTimeout` from `newHTTPServer` made the Pin fail for its named structural reason:
   ```text
   main_test.go:164: newHTTPServer.ReadTimeout = 0s; want > 10s (H1 fix sets it to 30s)
   --- FAIL: TestShutdownDeadlineIsHonouredAgainstSlowBody (0.00s)
   ```
3. **After reverting the Mutation:** the Pin passed again in 0.61s.
4. **Adversarial re-probe of the actual defaults:** a temporary `TestRound2DefaultSlowBodyShutdown` used `parseConfig()` unchanged, held a request body open, and called `Shutdown` with the configured 10s deadline:
   ```text
   t0_round2_probe_test.go:50: Shutdown = context deadline exceeded after 10s; default ReadTimeout 30s outlives the graceful-shutdown deadline (H1)
   --- FAIL: TestRound2DefaultSlowBodyShutdown (10.00s)
   ```
   A separate structural probe failed immediately with `newHTTPServer ReadTimeout = 30s; want less than shutdown timeout 10s`.

**Verdict for H1: re-opened.** The Mutation makes the existing test red, but the test is not load-bearing against the original defect. Its production assertion requires the unsafe inequality `30s > 10s`, while its behavioural setup quietly changes that to the safe inequality `80ms < 500ms`. The current defaults still take the original slow-body error path.

### H2 — closed, zero residue

**Pin:** `go build ./cmd/thutapi && git status --short && git check-ignore thutapi`

1. **Current commit:** build succeeded; `git status --short` had no `?? thutapi`; `git check-ignore thutapi` printed `thutapi` and exited 0.
2. **Mutation:** deleting `/thutapi` from `.gitignore` made the same fresh build show:
   ```text
    M .gitignore
    M cmd/thutapi/main.go
   ?? thutapi
   ```
   (`cmd/thutapi/main.go` was concurrently carrying the other requested Mutations.) `git check-ignore thutapi` printed nothing and the command exited 1.
3. **After reverting the Mutation:** status again had no `thutapi` row; `git check-ignore` printed `thutapi` and exited 0.

**Verdict for H2: closed (zero residue).** The build artifact is ignored only while the new top-level rule exists, and the shell Pin catches its removal in both required ways.

### H3 — closed, zero residue

**Pin:** `TestParseFlagsRejectsNonPositiveTimeout`

1. **Current commit:** both `zero` and `negative` subtests passed; parent passed in 0.00s.
2. **Mutation:** deleting the `cfg.timeout <= 0` validation produced the named failures for both values:
   ```text
   main_test.go:288: parseFlags(-shutdown-timeout=0s) returned nil error; want errShutdownTimeout
   main_test.go:288: parseFlags(-shutdown-timeout=-5s) returned nil error; want errShutdownTimeout
   --- FAIL: TestParseFlagsRejectsNonPositiveTimeout (0.00s)
   ```
3. **After reverting the Mutation:** both subtests and the parent passed again in 0.00s.

**Verdict for H3: closed (zero residue).** Both forbidden boundary classes are rejected with the typed error, and the Pin goes red when that branch is absent.

### M2 — closed, zero residue

**Pin:** `TestParseFlagsAfterOperandIsNotSilentlyDropped`

1. **Current commit:** passed in 0.00s.
2. **Mutation:** deleting the `fs.NArg() != 0` branch produced the named failure:
   ```text
   main_test.go:313: parseFlags(unexpected -shutdown-timeout=1s) returned nil error; trailing flag is being silently dropped (M2)
   --- FAIL: TestParseFlagsAfterOperandIsNotSilentlyDropped (0.00s)
   ```
3. **After reverting the Mutation:** passed again in 0.00s.

**Verdict for M2: closed (zero residue).** The operand path reaches `errUnexpectedOperand`, and deleting the dispatch branch is detected.

### L1 — closed functionally, zero behavioural residue

**Pin:** `TestShutdownLogRecordsSignalName`

1. **Current commit:** passed in 0.02s and observed `signal="terminated"`.
2. **Literal remediation-record Mutation:** replacing only `sig.String()` with `context.Canceled.Error()` did not reach the named assertion because Go correctly rejected the now-unused receive binding:
   ```text
   cmd/thutapi/main.go:162:7: declared and not used: sig
   FAIL  thutapi/cmd/thutapi [build failed]
   ```
3. **Complete round-1 “revert the fix” Mutation:** changing the receive to `case <-sigs:` as well as logging `context.Canceled.Error()` compiled and produced the required named failure:
   ```text
   main_test.go:381: shutdown log signal field = "context canceled"; want "terminated" (L1 fix logs sig.String(), not ctx.Err().Error())
   --- FAIL: TestShutdownLogRecordsSignalName (0.02s)
   ```
4. **After reverting the Mutation:** passed again in 0.02s.

**Verdict for L1: closed (zero functional residue).** The production dispatch receives the concrete `os.Signal`, forwards its string form to the log, and the compilable old-behaviour Mutation is caught for the named reason. The remediation record's one-expression Mutation description is incomplete and should describe both changed lines when the next remediation record summarizes this round.

### M1 — waiver confirmed

**Pin/Mutation:** none; this finding was expressly waived by the user on 2026-09-04.

* `go.mod:3` is `go 1.27.1`.
* `AGENTS.md:57` says `Go 1.27.1`.
* `dev-diary/project.md:251` says `Go 1.27.1`.

**Verdict for M1: waived as recorded.** The module and both authoritative documents agree; no remediation or mutation is outstanding.

## What held up under attack

* `go vet ./...` exited 0 with no output.
* `go test ./... -count=1` passed: `ok thutapi/cmd/thutapi 0.639s`.
* `go test ./... -race` passed: `ok thutapi/cmd/thutapi 1.657s`.
* `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/thutapi` exited 0 with no output.
* The actual built binary was launched with `ADDR=127.0.0.1:18084`. `GET /healthz` returned HTTP 200, `Cache-Control: no-store`, `Content-Type: application/json`, and `{"status":"ok",...}`. A real SIGTERM logged `signal":"terminated"`, logged `thutapi stopped cleanly`, and the process exited 0. This confirms `main()` still registers and forwards the production signal channel into `run` correctly on the normal path.
* Bind failure still propagates out of `run` to `main` as a non-nil error; clean `http.ErrServerClosed` remains filtered by the existing dispatch branch.
* H2, H3, M2, and the functional L1 behaviour each survived their exact or complete old-behaviour Mutation and restoration cycles.

The first simultaneous launch of the normal and race suites exposed their shared port-8080 dependency: the normal suite failed in `TestShutdownLogRecordsSignalName` while the race suite held the port. Sequential reruns passed. A dedicated occupied-port probe reproduced the same failure deterministically; that is L2, not a full-suite product failure.

## New findings

### L2 — signal-log Pin binds the production port

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `cmd/thutapi/main_test.go:345-367` (`TestShutdownLogRecordsSignalName`). |
| **What** | The test does not set `ADDR`, so `run()` binds `0.0.0.0:8080`. With port 8080 occupied, `run` returns the listen error before it consumes the synthetic SIGTERM and the Pin fails without testing signal logging. This is a deterministic environmental dependency in a newly added test. |
| **Pin** | Hold `127.0.0.1:8080`, then run `go test ./cmd/thutapi -run TestShutdownLogRecordsSignalName -count=1 -v`; require the current test to fail with `bind: address already in use` and `no shutdown signal received log line`. |
| **Mutation** | After adding `t.Setenv("ADDR", "127.0.0.1:0")` to isolate the Pin on an ephemeral port, remove that line. The occupied-port Pin must fail again for the two named reasons. |

No other new resource leak, broken dispatch path, missing error path, or production `main()` regression was found in the `185b37c..5a7e716` diff.

## Done-when audit — `dev-diary/PLAN.md` §T0

| Criterion | Met? | Evidence |
|---|:---:|---|
| `go build ./...` / command build is green | **Yes** | Default build passed in the H2 cycle; the required static `CGO_ENABLED=0` build also passed. |
| `./thutapi` serves `/healthz` | **Yes** | Actual production binary returned HTTP 200 JSON on `127.0.0.1:18084`. |
| T0 review loop reaches APPROVE with zero residue | **No** | H1 is re-opened and L2 is new; this round is REMEDIATE. |

The direct runtime conditions in PLAN §T0 pass, but the binding repository definition of done in `AGENTS.md` also requires an APPROVE round with zero residue. T0 therefore remains open.

## Go-signal matrix

| Downstream | Unblocked? | Condition |
|---|:---:|---|
| **T1 — deployment path** | **Conditional** | H1 still permits SIGTERM during a newly accepted slow body to exceed the graceful-shutdown deadline and exit 1. T1 becomes unconditional only after the corrected production-default Pin passes its Mutation cycle. |
| **T2 — GMI clients** | **Yes** | The HTTP handler seam and flag parser remain usable; neither open finding changes the client contract. |
| **T3 — store and media** | **Yes** | No store/media boundary is affected. |
| **T4 — interview loop** | **Conditional** | T4 introduces body-reading endpoints, which are the concrete path H1's current timeout ordering fails to drain on SIGTERM. |

T1 is not unconditional because zero residue was not achieved.

## Recommended actions

1. **H1:** make the active-request timeout ordering safe under the real defaults: in particular, `ReadTimeout` must expire strictly before the graceful-shutdown context. Rewrite `TestShutdownDeadlineIsHonouredAgainstSlowBody` to exercise `parseConfig()`'s production relationship (or a uniformly scaled version preserving that relationship), and require the unsafe ordering Mutation to fail for `context deadline exceeded`.
2. **L2:** add `t.Setenv("ADDR", "127.0.0.1:0")` before `run()` in `TestShutdownLogRecordsSignalName`, then rerun the occupied-port Pin in both directions.
3. In the next remediation record, describe L1's old-behaviour Mutation as the two-line receive-and-log change; replacing only the expression is not a compilable Mutation.
