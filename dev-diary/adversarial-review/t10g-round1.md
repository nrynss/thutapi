# T10g round 1 — implementation record

**Track:** T10g — the words on the page (PLAN.md §T10g).
**Agent:** T10gImplementer. **Base:** `main` @ `a94accb`.
**Date:** 2026-09-06.
**Status:** implemented, gates run locally (see §Gates). Awaiting adversarial review.

---

## Contract rows (file:line:change:reason)

T10g owns its own caption lines inside closed T10a's `internal/bookvideo`
package and the line-wrap helper they need, plus — because the PLAN §T10g
activation annotation and §T10f "Superseded in part by §T10g" block make it a
spec requirement — the bookgen outage-branch flip (film always rendered; the
outage costs voices, not the video). Rows below name file, line (post-edit),
change and reason, per the T7-into-T6 / T10d-into-mediastore mechanism.

| # | Where (post-edit) | Change | Reason |
| --- | --- | --- | --- |
| 1 | `internal/bookvideo/video.go` BuildPageSegment signature + body | Page segment accepts per-page text, audio becomes optional (silent tier synthesises anullsrc of a words-derived hold), art scale/crop 1080×1350 + surface band 1080×270 + caption drawtext | T10g geometry (2:3 master) and captions; silent-caption tier (§T10g) |
| 2 | `internal/bookvideo/video.go` BuildTitleCard | Frame 1080×1620, blurred page-1 art cover-crops the frame; drawtext uses the embedded Fredoka Regular | Master geometry incl. cards; no font exists in the runtime image, drawtext without fontfile fails in-container (verified) |
| 3 | `internal/bookvideo/video.go` BuildEndCard | Frame 1080×1620 on `--film` `0x12241e` (was `0x1b1614`) | Card ground moves to §The look's `--film` (§T10g) |
| 4 | `internal/bookvideo/types.go` PageInput | New `Text` field | Page words reach the caption band |
| 5 | `internal/bookvideo/types.go` / `command.go` geometry constants | One geometry constant set used by every segment builder | concat `-c copy` agreement |
| 6 | `internal/bookvideo/` new caption layout helpers | rune-count wrap, ≤3 lines, one shrink step, ellipsis truncation; silent hold = words/2.0 clamp [4s,14s] | drawtext does not wrap or centre lines |
| 7 | `internal/bookvideo/` embedded Fredoka-Regular (derived instance + OFL.txt) | FontFile defaults to the embedded face materialised into the render workdir | No font files exist in the runtime image; the vendored variable face renders only its default Light instance under drawtext (measured) |
| 8 | `internal/bookgen/pipeline.go` runBook + renderFilm | Outage (gmi.ErrTransient) → `narration_unavailable {}` once, film STILL rendered captioned-silent; renderFilm accepts nil clips | §T10g three tiers; §T10f activation annotation (both-URLs contract) |
| 9 | `internal/bookgen/bookgen.go` generateJob + bookReadyEvent + package doc | `book_ready` always carries `pdf_url` + `video_url` | T10f activation: outage costs voices, not the video |
| 10 | `internal/bookgen/*_test.go` | Outage pins flipped: renderer called once with silent text pages, video row present, wire has both URLs | pins are T10c's, change is T10g's |

Design decisions with evidence in §Decisions below. Files planned/changed in
§Files. No PLAN.md edit, no live call.

---

## Decisions

(Evidence and rationale — filled during implementation.)

### D1 — geometry

One constant set, applied at every segment builder:

- master frame 1080×1620 (2:3)
- page art area 1080×1350 (4:5, full bleed top)
- caption band 1080×270 below, ground `--surface` `0xd8efe3`
- card ground `--film` `0x12241e` (replaces the pre-look `0x1b1614`)
- all segments `setsar=1,format=yuv420p`, 25 fps, aac 44100 stereo →
  `concat -c copy` stays free

Evidence: composition rendered inside the shipping ffmpeg 7.1 image — art
crop to 1080×1350, pad band below (rows 1350–1619 surface, measured),
caption ink centred at band middle; title card (color source) + jpg page +
end card concat `-c copy` clean at 1080×1620.

