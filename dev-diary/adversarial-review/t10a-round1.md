# T10a round 1 — adversarial review of `internal/bookvideo` and `Dockerfile`

| | |
|---|---|
| **Target** | Track T10a, uncommitted working tree on `main` at base `6664be3`. Scope: `Dockerfile` (ffmpeg multi-stage lines) and `internal/bookvideo/**` (`types.go`, `command.go`, `video.go`, `bookvideo_test.go`). |
| **Evidence** | `AGENTS.md` in full; `dev-diary/PLAN.md` §T10, §T10a, §Architectural invariants; `dev-diary/adversarial-review/t10-video-record.md` (runs 1–3, verified recipe, verification gaps); `go test -race -cover ./internal/bookvideo/...` (90.0% coverage); `go vet ./...` (clean); `gofmt -l .` (clean); python & ffmpeg filtergraph probes on quote escaping and concurrency. |
| **Method** | Adversarial audit against AGENTS.md, PLAN.md §T10, and t10-video-record.md. Tested recipe conformance (1080x1350, `-shortest`, blur title card, flat end card, concat `-c copy` + tags). Executed offline environment test (binary missing skips `TestRender_RealFFmpeg` cleanly). Tested filtergraph escaping against ffmpeg CLI to identify escaping mismatches. Tested concurrent invocations of exported functions to verify scratch file safety. |
| **Date** | 2026-09-05. Reviewer: fresh adversarial review agent. No remediation attempted; review only. |

**Verdict: REMEDIATE — 0 × C, 1 × H, 1 × M, 2 × L.**

---

## Summary of the diff

Track T10a implements the book video generation subsystem (`internal/bookvideo`) and adds static ffmpeg to the multi-stage Docker build:

1. **`Dockerfile`**: Added `FROM mwader/static-ffmpeg:7.1 AS ff` and copied `/ffmpeg` into `/usr/local/bin/ffmpeg` in the final distroless runtime.
2. **`internal/bookvideo/types.go`**: Config, input, and page structs (`Config`, `Input`, `PageInput`), sentinels (`ErrInvalidInput`, `ErrFFmpegNotFound`, `ErrFFmpegFailed`), and `Runner` interface.
3. **`internal/bookvideo/command.go`**: Default runner wrapping `exec.CommandContext`, filter/concat path escaping helpers, byline formatting, and atomic/fallback file copy helper (`copyOrMoveFile`).
4. **`internal/bookvideo/video.go`**:
   - `BuildTitleCard`: Page 1 through `boxblur=18:2,eq=brightness=-0.22:saturation=0.8` with centered title and byline via `textfile=`, stereo silence audio, 1080x1350 letterboxed/padded to `0x1b1614`.
   - `BuildPageSegment`: Encodes single page image with its narration audio, `-shortest` timing, x264/aac, 1080x1350, `+faststart`.
   - `BuildEndCard`: Flat `0x1b1614` with "Made with Thutapi" and domain, stereo silence audio, `-shortest`.
   - `ConcatSegments`: Demuxer concat (`-f concat -safe 0 -i list.txt -c copy -movflags +faststart`) and MP4 metadata tagging (`title`, `artist=Thutapi`, `comment`).
   - `Render`: End-to-end pipeline orchestrating materialized assets, bounded parallel rendering via `errgroup.SetLimit(concurrency)`, and final assembly into `in.OutputPath`.
5. **`internal/bookvideo/bookvideo_test.go`**: Unit test suite with mock runners and real ffmpeg integration test (`TestRender_RealFFmpeg`, skipped if ffmpeg is missing). Statement coverage is **90.0%**.

The recipe fidelity to `t10-video-record.md` is high and the demo renders cleanly. However, four findings were identified across concurrency safety, filtergraph escaping, error conventions, and Dockerfile digest pinning.

---

## Severity counts

**0 C / 1 H / 1 M / 2 L**

---

