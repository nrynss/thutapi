# T10a round 2 — adversarial review of the round-1 remediation

| | |
|---|---|
| **Target** | Track T10a round-1 remediation (`t10a-remediation-round1.md`), covering the four findings of `t10a-round1.md` (0 C / 1 H / 1 M / 2 L) across `internal/bookvideo/**` (`command.go`, `video.go`, `types.go`, `bookvideo_test.go`) and `Dockerfile`. |
| **Evidence** | `AGENTS.md` in full; `dev-diary/PLAN.md` §T10, §T10a, §Architectural invariants; `dev-diary/adversarial-review/t10-video-record.md` (runs 1–3, verified recipe, verification gaps); `t10a-round1.md` (the round 1 review); `t10a-remediation-round1.md` (the round 1 remediation record); `go test -race -cover ./internal/bookvideo/...` (89.1% coverage); `go test -count=1 ./... -race` (clean repo-wide); `go vet ./...` (clean); `gofmt -l .` (clean); real ffmpeg execution verifying quote escaping, font, and concurrent scratch isolation. |
| **Method** | Fresh-agent adversarial audit for Round 2. Trust neither the round 1 assertions nor the remediation claims. Re-read all diffs and source files. Re-audit each finding (H1, M1, L1, L2) against the code, tests, and mutants. Verify concurrent goroutine isolation in `workDir`. Verify ffmpeg filtergraph parameter escaping against real ffmpeg with complex paths containing single quotes, colons, and spaces. Verify `defaultRunner.Run` error return invariants against AGENTS.md §Go style. Verify immutable sha256 digest pin in `Dockerfile`. Verify absence of scope creep, interface leaks, and unowned file edits. |
| **Date** | 2026-09-05. Reviewer: fresh adversarial review agent (not the implementer, not the round 1 reviewer, not the remediation agent). |

**Verdict: APPROVE — 0 × C, 0 × H, 0 × M, 0 × L (zero residue).**

---

## Summary of Round-1 Remediation Audit

Round 1 identified 4 findings across concurrency safety, filtergraph escaping, error conventions, and Dockerfile digest pinning (0 C / 1 H / 1 M / 2 L). All 4 findings have been thoroughly remediated and backed by automated test pins:

1. **H1 (Concurrency safety in scratch files):**
   - In `internal/bookvideo/command.go`, an unexported helper `writeTempFile(dir, pattern string, data []byte) (string, error)` was introduced using `os.CreateTemp(dir, pattern)`. It ensures atomic descriptor cleanup and deletion on write errors.
   - In `internal/bookvideo/video.go`, all five instances of `os.Getpid()` (`title-%d.txt`, `byline-%d.txt`, `end_title-%d.txt`, `end_domain-%d.txt`, `concat_list-%d.txt`) were replaced with unique random filenames created via `writeTempFile` (`title-*.txt`, `byline-*.txt`, `end_title-*.txt`, `end_domain-*.txt`, `concat_list-*.txt`) and registered with `defer os.Remove(...)`.
   - Grep verification across `internal/bookvideo` confirms zero remaining occurrences of `os.Getpid()`.
   - Pinned by `TestBuildTitleCard_ConcurrentScratchFiles`, running 12 concurrent goroutines in the same `workDir` asserting all distinct titles are observed by the runner without collisions or deletion races.

2. **M1 (FFmpeg filtergraph parameter escaping):**
   - `escapeFilterPath` in `internal/bookvideo/command.go` was completely rewritten to match FFmpeg's two-level filtergraph parameter parser without enclosing single quotes. Single quotes are escaped with `\\\'`, colons with `\\:`, backslashes with `\\\\`, and filtergraph delimiters/spaces (` `, `,`, `;`, `[`, `]`) with `\`.
   - `escapeConcatPath` correctly retains single-quote enclosure (`'...'` with `'\''`) matching the `ffconcat` demuxer specification.
   - Pinned by `TestEscapeHelpers` and the integration pin `TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg`, which executes real ffmpeg on a directory and filenames containing `'`, `:`, and spaces (`child's_stories:and spaces/page'01.jpg`), successfully producing a valid video.

