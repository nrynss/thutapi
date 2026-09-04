# T0 round 1 — remediation

| | |
|---|---|
| **Target** | Five findings from `t0-round1.md` open at the start of remediation (H1, H2, H3, M2, L1). H4 closed in `185b37c`. **M1 waived by user in-session** (Go 1.27.1 stays; no remediation row, no go.mod edit). |
| **Date** | 2026-09-04 |
| **Commit** | `5a7e7169802951e6e05fae29d5bf783366beb2b1` — single focused commit covering all five remediations; the four go-code fixes share one refactor (a `newHTTPServer` helper and a `run(log, args, sigs)` test seam) so they cannot be split without the suite failing mid-flight. |
| **Round file** | `dev-diary/adversarial-review/t0-round1.md` is unchanged. The M1 waiver note + spec alignment were committed in `a52dd4a` by the parent agent prior to remediation; that is not part of this row set. |
| **Verification methodology** | For each finding: (a) Pin test passes on the fixed code; (b) applying the row's Mutation makes the Pin test fail with the reason named in its `t.Fatalf`; (c) reverting the Mutation makes the Pin pass again. Both directions are recorded verbatim below. The shell-pin for H2 is the same shape on a fresh `go build ./cmd/thutapi`: `git check-ignore thutapi` exits 0 with the fix and exits 1 with the Mutation; `git status --short` shows no `?? thutapi` row with the fix and shows one with the Mutation. |

## Rows

### H1

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `cmd/thutapi/main.go` — `newHTTPServer(cfg, h)` (formerly inline `http.Server{...}` literal in `main()`). |
| **What** | `newHTTPServer` now sets `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s` from cfg defaults. Before the fix all three were Go's zero value (no timeout); a slow request body held past the shutdown deadline, `srv.Shutdown` returned `context.DeadlineExceeded`, and the process exited 1 — which the Hetzner Traefik orchestrator treats as unhealthy and restart-loops on SIGTERM during deploy. Values are read from cfg (`parseConfig` defaults to 30/30/120) so the Pin test can shorten them and observe the cancellation end-to-end. Each timeout exceeds the default 10s shutdown deadline so a slow request is force-closed before the deadline fires. |
| **Pin** | `TestShutdownDeadlineIsHonouredAgainstSlowBody` in `cmd/thutapi/main_test.go`. |
| **Mutation** | Delete the three timeout fields from the `http.Server` literal inside `newHTTPServer`; leave only `ReadHeaderTimeout: 5 * time.Second`. |
| **Commit SHA** | `5a7e7169802951e6e05fae29d5bf783366beb2b1` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestShutdownDeadlineIsHonouredAgainstSlowBody -count=1 -v -timeout 30s`: `--- PASS: TestShutdownDeadlineIsHonouredAgainstSlowBody (0.61s)` |
| **Verification (Pin on Mutation)** | `go test ./cmd/thutapi -run TestShutdownDeadlineIsHonouredAgainstSlowBody -count=1 -v -timeout 30s`: `main_test.go:164: newHTTPServer.ReadTimeout = 0s; want > 10s (H1 fix sets it to 30s)` then `--- FAIL: TestShutdownDeadlineIsHonouredAgainstSlowBody (0.00s)` |

### H2

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `.gitignore` (top-level). |
| **What** | Added `/thutapi` to the `# Build output` block. `go build ./cmd/thutapi` writes `thutapi` to the working directory by default; without the rule, `git status` shows `?? thutapi` and a judge cloning the repo could either commit the binary or spend time wondering if the tree is dirty. The repo is going public for the judging window, so this matters. |
| **Pin** | Shell probe — `git check-ignore thutapi` exits 0 after `go build ./cmd/thutapi`, and `git status --short` shows no `?? thutapi` row. |
| **Mutation** | Drop the `/thutapi` line from `.gitignore`. |
| **Commit SHA** | `5a7e7169802951e6e05fae29d5bf783366beb2b1` |
| **Verification (Pin on fixed)** | `go build ./cmd/thutapi && git status --short && git check-ignore thutapi; echo "exit=$?"` — `git status --short` is empty (no `?? thutapi`); `git check-ignore thutapi` prints `thutapi` and exits 0. |
| **Verification (Pin on Mutation)** | `go build ./cmd/thutapi && git status --short && git check-ignore thutapi; echo "exit=$?"` — `git status --short` shows `?? thutapi`; `git check-ignore thutapi` exits 1 with no output. |

