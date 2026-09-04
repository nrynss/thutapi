# T0 round 1 — adversarial review of the repo skeleton

| | |
|---|---|
| **Target** | T0 repo skeleton (commits `2cb3863` + `5b177c9` on `main`, tree clean) |
| **Evidence** | `AGENTS.md`, `go.mod`, `cmd/thutapi/main.go`, `cmd/thutapi/main_test.go`, `.gitignore`, `LICENSE`, `dev-diary/PLAN.md`, `dev-diary/project.md`, `dev-diary/adversarial-review/README.md`; live probes by reviewer `T0Review-2` plus a re-run of the same probes here |
| **Method** | Read every source file, re-run `go vet ./...`, `go test ./... -count=1 -race`, `go test ./... -count=1 -v`, and `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/thutapi` from a clean tree. Then default `go build ./cmd/thutapi` (no `-o`) to confirm artifact placement. Slow-body shutdown probe and flag-after-operand probe were run by the reviewer and the results are reproduced below. |
| **Date** | 2026-09-04 |
| **Reviewer file status** | Reviewer declined to author this round file directly per a reviewer-side read-only constraint. Composition from reviewer structured output (transcript `history://T0Review-2`) and a re-run probe set is recorded here verbatim. Remediation must treat every row as a real finding. |

## Verdict

**REMEDIATE — 0 × C, 4 × H, 2 × M, 1 × L.**

The skeleton runs and the tests pass under `-race`, but seven real
defects are visible. Four are High severity because each can either
defeat graceful shutdown, leave a dirty public-repo tree, or contradict
the loop's severity vocabulary.

## Severity key

| Level | Meaning |
|---|---|
| **C** | Breaks the demo. Cannot ship. |
| **H** | Real defect the demo survives. Must fix before the track closes. |
| **M** | Real defect with workaround. Must fix before the track closes. |
| **L** | Polish / hygiene. Must fix before the track closes. |

**No severity is exempt.** Every finding lands in a remediation file
unless it is so trivial that it is a single-line doc typo or type
annotation needing no review. Nothing in this round qualifies for the
in-line exception.

## Findings

### H1 — slow-body request blows the graceful shutdown deadline

**Where:** `cmd/thutapi/main.go:99-108` (the `http.Server` literal).

**What:** `ReadTimeout`, `WriteTimeout` and `IdleTimeout` are all
unset — Go's zero value means **no timeout**. The only header timeout
set is `ReadHeaderTimeout: 5 * time.Second`. A client that holds the
request body open past the shutdown deadline will cause
`srv.Shutdown(shutdownCtx)` to return `context.DeadlineExceeded`, the
process logs `graceful shutdown failed` and exits 1.

**Probe (reviewer, 2026-09-04 15:48 IST):**

```bash
curl --http1.1 --limit-rate 1 --data-binary @LICENSE \
  http://127.0.0.1:18084/healthz &
SIGTERM
# server log: "shutdown signal received" at 15:48:43
# server log: "graceful shutdown failed: context deadline exceeded"
# exit code: 1
```

A container orchestrator (Traefik on the Hetzner box) treats exit 1 as
unhealthy and can restart-loop on SIGTERM during deploy.

**Pin:** write an httptest handler that sleeps inside a long-running
request body, start `srv` with the same `http.Server{...}` literal, send
`Shutdown` with a 100 ms context; assert the error wraps
`context.DeadlineExceeded` and that the handler was not cancelled. Test
name: `TestShutdownDeadlineIsHonouredAgainstSlowBody`.

**Mutation:** delete the three timeout fields from the `http.Server`
literal after the fix lands; the new test must fail with the reason
named in its `t.Fatalf`.

### H2 — `go build ./cmd/thutapi` leaves an untracked `thutapi` binary at the repo root

**Where:** `.gitignore`.

**What:** A reviewer running `CGO_ENABLED=0 go build ./cmd/thutapi`
with no `-o` flag gets `thutapi` in the working directory, which
appears as `?? thutapi` under `git status`. There is no rule that
ignores it. The repo is going public for the judging period, so
forgetting to gitignore the artifact is a real risk: a future
implementer runs the same command, sees an untracked binary, and
either commits it or spends time wondering if the project is dirty.

**Pin:** with the fix applied, `git status --short` after
`go build ./cmd/thutapi` must show no `thutapi` row, and
`git check-ignore thutapi` must exit 0.