### D2 — wrap helper (rune budget, shrink, ellipsis)

drawtext does not wrap. Wrap in Go: rune count (not bytes) against the band
width; break on spaces, never mid-word; max 3 lines. A page whose text needs
more than 3 lines at the base caption size is rendered one step smaller; a
page that still overflows is truncated at a word boundary and the third line
ends with an ellipsis `…`.

Budgets derive from the measured Fredoka-Regular advances (fontTools on the
instance): lowercase+space ≈ 0.50 em, sentence-case live page text ≈
0.47–0.50 em, uppercase ≈ 0.66 em. Layout uses a conservative 0.55 em/rune
planning advance with a 1000 px usable width (40 px side margins each) →

- base caption size 60 px → 30 runes/line → 90 runes in 3 lines
- shrink step 52 px → 34 runes/line → 102 runes in 3 lines

Every one of the eight real T6b page texts (58–95 runes, 10–16 words) wraps
to ≤3 lines (six at base, the two longest at the shrink step; none truncated).
A 25-word worst case needs the shrink step and still fits; only a ~159-rune
page hits the ellipsis. Vertical fit verified in-container: three lines at
fontsize 60/56/64 all sit inside the 270 px band with margins.

### D3 — caption drawtext line

One drawtext per page (never one per line) with `textfile=` (never `text=`),
`expansion=none` (apostrophes, colons, `%` and backslashes are literal),
`text_align=C` (drawtext centres only the block; the flag centres each line),
positioned `x=(w-text_w)/2`, `y=1350+(270-text_h)/2` over the pad band.
Verified in-container: three real lines of differing widths each centred at
x≈540; block centred in the band.

### D4 — font

The runtime image carries **no font files and no fontconfig config** —
verified by running drawtext without `fontfile` inside the runtime-shaped
image (`thutapi-ffprobe-check`): `Cannot find a valid font for the family
Sans`. Production wires `bookvideo.Config{}` (FontFile empty), so every
drawtext in the shipped container would fail without a real `fontfile=`.

The vendored variable face `static/vendor/fonts/Fredoka[wdth,wght].ttf` IS
renderable by drawtext (in-container), but freetype renders only its default
instance — the name table's default is Fredoka **Light** (wght 300), measured
as a visibly thin 5971 ink px vs 8961 px for the instanced Regular at the same
size/string. The PDF body (§T10f) is Fredoka Regular 400; for the film to read
as the same book and keep early-reader legibility, the caption/card face is
Fredoka **Regular 400**.

Because `//go:embed` cannot reach `static/` from `internal/bookvideo`
(t10f-round2 L5 constraint), bookvideo carries its own **single-source-derived
static instance** of the vendored variable face — `wght=400 wdth=100`
instancer command identical to bookpdf's L5 derivation — with the OFL 1.1
licence text beside it, embedded via `//go:embed`. When `Config.FontFile` is
empty (production), the embedded bytes are materialised into the render
workdir before ffmpeg runs and every drawtext (title, byline, caption, end
card) points at that absolute in-container path. An explicit `FontFile`
still overrides (operator/test escape hatch).

### D5 — silent tier + hold

- narration present → identical `-shortest` semantics against the page clip;
  captions add no duration
- narration absent → page segment synthesises `anullsrc` of
  `words/2.0` s clamped to [4 s, 14 s] (2.0 wps = read-aloud-to-a-child
  pace, PLAN §T10g), same geometry/audio layout so concat stays free
- verified in-container: `-loop 1 -t 6` image + `anullsrc -t 6` → exactly
  6.000000 s AAC 44100 stereo segment; concat agreement holds

### D6 — bookgen branch flip (activation)

Narration `gmi.ErrTransient` (503) → `narration_unavailable {}` published
once, narration skipped, but **the captioned silent film is still rendered**,
persisted and attached; `book_ready` carries both `pdf_url` and `video_url`;
non-transient narration failure still fails the run before the PDF stage.
This implements §T10f's activation annotation (PLAN.md:2146-2155) under
T10g's own contract rows into `internal/bookgen`.

---

## Files