### H3

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `cmd/thutapi/main.go` — `parseFlags`. |
| **What** | `parseFlags` rejects `cfg.timeout <= 0` with the typed `errShutdownTimeout`. `flag.DurationVar` happily parses `-shutdown-timeout=0s` and `-shutdown-timeout=-5s`; without the validation, `srv.Shutdown` is given a context that fires immediately, every SIGTERM logs `graceful shutdown failed` and exits 1. The error is exported so callers and tests can `errors.Is` against it. |
| **Pin** | `TestParseFlagsRejectsNonPositiveTimeout` in `cmd/thutapi/main_test.go` — two subtests, `zero` and `negative`. |
| **Mutation** | Drop the `if cfg.timeout <= 0 { return cfg, errShutdownTimeout }` block from `parseFlags`. |
| **Commit SHA** | `5a7e7169802951e6e05fae29d5bf783366beb2b1` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestParseFlagsRejectsNonPositiveTimeout -count=1 -v`: `--- PASS: TestParseFlagsRejectsNonPositiveTimeout/zero`, `--- PASS: TestParseFlagsRejectsNonPositiveTimeout/negative`, parent `--- PASS` in 0.00s. |
| **Verification (Pin on Mutation)** | `go test ./cmd/thutapi -run TestParseFlagsRejectsNonPositiveTimeout -count=1 -v`: `main_test.go:288: parseFlags(-shutdown-timeout=0s) returned nil error; want errShutdownTimeout` and the negative counterpart; parent `--- FAIL` in 0.00s. |

### M2

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `cmd/thutapi/main.go` — `parseFlags`. |
| **What** | `parseFlags` now returns `errUnexpectedOperand` when `fs.NArg() != 0` after parsing. `flag.NewFlagSet` stops at the first non-flag token, so a binary invoked as `thutapi unexpected -shutdown-timeout=1s` silently kept the default 10s timeout and dropped the trailing flag — a footgun for anyone scripting the binary. The error is exported for `errors.Is`. |
| **Pin** | `TestParseFlagsAfterOperandIsNotSilentlyDropped` in `cmd/thutapi/main_test.go`. |
| **Mutation** | Drop the `if fs.NArg() != 0 { return cfg, errUnexpectedOperand }` block from `parseFlags`. |
| **Commit SHA** | `5a7e7169802951e6e05fae29d5bf783366beb2b1` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestParseFlagsAfterOperandIsNotSilentlyDropped -count=1 -v`: `--- PASS: TestParseFlagsAfterOperandIsNotSilentlyDropped (0.00s)`. |
| **Verification (Pin on Mutation)** | `go test ./cmd/thutapi -run TestParseFlagsAfterOperandIsNotSilentlyDropped -count=1 -v`: `main_test.go:313: parseFlags(unexpected -shutdown-timeout=1s) returned nil error; trailing flag is being silently dropped (M2)` then `--- FAIL` in 0.00s. |

### L1

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `cmd/thutapi/main.go` — `run(log, args, sigs)`; the `case sig := <-sigs` branch. |
| **What** | `run()` accepts a typed `<-chan os.Signal` and logs `sig.String()` (e.g. `"terminated"`, `"interrupt"`) on the `shutdown signal received` line. Before the fix, the code logged `ctx.Err().Error()` which printed `"context canceled"` — the operator signal name was invisible in the log. `main()` registers `signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)` and passes `sigs` to `run()`; tests can inject a synthetic signal source directly into the same channel without spawning the real process. |
| **Pin** | `TestShutdownLogRecordsSignalName` in `cmd/thutapi/main_test.go`. |
| **Mutation** | Replace `sig.String()` with `context.Canceled.Error()` in the `shutdown signal received` log line. |
| **Commit SHA** | `5a7e7169802951e6e05fae29d5bf783366beb2b1` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestShutdownLogRecordsSignalName -count=1 -v -timeout 10s`: `--- PASS: TestShutdownLogRecordsSignalName (0.02s)`. |
| **Verification (Pin on Mutation)** | `go test ./cmd/thutapi -run TestShutdownLogRecordsSignalName -count=1 -v -timeout 10s`: `main_test.go:381: shutdown log signal field = "context canceled"; want "terminated" (L1 fix logs sig.String(), not ctx.Err().Error())` then `--- FAIL` in 0.02s. |

### M1 (waived)

| Field | Value |
|---|---|
| **Severity** | M |
| **Status** | **Waived by user, 2026-09-04.** `go.mod` keeps `go 1.27.1`; `AGENTS.md` and `dev-diary/project.md` were aligned to 1.27.1 in commit `a52dd4a` (parent agent). The round file records the waiver as an audit-trail note. No row in the Pin/Mutation form; nothing to verify. |

## Aggregate verification (after the commit lands)

```
$ go vet ./...
$ go test ./... -count=1 -timeout 30s
ok      thutapi/cmd/thutapi  0.638s
$ go test ./... -count=1 -race -timeout 60s
ok      thutapi/cmd/thutapi  1.655s
$ go build ./cmd/thutapi 2>&1 && git check-ignore thutapi; echo "exit=$?"
thutapi
exit=0
```

12 tests pass under both `-count=1` and `-race` (8 pre-existing + 4 Pin tests). The H2 shell pin is clean. The combined commit leaves a clean working tree.

## Files changed in `5a7e716`

* `cmd/thutapi/main.go` — +86 / -28: `cfg` gains `readTimeout/writeTimeout/idleTimeout`; `parseConfig` defaults them to 30/30/120s; `parseFlags` rejects non-positive timeout and trailing positional operand with typed errors; `newHTTPServer` helper sets the three timeouts from cfg; `run(log, args, sigs)` test seam logs `sig.String()`; `main()` registers the signal channel and delegates to `run`.
* `cmd/thutapi/main_test.go` — +268: four Pin tests with their own Pin/Mutation comments; `isTimeout` helper for the H1 behavioural assertion.
* `.gitignore` — +1: `/thutapi` rule.

The Pin tests do not duplicate any pre-existing assertion in `cmd/thutapi/main_test.go`; the closest pre-existing test (`TestParseFlagsBadFlagReturnsError`) covers flag-parse errors from `flag.Parse`, not the typed errors this remediation introduces. No pre-existing test was modified, removed, or weakened.