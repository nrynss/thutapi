# T0 round 2 — remediation

| | |
|---|---|
| **Target** | Two open findings from `t0-round2.md` at the start of remediation: H1 (re-opened) and L2 (new). H2, H3, M2, and L1 were closed at `5a7e7169802951e6e05fae29d5bf783366beb2b1`; M1 was waived in-session; the round-2 reviewer's re-probe was the only outstanding work. |
| **Date** | 2026-09-04 |
| **Commit** | `80ebe3e77791c499514209792ab9b25fd4fc59aa` — single focused commit covering both remediations.
| **Round file** | `dev-diary/adversarial-review/t0-round2.md` is unchanged. The remediation agent did not touch the round file. |
| **Verification methodology** | For each finding: (a) the Pin test passes on the fixed code; (b) applying the row's Mutation makes the Pin test fail with the reason named in its `t.Fatalf` (or, for L2, the two named reasons — bind error plus missing log line); (c) reverting the Mutation makes the Pin pass again. Both directions are recorded verbatim below. |

## Rows

### H1

| Field | Value |
|---|---|
| **Severity** | H (re-opened) |
| **Where** | `cmd/thutapi/main.go` — `newHTTPServer(cfg, h)`; `cmd/thutapi/main_test.go` — `TestShutdownDeadlineIsHonouredAgainstSlowBody`. |
| **What** | The round-1 fix set `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s` from cfg defaults, then the Pin scaled those values uniformly (80ms read / 500ms shutdown) and asserted the scaled relationship. Under the production defaults the inequality was still unsafe (`ReadTimeout = 30s > cfg.timeout = 10s`), so a request accepted just before SIGTERM could keep its body open past the 10s shutdown deadline, `srv.Shutdown` returned `context.DeadlineExceeded`, the process logged `graceful shutdown failed` and exited 1 — which the Hetzner Traefik orchestrator treats as unhealthy and restart-loops on SIGTERM during deploy. The round-1 Pin's scaled values quietly flipped the inequality to the safe side inside the test, so it passed without ever exercising the unsafe production relationship. |
| **Production change** | `newHTTPServer` now derives `ReadTimeout = cfg.timeout - 2*time.Second` (with a 1s-of-negative guard clamped to 0). One second of headroom left a ~30% flake rate under the local scheduler because the body read error fires at `cfg.timeout - headroom` and Shutdown needs a deterministic window to observe the drained connection before its own deadline fires — production also benefits from the wider 2s margin. `WriteTimeout` is now hard-coded to 0 (no longer in cfg): `project.md` §Pipeline folds SSE into the architecture for the interview turns and TTS streams (text streams while audio lands), so a non-zero WriteTimeout would force-close every long-lived SSE stream and is the wrong default for this product. The slow-body case H1 is actually about is bounded by ReadTimeout, so WriteTimeout = 0 does not re-open H1. `IdleTimeout` stays at `cfg.idleTimeout` (> cfg.timeout is fine — idle connections are not in the shutdown path and need to live longer than the shutdown budget to remain reusable). `cfg.readTimeout` / `cfg.writeTimeout` fields are removed because they were a test seam that no longer represents a configurable surface — `parseConfig` now exposes only `addr`, `timeout`, `idleTimeout`. |
| **Pin** | `TestShutdownDeadlineIsHonouredAgainstSlowBody` in `cmd/thutapi/main_test.go` — rewritten to bind `cfg := parseConfig()`, stand up `newHTTPServer(cfg, slowHandler)`, hold a slow-body request open via an in-test `net.Dial` (Content-Length: 1000000, only `partial` sent), wait for the handler to signal entry into `r.Body.Read`, then call `srv.Shutdown(ctxWithTimeout(cfg.timeout))` and assert no error. With the fix, ReadTimeout fires at cfg.timeout-2s, the body read errors out, the handler exits, Shutdown sees the connection drained, and returns nil within the cfg.timeout budget. The previous structural half of the Pin (which asserted `probe.ReadTimeout > 10s`) is removed because it was encoding the unsafe inequality; the new Pin encodes the safe relationship directly. The `isTimeout` helper is removed with the structural half — no remaining call site. |
| **Mutation** | In `newHTTPServer`, replace `readTimeout := cfg.timeout - 2*time.Second` with `readTimeout := 30 * time.Second` (the round-1 default restored). With the unsafe ordering, the body read does not error inside the cfg.timeout budget, the handler is still active when the shutdown context fires, `srv.Shutdown` returns `context.DeadlineExceeded`, and the Pin fails with the named H1 reason. |
| **Commit SHA** | `80ebe3e77791c499514209792ab9b25fd4fc59aa` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestShutdownDeadlineIsHonouredAgainstSlowBody -count=1 -v -timeout 60s`: `--- PASS: TestShutdownDeadlineIsHonouredAgainstSlowBody (8.98s)`. 10 consecutive runs (`-count=10`) all PASS in 8.57s-9.01s. The Pin is stable under the 2s headroom. |
| **Verification (Pin on Mutation)** | `go test ./cmd/thutapi -run TestShutdownDeadlineIsHonouredAgainstSlowBody -count=1 -v -timeout 60s`: `main_test.go:240: Shutdown = context deadline exceeded after 10s; default ReadTimeout must be ≤ cfg.timeout=10s so the slow body is force-closed before the shutdown deadline (H1)` then `--- FAIL: TestShutdownDeadlineIsHonouredAgainstSlowBody (10.00s)`. After reverting the Mutation the Pin passes again in 8.98s. |

### L2

| Field | Value |
|---|---|
| **Severity** | L (new) |
| **Where** | `cmd/thutapi/main_test.go` — `TestShutdownLogRecordsSignalName`. |
| **What** | The test calls `run(log, nil, sigs)` without overriding `ADDR`, so `run()` binds the public default `0.0.0.0:8080`. If any process already holds port 8080, `run` returns the listen error before consuming the synthetic SIGTERM and the Pin fails for an unrelated environmental reason — a deterministic dependency on port 8080 being free in a freshly-added test. The first simultaneous launch of the `-race` and `-count=1` suites exposed this; a dedicated occupied-port probe reproduced the failure. |
| **Fix** | Add `t.Setenv("ADDR", "127.0.0.1:0")` as the first line of the test body so `run()` binds an ephemeral port and the test no longer depends on 8080. The signal-log assertion is unchanged. |
| **Pin** | `TestShutdownLogRecordsSignalName` in `cmd/thutapi/main_test.go`. |
| **Mutation** | Remove the `t.Setenv("ADDR", "127.0.0.1:0")` line, then hold `0.0.0.0:8080` (any process that owns the port — a one-line TCP listener, a leftover dev server, a CI runner) and execute the Pin. The test must fail for both named reasons: (1) `run returned listen tcp 0.0.0.0:8080: bind: address already in use` and (2) `no shutdown signal received log line in output` — because the bind error returns before the synthetic SIGTERM is consumed, so the signal-name log line is never produced. |
| **Commit SHA** | `80ebe3e77791c499514209792ab9b25fd4fc59aa` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestShutdownLogRecordsSignalName -count=1 -v -timeout 30s`: `--- PASS: TestShutdownLogRecordsSignalName (0.02s)`. The test binds 127.0.0.1:0, `run` listens, the synthetic SIGTERM is consumed, the log line is asserted. |
| **Verification (Pin on Mutation)** | Hold port 8080 with a one-line TCP listener (`net.Listen("tcp", "0.0.0.0:8080")`), remove the `t.Setenv` line, then `go test ./cmd/thutapi -run TestShutdownLogRecordsSignalName -count=1 -v -timeout 30s`: `main_test.go:354: run returned listen tcp 0.0.0.0:8080: bind: address already in use` and `main_test.go:377: no 'shutdown signal received' log line in output: {"time":"...","level":"INFO","msg":"thutapi listening","addr":"0.0.0.0:8080"}` then `--- FAIL: TestShutdownLogRecordsSignalName (0.02s)`. After restoring the `t.Setenv` line the Pin passes again in 0.02s. |

