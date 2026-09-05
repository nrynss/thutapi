# T10f round 3 — adversarial re-review of printable PDF book

| | |
|---|---|
| **Target** | Track T10f — printable PDF book (PLAN.md §T10f, contract rows C1: mediastore, C2: bookgen, C3: cmd/thutapi), base `main` @ `c22696c` + the T10f working tree **including round-2 remediation** (`internal/bookpdf/**`, `internal/bookgen/**`, `internal/mediastore/**`, `cmd/thutapi/**`, `go.mod`, `go.sum`, review records through `t10f-remediation-round2.md`). |
| **Evidence** | Git diff `c22696c` → working tree; full chain t10f-round1 → t10f-remediation-round1 → t10f-round2 → t10f-remediation-round2; PLAN.md §T10f (2061–2172) incl. the reconciliation block and the activation annotation (~2150), §T10g (2175+, spec-only), the flow event table (1525–1595, 2037–2041); the current code read in full: `internal/bookpdf/**` (embed comment + derived instances), `internal/bookgen/**` (package doc, `supersedePDFs`, outage path, tests incl. extended `TestDoubleFireRefused` and the content-observing byline pin), the bookgen/mediastore/cmd diffs vs HEAD, go.mod/go.sum. |
| **Method** | Fresh reviewer with **no prior T10f round** (round-1/2 records not trusted — every closure re-verified by re-applying the mutants myself). Probe, don't trust prose: byte-identical mutation re-runs (`cp /tmp` backups + md5 before/after), full gate battery, font-derivation re-run with fontTools 4.64.0 in /tmp, raw-wire and doc inspection. READ-ONLY except this file. No live calls (GMI_API_KEY unset for every test run). |
| **Date** | 2026-09-06, round-3 reviewer `T10fRound3Reviewer`. |
| **Verdict** | **APPROVE — 0 × C, 0 × H, 0 × M, 0 × L** |

**Activation context (unchanged from round 2, re-confirmed):** the §T10f "Superseded in part by §T10g" block (PLAN.md:2146-2155) is **spec-only** — §T10g is unimplemented (no captions code exists in `internal/bookvideo`, which still requires per-page narration; geometry still 1080×1350). The orchestrator's activation annotation at ~2150 makes this explicit. This review therefore holds T10f to the **pre-activation contract**, the only implementable one: PDF always; narration 503 (`gmi.ErrTransient`) → `narration_unavailable {}` once + run SUCCEEDS PDF-only with the film skipped; narration non-transient failure → run failed; wire `pdf_url` present, `video_url` omitted when no film. The both-URLs contract is T10g's to land. Code under review implements the pre-activation contract faithfully (verified in §4).

---

## 1. Round-2 Ls — each re-verified by this reviewer, not taken on record

| # | Round-2 finding | Closure verification (this round) | Result |
|---|---|---|---|
| **L1** | `go.mod` declared directly-imported fpdf as `// indirect`; tree not tidy-stable | `go mod tidy -diff` on the pristine tree prints **nothing**, exit 0. `go.mod` read: fpdf sits in the **first (direct) require block** (`github.com/go-pdf/fpdf v0.9.0` at go.mod:6), no `// indirect` marker; second block holds only true indirects. go.sum md5 `114e0f16…` unchanged across tidy (round-2 record). | **CLOSED** |
| **L2** | PDF supersede-on-regeneration unpinned — no-op mutant stayed green | **Mutant N2 re-applied by me**: `supersedePDFs` → immediate `return`. Pin `TestDoubleFireRefused` → **RED** at `pipeline_test.go:728`: `pdf rows after two runs = 2, want 1 (the old PDF is superseded)`. Restored byte-identical (pipeline.go md5 `f337f00a…` OK). The extended assertions (read at pipeline_test.go:718-736) check exactly-one surviving `application/pdf` row = the **second** run's pdf id, distinct from the first run's. | **CLOSED** |
| **L3** | Render-level byline pin decorative; call-site bypass stayed green | The pin (`TestRender_BylinePrefixDeduplication`, bookpdf_test.go:324-360) is now **content-observing**: `pdfContentText` (:281-301) decompresses every FlateDecode stream; assertions (:352-357) check the **UTF-16BE encoding of the exact expected byline** is present and the doubled `"By By"` is absent, per case over the decompressed text. **Mutant N3 re-applied by me** (call-site bypass, bookpdf.go:122 `formatByline(in.Byline)` → `in.Byline`) → **RED**: `with_by_prefix` and `without_prefix` subtests fail `bookpdf_test.go:353: cover byline "By Leo"/"By Mira" missing from the rendered text` — the pin reads genuine rendered glyph text, not byte-counts. Restored byte-identical (bookpdf.go md5 `2436f301…` OK). | **CLOSED** |
| **L4** | bookgen package doc describes pre-T10f pipeline | Doc-vs-code audit in §3: event catalogue, `book_ready` firing semantics, stage list (narrate 3 → PDF 4 → film 5), `renderFilm`'s renumbered header (pipeline.go:246 "renderFilm is stage 5 (PDF stage 4 always ran first)") — all four round-2 facts now match the code, and the surrounding doc reads consistent (catch-up sentence, total-failure paragraph, outage note, §T10f references). No remaining contradiction in the wire/stage/outage surface. | **CLOSED** |
| **L5** | Two independent Fredoka vendor points; embedded pair of unknown provenance | Single-source derivation **verified byte-level**, see §2. | **CLOSED** |

