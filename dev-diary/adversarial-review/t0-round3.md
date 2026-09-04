# T0 round 3 — adversarial re-review

| | |
|---|---|
| **Target** | T0 at `c9e25b6` ("H1 floor: make ReadTimeout positive by construction") + `521d4ef` ("H1 floor: remediation record"); all remediation commits in history (`5a7e7169`, `80ebe3e7`, `c9e25b6`, `521d4ef`). Working tree clean at review time. |
| **Evidence** | `dev-diary/adversarial-review/t0-round1.md`, `t0-remediation-round1.md`, `t0-round2.md`, `t0-remediation-round2.md` (read in order); `cmd/thutapi/main.go` and `cmd/thutapi/main_test.go` in full; `.gitignore`; `go.mod`; `AGENTS.md`; `dev-diary/project.md`; `dev-diary/PLAN.md`; `git show`/`git blame` on the remediation commits; every Pin/Mutation transcript below re-run by this reviewer on 2026-09-04. |
| **Method** | Re-probe. Every prior-round Pin was run on the current tree (green required), its Mutation applied (red required, for the named reason), reverted, and re-run (green required). Mutations were applied in a throwaway clone (`git clone --shared . /tmp/thutapi-t0-round3`, removed after use); the main working tree was never mutated. Full validation (`go vet`, `go test -count=1`, `go test -race`, `CGO_ENABLED=0 go build`), concurrent-suite probe, `gofmt -l`, occupied-port probe, and two live-binary smokes (normal SIGTERM and slow-body SIGTERM) were run against the clean main tree. Remediation-file claims were not trusted; every code path was re-read and every probe re-executed. |
| **Date** | 2026-09-04 |

## Verdict

**REMEDIATE — 0 × C, 0 × H, 0 × M, 2 × L.**

Every prior-round finding — H1 (original), H1 (re-opened), H1 floor, H2, H3,
M2, L1, L2, with M1 waived — is verified **zero residue, severity by
severity**: each Pin passes on the current code and fails under its
documented Mutation, and the production behaviour is confirmed end-to-end on
the real binary. The round-1 `WriteTimeout=0` disposition is correct and
explicitly verified below.

Two **new L-severity** findings were introduced inside the remediation series
itself and were never flagged by rounds 1–2: `gofmt` drift across both Go
files (five deformities, four blame-attributed to remediation commits, one
pre-existing from the skeleton and swept by the same mechanical fix) and a
stale `cfg.timeout-1s` comment inside the H1 Pin. Under
`dev-diary/adversarial-review/README.md` ("No severity is exempt"), these must
be remediated before an APPROVE round, so T0 cannot close here.

## Severity key

| Level | Meaning |
|---|---|
| **C** | Breaks the demo. Cannot ship. |
| **H** | Real defect the demo survives. Must fix before the track closes. |
| **M** | Real defect with workaround. Must fix before the track closes. |
| **L** | Polish / hygiene. Must fix before the track closes. |

## Per-finding re-verification

### H1 (round 2, re-opened) — zero residue

**Pin:** `TestShutdownDeadlineIsHonouredAgainstSlowBody` (production defaults, `parseConfig()` unchanged).

1. **Current commit (`521d4ef`, clean main tree):**
   ```text
   === RUN   TestShutdownDeadlineIsHonouredAgainstSlowBody
   --- PASS: TestShutdownDeadlineIsHonouredAgainstSlowBody (9.01s)
   PASS
   ok  	thutapi/cmd/thutapi	9.012s
   ```
2. **Round-2 Mutation** (`readTimeout := 30 * time.Second` restored in `newHTTPServer`, clone):
   ```text
   main_test.go:240: Shutdown = context deadline exceeded after 10s; default ReadTimeout must be ≤ cfg.timeout=10s so the slow body is force-closed before the shutdown deadline (H1)
   --- FAIL: TestShutdownDeadlineIsHonouredAgainstSlowBody (10.00s)
   ```