## Findings table

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| 1 | **H** | `internal/bookvideo/video.go:68, 77, 182, 188, 249` | Scratch text/list files use `os.Getpid()` (`title-%d.txt`, `concat_list-%d.txt`), causing collisions and deletion races when exported functions are called concurrently in the same process | Concurrent goroutines calling `BuildTitleCard` with distinct titles in the same `workDir` collide on `title-<pid>.txt` and fail when `defer os.Remove` deletes the file | Replace `os.Getpid()` with `os.CreateTemp` or unique random/counter IDs; reverting to `os.Getpid()` reintroduces the race |
| 2 | **M** | `internal/bookvideo/command.go:42-47` | `escapeFilterPath` uses shell idiom `'\'''` wrapped in `'...'`, which breaks ffmpeg filtergraph parser whenever a path contains a single quote `'` | Call `BuildTitleCard` with `workDir` containing `'` (e.g. `/tmp/user's_dir`) and observe ffmpeg exit 254 (`Cannot read file: No such file or directory`) | Replace shell-style escaping with ffmpeg-native filtergraph parameter escaping; reverting causes quote-containing paths to fail |
| 3 | **L** | `internal/bookvideo/command.go:21, 23` | `defaultRunner.Run` returns non-nil `out` alongside non-nil `error`, violating AGENTS.md §Go style ("Never return a non-nil value alongside a non-nil error") | Assert in test that `defaultRunner.Run` returns `nil` slice when error is non-nil | Change `return out, err` to `return nil, err`; reverting returns non-nil slice alongside error |
| 4 | **L** | `Dockerfile:20` | `mwader/static-ffmpeg:7.1` is unpinned by sha256 digest, violating PLAN.md §T10 explicit track ownership ("pinning the ffmpeg base image by digest") | Inspect `Dockerfile:20` for `@sha256:` | Pin to `mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4`; reverting leaves image unpinned |

---

## Detailed findings

### H1 — Scratch text and list files use `os.Getpid()`, colliding under concurrent execution

**Severity:** H. (Real defect; `Render` creates an isolated `os.MkdirTemp`, so full renders avoid colliding, but exported functions `BuildTitleCard`, `BuildEndCard`, and `ConcatSegments` race under concurrent usage.)

**Where:**
- `internal/bookvideo/video.go:68`: `filepath.Join(workDir, fmt.Sprintf("title-%d.txt", os.Getpid()))`
- `internal/bookvideo/video.go:77`: `filepath.Join(workDir, fmt.Sprintf("byline-%d.txt", os.Getpid()))`
- `internal/bookvideo/video.go:182`: `filepath.Join(workDir, fmt.Sprintf("end_title-%d.txt", os.Getpid()))`
- `internal/bookvideo/video.go:188`: `filepath.Join(workDir, fmt.Sprintf("end_domain-%d.txt", os.Getpid()))`
- `internal/bookvideo/video.go:249`: `filepath.Join(workDir, fmt.Sprintf("concat_list-%d.txt", os.Getpid()))`

**What:** In Go, all goroutines in a process share the same process ID (`os.Getpid()`). When callers invoke exported helpers (`BuildTitleCard`, `BuildEndCard`, `ConcatSegments`) concurrently in the same working directory (or with `workDir == ""`, defaulting to `filepath.Dir(outPath)` where outputs share a target folder):
1. Goroutine A writes `title-<pid>.txt` with title A.
2. Goroutine B overwrites `title-<pid>.txt` with title B.
3. Goroutine A executes ffmpeg, reading title B instead of title A.
4. Goroutine A exits and executes `defer os.Remove(titleFile)`, deleting `title-<pid>.txt`.
5. Goroutine B executes ffmpeg and fails with `The text file 'title-<pid>.txt' could not be read or is empty`.

Temporary files should use unique random names (`os.CreateTemp(workDir, "title-*.txt")` or a random suffix) rather than the process PID.

**Pin:** Run two concurrent goroutines calling `BuildTitleCard` with different titles into the same `workDir` and observe text corruption and `os.ErrNotExist` / ffmpeg exit failures.

**Mutation:** Replace `fmt.Sprintf("...-%d.txt", os.Getpid())` with unique filenames via `os.CreateTemp` (or unique random suffixes). Reverting to `os.Getpid()` reintroduces the collision.

---

### M1 — `escapeFilterPath` implements shell-style escaping, breaking ffmpeg on paths with single quotes

**Severity:** M. (Real defect with workaround: standard server paths without single quotes work, but any path with a single quote crashes ffmpeg filtergraph parsing.)