**Mutation:** drop the `thutapi` ignore pattern from `.gitignore`; the
pin must observe an untracked `?? thutapi` row from a fresh build.

### H3 — non-positive shutdown deadlines are silently accepted

**Where:** `cmd/thutapi/main.go:53-61` (`parseFlags`).

**What:** `flag.DurationVar` accepts `-shutdown-timeout=0s` and
negative durations, and `parseFlags` does not validate. With a slow
active request and `-shutdown-timeout=0s`, SIGTERM immediately logs
`graceful shutdown failed` and exits 1. Zero and negative deadlines
cannot permit any request to drain — the very point of a graceful
shutdown is to give in-flight requests time to finish.

**Pin:** add `TestParseFlagsRejectsNonPositiveTimeout` that calls
`parseFlags([]string{"-shutdown-timeout=0s"})` and
`parseFlags([]string{"-shutdown-timeout=-5s"})` and asserts both
return a non-nil error. The current implementation returns
`(cfg, nil)`; the test must fail.

**Mutation:** drop the validation; the new test must fail.

### H4 — README and AGENTS.md still reference `P3` despite C/H/M/L vocabulary

**Where:** `dev-diary/adversarial-review/README.md:40` and
`AGENTS.md:23-25` before this round.

**What:** The reconciliation commit `5b177c9` switched the severity
vocabulary to C/H/M/L but left the trivial-in-line exemption clause
phrased as **"P3 is not exempt"**, plus a one-line mention in
AGENTS.md. Reviewers reading the docs have no unambiguous name for
the documented low-severity exception. This defeats the
reconciliation commit's stated purpose.

**Pin:** `grep -E '\bP3\b' dev-diary/adversarial-review/README.md
AGENTS.md` must return zero hits after the fix. The remediation agent
must commit both edits together.

**Mutation:** restore the P3 phrase in either file; the grep must
return hits.

### M1 — `go.mod` pins Go 1.27.1; spec mandates 1.24

**Where:** `go.mod:3`.

**What:** `go 1.27.1` was produced by `go mod init` against the
installed toolchain (1.27.1). The spec (`dev-diary/project.md` §Stack)
and AGENTS.md both say Go 1.24. Go 1.27 is forward-compatible, so the
binary builds and tests pass on the box — but on a fresh Go 1.24
toolchain (the spec pin), this module refuses to build without
downloading a newer toolchain. The repo is going public, so the wrong
pin propagates to judges.

**Pin:** `grep '^go ' go.mod` must show `go 1.24` (or `1.24.x`); tests
must still pass on Go 1.27.1 after the downgrade.

**Mutation:** `go 1.27.1` → `go 1.24`; `go test ./...` must still be
green on the box.

### M2 — flag parsing silently drops flags after a positional operand

**Where:** `cmd/thutapi/main.go:53-61` (`parseFlags`).

**What:** `flag.NewFlagSet("thutapi", flag.ContinueOnError)` is called
without `flag.CommandLine` rules. Running the binary with a positional
operand followed by `-shutdown-timeout=1s` silently keeps the default
10 s timeout and the flag is dropped. The reviewer ran the binary with
`unexpected -shutdown-timeout=1s`; the server listened with the default
10 s timeout.

**Pin:** add a test `TestParseFlagsAfterOperandIsNotSilentlyDropped`
that calls `parseFlags([]string{"unexpected", "-shutdown-timeout=1s"})`
and asserts an error is returned (or `cfg.timeout == 1 * time.Second`
after fix). The current implementation returns `(cfg, nil)`; the test
must fail.

**Mutation:** revert the parseFlags fix; the new test must fail with
the reason named in its `t.Fatalf`.

### L1 — shutdown log field is literally `context canceled`

**Where:** `cmd/thutapi/main.go:113`.

**What:** `log.Info("shutdown signal received", "signal", ctx.Err().Error())`
prints `context canceled` because `signal.NotifyContext` cancels the
context on SIGINT/SIGTERM and the log reads the cancel message. The
operator signal is invisible in the log; the field is
`signal: context canceled`, not `signal: SIGTERM`. Fix by deriving
the `signal` field from `os.Signal` channel directly.

**Pin:** add a test `TestShutdownLogRecordsSignalName` that triggers
`SIGTERM` against the running binary and asserts the log line contains
`signal=SIGTERM` (or `signal=terminated`). Mutation: revert the fix;
the new test must fail.

## What held up under attack

* `go vet ./...` — clean.
* `go test ./... -count=1 -race` — `ok thutapi/cmd/thutapi 1.009s`,
  8 tests pass.