- `internal/bookvideo/types.go` — geometry/typography constants, `PageInput.Text`
- `internal/bookvideo/command.go` — (helpers only if needed)
- `internal/bookvideo/video.go` — page/title/end builders, Render, tiers
- `internal/bookvideo/caption.go` (new) — wrap/truncate + hold helpers
- `internal/bookvideo/fonts.go` (new) — embedded Fredoka Regular + OFL
- `internal/bookvideo/fonts/Fredoka-Regular.ttf`, `OFL.txt` (new, derived instance)
- `internal/bookvideo/bookvideo_test.go` (+ caption tests) — updated pins
- `internal/bookgen/pipeline.go` — outage flip, renderFilm nil-clips
- `internal/bookgen/bookgen.go` — event/doc updates
- `internal/bookgen/pipeline_test.go`, `bookgen_test.go` — outage/wire pins
- `dev-diary/adversarial-review/t10g-round1.md` — this record


## Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | PASS — 0 files |
| Vet | `go vet ./...` | PASS |
| Build | `go build ./...` | PASS |
| Workspace | `go test ./... -race -count=1` | PASS — 17/17 packages green |
| Touched packages 5× race | `go test ./internal/bookvideo ./internal/bookgen -race -count=5` | PASS |
| Coverage | `go test ./internal/bookvideo ./internal/bookgen -cover` | PASS — bookvideo **88.2%**, bookgen **81.8%** (≥75% floor) |
| Real ffmpeg smoke | `TestRender_RealFFmpeg` (runs when ffmpeg present) | PASS — 1080×1620 probed; captions rendered |
| Scope | `git status` vs pre-existing T9 work | PASS — only `internal/bookvideo/**`, `internal/bookgen/**` (outage row + pins) and this record; no PLAN.md, no live calls |

## Open items for the reviewer

- Card text colours (white title/byline/end-domain on blurred art / `--film`) were
  left as T10a shipped; §T10g's colour bullet moved only the card *ground* hex
  to `--film` `0x12241e`.
- The title card geometry changed from art-fill-1350 to art + film pad at 1620;
  the blur/darken recipe is unchanged.
- `bookvideo_test.go` pins were rewritten for the new signature/geometry (T10g's
  own test surface), and the real-ffmpeg smoke now exercises captions.

---

# T10g round 1 — adversarial review of "the words on the page"

