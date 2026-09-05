# T10a round 1 — remediation

| | |
|---|---|
|**Target**|The four round-1 findings of `t10a-round1.md` (0 C / 1 H / 1 M / 2 L) across `internal/bookvideo/**` and `Dockerfile`.|
|**Date**|2026-09-05. Remediation agent fresh to the round.|
|**Status**|COMPLETE — all four findings remediated; each pin verified red under its re-applied mutant; all CI gates green. Zero residue is claimed only after round 2's verdict.|

---

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **H1** (`internal/bookvideo/video.go:68, 77, 182, 188, 249`): Scratch text/list files used `os.Getpid()` (`title-%d.txt`, `byline-%d.txt`, `end_title-%d.txt`, `end_domain-%d.txt`, `concat_list-%d.txt`), causing collisions and deletion races under concurrent calls to exported functions (`BuildTitleCard`, `BuildEndCard`, `ConcatSegments`) in the same process/workDir. | Added unexported helper `writeTempFile(dir, pattern string, data []byte) (string, error)` using `os.CreateTemp(dir, pattern)` in `command.go`. Replaced all five `os.Getpid()` scratch file paths in `video.go` with unique temporary files via `writeTempFile` (`title-*.txt`, `byline-*.txt`, `end_title-*.txt`, `end_domain-*.txt`, `concat_list-*.txt`) with `defer os.Remove(...)` for clean removal. Added pin test `TestBuildTitleCard_ConcurrentScratchFiles` running 12 concurrent goroutines in the same `workDir` calling `BuildTitleCard` with distinct titles and verifying all distinct titles are read by the runner without collisions or deletion errors. | **DONE** — red on mutant (below), green on pristine tree |
| 2 | **M1** (`internal/bookvideo/command.go:42-47`): `escapeFilterPath` used shell idiom `'\'''` wrapped in `'...'`, breaking ffmpeg's filtergraph parser on any path containing a single quote `'` with exit code 254 (`Cannot read file: No such file or directory`). | Rewrote `escapeFilterPath` to use ffmpeg filtergraph parameter escaping without enclosing quotes: single quotes are escaped with `\\\'`, colons with `\\:`, backslashes with `\\\\`, and filtergraph delimiters/spaces (` `, `,`, `;`, `[`, `]`) with `\`. Concat demuxer escaping in `escapeConcatPath` remains enclosed in single quotes per the `ffconcat` specification. Updated `TestEscapeHelpers` and added integration pin test `TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg` verifying that `BuildTitleCard` with real ffmpeg succeeds on directories and filenames containing single quotes, colons, and spaces (`child's_stories:and spaces/page'01.jpg`). | **DONE** — red on mutant (below), green on pristine tree |
| 3 | **L1** (`internal/bookvideo/command.go:21, 23`): `defaultRunner.Run` returned non-nil `out` byte slice alongside non-nil `error`, violating AGENTS.md §Go style ("Never return a non-nil value alongside a non-nil error"). | Changed `defaultRunner.Run` to return `nil, fmt.Errorf(...)` on both error branches (`ctx.Err() != nil` and execution error). Updated `TestDefaultRunner_ContextCancellation` and added unit test `TestDefaultRunner_NilOutputOnError` asserting that `out == nil` when `err != nil` for both missing executables and commands that exit non-zero with combined output. | **DONE** — red on mutant (below), green on pristine tree |
| 4 | **L2** (`Dockerfile:20`): `mwader/static-ffmpeg:7.1` base image was not pinned by sha256 digest, violating PLAN.md §T10 explicit track ownership ("pinning the ffmpeg base image by digest"). | Pinned `Dockerfile:20` to `mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4 AS ff`. Added automated check `TestDockerfile_FFmpegPinnedByDigest` asserting that `Dockerfile` contains the exact `@sha256:` digest pin. | **DONE** — red on mutant (below), green on pristine tree |

---

## Mutation verification — re-running mutants against pins

Each mutant was applied individually to verify that the corresponding pin test goes RED on reversion, then restored to verify that the pin returns to GREEN.

| Finding / Pin | Mutant applied | Result on pin | Restore verification |
|---|---|---|---|
| **H1**<br>`TestBuildTitleCard_ConcurrentScratchFiles` | Reverted `BuildTitleCard` in `video.go` to use `title-%d.txt, os.Getpid()` | **RED — failed:** `concurrent BuildTitleCard failed: bookvideo: ffmpeg command failed: read textfile ".../title-680013.txt": open .../title-680013.txt: no such file or directory` (sibling goroutine's defer deleted the collided file) | Restored `writeTempFile(workDir, "title-*.txt", ...)`: pin **GREEN (0.00s)** |
| **M1**<br>`TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg` & `TestEscapeHelpers` | Reverted `escapeFilterPath` in `command.go` to shell-style `'\'''` enclosed in `'...'` | **RED — failed:** `TestEscapeHelpers` failed on quotes and colons; `TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg` failed with ffmpeg exit 254: `Cannot read file '.../childs_stories\:and spaces/title-3696427407.txt:fontcolor=white:fontsize=56...': No such file or directory` | Restored filtergraph escaping: pins **GREEN (0.00s / 0.97s)** |
| **L1**<br>`TestDefaultRunner_NilOutputOnError` | Reverted `defaultRunner.Run` in `command.go` to return `out, fmt.Errorf(...)` | **RED — failed:** `out = [102 97 105 108 32 111 117 116 112 117 116 10], want nil slice on error when command produced output` | Restored `return nil, fmt.Errorf(...)`: pin **GREEN (0.00s)** |
| **L2**<br>`TestDockerfile_FFmpegPinnedByDigest` | Reverted `Dockerfile:20` to `FROM mwader/static-ffmpeg:7.1 AS ff` (omitting `@sha256:...`) | **RED — failed:** `Dockerfile does not contain expected pinned ffmpeg base image line: want: FROM mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4 AS ff` | Restored sha256 digest pin: pin **GREEN (0.00s)** |

---

## Gates

All automated verification checks run from a clean tree pass cleanly:

| Gate | Result |
|---|---|
| `go vet ./...` | **clean** (no diagnostics across all packages) |
| `gofmt -l .` | **clean** (zero diffs across repo) |
| `go test ./... -race` | **PASS** (all packages pass under race detector) |
| `go test -cover ./internal/bookvideo/...` | **89.1%** statement coverage (exceeds 75% package floor) |
| Dockerfile digest pin | Pinned to `@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4` |

---

## Residue note

- All 4 findings (H1, M1, L1, L2) are fully remediated inside the track's Owned paths (`internal/bookvideo/**` and `Dockerfile`).
- No changes made outside the track's Owns boundaries (zero scope creep).
- In accordance with AGENTS.md, the remediation agent does not alter the review verdict. Round 2 review must independently audit the remediation and confirm zero residue before issuing an APPROVE verdict.