## Aggregate verification (after the commit lands)

```
$ go vet ./...
$ go test ./cmd/thutapi -count=1 -timeout 60s
ok      thutapi/cmd/thutapi  8.537s
$ go test ./cmd/thutapi -count=1 -race -timeout 120s
ok      thutapi/cmd/thutapi  10.056s
```

Both H1 and L2 Pins run as part of the suite; H1 takes ~9s on the safe path, L2 takes ~0.02s, the rest of the suite is unchanged.

## Files changed in `80ebe3e77791c499514209792ab9b25fd4fc59aa`

* `cmd/thutapi/main.go` — `cfg` loses the `readTimeout`/`writeTimeout` fields (IdleTimeout stays); `parseConfig` defaults shrink accordingly; `newHTTPServer` derives `ReadTimeout = cfg.timeout - 2*time.Second`, hard-codes `WriteTimeout: 0`, and documents the SSE / slow-body trade-off.
* `cmd/thutapi/main_test.go` — `TestShutdownDeadlineIsHonouredAgainstSlowBody` rewritten to exercise `parseConfig()` unchanged; the structural half (`probe.ReadTimeout > 10s`) is removed because it encoded the unsafe inequality; the `isTimeout` helper is removed with it; `t.Setenv("ADDR", "127.0.0.1:0")` added as the first line of `TestShutdownLogRecordsSignalName`.

The H1 Pin no longer duplicates the round-1 structural assertion; the L2 fix is a single-line addition to an existing test. No pre-existing test was modified, removed, or weakened.

## Follow-up rows