3. **After reverting the Mutation (clone):** `--- PASS (8.98s)`.
4. **Stability:** `-count=3` on the main tree → `ok thutapi/cmd/thutapi 26.839s` (three consecutive green runs of the ~9 s pin).
5. **End-to-end on the real production binary** (round-1's original probe shape — `curl --http1.1 --limit-rate 1 --data-binary @LICENSE`, then SIGTERM):
   ```text
   {"msg":"thutapi listening","addr":"127.0.0.1:18086"}
   {"msg":"shutdown signal received","signal":"terminated"}
   {"msg":"thutapi stopped cleanly"}
   exit=0 elapsed=7.35s
   ```
   The round-1 failure mode (`graceful shutdown failed`, exit 1) no longer
   occurs: `ReadTimeout = 10s − 2s` force-closes the body inside the 10 s
   budget and `Shutdown` returns nil. The elapsed time matches the design
   arithmetic (request started ~1 s before SIGTERM; read deadline fires 8 s
   after request start).

**Zero residue: confirmed.** The Pin now exercises `parseConfig()`
unchanged (it hard-fails if `cfg.timeout != 10s`), the unsafe ordering makes
it red for the named reason, and the process-level behaviour is verified.

### H1 floor (round 2 follow-up) — zero residue

**Pin:** `TestNewHTTPServerReadTimeoutIsAlwaysPositive` over `{1s, 2s, 10s}`.

1. **Current commit:**
   ```text
   --- PASS: TestNewHTTPServerReadTimeoutIsAlwaysPositive (0.00s)
       --- PASS: …/1s_floor (0.00s)
       --- PASS: …/2s_floor (0.00s)
       --- PASS: …/10s_default (0.00s)
   ```
2. **Mutation** (naive clamp restored: `readTimeout := cfg.timeout - 2*time.Second; if readTimeout < 0 { readTimeout = 0 }`, clone):
   ```text
   main_test.go:304: ReadTimeout = 0s for cfg.timeout=1s; want > 0 (H1 floor: ReadTimeout=0 is Go's 'no timeout' and silently re-opens H1 for legal -shutdown-timeout=1s and -shutdown-timeout=2s)
   main_test.go:304: ReadTimeout = 0s for cfg.timeout=2s; want > 0 (…)
   --- FAIL: TestNewHTTPServerReadTimeoutIsAlwaysPositive (0.00s)
       --- FAIL: …/1s_floor (0.00s)
       --- FAIL: …/2s_floor (0.00s)
       --- PASS:   …/10s_default (0.00s)
   ```
   The `10s_default` subtest passing under the Mutation (8s < 10s still
   holds) is exactly the masking the floor Pin exists to catch — the floor
   subtests go red, so the Pin is load-bearing.
3. **After reverting (clone):** all four PASS again in 0.00s.

Derivation re-checked by reading: `headroom = min(2s, cfg.timeout/2)` ⇒
`readTimeout = cfg.timeout − headroom` is strictly positive for every legal
positive timeout (including sub-2s values, where duration division truncates
but never below positivity: `1s → 500ms`, `2s → 1s`, `10s → 8s`) and strictly
less than `cfg.timeout` always. **Zero residue: confirmed.**

### H2 — zero residue

**Pin (shell probe):** `go build ./cmd/thutapi && git status --short && git check-ignore -v thutapi`.

1. **Current commit (main tree):** build green; `git status --short` shows no
   `thutapi` row; `git check-ignore -v thutapi` →
   `.gitignore:5:/thutapi	thutapi`, exit 0.
2. **Mutation** (`/thutapi` line dropped from `.gitignore`, clone): fresh
   build → `git status --short` shows `?? thutapi`; `git check-ignore
   thutapi` prints nothing, exit 1.
3. **After reverting (clone):** `?? thutapi` gone; `check-ignore` exit 0.
   Probe binary removed from the main tree after the fixed-side run.

**Zero residue: confirmed.** The ignore rule exists only while the mutation
is applied, and the probe catches its removal both ways.

### H3 — zero residue

**Pin:** `TestParseFlagsRejectsNonPositiveTimeout`.

1. **Current commit:** `zero` and `negative` subtests PASS; parent PASS 0.00s.
2. **Mutation** (`if cfg.timeout <= 0` block deleted from `parseFlags`, clone):
   ```text
   main_test.go:342: parseFlags(-shutdown-timeout=0s) returned nil error; want errShutdownTimeout
   main_test.go:342: parseFlags(-shutdown-timeout=-5s) returned nil error; want errShutdownTimeout
   --- FAIL: TestParseFlagsRejectsNonPositiveTimeout (0.00s)
   ```
3. **After reverting (clone):** both subtests and parent PASS 0.00s.

**Zero residue: confirmed.**

### M2 — zero residue

**Pin:** `TestParseFlagsAfterOperandIsNotSilentlyDropped`.

1. **Current commit:** PASS 0.00s.
2. **Mutation** (`if fs.NArg() != 0` block deleted, clone):
   ```text
   main_test.go:367: parseFlags(unexpected -shutdown-timeout=1s) returned nil error; trailing flag is being silently dropped (M2)
   --- FAIL: TestParseFlagsAfterOperandIsNotSilentlyDropped (0.00s)
   ```
3. **After reverting (clone):** PASS 0.00s.

**Zero residue: confirmed.**

### L1 — zero residue

**Pin:** `TestShutdownLogRecordsSignalName`, asserting `signal="terminated"`.

1. **Current commit:** PASS 0.02s.
2. **Mutation** (the complete two-line old-behaviour change round 2 specified —
   `case <-sigs:` plus `context.Canceled.Error()` in the log line, clone):
   ```text
   main_test.go:443: shutdown log signal field = "context canceled"; want "terminated" (L1 fix logs sig.String(), not ctx.Err().Error())
   --- FAIL: TestShutdownLogRecordsSignalName (0.02s)
   ```
3. **After reverting (clone):** PASS 0.02s.
4. **Live binary:** the SIGTERM smoke above logged
   `"signal":"terminated"` on the production `main()` path.

**Zero residue: confirmed.** The round-2 lesson (expression-only mutation
does not compile) was honored and the compilable old-behaviour mutation is
caught for the named reason.

### L2 — zero residue

**Pin:** `TestShutdownLogRecordsSignalName` with `t.Setenv("ADDR", "127.0.0.1:0")` as the first line (`main_test.go:405`), under an occupied `127.0.0.1:8080`.

1. **Port held** by a one-line `net.Listen("tcp", "127.0.0.1:8080")` holder
   process for the entire L2 cycle below.
2. **Current commit, port held:** `--- PASS (0.02s)` — the ADDR isolation is
   in place and the Pin is immune to the occupied port.
3. **Mutation** (`t.Setenv("ADDR", "127.0.0.1:0")` line removed, port still
   held, clone):
   ```text
   main_test.go:422: run returned listen tcp 0.0.0.0:8080: bind: address already in use
   main_test.go:445: no `shutdown signal received` log line in output:
       {"time":"…","level":"INFO","msg":"thutapi listening","addr":"0.0.0.0:8080"}
   --- FAIL: TestShutdownLogRecordsSignalName (0.02s)
   ```
   Both named reasons reproduced: the bind error **and** the missing log
   line.
4. **After reverting (clone, port still held):** `--- PASS (0.02s)`.
5. **Concurrent-suite probe** (the exact condition that exposed L2 in round
   2): `go test ./... -count=1` and `go test ./... -count=1 -race` launched
   simultaneously on the main tree:
   ```text
   ok  	thutapi/cmd/thutapi	8.999s   (A-exit=0)
   ok  	thutapi/cmd/thutapi	9.970s   (B-exit=0)
   ```
   Both green — the shared port-8080 dependency is gone.

**Zero residue: confirmed.**

### M1 — waiver confirmed

No Pin/Mutation; waived by the user on 2026-09-04. Re-checked that all three
authorities agree on Go 1.27.1: `go.mod:3` (`go 1.27.1`), `AGENTS.md:57`
("Backend: Go 1.27.1, stdlib-first"), `dev-diary/project.md:251` ("Go 1.27.1
code looks like Go 1.16 code"). **Waiver intact; nothing outstanding.**

### WriteTimeout = 0 — disposition explicitly verified (round-1 finding text included it)

Round 1's H1 text counted `WriteTimeout` among the unset (zero-value)
timeouts. Rounds 2–3 deliberately leave `WriteTimeout: 0`
(`main.go:178`). This is now **correct by design, not residue**, for four
verified reasons:

1. **The product architecture is SSE-first.** `project.md:238` — "Go
   backend, `html/template` + SSE + a little vanilla JS"; `project.md:295` —
   `internal/stream/broker.go` is "a working SSE broker, directly reusable
   for streaming interview turns" (T4); `project.md:358-359` — "Stream the
   question **text over SSE immediately**, fire the TTS call in parallel"
   (T8). A server-wide non-zero `WriteTimeout` force-closes every long-lived
   SSE stream mid-flight; it is the wrong default for this system.
2. **The threat H1 actually targets is read-side.** The round-1/round-2
   defect was a client holding the request **body** open past the shutdown
   deadline. That is bounded by `ReadTimeout` (now `cfg.timeout − headroom`,
   positive by construction, strictly ordered against the shutdown
   deadline), `ReadHeaderTimeout: 5s` (`main.go:176`), and `IdleTimeout:
   120s` (`main.go:179`). All three are pinned this round.
3. **Per-route deadlines are the right tool for stream lifetime, and they
   arrive with the streams.** T4/T8 own the streaming endpoints and can
   bound each response with `http.ResponseController.SetWriteDeadline`
   per-route — a decision documented for those tracks, not bootstrap code.
4. **The decision is written down at the code.** The comment block
   `cmd/thutapi/main.go:155-160` ("WriteTimeout is 0 by design … a
   non-zero WriteTimeout would force-close every long-lived SSE stream …
   The slow-body case H1 is actually about is fully bounded by ReadTimeout,
   so leaving WriteTimeout at 0 does not re-open H1") and the config comment
   `main.go:40-46` both record the rationale and point at `project.md`.

Round 3 accepts this disposition: `WriteTimeout=0` is a deliberate,
documented, architecture-grounded choice, and the slow-body exit-1 failure
mode it was originally bundled with is closed and mutation-pinned.

## New findings

### N1 (L) — `gofmt` drift in both Go files, introduced across the remediation series

| Field | Value |
|---|---|
| **Where** | `cmd/thutapi/main.go:165` (stray trailing `//` before `func newHTTPServer`); `cmd/thutapi/main_test.go:241` (`}` at column 0 closing the `if err := srv.Shutdown` block); `main_test.go:312-313` and `main_test.go:406-407` (double blank lines). |
| **What** | `gofmt -l cmd/thutapi/` lists **both** files. `AGENTS.md` names gofmt as the repo's style-leveler ("gofmt removes style drift between three different agents"), and `gofmt -d` shows five concrete deformities. Blame attributes four to the remediation series: `main.go:165` → `80ebe3e`; `main_test.go:241` → `5a7e7169`; `main_test.go:313` + `406-407` → `80ebe3e`; `main_test.go:312` → `521d4ef`. The fifth (missing newline at end of `main.go`) predates the series (skeleton commit `2cb3863`) and is swept by the same one-command fix; recorded here for transparency, not counted as series residue. |
| **Pin** | `gofmt -l cmd/thutapi/` must print nothing (currently prints `cmd/thutapi/main.go` and `cmd/thutapi/main_test.go`). After `gofmt -w cmd/thutapi/main.go cmd/thutapi/main_test.go`, re-run: empty output, and `go test ./... -count=1` still green (whitespace-only diff). |
| **Mutation** | Re-introduce any deformity — e.g. delete the file-final newline of `main.go`, or re-insert a second blank line at `main_test.go:312` — and `gofmt -l cmd/thutapi/` must list the file again. |

### N2 (L) — stale `cfg.timeout-1s` in the H1 Pin's explanatory comment

| Field | Value |
|---|---|
| **Where** | `cmd/thutapi/main_test.go:160` and `main_test.go:191`. |
| **What** | The H1 Pin's comment block says "With the fix, ReadTimeout fires at cfg.timeout-1s" and "the read returns a timeout error after cfg.timeout-1s". Under the shipped code (`headroom = min(2s, cfg.timeout/2)`) and at the 10s default the Pin pins, ReadTimeout is 8s — `cfg.timeout − 2s`. The "-1s" is leftover from the abandoned 1s-headroom iteration that `t0-remediation-round2.md` itself records ("One second of headroom left a ~30% flake rate"); it was wrong when written at `80ebe3e` and survived `c9e25b6`. A maintainer debugging the ~9s test duration would compute the wrong expected firing time. (The same block's "ReadTimeout = cfg.timeout - 2s" at line 146 remains accurate for the default, and the floor generalization is correctly documented in the floor Pin's own comment — no change needed there.) |
| **Pin** | `grep -n 'cfg.timeout-1s' cmd/thutapi/main_test.go` must return zero hits (currently returns lines 160 and 191). |
| **Mutation** | Restore `-1s` on either line; the grep must hit again. |

No other new defect was found: `run()`'s dispatch (bind error → non-nil
return; `http.ErrServerClosed` filtered; signal → `sig.String()` →
`Shutdown(ctxWithTimeout(cfg.timeout))`), the floor derivation under
sub-2s and sub-nanosecond timeouts, the flag help/exit-code paths, and the
production `main()` were all re-read and re-exercised without finding
functional residue.

## Done-when audit re-run — `dev-diary/PLAN.md` §T0

| Criterion | Met? | Evidence (this round) |
|---|:---:|---|
| `go build ./...` / command build is green | **Yes** | `go vet ./...` clean; `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/thutapi` → 6.7 MB static ELF, stripped; artifact ignored (H2 probe) |
| `./thutapi` serves `/healthz` | **Yes** | Live binary on `127.0.0.1:18085`: HTTP 200, `Cache-Control: no-store`, `application/json`, `{"status":"ok",...}` |
| Graceful SIGTERM exits 0 | **Yes** | Normal path: exit 0, `thutapi stopped cleanly`. Slow-body path (round-1 probe shape): exit 0 in 7.35s, within the 10s budget |
| Full suite green | **Yes** | `-count=1` → `ok 8.994s`; `-race` → `ok 9.550s`; normal+race launched concurrently → both `ok` (L2 immunity) |
| Review loop reaches APPROVE with zero residue | **No** | All prior findings zero residue, but N1 + N2 (new L) force one more remediation round |

`AGENTS.md` definition-of-done items 1 (PLAN status = DONE) and 5 (APPROVE'd
track committed) land with the closing commit after the next round;
`PLAN.md`'s T0 status row should be updated then.

## Go-signal matrix

| Downstream | Unblocked? | Condition |
|---|---|---|
| **T1** — deployment path | **Conditional** | Gated only on N1/N2 — both mechanical, one commit, zero behavioural risk (whitespace + two comment words). Every functional gate T1 cared about (H1 ordering + floor, H2 artifact ignore, H3/M2 flag surface) is zero residue and mutation-pinned, and the slow-body SIGTERM smoke exits 0 on the real binary. T1 becomes unconditional the moment the L-remediation lands. |
| **T2** — GMI clients | **Yes** | No open finding touches the HTTP handler seam or flag parser contract. |
| **T3** — store and media | **Yes** | No store/media boundary is affected. |
| **T4** — interview loop | **Yes** | T4 owns the SSE endpoints and their per-route `ResponseController` write deadlines (see WriteTimeout disposition); T0's `WriteTimeout=0` base default is the correct substrate for it. |
| **T8** — audio/TTS | **Yes** | Text-streams-while-TTS-runs is exactly the architecture the WriteTimeout disposition protects; per-route deadlines arrive with T8's streams. |

## Recommended actions (REMEDIATE)

1. **N1:** `gofmt -w cmd/thutapi/main.go cmd/thutapi/main_test.go` (also restores the skeleton-era missing final newline in `main.go`); verify with `gofmt -l cmd/thutapi/` (empty) and a full-suite re-run.
2. **N2:** in `cmd/thutapi/main_test.go`, change `cfg.timeout-1s` → `cfg.timeout-2s` at lines 160 and 191; verify with `grep -n 'cfg.timeout-1s' cmd/thutapi/main_test.go` (zero hits).
3. Both fixes are whitespace/comment-only: one commit, one remediation-record row set (N1, N2), then round 4 re-review — which, on the evidence here, has no functional work left to verify and should be a fast zero-residue confirmation.
4. On APPROVE, update `PLAN.md`'s T0 status row per `AGENTS.md` definition-of-done item 1.