## 2. L5-derivation verdict

**Verified.** The embedded pair is derived from the canonical vendored variable face by the recorded commands, reproduced byte-for-byte.

Evidence — metadata (fontTools 4.64.0, the version the record names):

| File | md5 | Size | Name table (fam/sub) | usWeightClass | fvar |
|---|---|---|---|---|---|
| `static/vendor/fonts/Fredoka[wdth,wght].ttf` (canonical, commit 95cd577) | `7b33fede…` | 159184 B | "Fredoka Light" / "Regular" | 300 | wght 300–700 (default 300), wdth 75–125 (default 100); named instances incl. Regular 400, Bold 700 |
| `internal/bookpdf/fonts/Fredoka-Regular.ttf` | `e980bc2a…` | 50196 B | "Fredoka" / "Regular" | **400** | **absent** |
| `internal/bookpdf/fonts/Fredoka-Bold.ttf` | `fdc9dae6…` | 49908 B | "Fredoka" / "Bold" | **700** | **absent** |
| `OFL.txt` at both `internal/bookpdf/fonts/` and `static/vendor/fonts/` | `21f5400b…` (identical) | 4388 B | SIL OFL 1.1 text | — | — |

Derivation re-run (this round, in `/tmp/t10f-r3-inst/`, then cleaned):

```
python3 -m fontTools.varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' wght=400 wdth=100 --update-name-table -o …/Fredoka-Regular.ttf
python3 -m fontTools.varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' wght=700 wdth=100 --update-name-table -o …/Fredoka-Bold.ttf
```

My re-instance and the checked-in file differ in **exactly 6 raw bytes**, all in the 8-byte `head.modified` field — fontTools stamps the instancer run's wall-clock time there (my run 00:10 vs the recorded run 00:02, 510 s apart; the field is seconds since 1904). After neutralizing `head.modified` to 0 in both, the re-instance and the checked-in file are **byte-identical** (md5 `7a0b3929…` Regular, `df6c505e…` Bold). Same input file, same tool version, same command line → deterministic output. The checked-in instances' mtimes (Sep 6 00:02) postdate the canonical vendor file (Sep 5 23:30), consistent with derivation order.

Claims verified true: the embed comment (bookpdf.go:17-33) names the canonical source + commit, records both instancer commands, and documents the constraint honestly — `//go:embed` patterns cannot cross the package directory boundary to `static/`, and fpdf reads only a font's **default** instance (the variable face defaults to Fredoka Light, wght 300, verified in the name table/`fvar` above), so one file cannot serve both weights. SIL OFL 1.1 text sits beside both copies (OFL clause 4/5 compliance for redistribution; clause 2 permits the PDF embedding itself). The bookpdf suite renders green on the instanced pair (real fonts, not fakes).

**Verdict: single source confirmed.** The tree record (recorded commands + tool version in the embed comment and remediation file) plus the reproducible byte-level derivation suffice — no residual provenance gap.

## 3. L4 doc audit — read against the code (round-3 item 1, L4 bullet)

Round-2's four named facts, each confirmed clean on the current text:

1. **Event catalogue** (bookgen.go:32-35) now lists `narration_unavailable → {}` and `book_ready → {"pdf_url":…,"video_url":…}`, and the fires-paragraph (:39-42) carries the note that a transient narration outage leaves `video_url` out of the payload. Matches `bookReadyEvent` (pdf_url + `video_url,omitempty`, :173-176) and `generateJob`'s conditional VideoURL (:477-482).
2. **`book_ready` firing** (:39-40) — "fires once the run's finished artifacts are in place: the PDF always, and the film whenever narration clips exist" — matches `runBook` (pipeline.go:89-103: PDF unconditional stage 4; film only `if clips != nil`) and `generateJob` publishing after `runBook` returns nil.
3. **Stage list** (bookgen.go stages 3-5, :68-85) — narrate (with the outage branch: `gmi.ErrTransient` → skip narration+film, publish once, continue to PDF and succeed PDF-only; any other narration failure is total "before the PDF stage"), PDF (4), film (5, "Rendered only when narration clips exist"). Matches `runBook` exactly (pipeline.go:70-101).
4. **`renderFilm`'s header** (pipeline.go:246) — "renderFilm is stage 5 (PDF stage 4 always ran first)" — and `renderPDF`'s (pipeline.go:352) "renderPDF is stage 4". `runBook`'s own doc (:22-29) numbers the same order. Consistent.

Also read and clean: the intro enumeration (:1-8) lists the PDF between narrate and the film render; the catch-up sentence (:44-50) names the finished film and PDF as the book's `video/mp4` and `application/pdf` media rows; the total-failure paragraph (:87-91) matches `generateJob`'s panic/error paths; the §T10f references and `pdfContentType`/`narrationUnavailableData` consts (:159-163) match the persist and publish sites. **No remaining contradiction in the patched wire/stage/outage behaviour.** (Two *pre-existing, out-of-delta* stale sentences elsewhere in the file are recorded in §8 — not T10f findings.)

## 4. Pre-activation contract — end to end (round-3 item 3)

| # | Contract point | Evidence this round | Result |
|---|---|---|---|
| P1 | **PDF always** — every run renders, persists, attaches `application/pdf` and supersedes older PDFs | `runBook` stage 4 unconditional (pipeline.go:89-93); `renderPDF` persist→`SetMediaPlace`→`supersedePDFs` (:355-398); `TestPipeline_EndToEnd` asserts one attached pdf row whose served bytes match the renderer and `book_ready pdf_url` = row id; **M4-class stage order pinned** (stage list `…tts, pdf, render` + film after, pipeline_test.go:259-277). | PASS |
| P2 | **Transient narration error** → `narration_unavailable {}` **once**, film skipped, PDF-only success, `job.StatusDone` | Outage branch pipeline.go:80-83 (publish inside the branch, before `clips = nil`); `TestPipeline_NarrationTransientOutageProducesPDF` pins empty payload, zero film-renderer calls, zero `video/mp4` rows, absent/empty `video_url`, `StatusDone` + nil error. **M3 re-applied by me** (branch disabled) → **RED** `pipeline_test.go:543: event = "failed" ({}), want "narration_unavailable"`; restored md5 OK. Exactly-once: publish is a plain broker call inside the once-protected terminal wrapper's scope and the test would catch a duplicate as a wrong next event (round-2 N1, structure unchanged by remediation — re-inspected, not re-run). | PASS |
| P3 | **Non-transient narration error** → run failed | else-branch pipeline.go:84-86; `TestPipeline_NarrationNonTransientFailureFailsRun` pins `failed {}` + `StatusError`. | PASS |
| P4 | **Wire**: `pdf_url` always, `video_url` `omitempty` pinned raw | `bookReadyEvent` tags (bookgen.go:173-176); `TestWirePayloadRawJSON` pins both shapes `{"pdf_url":"…","video_url":"…"}` and outage `{"pdf_url":"…"}` (video_url key omitted). **M2 re-applied by me** (`pdf_url`→`pdf`) → **RED** `bookgen_test.go:111`; restored md5 OK. | PASS |