**Where:** `internal/bookvideo/command.go:42-47`
```go
func escapeFilterPath(p string) string {
	s := strings.ReplaceAll(p, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `'\''`)
	s = strings.ReplaceAll(s, `:`, `\:`)
	return "'" + s + "'"
}
```

**What:** In POSIX shells (bash/sh), `'foo'\''bar'` escapes a single quote by closing the single-quoted string, inserting an escaped literal `'`, and reopening single quotes.
However, `bookvideo` invokes ffmpeg via `exec.CommandContext` without a shell. Inside ffmpeg's filtergraph parser:
- An option value enclosed in single quotes terminates at the *first* unescaped single quote.
- `'/path/with'\''quote'` is parsed as string `'/path/with'`, followed by unquoted backslash `\`, followed by `'quote'`.
- Any subsequent colons (e.g. `:fontcolor=white:fontsize=...`) are no longer protected by quotes and are parsed as unquoted parameter delimiters or swallowed into the filename, causing ffmpeg to abort with exit code 254:
  `[Parsed_drawtext_0] [FILE] Cannot read file '/path/withquote:fontcolor=white:fontsize=56...': No such file or directory`.

`TestEscapeHelpers` in `bookvideo_test.go` only asserted that the helper enclosed the string in quotes, but never passed the result to ffmpeg to verify that ffmpeg accepted it.

**Pin:** Call `BuildTitleCard` where `WorkDir` or `FontFile` has a single quote in its path (e.g. `tmpDir + "/user's_dir"`) and verify that ffmpeg exits with code 254.

**Mutation:** Update `escapeFilterPath` to use ffmpeg filtergraph escaping (e.g. without enclosing `'...'`, escaping `:`, `\`, and `'` with `\` or correct ffmpeg filtergraph level-1/level-2 escaping). Reverting to shell-style `strings.ReplaceAll(s, "'", "'\\''")` breaks on single quotes.

---

### L1 — `defaultRunner.Run` returns non-nil slice alongside non-nil error

**Severity:** L. (Style / invariant defect.)

**Where:** `internal/bookvideo/command.go:21, 23`
```go
func (defaultRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return out, fmt.Errorf("%w: %w", ErrFFmpegFailed, ctx.Err())
		}
		return out, fmt.Errorf("%w: %v (%s)", ErrFFmpegFailed, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
```

**What:** AGENTS.md §Go style explicitly mandates:
> **Never return a non-nil value alongside a non-nil error.** A caller must be able to trust that `err != nil` means the rest is meaningless. (`t2-round3.md` L3.)

When `cmd.CombinedOutput()` fails, `out` contains any stdout/stderr bytes produced before failure. Returning `out` alongside the error violates this invariant. On error, `defaultRunner.Run` must return `nil, fmt.Errorf(...)`.

**Pin:** In a unit test, call `defaultRunner.Run` with a failing command that produces output and assert `out == nil` when `err != nil`.

**Mutation:** Change `return out, fmt.Errorf(...)` to `return nil, fmt.Errorf(...)`. Reverting returns a non-nil byte slice alongside error.

---

### L2 — `Dockerfile` static ffmpeg base image is not pinned by sha256 digest

**Severity:** L. (Packaging / reproducibility requirement.)

**Where:** `Dockerfile:20`
```dockerfile
FROM mwader/static-ffmpeg:7.1 AS ff
```

**What:** `PLAN.md` §T10 explicitly names the gaps this track still owns:
> *"What that record does not cover, and this track therefore still owns: ... pinning the ffmpeg base image by digest ..."*

Tag `7.1` is mutable upstream and susceptible to upstream registry updates. It should be pinned with its immutable digest:
`mwader/static-ffmpeg:7.1@sha256:a8090df5f5608daef387e1b2e93b98aaacb4d92153ad904e7d715c725724fca4`.

**Pin:** Automated check asserting `Dockerfile` contains `@sha256:` on the ffmpeg `FROM` line.

**Mutation:** Update `Dockerfile:20` to include `@sha256:...`. Reverting leaves it as mutable tag `7.1`.

---

## Verdict and next step

**Verdict: REMEDIATE (0/1/1/2).**
Track T10a cannot advance to APPROVE until all findings across all severities are remediated by a remediation agent in `t10a-remediation-round1.md` and re-evaluated in Round 2.
