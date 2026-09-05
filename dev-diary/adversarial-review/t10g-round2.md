# T10g round 2 — adversarial re-review of the round-1 remediation

| | |
|---|---|
| **Target** | Track T10g (PLAN.md §T10g, §T10f activation annotation at :2176-2185) — round-1 remediation of `t10g-round1.md` (0 C / 0 H / 1 M / 1 L → REMEDIATE): **F1 (M)** migrate the stale 1080×1350 geometry pin in the T10e live gate; **F2 (L)** rewrite the exported `Config.FontFile` doc to match `materializeFont`. Working tree on `main` @ `b5fcfe5` + the uncommitted T10g delta (implementation + remediation + review records). |
| **Reviewer** | Fresh adversarial Review Agent (`T10gRound2Reviewer`), 2026-09-06. **No prior T10g round** — round-1 and remediation records read as claims, never as evidence; every closure re-verified independently (md5s, mutants re-applied, gates re-run). |
| **Shared tree** | Operator's T9 lane is mid-flight in the same tree (`internal/interview/**`, `static/**`, `internal/web/**`, `cmd/thutapi/main.go`, t9-*.md records) — external; observed, not reviewed, not touched. T9 churn during this review was limited to test-process flakiness in `internal/interview` under full-suite parallel `-race` load (see §Gates). |
| **Method** | READ-ONLY except this verdict file and transient byte-identical mutation re-runs. No live GMI calls (the T10e live probe is an operator step; judged statically + via the non-live real-ffmpeg test). Every mutant applied as a single-token replace, pin run, file restored from `/tmp` backups with md5 verification. |
| **Verdict** | **APPROVE — 0 × C, 0 × H, 0 × M, 0 × L** |

---

## 1. F1 closure (M) — the live gate now pins the master geometry the pipeline emits

| Check | Evidence | Result |
|---|---|---|
| Migrated pin reads the new geometry | `internal/bookgen/live_test.go:1002-1003`: `if s.Width != 1080 \|\| s.Height != 1620 { t.Errorf("video dimensions = %dx%d, want 1080x1620", …) }` — file md5 `1610b1f4…`, exactly the remediation record's claimed final md5 (was `cb5f3f91…`) | **PASS** |
| Nothing else in the live probe pins the old geometry | grep of the whole file for `1350\|1080\|1620\|Width !=\|Height !=\|dimensions` → **only lines 1002-1003** carry a geometry assertion | **PASS** |
| The migrated pin asserts what the real pipeline emits | Geometry constants `caption.go:16-19`: `frameWidth 1080`, `frameHeight 1620`, art 1080×1350 + band 270; real-ffmpeg non-live smoke `TestRender_RealFFmpeg` probes the full concat output as `h264,1080,1620` (this round: **PASS**, log `ffprobe stream output: h264,1080,1620`) | **PASS** |
| Probe renders through the same real pipeline | `live_test.go:645`: `realVideo := NewFFmpegRenderer(bookvideo.Config{})` — empty config, so the embedded-font default path is exercised exactly as in production; the migrated assertion inspects the probed frame loop that follows a genuine `Render` | **PASS** |
| File vet-compiles under the live tag | `go vet -tags live ./internal/bookgen` → **exit 0** | **PASS** |
| Stale pin is red on the correct output (the round-1 defect class) | **Mutant G**: probe expectation in the non-live real-ffmpeg smoke reverted to `1350` (bookvideo_test.go:833) → **RED**: `expected 1080x1620 in ffprobe output, got h264,1080,1620` — the real render is 1080×1620 and a 1350 pin fails on it, exactly F1's mechanism | **PASS** (mutant RED, restored byte-identical `6812a4f4`) |

The operator's T10e live re-run (§T10g PLAN.md:2281-2283) now passes on the correct output. F1 is **closed**.

## 2. F2 closure (L) — the FontFile doc matches materializeFont; no stale fontconfig claims remain