| | |
|---|---|
| **Target** | Track T10g (PLAN.md §T10g) — caption band + geometry on the film, embedded Fredoka, silent tier, and the §T10f activation flip in bookgen. Working tree on `main` @ `b5fcfe5` (the code state equals the implementer's base `a94accb`: commits `a94accb..b5fcfe5` touched only `dev-diary/PLAN.md`). |
| **Reviewer** | Fresh adversarial Review Agent (`T10gRound1Reviewer`), 2026-09-06. No prior T10g round; implementer stub read as claim, never as evidence. |
| **Shared tree** | Operator's T9 track is mid-flight in the same tree (`internal/interview/**`, `static/race/**`, `cmd/thutapi/main.go` web routes, `internal/web/**`, t9-*.md records). T9 churn was observed during this review (`internal/interview/turn.go` became modified mid-flight) — attributed external. T10g's delta was reviewed ONLY. |
| **Owns** | T10g's own caption lines in `internal/bookvideo`, its line-wrap helper, the embedded font instance, and (per the PLAN activation annotation + §T10f "Superseded in part by §T10g" block) the bookgen outage-branch flip + pins. |
| **Files changed (T10g)** | `internal/bookvideo/{types,video}.go`, new `caption.go`, `caption_test.go`, `fonts.go`, `fonts/{Fredoka-Regular.ttf,OFL.txt}` (derived instance), `bookvideo_test.go`; `internal/bookgen/{bookgen,pipeline}.go` + `{bookgen,pipeline}_test.go`; `cmd/thutapi/main.go` diff is T9-lane only (web routes — no FontFile wiring; verified below). |
| **Verdict** | **REMEDIATE — 0 × C, 0 × H, 1 × M, 1 × L** |

---

## Findings

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| **F1** | **M** | `internal/bookgen/live_test.go:1002-1004` (T10e's live probe, `//go:build live`) | The T10e live gate still pins the OLD master geometry: `if s.Width != 1080 \|\| s.Height != 1350 { video dimensions = … want 1080x1350 }`. T10g moved every segment to 1080×1620 (verified: `TestRender_RealFFmpeg` probes the real output as `h264,1080,1620`, and the live probe renders through the same `NewFFmpegRenderer(bookvideo.Config{})`, live_test.go:645), so the operator's next run of the very re-verification §T10g mandates ("T10e verified the pipeline live at 4:5. Changing the master geometry … **re-run it**", PLAN.md:2281-2283) fails deterministically on the correct new geometry. The flipped pins elsewhere (row 10) were migrated; this sibling pin in the same package was not. | `go test -tags live -run TestLiveBookGeneration` → `video dimensions = 1080x1620, want 1080x1350` at live_test.go:1003. | Restoring the T10g flip (master frame back to 1080×1350, the pre-T10g constant set) turns the pin green again while violating the spec geometry — the pin is load-bearing but asserts the superseded shape. |
| **F2** | **L** | `internal/bookvideo/types.go:66-68` (Config.FontFile doc; mismatch created by T10g's `materializeFont`, video.go:45-60) | The exported field doc still reads "If empty, ffmpeg uses system fontconfig defaults." That described pre-T10g behaviour (fontOpt emitted no `fontfile=` when empty). T10g repurposed empty FontFile to mean "materialise the embedded Fredoka-Regular instance into the render workdir and pass it as `fontfile=` to every drawtext" — so the doc now misdescribes the shipped default, and a reader would wrongly believe the container's fontconfig (which does not exist — D4) or the system default face is what renders when the field is left empty. Doc-only, but on the exported Config surface AGENTS.md §Go style protects. | Inspection: `types.go:66-68` vs `materializeFont` + `buildFontOpt` (video.go:45-66) and the always-present `fontfile=` pins (`TestBuildPageSegment_ArgumentConstruction`, `TestGeometrySharedAcrossSegments`). | Reverting `materializeFont` to the old semantics (no `fontfile=` when FontFile empty — the pre-T10g shape) makes the doc true again and turns the `fontfile=`-present pins RED (`TestBuildPageSegment_ArgumentConstruction`: missing "fontfile="). |

**Severity counts: 0 C / 0 H / 1 M / 1 L → REMEDIATE.**

F1 needs one remediation round (live pin migrated to 1620, and the probe re-run by the operator at T10e). F2 is a two-line doc fix on an exported field — it touches prose only and is a candidate for the orchestrator's in-line L fast path (AGENTS.md), but as written it is a real doc-vs-behaviour drift, not exempt trivia until ruled.

---

## Contract-row rulings (the stub's rows 1-10, ruled against the spec)

| Row | Stub claim | Ruling |
|---|---|---|
| 1 | BuildPageSegment signature/body: per-page text, optional audio, art 1080×1350 + band 1080×270 + caption drawtext | **PASS.** `artAndBandVF` = `scale=…increase,crop=1080:1350,pad=1080:1620:0:0:color=0xd8efe3,setsar=1`; one drawtext (`textfile=`, `expansion=none`, `text_align=C`, ink `0x17332b`, `y=1350+((270-text_h)/2)`); silent tier `anullsrc=channel_layout=stereo:sample_rate=44100` at `captionHold(text)` = words/2.0 s clamp [4s,14s]; narrated tier unchanged (`-shortest`). Pinned raw at every call site. |
| 2 | BuildTitleCard frame 1080×1620, embedded Fredoka | **PASS** on geometry/font (scale-decrease + film pad at 1620, always `fontfile=`); prose "cover-crops" is loose — the art is decrease-fitted and *padded* with `--film` bars (no crop) — see O2; no code defect. |
| 3 | BuildEndCard 1080×1620 on `--film` 0x12241e | **PASS.** `color=c=0x12241e:s=1080x1620:r=25`, pinned (`TestBuildEndCard_ArgumentConstruction`). |
| 4 | PageInput.Text | **PASS.** New field with doc; Render validation flipped from "requires audio" to "requires words" (TrimSpace), audio optional. |
| 5 | One geometry constant set, every call site | **PASS.** Constants in caption.go:15-26 used by all three builders + layout; no stray geometry/colour literals outside tests (grep clean); `bandHeight = frameHeight - artHeight` derived. |
| 6 | Caption helpers: rune wrap, ≤3 lines, one shrink, ellipsis; hold = words/2.0 clamp [4,14] | **PASS.** Budgets: base 60 px → 30 runes/line, shrink 52 px → 34 runes/line (usable 1000 px at 0.55 em/rune). Real-text fit independently reproduced (O5). Hold floor/ceiling + empty-text floor pinned. |
| 7 | Embedded derived Fredoka-Regular (wght 400 wdth 100) + OFL beside; FontFile override | **PASS, byte-verified.** `internal/bookvideo/fonts/Fredoka-Regular.ttf` md5 `e980bc2abae3afa14c6d56a1f0555516` — **byte-identical** to bookpdf's round-3-audited instance (`e980bc2a…`, same instancer command); OFL.txt identical across bookvideo/bookpdf/static-vendor (`21f5400b…`). `Config.FontFile` override honoured by `materializeFont`. Runtime-image no-fonts claim is container-level (transcript; D4) — code is robust either way because `fontfile=` is now unconditional. |
| 8 | Outage flip: ErrTransient → `narration_unavailable {}` once, film STILL rendered captioned-silent; renderFilm accepts nil clips | **PASS.** Exactly the annotated §T10f activation (§T10f block PLAN.md:2176-2185 + §T10g three tiers): publish once inside the branch, `clips = nil`, PDF stage, film stage unconditional; `renderFilm` nil-clips guards (`clips != nil &&` on count and order checks) — mutants M6/M8/M9 all RED on the new pin. Non-transient narration failure still total (unchanged pin). |
| 9 | book_ready always carries both URLs; omitempty dropped | **PASS as implemented, NOT pin-load-bearing** — see O1: no reachable state emits an empty `video_url`, so restoring `omitempty` (M7) leaves every pin GREEN. The tag removal is contract hygiene for the "always present" §T9/§T10b reading. |
| 10 | Outage pins flipped to the current contract | **PASS — the right pins.** Renamed `TestPipeline_NarrationTransientOutageProducesCaptionedSilentFilm` is content-observing: renderer called once, every page carries `Text == story text`, zero `AudioBytes`, images present; exactly one `application/pdf` row + one `video/mp4` row; `book_ready` `video_url` id == attached film row id; job `StatusDone`; wire test marshals the full both-URLs shape. The old T10c-shaped pin (film skipped, no rows) is gone and its mutant (M6) is RED against the new pin — the flip is exactly the annotated activation. |

**T10f round-3 observation fix (filmContentType doc lines):** intact. `filmContentType` const comment (bookgen.go:154-158) and the "Money and fakes" paragraph (bookgen.go:107-117) now state T10d closed the C2 gap; mediastore's closed set does carry `video/mp4` (verified). **cmd/thutapi wiring:** the main.go diff is T9-lane only (web routes); production wires `NewFFmpegRenderer(bookvideo.Config{})` with an empty config (main.go:317), so FontFile empty → embedded-font default — matches the code's defaults; no FontFile wiring was needed or missing.

---

## In-container claims vs pins (what the tests hold vs what only the transcript claims)

| Implementer claim | Held by a test? | Where it is held / what to re-run |
|---|---|---|
| Full composition rendered in the shipping ffmpeg 7.1 image at 1080×1620 | **Pinned offline** | `TestRender_RealFFmpeg` renders title + captioned pages + end + concat `-c copy` with real ffmpeg and probes `h264,1080,1620` (this review: **PASS** on host ffmpeg n9.0.1). Any segment-geometry disagreement fails the concat. The 7.1-image variant itself is transcript/operator. |
| Caption ink centred at band middle; 3 lines at 60/56/64 px fit the 270 px band | **Wire only** | Pins assert `x=(w-text_w)/2`, `y=1350+((270-text_h)/2)`, `line_spacing=0` and the drawtext block shape; the *pixel* outcome (centring, band fit, real-glyph widths at 0.55 em/rune) is transcript-only — needs the operator's eyeball at T10e's geometry re-verification (PLAN.md:2281-2283, 2330-2331). |
| 6.000000 s silent segment; concat agreement | **Pinned offline** | Silent-tier args pinned raw (`-t 5.000`, `anullsrc=…44100`, `-shortest`, geometry); reviewer replicated the exact silent command with real ffmpeg in /tmp: **duration 5.000000 s, h264 1080×1620 SAR 1:1, aac 44100 stereo** — the mechanism is exact; the container's 6.000000 s figure is transcript. |
| `text_align=C` present in shipping ffmpeg 7.1 | **Wire + host** | Option emitted and pinned raw; accepted by real ffmpeg here (n9). 7.1 support was operator-verified 2026-09-05 (PLAN.md:2296-2297) — still transcript for this round. |
| No fonts / no fontconfig in the runtime image; variable face renders Light; Regular instance measured | **None (container-level)** | Motivates `fontfile=`-unconditional + the embedded instance; the code is correct whether or not the claim holds. Offline evidence: embedded TTF is byte-identical to the audited bookpdf Regular (md5), and real-ffmpeg tests render through the materialised embedded bytes (PASS). Container re-verification is an operator item, not a code gap. |
| Font derive provenance | **Byte-verified** | md5 `e980bc2a…` == bookpdf's round-3-audited file; OFL `21f5400b…` ×3. No re-derivation needed. |

**ffmpeg in tests / CI implication:** command tests use `mockRunner` at the `Runner` seam (no binary). Real-binary tests (`TestRender_RealFFmpeg`, `TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg`) self-skip when ffmpeg is absent — CI (`verify.yml`, ubuntu-latest) installs no ffmpeg, so CI runs the seam pins only; the real-render path is exercised where ffmpeg exists (this box, the operator's container, live runs). Same division of labour T10a established.

---

## Mutation table — re-run this round (md5 evidence)

Method: each mutant applied as a single-token replace on the live file, pin run, file restored from `/tmp` backups; every restore md5-verified against the pre-mutation snapshot. Pre-state md5s: `video.go 13d59ca8d830a032b8e876ce5b53eb40`, `caption.go c5683108c9cffb449f4fb2c99104e4f2`, `pipeline.go f7c161d6b7b1c06ef775ed4188a6d4f3`, `bookgen.go fd4968d685f754d9df45c2bda9a60d62`. All four files md5-OK after every restore; final `git status --porcelain` shows no T10g-side residue (only the external T9 lane's own churn).

| # | Mutant | Pin | Result |
|---|---|---|---|
| M1 | Caption drawtext dropped (`if false && captionFile != ""`) | `TestBuildPageSegment_ArgumentConstruction` | **RED** — missing drawtext/textfile/text_align pins |
| M2 | wrapText splits an overlong word mid-word | `TestLayoutCaption_NoMidWordBreak` | **RED** — long word must stay whole |
| M3 | Ellipsis never fires (`shrink[:3]` returned instead of `truncateWithEllipsis`) | `TestLayoutCaption_TruncateWithEllipsis` | **RED** — last line lacks `…` |
| M4 | End-card call site diverges: color source `frameHeight` → `artHeight` (1080×1350) | `TestBuildEndCard_ArgumentConstruction` | **RED** — wants `s=1080x1620` |
| M5 | Silent ceiling clamp removed | `TestCaptionHold` + `TestBuildPageSegment_SilentHoldClamps` | **RED** — 60 words no longer `-t 14.000` |
| M6 | Outage branch back to film-skipped (old T10c shape `if clips != nil`) | `TestPipeline_NarrationTransientOutageProducesCaptionedSilentFilm` | **RED** — `video_url = /media/, want /media/<id>` |
| M7 | `json:"video_url,omitempty"` restored | `TestWirePayloadRawJSON` | **GREEN** — see O1; not load-bearing today |
| M8 | renderFilm nil-clips guard reverted (`len(clips) != len(st.Pages)`) | outage pin | **RED** — event `failed {}`, want `book_ready` |
| M9 | Page `Text` dropped in renderFilm PageInput | outage pin | **RED** — `render input page 1 text = "", want the page's own words` |

---

## Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | **FAIL (external)** — `internal/interview/http.go`, `internal/interview/interview.go` (T9 lane mid-flight; all T10g files clean) |
| Vet | `go vet ./...` | **PASS** |
| Build | `go build ./...` | **PASS** |
| Workspace | `go test ./... -race -count=1` | **PASS** — 17/17 packages green (incl. T9-mid-flight cmd/thutapi, interview, web) |
| Touched packages 5× race | `go test ./internal/bookvideo ./internal/bookgen -race -count=5` | **PASS** |
| Coverage | `go test ./internal/bookvideo ./internal/bookgen -cover` | **PASS** — bookvideo **88.2%**, bookgen **81.8%** (≥75% floor) |
| Real-ffmpeg smoke | `go test ./internal/bookvideo -run 'TestRender_RealFFmpeg\|TestBuildTitleCard_PathWithSingleQuote_RealFFmpeg' -v` | **PASS** — probed `h264,1080,1620`; full concat `-c copy` at the new geometry; quote/colon paths render |
| Silent-tier replication (reviewer, /tmp) | exact code-emitted silent command with real ffmpeg | **PASS** — duration 5.000000 s, 1080×1620 SAR 1:1, aac 44100 stereo |
| Font byte-identity | md5 bookvideo vs bookpdf Fredoka-Regular.ttf, OFL ×3 | **PASS** — `e980bc2a…` / `21f5400b…` |
| Tree state | md5 of the 4 mutated files after each restore; `git status --porcelain` | **PASS** — byte-identical restores; no T10g residue; T9 lane's own changes (e.g. `turn.go`) observed external |

---

## Observations (not findings)

- **O1 — row 9's omitempty drop has no load-bearing pin.** M7 (restore `omitempty`) stays GREEN because no reachable path publishes `book_ready` with an empty `video_url` (runBook returns an error or a non-empty film id, and the outage pin's `/media/<id>`-shape checks would catch an empty id at the content level). The always-present-key contract is therefore enforced by the content pins, not by the tag. Optional hardening: marshal `bookReadyEvent{PDFURL: "/media/pdf1"}` in `TestWirePayloadRawJSON` and pin `{"pdf_url":"/media/pdf1","video_url":""}`.
- **O2 — stub row 2 prose** "blurred page-1 art cover-crops the frame": the code decrease-fits the art inside 1080×1620 and *pads* `--film` bars top/bottom (no crop). Prose looseness only; the treatment matches the spec's card-ground reading.
- **O3 — pre-existing stale sentence** "the Fredoka font once T9 vendors it" on `bookgen.Config.VideoCfg` (bookgen.go:317-319) was already false at the review base (Fredoka vendored at `95cd577`, an ancestor); out of T10g's delta, not a finding.
- **O4 — real-page budget reproduced.** The eight real T6b texts (`data/live/cache/story.json`) measure 58–95 runes / 10–16 words; simulating `wrapText` at 30 and 34 runes/line gives six texts at base and exactly pages 5 and 6 needing the one shrink step, none truncated — matches stub D2. The 159-rune worst case is not unit-pinned as a literal fixture; the synthetic overlong-word tests hold the truncation path.

---

## Zero-residue / disposition

Round 1 for this track — no prior-round findings exist to carry forward. Residue against this round: **F1 (M) and F2 (L)**. F1 requires a remediation round (migrate `live_test.go:1002-1004` to 1080×1620 and re-run the live probe at T10e's geometry re-verification). F2 is a two-line exported-field doc correction (prose only — eligible for the orchestrator's in-line L fast path if ruled trivial). Everything else reviewed clean: geometry/audio-tier/caption lines match §T10g; the outage flip is exactly the annotated §T10f activation; the flipped pins assert the current contract and are content-observing; fonts byte-verified; mutants M1-M6/M8/M9 red on their pins with byte-identical restores; tree carries no T10g-side writes.

---

**VERDICT: REMEDIATE — 0 × C, 0 × H, 1 × M, 1 × L** (F1: stale 1080×1350 pin in the T10e live gate; F2: Config.FontFile doc misdescribes the embedded-font default). A remediation round addresses both; re-review then rules closure.