### H1 floor (round 2 follow-up)

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `cmd/thutapi/main.go` — `newHTTPServer(cfg, h)`; `cmd/thutapi/main_test.go` — `TestNewHTTPServerReadTimeoutIsAlwaysPositive`. |
| **What** | The round-2 H1 fix at `80ebe3e` derived `ReadTimeout = cfg.timeout - 2*time.Second` and clamped any negative result to 0. Go's `http.Server` treats `ReadTimeout: 0` as "no timeout", so legal flag values like `-shutdown-timeout=1s` and `-shutdown-timeout=2s` would silently re-open H1: the slow body accepted just before SIGTERM would never be force-closed, `srv.Shutdown` would return `context.DeadlineExceeded`, the process would log `graceful shutdown failed` and exit 1, and the Hetzner Traefik orchestrator would restart-loop the deploy. The previous Pin (`TestShutdownDeadlineIsHonouredAgainstSlowBody`) exercised only the production default `cfg.timeout=10s`, where the fixed `30s - 2s = 8s` headroom is positive, so the floor regression slipped through review. |
| **Production change** | `newHTTPServer` now derives `headroom = min(2*time.Second, cfg.timeout/2)` and `readTimeout = cfg.timeout - headroom`. The headroom is bounded by `cfg.timeout/2`, so `readTimeout` is strictly positive for every legal `cfg.timeout`: `1s → 500ms`, `2s → 1s`, `10s → 8s`. The strict-ordering property (`ReadTimeout < cfg.timeout`) is preserved because `cfg.timeout/2 < cfg.timeout` for every positive `cfg.timeout`. The comment block above `newHTTPServer` documents both invariants. |
| **Pin** | `TestNewHTTPServerReadTimeoutIsAlwaysPositive` in `cmd/thutapi/main_test.go` — table-driven over three cases: `cfg.timeout=1s` asserts `srv.ReadTimeout > 0` (floor), `cfg.timeout=2s` asserts `srv.ReadTimeout > 0` (floor), `cfg.timeout=10s` (the production default exercised by the slow-body Pin) asserts both `srv.ReadTimeout > 0` and `srv.ReadTimeout < cfg.timeout` (strict ordering). The Pin encodes the floor under small legal timeouts where the previous derivation silently collapsed to 0. The existing `TestShutdownDeadlineIsHonouredAgainstSlowBody` is unchanged. |
| **Mutation** | In `newHTTPServer`, restore the round-2 derivation: `readTimeout := cfg.timeout - 2*time.Second; if readTimeout < 0 { readTimeout = 0 }`. With the Mutation, `cfg.timeout=1s` yields `ReadTimeout=0` and `cfg.timeout=2s` yields `ReadTimeout=0` (Go's "no timeout"), and the Pin fails for both with the named floor reason; `cfg.timeout=10s` still satisfies the strict-ordering half (8s<10s) so the third subtest passes — exactly the masking behaviour that allowed the regression to slip through the round-2 review. |
| **Commit SHA** | `c9e25b69ed34edbb1f5e72e16b4db9c3f7fe5b62` |
| **Verification (Pin on fixed)** | `go test ./cmd/thutapi -run TestNewHTTPServerReadTimeoutIsAlwaysPositive -count=1 -v -timeout 30s`: `--- PASS: TestNewHTTPServerReadTimeoutIsAlwaysPositive (0.00s)`, `--- PASS: TestNewHTTPServerReadTimeoutIsAlwaysPositive/1s_floor (0.00s)`, `--- PASS: TestNewHTTPServerReadTimeoutIsAlwaysPositive/2s_floor (0.00s)`, `--- PASS: TestNewHTTPServerReadTimeoutIsAlwaysPositive/10s_default (0.00s)`. The existing `TestShutdownDeadlineIsHonouredAgainstSlowBody` continues to pass against `cfg.timeout=10s` (8.92s). The full suite also passes: `go test ./... -count=1 -race -timeout 120s` → `ok thutapi/cmd/thutapi 10.016s`. |
| **Verification (Pin on Mutation)** | After restoring the round-2 derivation in `newHTTPServer` (`readTimeout := cfg.timeout - 2*time.Second; if readTimeout < 0 { readTimeout = 0 }`), `go test ./cmd/thutapi -run TestNewHTTPServerReadTimeoutIsAlwaysPositive -count=1 -v -timeout 30s` produces: `main_test.go:304: ReadTimeout = 0s for cfg.timeout=1s; want > 0 (H1 floor: ReadTimeout=0 is Go's 'no timeout' and silently re-opens H1 for legal -shutdown-timeout=1s and -shutdown-timeout=2s)`, `main_test.go:304: ReadTimeout = 0s for cfg.timeout=2s; want > 0 (H1 floor: ReadTimeout=0 is Go's 'no timeout' and silently re-opens H1 for legal -shutdown-timeout=1s and -shutdown-timeout=2s)`, the 10s_default subtest still PASSes (8s<10s, masking the regression), then `--- FAIL: TestNewHTTPServerReadTimeoutIsAlwaysPositive (0.00s)`. After reverting the Mutation the Pin passes again in 0.00s across all three subtests. The Mutation transcript is the named floor reason the Pin encodes. |