3. **L1 (Nil output on runner error):**
   - In `internal/bookvideo/command.go`, `defaultRunner.Run` now returns `nil, fmt.Errorf(...)` on both error branches (`ctx.Err() != nil` and command execution error), adhering strictly to AGENTS.md §Go style ("Never return a non-nil value alongside a non-nil error").
   - Pinned by `TestDefaultRunner_NilOutputOnError` (asserting `out == nil` for nonexistent commands and non-zero exits with output) and `TestDefaultRunner_ContextCancellation`.

4. **L2 (Dockerfile digest pin):**
   - `Dockerfile:20` was updated from `FROM mwader/static-ffmpeg:7.1 AS ff` to `FROM mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4 AS ff`.
   - Pinned by `TestDockerfile_FFmpegPinnedByDigest` in `internal/bookvideo/bookvideo_test.go`.

---

## Severity Counts

**0 C / 0 H / 0 M / 0 L**

---

## Remediation Evaluation Table

| # | Round 1 Finding | Severity | Evaluation / Audit Findings | Status |
|---|---|---|---|---|
| 1 | **H1** (`video.go:68, 77, 182, 188, 249`): Scratch files used `os.Getpid()`, causing collisions and deletion races in concurrent calls. | **H** | **Resolved.** Replaced all five `os.Getpid()` scratch file paths with `writeTempFile` using `os.CreateTemp`. All temporary files are cleaned up with `defer os.Remove(...)`. No `os.Getpid()` remains in the package. Pin `TestBuildTitleCard_ConcurrentScratchFiles` tests 12 concurrent goroutines in the same directory and confirms no races or collisions. | **CLOSED** (0 residue) |
| 2 | **M1** (`command.go:42-47`): `escapeFilterPath` used shell-style `'\'''` wrapped in `'...'`, breaking ffmpeg filtergraph parser on paths with single quotes. | **M** | **Resolved.** Rewrote `escapeFilterPath` to escape `\`, `'`, `:`, `,`, `;`, `[`, `]`, and spaces using backslashes without enclosing quotes. `TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg` executed against real ffmpeg in 0.92s with quotes, colons, and spaces in path, verifying exit code 0 and valid MP4 output. | **CLOSED** (0 residue) |
| 3 | **L1** (`command.go:21, 23`): `defaultRunner.Run` returned non-nil `out` alongside non-nil error. | **L** | **Resolved.** `defaultRunner.Run` returns `nil, fmt.Errorf(...)` on all error branches. Pinned by `TestDefaultRunner_NilOutputOnError` and `TestDefaultRunner_ContextCancellation`. | **CLOSED** (0 residue) |
| 4 | **L2** (`Dockerfile:20`): `mwader/static-ffmpeg:7.1` base image was not pinned by sha256 digest. | **L** | **Resolved.** `Dockerfile:20` pinned to `mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4 AS ff`. Pinned by `TestDockerfile_FFmpegPinnedByDigest`. | **CLOSED** (0 residue) |

---

## Mutation and Pin Robustness Analysis

Each pin was audited for its ability to catch regressions if the remediation were reverted:

1. **H1 Mutant (Revert to `os.Getpid()` in `BuildTitleCard`):**
   - Under `title-%d.txt`, 12 concurrent goroutines share `title-<pid>.txt`. The first goroutine to complete executes `defer os.Remove(titleFile)`, deleting the file while other goroutines are attempting to read it, or runner reads another goroutine's overwritten title.
   - `TestBuildTitleCard_ConcurrentScratchFiles` deterministically catches this by having the mock runner inspect the textfile path and assert all 12 distinct titles are read. Reverting causes `no such file or directory` or missing title assertions.