| Check | Evidence | Result |
|---|---|---|
| Doc says empty → embedded face materialised into the workdir | `types.go:66-72` (md5 `2b4bcf64…` = the remediation record's final md5): "If empty, the package's embedded Fredoka-Regular instance is materialised into the render workdir and passed as `fontfile=` to every drawtext — the runtime image ships no fonts and no fontconfig config, so there is no system fallback (§T10g D4). An explicit path overrides the embedded default." | **PASS** |
| Doc matches the code exactly | `materializeFont` (video.go:45-60): empty → `writeTempFile(dir, "fredoka-*.ttf", fredokaRegularTTF)` in the render workdir; `buildFontOpt` (video.go:64-66) prepends `fontfile=` (filter-path-escaped) **unconditionally**; `Render` materialises once and sets `resolved.FontFile = fontPath` (video.go:447-455); every drawtext (title, byline, caption, end-card ×2) carries `fontfile=` — pinned raw by `TestBuildTitleCard_WithFontFile`, `TestBuildPageSegment_ArgumentConstruction` ("fontfile=", bookvideo_test.go:309-310) and the end-card raw args | **PASS** |
| Explicit path overrides | `resolveConfig` stat-checks a non-empty `FontFile` (video.go:31-35); `materializeFont` returns it untouched; `TestBuildTitleCard_WithFontFile` pins the override | **PASS** |
| No other stale fontconfig claims in the package docs | grep of `internal/bookvideo` for `fontconfig\|system font\|defaults\|FontFile\|fontfile` → only accurate statements: fonts.go:20-24 ("The runtime image carries no fonts and no fontconfig config … An explicit Config.FontFile overrides the embedded default"), video.go:47, and the fixed types.go doc. The old "If empty, ffmpeg uses system fontconfig defaults" sentence is gone | **PASS** |
| Doc revert is prose-only | Reverting the doc cannot change any branch, value or signature (it is a comment block) — verified by inspection, no runnable pin exists or is needed | **PASS** |

F2 is **closed**.

## 3. Round-1 contract rows and mutation battery — no behavioural drift (md5 evidence)

Remediation's touch scope claim is **exactly two code files + the record**: `internal/bookgen/live_test.go` and `internal/bookvideo/types.go`. Everything else on the T10g side is byte-identical to the round-1-reviewed bytes:

| File | Round-1 manifest md5 | This round md5 | Drift |
|---|---|---|---|
| `internal/bookvideo/video.go` | `13d59ca8d830a032b8e876ce5b53eb40` | `13d59ca8d830a032b8e876ce5b53eb40` | **none** |
| `internal/bookvideo/caption.go` | `c5683108c9cffb449f4fb2c99104e4f2` | `c5683108c9cffb449f4fb2c99104e4f2` | **none** |
| `internal/bookgen/pipeline.go` | `f7c161d6b7b1c06ef775ed4188a6d4f3` | `f7c161d6b7b1c06ef775ed4188a6d4f3` | **none** |
| `internal/bookgen/bookgen.go` | `fd4968d685f754d9df45c2bda9a60d62` | `fd4968d685f754d9df45c2bda9a60d62` | **none** |
| `internal/bookvideo/types.go` | (round-1: `1547f7b5…`) | `2b4bcf64…` | F2 doc only |
| `internal/bookgen/live_test.go` | (round-1: `cb5f3f91…`) | `1610b1f4…` | F1 assertion only |

The four behavioural files carrying contract rows 1-10 are byte-identical to what round 1 ruled PASS row-by-row (with mutants M1-M6/M8/M9 red), so those rulings transfer on identical bytes. The two changed files are doc/assertion-only: `types.go` changed a comment block; `live_test.go` changed one comparison operand under `//go:build live` (never compiled into a normal build). No behavioural drift is possible from either.

Round-1's ten-row table and mutation battery therefore still hold; this round re-applied four spot mutants to the live tree to confirm the pins are live, with byte-identical restores:

| # | Mutant | Pin | Result | Restore md5 |
|---|---|---|---|---|
| **G** | Geometry probe expectation reverted `1620` → `1350` (the F1 stale-pin shape, bookvideo_test.go:833) | `TestRender_RealFFmpeg` | **RED** — `expected 1080x1620 in ffprobe output, got h264,1080,1620`: real output is the migrated pin's 1080×1620; a stale 1350 pin fails it | `6812a4f4…` OK |
| **M4** | End-card color source diverges `frameHeight` → `artHeight` (video.go:324) | `TestBuildEndCard_ArgumentConstruction` | **RED** — `missing "color=c=0x12241e:s=1080x1620:r=25"` (mutant emitted `s=1080x1350`); shared-geometry constant still enforced at the call site | `13d59ca8…` OK |
| **M8** | renderFilm nil-clips guard reverted `if clips != nil && len(clips)…` → `if len(clips)…` (pipeline.go:266) | `TestPipeline_NarrationTransientOutageProducesCaptionedSilentFilm` | **RED** — `event = "failed" ({}), want "book_ready"`: the outage run must succeed with the captioned-silent film | `f7c161d6…` OK |
| **M9** | Page `Text` dropped from renderFilm's `PageInput` (pipeline.go:282) | same outage pin | **RED** — `render input page 1 text = "", want the page's own words "Page 1 of the story."`: the words reach the renderer on the outage path | `f7c161d6…` OK |
| **F2** | FontFile doc reverted (prose-only) | — | verified by inspection (§2): the only way to revert it is to re-write the comment; no code path observes it | — |

## 4. Gates

| Gate | Command | Result |
|---|---|---|
| Format (T10g) | `gofmt -l internal/bookvideo/*.go internal/bookgen/*.go` | **PASS** — 0 files |
| Format (tree) | `gofmt -l .` | **FAIL (external)** — `internal/interview/http.go`, `internal/interview/interview.go` (the T9 lane's own mid-flight files, the same two observed in round 1 and the remediation); no T10g file listed |
| Vet | `go vet ./...` | **PASS** |
| Vet (live tag) | `go vet -tags live ./internal/bookgen` | **PASS** — the migrated probe vet-compiles under `-tags live` |
| Build | `go build ./...` | **PASS** |
| Workspace | `go test ./... -race -count=1` | **FAIL, external flake** — 16/17 packages green; `internal/interview` `TestRestartedHandlerSeesEndedInterview` timed out (5 s poll) in both full-suite runs this round yet **passes standalone** (1/1) on this exact tree. `internal/interview` is the T9 lane's package: T10g's delta touches neither it nor any package it imports, and both T10g packages (`bookvideo`, `bookgen`) are green in every run. Attributed external (T9-lane mid-flight + full-suite parallel `-race` load); round 1's 17/17 was a different T9 snapshot. T10g-side gate: **PASS** |
| Touched packages 5× race | `go test ./internal/bookvideo ./internal/bookgen -race -count=5` | **PASS** — bookvideo ok, bookgen ok |
| Coverage | `go test ./internal/bookvideo ./internal/bookgen -cover` | **PASS** — bookvideo **88.2%**, bookgen **81.8%** — identical to the round-1/remediation claims (≥75% floor) |
| Real-ffmpeg smoke | `go test ./internal/bookvideo -run 'TestRender_RealFFmpeg\|TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg'` | **PASS** — full concat `-c copy` at 1080×1620, probed `h264,1080,1620`, captions rendered |
| Font byte-identity | md5 bookvideo vs bookpdf Fredoka-Regular.ttf, OFL ×vendor | **PASS** — `e980bc2abae3afa14c6d56a1f0555516` (bookvideo == bookpdf's round-3-audited instance), OFL `21f5400b…` (bookvideo == static/vendor) |
| Tree state | `git status --porcelain` + md5 of all mutated files | **PASS** — 15 modified + 21 untracked, byte-identical to the pre-review survey; every mutant restore md5-verified; the only addition is this record |

## 5. Zero-residue claim

Against round 1's two findings, residue is **zero**:

- **F1 (M)** — closed: the live gate's geometry pin at `live_test.go:1002-1003` now asserts 1080×1620 (md5 `1610b1f4…`), matches the geometry constants (`caption.go:16-19`), matches the real pipeline's probed output (`h264,1080,1620` via the non-live real-ffmpeg test), is the only geometry assertion left in the probe file, and vet-compiles under `-tags live`. Mutant G proves the round-1 defect class (stale 1350 pin → RED on the correct render) is gone. The live probe's execution remains the operator's T10e step — the migrated pin is what makes that step pass on correct output rather than fail on it.
- **F2 (L)** — closed: `types.go:66-72` documents empty-`FontFile` → embedded Fredoka-Regular materialised into the render workdir, `fontfile=` on every drawtext, explicit path override — each clause matches `materializeFont`/`buildFontOpt`/`Render` exactly, and no other stale fontconfig claim remains in the package docs.
- No new findings of any severity were introduced by the remediation (the two touched code files are doc/assertion-only; all four behavioural files byte-identical to their round-1-reviewed md5s). No finding in this round against the ten contract rows, the outage flip, the geometry/typography implementation, or the pins.

**VERDICT: APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.** Round-1 residue zero (F1 M and F2 L closed and re-verified above); no new findings; gates green on the T10g side (the only workspace failure is the external T9-lane `internal/interview` flake under parallel load, which passes standalone); tree carries no T10g-side writes beyond this record.