Round-2 R1-R18 rows remain as evaluated (no code under those rows changed in the remediation except docs/tests/pins — verified by reading the full delta; the only production-code deltas vs round-2's review are the package-doc text and one const-comment-adjacent insertion, both non-behavioural).

## 5. Round-1 closures — spot-checked after the round-2 remediation touched bookpdf/go.mod

| Finding | Re-verified | Result |
|---|---|---|
| **H1** (`newTestServer` missing `Config.PDF`) | **Mutant re-applied by me**: removed the bookpdf import + `PDF: bookpdf.NewRenderer(),` from `newTestServer` → `go test ./cmd/thutapi` → **RED**, exactly round-1's letter: 5 tests (`TestHealthzReturnsOK`, `TestHealthzRejectsNonGET`, `TestMediaRouteServesThroughMux`, `TestInterviewRoutesServeThroughMux`, `TestGenerateRoutesServeThroughMux`) at `build generation handler: bookgen: handler not configured`. Restored byte-identical (main_test.go md5 `17b6e56b…` OK — unchanged by remediation, as recorded). Pristine cmd suite green under `-race`. | CLOSED |
| **L1** (mediastore package doc omitted the PDF) | Inspection: mediastore.go:1-3 names "the images and audio GMI returns, the MP4 book film, and the printable PDF book"; `supportedTypes` carries `application/pdf` with doc comment; md5 `56ed89a1…` — unchanged since round 2's verified closure. | CLOSED |
| **L2** (cover "By " duplication) | **Both pins re-run by me**: unit `TestFormatByline` (11 subtests) green pristine. Naive-helper mutant (round-1 L2's form, `"By " + trimmed`, no stripping) → **RED at the render surface too**: `with_By_prefix` fails the absence pin `bookpdf_test.go:356: cover text contains the doubled byline prefix "By By"`, `with_by_prefix` fails the presence pin `:353`. Restored md5 `2436f301…` OK. | CLOSED |

## 6. Mutation table — this round's re-runs (md5 evidence)

Method: each mutant applied to the live file via a single-token replace, pin run, file restored from `/tmp/t10f-r3-mut/` backups and md5-verified byte-identical to the pre-mutation snapshot. Pre-mutation md5s (pristine, matching the remediation record): `pipeline.go f337f00a0347d2f2fde1bbd370a9f6e4`, `bookpdf.go 2436f30150b55dbec25a32058aeded46`, `main_test.go 17b6e56b6e64efec8c29944ff8e5d956`, `bookgen.go 11ab80adaaca2ed17ce418ef8a790d4f`. All restores md5-OK; final tree md5s over all 19 code/font files identical to the pre-review survey; `git status --porcelain` = 18 lines before and after (13 M + 5 ??).

| # | Mutant | Pin | Result | Restore md5 |
|---|---|---|---|---|
| **N2** | `supersedePDFs` → no-op (immediate `return`) | `TestDoubleFireRefused` | **RED** — `pipeline_test.go:728: pdf rows after two runs = 2, want 1 (the old PDF is superseded)` — exactly the remediation-recorded failure | pipeline.go `f337f00a…` OK; suite green after restore |
| **N3** | Call-site bypass: bookpdf.go:122 `formatByline(in.Byline)` → `in.Byline` | `TestRender_BylinePrefixDeduplication` | **RED** — `with_by_prefix`/`without_prefix`: `bookpdf_test.go:353: cover byline "By Leo"/"By Mira" missing from the rendered text` (unit `TestFormatByline` stays green under this mutant — the render pin is what observes the call site) | bookpdf.go `2436f301…` OK; suite green |
| **r1-L2 form** | Naive helper `"By " + TrimSpace(b)` (no prefix stripping) | `TestRender_BylinePrefixDeduplication` | **RED** — `with_By_prefix`: `:356: cover text contains the doubled byline prefix "By By"` (absence pin); `with_by_prefix`: `:353` (presence pin) | bookpdf.go `2436f301…` OK; suite green |
| **H1 closure** | Remove bookpdf import + `PDF:` line from `newTestServer` | `go test ./cmd/thutapi` | **RED** — 5 tests, `build generation handler: bookgen: handler not configured` | main_test.go `17b6e56b…` OK |
| **M2** | `json:"pdf_url"` → `json:"pdf"` | `TestWirePayloadRawJSON` | **RED** — `bookgen_test.go:111: book_ready wire = {"pdf":"…"}, want {"pdf_url":"…"}` | bookgen.go `11ab80ad…` OK |
| **M3** | Outage branch disabled (`if false && errors.Is…`) | `TestPipeline_NarrationTransientOutageProducesPDF` | **RED** — `pipeline_test.go:543: event = "failed" ({}), want "narration_unavailable"` | pipeline.go `f337f00a…` OK; suite green |
| **L1** | `go mod tidy -diff` (read-only) | n/a | **CLEAN** — no output, exit 0; fpdf in the direct require block | tree untouched |

## 7. Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | **PASS** — 0 files |
| Vet | `go vet ./...` | **PASS** |
| Build | `go build ./...` | **PASS** |
| Tidy | `go mod tidy -diff` | **PASS** — clean, exit 0 |
| Workspace | `go test ./... -race -count=1` | **PASS** — 16/16 packages green. (Round 2's external `internal/interview` flake under full-suite `-race` load did not fire this run; interview passed at 3.066 s.) |
| Coverage | `go test ./internal/bookpdf ./internal/bookgen ./internal/mediastore ./cmd/thutapi -race -count=1 -cover` | **PASS** — bookpdf **96.4%**, bookgen **81.7%**, mediastore **95.6%**, cmd/thutapi **86.1%** — all ≥ the 75% floor, exactly reproducing rounds 1–2 claims (remediation added only test/doc statements, no production lines) |
| Tree state | md5 all 19 code/font files + `git status --porcelain` | **PASS** — byte-identical to the pre-review survey; 18 porcelain lines before and after; only this review file added |

## 8. Observations (not findings — recorded so the orchestrator can rule deliberately)

1. **Pre-existing stale "film content-type production gap" claims in `internal/bookgen/bookgen.go`.** The const comment at bookgen.go:152-157 says mediastore's closed content-type set "does not yet carry" `video/mp4` and "a production run fails at the film persist step", and the package-doc "# Money and fakes" paragraph (bookgen.go:111-115) calls the film content type "the one seam with a recorded production gap … must gain video/mp4 before a production run can land its film". Both are **false at the review base**: `video/mp4` entered `supportedTypes` at T10d (commit `e8714ee`, an ancestor of `c22696c`), which resolved t10c contract row C2; the T10f delta itself removed the same claim from the sibling `filmStore` interface doc (bookgen.go:263-268) and its stage-5 text describes the MP4 persist as succeeding. The lines are untouched by the T10f diff (verified against hunk boundaries) and their falsehood was caused by T10d, not by this patch — hence not a T10f finding under this review's patch-anchored bar. They are prose-only and cannot change behaviour, so they qualify for the orchestrator's fast path (AGENTS.md:76-97): delete the stale sentences (or mark them historical, e.g. "since T10d the closed set carries video/mp4"). Named here by file and line for the record.
2. **`internal/bookgen/live_test.go` stage labels** (remediation-round-2 asked round 3 to rule). Ruling: **not a defect.** The probe's `Stage N:` log strings and STAGE TIMINGS BREAKDOWN number its own measured rows — structure 1, illustrate 2, narrate 3, film render 4, film persist 5 — splitting the film into two rows no pipeline definition uses, which shows the numbering is the probe's timing-phase ordinals, not pipeline stage numbers. The PDF renderer is wired raw in the live path (`PDF: bookpdf.NewRenderer()`, live_test.go:666) and untimed, so no PDF row exists to number. The log is `//go:build live`-gated evidence output; the authoritative stage numbering lives in the package doc and pipeline.go stage comments, which are consistent (stage 5 film). No behavioural or doc claim is falsified.

## 9. Zero-residue claim

Against **round 1** (H1, L1, L2): zero residue — all three closures re-verified by me in §5 (H1 mutant red on this tree, L2 red at both the unit and the render surface, L1 by inspection). Against **round 2** (L1–L5): zero residue — all five closures re-verified in §1–§3 and §7 (tidy-diff clean; supersede no-op mutant red; call-site bypass mutant red on a content-observing pin; package doc contradiction-free on the wire/stage/outage surface; Fredoka single-source derivation reproduced byte-for-byte). No new patch-introduced findings at any severity. **Activation-conditional reading stated:** this approval holds while §T10g is spec-only and T10f lands pre-activation (narration 503 → `narration_unavailable {}` + PDF-only success; non-transient → failed; `video_url` omitted without a film). When T10g's captioned/silent film lands, the both-URLs contract enters under T10g's own contract rows; nothing in this delta pre-implements or pre-blocks it. The two §8 observations are not residue of any prior round and do not block this verdict; item 1 is fast-path eligible at landing.

---

**VERDICT: APPROVE — 0 × C, 0 × H, 0 × M, 0 × L**, zero residue against rounds 1 and 2 (severity by severity), on the pre-activation §T10f contract as stated.