2. **M1 Mutant (Revert to shell-style `'\'''` in `escapeFilterPath`):**
   - When a path containing single quotes is passed to drawtext inside single quotes, ffmpeg's filtergraph parser terminates the quote early and fails with exit code 254 (`Cannot read file: No such file or directory`).
   - `TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg` invokes real ffmpeg with a path containing single quotes, colons, and spaces (`child's_stories:and spaces/page'01.jpg`). Reverting triggers exit 254 from ffmpeg.

3. **L1 Mutant (Revert `defaultRunner.Run` to `return out, err`):**
   - `TestDefaultRunner_NilOutputOnError` executes `sh -c "echo 'fail output'; exit 1"`. If `out` is returned alongside `err`, `out != nil` triggers immediate `t.Errorf`.

4. **L2 Mutant (Revert `Dockerfile` to unpinned `FROM mwader/static-ffmpeg:7.1 AS ff`):**
   - `TestDockerfile_FFmpegPinnedByDigest` reads `Dockerfile` and asserts presence of the exact pinned sha256 string. Reverting causes an immediate string mismatch failure.

---

## Architectural & Style Compliance Check

- **Ownership Seam (AGENTS.md):**
  Track T10a owns `internal/bookvideo/**` and ffmpeg lines in `Dockerfile`.
  `git status --porcelain` shows modifications only to `Dockerfile` and `internal/bookvideo/**` (plus sibling documentation in `dev-diary/PLAN.md` / `dev-diary/adversarial-review/**`). Zero code touches outside owned paths.
- **Go Style Rules (AGENTS.md):**
  - Small structs by value (`Config`, `Input`, `PageInput`).
  - `ctx context.Context` is the first parameter of all I/O functions.
  - Exported interfaces accepted (`Runner`); concrete types returned.
  - Zero pointer-to-pointer (`**`), pointer-to-slice (`*[]`), or pointer-to-interface (`*Runner`).
  - No `interface{}` used; `any` spelling respected.
  - Every exported identifier has a doc comment beginning with its own name.
  - Every `_ = f()` statement has an explanatory comment on the same line.
  - No `panic` or `log.Fatal` outside `main`.
  - Sentinels wrapped with `%w` (`ErrInvalidInput`, `ErrFFmpegNotFound`, `ErrFFmpegFailed`).
- **Concurrency:**
  - `errgroup.WithContext(ctx)` with `g.SetLimit(resolved.Concurrency)`.
  - Clean exit paths and context cancellation propagation.

---

## Verification Gates Output

All automated checks run from the clean working tree:

### 1. `go test -race -cover ./internal/bookvideo/...`
```console
ok  	thutapi/internal/bookvideo	3.641s	coverage: 89.1% of statements
```
*(Package floor is 75%; actual is 89.1%.)*

### 2. `go test -count=1 ./... -race`
```console
ok  	thutapi/cmd/thutapi	10.133s
ok  	thutapi/internal/bookvideo	3.832s
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	6.048s
ok  	thutapi/internal/gmi/text	1.856s
ok  	thutapi/internal/illustrate	1.923s
ok  	thutapi/internal/interview	2.553s
ok  	thutapi/internal/job	1.436s
ok  	thutapi/internal/mediastore	1.917s
ok  	thutapi/internal/store	2.319s
ok  	thutapi/internal/story	1.057s
ok  	thutapi/internal/stream	16.487s
```

### 3. `go vet ./...`
```console
(clean, no output)
```

### 4. `gofmt -l .`
```console
(clean, no output)
```

---

## Zero-Residue Claim

- **Critical (C):** 0 remaining.
- **High (H):** 0 remaining (H1 fully remediated with `os.CreateTemp`).
- **Medium (M):** 0 remaining (M1 fully remediated with level-1/2 filtergraph escaping).
- **Low (L):** 0 remaining (L1 and L2 remediated and pinned).

There is **zero residue** against all findings from Round 1. No new defects or regressions were found.

---

## Final Verdict

**APPROVE (0/0/0/0)** — Track T10a is approved for landing on `main`.