* `go test ./... -count=1 -v` — all 8 tests print `PASS`.
* `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/thutapi`
  produces a 6.7 MB static binary (clean path).
* Normal smoke: binary on `:18081`, `GET /healthz` returns 200 with the
  expected JSON, `SIGTERM` logs `thutapi stopped cleanly` and exits 0.
* `parseFlags([]string{"-this-flag-does-not-defined"})` returns an error
  and exits 2 — `TestParseFlagsBadFlagReturnsError` covers this.
* `.gitignore` correctly excludes `.env*`, `*.db*`, `data/`, `media/`,
  editor and OS junk.
* `LICENSE` is Apache 2.0 (matches `dryrun` sibling project) — public
  repo, submission-compatible.
* `AGENTS.md` binds the workflow this round is executing.
* The reconciliation commit `5b177c9` switched the severity vocabulary
  to C/H/M/L — **but missed the P3 phrase**, which is H4.

## Done-when audit (T0 — from `dev-diary/PLAN.md` §T0)

| Criterion | Met? | Note |
|---|:----:|------|
| Repo skeleton in place on `main` | **Yes** | `2cb3863` + `5b177c9` |
| `go.mod`, `cmd/thutapi/`, `LICENSE`, `AGENTS.md` | **Yes** | |
| `GET /healthz` returns 200 JSON | **Yes** | `TestHealthzReturnsOK` |
| Server compiles `CGO_ENABLED=0` | **Yes** | 6.7 MB static binary |
| `go vet ./...` clean | **Yes** | |
| `go test ./... -count=1 -race` clean | **Yes** | 8 tests, 1.009 s |
| Graceful SIGTERM exits 0 | **Partial** | Fast path yes; slow-body path **exits 1 (H1)** |
| `.gitignore` excludes build artifact | **No** | H2 |
| Go toolchain pin matches spec | **No** | M1 (1.27.1 vs 1.24) |
| Flag parsing is unambiguous | **No** | M2, H3 |
| README and AGENTS.md agree on C/H/M/L | **No** | H4 |

## Go-signal matrix

| Downstream | Unblocked? | Condition |
|---|---|---|
| **T1** Deployment path | **Conditional** | Unblocked on the `cmd/thutapi` binary; gated on H1 (slow-body shutdown) so a `docker run` SIGTERM under Traefik does not exit 1; H2 (artifact ignore) so a `docker build` does not pick up an untracked binary; M1 (toolchain pin) so the distroless image built on Go 1.24 succeeds. |
| **T2** GMI clients | **Yes** | T0's HTTP seam (`server`, `ServeHTTP`) is enough to host `/healthz` and later `/v1/*`; no T2 path depends on timeouts. M1 matters only at go.mod-edit time. |
| **T3** Store and media | **Yes** | None of the seven findings gate the SQLite / Docker-volume / Range-seam path. |
| **T4** Interview loop | **Yes** | Independent of the timeout surface. |

## Recommended actions (ordered)

1. **H1** — set `ReadTimeout`, `WriteTimeout`, `IdleTimeout` on the
   `http.Server` literal; pin with the slow-body test; mutation
   re-introduces.
2. **H2** — add `thutapi` to `.gitignore`; pin with `git check-ignore`.
3. **H3** — `parseFlags` rejects `-shutdown-timeout<=0` with a typed
   error; pin with `TestParseFlagsRejectsNonPositiveTimeout`.
4. **H4** — drop every `\bP3\b` reference in
   `dev-diary/adversarial-review/README.md` and `AGENTS.md`; commit both
   in one focused commit.
5. **M1** — downgrade `go 1.27.1` to `go 1.24` in `go.mod`; re-run tests.
6. **M2** — make `parseFlags` reject extra positional args after the
   flag set is parsed, with an error; pin with the new test.
7. **L1** — log the actual signal name (`SIGTERM` / `SIGINT`) instead
   of `ctx.Err().Error()`; pin with the signal-name test.

After all seven remediations land on `main`, this reviewer runs round 2
against the same commits and verifies zero residue.

---

## Reviewer-composition note (process, not a finding)

The reviewer agent declined to author this file directly per a
reviewer-side read-only constraint. The findings above are transcribed
from the reviewer's structured output (transcript at
`history://T0Review-2`) and a re-run probe set. The remediation agent
must treat every row as if a reviewer had written the file; this
composition note is recorded for process traceability and does not
affect severity or priority.