# T10f round 2 — adversarial re-review of printable PDF book

| | |
|---|---|
| **Target** | Track T10f — printable PDF book (PLAN.md §T10f, contract rows C1: mediastore, C2: bookgen, C3: cmd/thutapi), main @ `c22696c` + the uncommitted T10f working tree (`internal/bookpdf/**`, `internal/bookgen/**`, `internal/mediastore/**`, `cmd/thutapi/**`, `go.mod`, `go.sum`, review records `t10f-round1.md` + `t10f-remediation-round1.md`). |
| **Evidence** | Git diff `c22696c` → working tree (13 modified + 4 new files in `internal/bookpdf/`); review records of rounds 1 and remediation; PLAN.md §T10f incl. the §T10f↔§T10g reconciliation block and the activation annotation at §T10f ~2150. |
| **Method** | Fresh reviewer with **no prior T10f round** (did not trust round-1 or remediation records — every closure re-verified by re-applying the mutants myself). Probe, don't trust prose: byte-identical mutation re-runs (`cp /tmp` + md5 before/after), full gate battery, raw-wire and doc inspection. READ-ONLY except this file. No live calls (GMI_API_KEY unset for every test run). |
| **Date** | 2026-09-05, round-2 reviewer `T10fRound2Reviewer`. |
| **Verdict** | **REMEDIATE — 0 × C, 0 × H, 0 × M, 5 × L** |

**Activation context:** the §T10f "Superseded in part by §T10g" block (PLAN.md:2146-2155) is **spec-only** — §T10g is unimplemented (no captions code exists in `internal/bookvideo`, which still requires per-page narration, geometry 1080×1350). The orchestrator's activation annotation at ~2150 makes this explicit. This review therefore holds T10f to the **pre-activation contract**, which is the only implementable one today: PDF always; narration 503 (`gmi.ErrTransient`) → `narration_unavailable {}` + run SUCCEEDS PDF-only with the film skipped; narration non-transient failure → run failed. The both-URLs contract is T10g's to land. Code under review implements the pre-activation contract faithfully (verified below).

---

## 1. Round-1 closures — re-verified by this reviewer, not taken on record

All three round-1 findings were re-applied as mutants to the working tree and pinned red, then restored **byte-identical** (md5-verified):

| Finding | Mutant re-applied | Pin result | Restore |
|---|---|---|---|
| **H1** (`cmd/thutapi/main_test.go` missing `Config.PDF`) | Removed `"thutapi/internal/bookpdf"` import + `PDF: bookpdf.NewRenderer(),` from `newTestServer` | **RED** — exactly round-1's letter: 5 tests fail (`TestHealthzReturnsOK`, `TestHealthzRejectsNonGET`, `TestMediaRouteServesThroughMux`, `TestInterviewRoutesServeThroughMux`, `TestGenerateRoutesServeThroughMux`), all at `build generation handler: bookgen: handler not configured` | `main_test.go` md5 `17b6e56b…` — **OK** |
| **L1** (mediastore package doc omitted the PDF) | Reverted `mediastore.go:2` to "…and the MP4 book film — under the data dir…" | **RED by inspection** — package doc again contradicts `supportedTypes` (which carries `application/pdf`, and the doc comment at :71 names it) and the persist path | `mediastore.go` md5 `56ed89a1…` — **OK** |
| **L2** (cover byline "By " duplication) | Reverted `formatByline` to naive `"By " + strings.TrimSpace(b)` | **RED** — `TestFormatByline` fails 10 subtests (`with_By_prefix`: `formatByline("By Mira") = "By By Mira"`, `with_by_prefix`, `case-insensitive_BY`, `bare_by`, …) | `bookpdf.go` md5 `323d7b40…` — **OK** |

Current state of each fix confirmed on the pristine tree: `newTestServer` carries `PDF: bookpdf.NewRenderer()` (main_test.go:78, import :22); mediastore package doc line 2 names "the printable PDF book" and matches `supportedTypes`; `bookpdf.formatByline` (bookpdf.go:154-170) strips any leading case-insensitive `by ` and `Render` uses it (:103).

---

## 2. Findings

**0 × C / 0 × H / 0 × M / 5 × L**

### L1 — `go.mod` declares directly-imported fpdf as an indirect dependency; `go mod tidy` rewrites the file

| | |
|---|---|
| **Where** | `go.mod:11-13` — `github.com/go-pdf/fpdf v0.9.0 // indirect` sits in the second (indirect) require block. |
| **What** | `internal/bookpdf/bookpdf.go:13` imports `github.com/go-pdf/fpdf` directly in non-test code, so the module is a **direct** dependency. `go mod tidy -diff` (read-only) confirms the working tree is not tidy: tidy moves fpdf up into the first require block and drops the `// indirect` marker; `go.sum` is unaffected. The marker is factually wrong and the tree is not tidy-stable — every future `go mod tidy` dirties `go.mod`, and any hygiene gate built on a tidy check (the natural addition to `verify.yml`, which today runs vet/test/gofmt) fails on main. Round-1's own record flagged exactly this question ("fpdf is directly imported by bookpdf so it must be a DIRECT require"); the working tree did not fix it. |
| **Pin** | `go mod tidy -diff` prints a `go.mod` move of fpdf to the direct block (exit 0, tree untouched). |
| **Mutation** | Not a runnable mutant — the toolchain tolerates the wrong marker, which is why no test catches it. Evidence is the tidy diff itself. |
| **Fix** | Run `go mod tidy` (fpdf moves to the direct require block). |

### L2 — PDF supersede-on-regeneration is implemented but completely unpinned; the no-op mutant stays green

| | |
|---|---|
| **Where** | `internal/bookgen/pipeline.go:400-409` (`supersedePDFs`), exercised by `renderPDF` at :395. |
| **What** | Round-2's brief demanded the "PDF persist conflict-replace broken → red" mutant. Applying `supersedePDFs` as a no-op (`return` before any deletion) leaves **the entire bookgen suite green** (`TestDoubleFireRefused`, `TestPipeline_EndToEnd`, `TestPipeline_StageOrder`, catch-up, upsert — all pass). The only two-run test, `TestDoubleFireRefused` (pipeline_test.go:638-718), asserts exactly-one surviving **film** row and never asserts the PDF count, so the C2 behavior "supersedes older PDFs" on regeneration has no pin at all. Regression consequence is real and reachable: after a PDF-only outage run, screen 6's "try again with voices" re-POSTs the same book; if supersedePDFs regresses, stale `application/pdf` rows accumulate and `BookMedia` (oldest first) would surface a previous run's PDF — with stale title/text after a re-structure — as the book's current PDF. The film twin (`supersedeFilms`) is pinned by the same test; the PDF twin is not. |
| **Pin** | None today (proven by mutant N2). |
| **Mutation** | `supersedePDFs` → immediate `return` — **GREEN** across `go test ./internal/bookgen -count=1` (all tests), restored md5 `df56ef6d…` — **OK**. |
| **Fix** | Extend `TestDoubleFireRefused` (or a dedicated regeneration test) to assert exactly one surviving `application/pdf` row after the second run, that it is the second run's `pdf_url` id, and that the two runs produced different pdf ids. |

### L3 — the L2 remediation's render-level pin cannot observe byline text; a call-site bypass of `formatByline` leaves the whole bookpdf suite green

| | |
|---|---|
| **Where** | `internal/bookpdf/bookpdf_test.go:276-285` — `TestRender_BylinePrefixDeduplication`. |
| **What** | Round-1's L2 was found at the **public Render surface** ("Calling `r.Render` with `in.Byline = "By Mira"` produces a cover page containing `"By By Mira"`"). The remediation pinned only the private helper (`TestFormatByline`) and added a render-level test that cannot detect the defect class it names: its only assertions are `err == nil` and `len(pdfBytes) != 0`, and its own comment (test :303-305) concedes no content-stream inspection. Mutant evidence: bypassing `formatByline` at the call site (`bookpdf.go:103` → `in.Byline`) leaves **the entire bookpdf suite green** — a regression that makes the cover render "Mira" (prefix lost) or, combined with a naive helper, "By By Mira", ships silently. The call site is what round-1's finding actually observed; today it is protected only by code inspection. |
| **Pin** | `TestFormatByline` (unit) is load-bearing (red under the naive-helper mutant); the render-level test is decorative. |
| **Mutation** | N3 — `bookpdf.go:103` `formatByline(in.Byline)` → `in.Byline` — **GREEN** over `go test ./internal/bookpdf -count=1`, restored md5 `323d7b40…` — **OK**. |
| **Fix** | Either make the render test observe the cover: search the PDF bytes for the UTF-16BE hex of "By Mira"/"By Leo" (fpdf writes TTF text as UTF-16BE content streams), or delete `TestRender_BylinePrefixDeduplication` as weightless and rely on the helper pin plus an inspection note. |

### L4 — bookgen's package doc still describes the pre-T10f pipeline; the patch's behavior contradicts it on the wire shape, stage order, and outage path

| | |
|---|---|
| **Where** | `internal/bookgen/pipeline.go:89-98` (the patch's new Stage-4/5 block) — the change that outran the untouched package doc at `bookgen.go:31-38, 48-69` (event catalogue, stage list) and `renderFilm`'s own header at `pipeline.go:246-248`. |
| **What** | The patch added the PDF stage and the outage path and updated `runBook`'s doc and `generateJob`, but left the package doc stale on three load-bearing facts: (a) `bookgen.go:32` documents `event: book_ready → {"video_url":"/media/<id>"}` — the patch changed the payload to add `pdf_url` and make `video_url,omitempty`; (b) `bookgen.go:37-38` and :69 state `book_ready` fires "after the film is rendered, persisted and attached" / "only after the film row is placed" — false on the narration-outage path, where `book_ready` fires with **no** film row and the run succeeds; (c) the stage list (:64-69) numbers the film as stage 4 with no PDF stage between narrate and film, and `renderFilm`'s own doc still says "renderFilm is stage 4" (:246-248) while the film is now stage 5 (`runBook` :95-101). `narration_unavailable` is absent from the event catalogue. This is round-1-L1's own class (doc/behaviour drift) created by this patch's wire and stage changes. |
| **Pin** | None (doc drift; inspection). |
| **Mutation** | Not a mutant — the drift is the pristine state: compare `bookgen.go:31-38/48-69` and `pipeline.go:246-248` against the implemented wire (`bookgen.go:157-160`), stage order (`pipeline.go:89-101`) and outage path (:79-87). |
| **Fix** | Update the package doc event catalogue (`pdf_url`; add `narration_unavailable`), the stage list (narrate → PDF → film → ready, outage branch), the "book_ready only after film" claims, and renumber `renderFilm`'s doc to stage 5. |

### L5 — Fredoka exists in the repo as two independent vendor points; the PDF embeds a second download, not the vendored TTF

| | |
|---|---|
| **Where** | `internal/bookpdf/bookpdf.go:16-20` (`//go:embed fonts/Fredoka-Regular.ttf` + `Fredoka-Bold.ttf`) over `internal/bookpdf/fonts/` (two static TTFs, 48.9 KB + 48.6 KB, **no OFL.txt beside them**). |
| **What** | The tree carries Fredoka data in two places with independent provenance: (a) `static/vendor/fonts/Fredoka[wdth,wght].ttf` — the variable face vendored at `95cd577` with its OFL.txt beside it (commit message: "one variable face… as the licence requires"); (b) the bookpdf static pair, which the embed uses. They are **not the same file** (md5s differ; the vendored face's default instance is Fredoka **Light** per its name table, so fpdf cannot embed it directly for Regular/Bold), and the static pair is **not derived from** the vendored file by any recorded step: its name table carries Google-static naming ("Fredoka Regular"/"Fredoka Bold", `2.001;ssde`), its mtimes (23:16) predate the vendored file (23:30), and no instancer/tooling record exists. PLAN.md §T10f's written reason presumes one source — "the same family §The look vendors… The PDF needs the **TTF**; they are two formats of one licence" — and the task's rule is one source. Today a Fredoka update must touch two vendor points, the two copies can drift apart typographically (film vs PDF), and the redistributed embed copy lacks the OFL.txt the vendored copy carries. `[INFERENCE]` the pair was downloaded separately (no tool log exists to prove it); the duplication itself is fact. |
| **Pin** | Inspection: name-table + md5 + mtime comparison across the three files; go:embed lines reference only the bookpdf pair. |
| **Mutation** | Not a mutant — provenance/hygiene. |
| **Fix** | Instance the static Regular/Bold pair out of the vendored variable TTF with a recorded `fonttools instancer` step (one vendor point, one licence), or consolidate both under one vendored location; keep an OFL.txt with every redistributed copy per SIL OFL clause 4; note the single source in PLAN.md §T10f. |

---

## 3. Requirement-by-requirement evaluation (pre-activation spec)

| # | Requirement | Status | Evidence |
|---|---|---|---|
| R1 | **bookpdf: A4 portrait, one page per book page** | PASS | `fpdf.New("P", "mm", "A4", "")` (bookpdf.go:91); one `AddPage` per `PageInput` after the title page (:111-138). Page count/geometry are not byte-pinned (no PDF parser in tests); statement coverage 96.4% and wire-header pin. |
| R2 | **Image pass-through JPEG; text below** | PASS | `RegisterImageOptionsReader` + `ImageOptions` with raw bytes (no re-encode/resample; PNG tolerated as bonus :117-124); narrative text `MultiCell` below the image (:127-131); fpdf embeds the JPEG stream directly. |
| R3 | **Title page: title + byline; Fredoka embedded** | PASS (byline dedupe pin weak — L3) | Title in Fredoka Bold accent :98-101; byline via `formatByline` :103-108 (helper pinned; render surface unpinned → L3). `//go:embed` of two static TTFs — embed works (tests render); source duplication → L5. |
| R4 | **Validation** | PASS | Empty/whitespace title, empty pages, `N != i+1` (order/1-based), empty image bytes → `ErrInvalidInput` (bookpdf.go:76-89); pinned by `TestRender_InputValidations` (M5 red). |
| R5 | **ctx cancellation honored** | PASS | Entry check (:73-75) pinned by `TestRender_CancelledContextBefore`; per-page loop check (:112-114) present but only reachable by mid-render cancellation — `TestRender_CancelledContextDuring` cancels *before* Render, so the loop check is unpinned; not a defect (code correct). |
| R6 | **C1: `application/pdf` in mediastore `supportedTypes` + Range pin** | PASS | Map entry + doc (mediastore.go:71-80); `TestPersistAndServePDF_RangeRequest` pins 200 full GET, 206 sub-slice and suffix ranges, content-type, immutable cache, ETag (mediastore_test.go:332-415). M1 red. |
| R7 | **C2: PDF stage after narrate, before film; always runs** | PASS | `runBook` stage 4 `renderPDF`, stage 5 film-iff-clips (pipeline.go:89-101); stage order pinned by `TestPipeline_StageOrder` (imager→tts→pdf→render, film after) and `TestPipeline_EndToEnd` pdf assertions. M4 red. |
| R8 | **C2: persist + attach `application/pdf`; supersede older PDFs** | PARTIAL | Persist via the `Film` seam (production = the real `*mediastore.Store`; `Film: blobs` at main.go:312), `SetMediaPlace` attach, `supersedePDFs` removal — code correct and mirroring `supersedeFilms`, but the supersede leg is **unpinned** (mutant N2 green) → L2. |
| R9 | **PDF-failure semantics vs "the PDF stage never fails the run"** | PASS (spec-consistent) | The bullet's rationale is that the stage needs only images and text, both of which exist post-illustrate — i.e. the outage can never starve it. The code fails the run on a genuine render/persist/attach fault (pipeline.go:90-93 → `generateJob` → `failed {}`), which is the only coherent reading: swallowing a PDF fault would "succeed" without the artifact §T9/§T10b say never absents, and matches the general §T10c total-failure doctrine. `TestPipeline_RenderFailureAndFilmFailureAreTotal` now includes the `pdf render` row, pinning the intent. No finding. |
| R10 | **Outage: transient narration error → `narration_unavailable {}` once, film skipped, PDF attached, `book_ready` PDF-only, job `StatusDone`** | PASS | pipeline.go:79-87; `TestPipeline_NarrationTransientOutageProducesPDF` pins empty `{}` payload, PDF render + row, film renderer never called, zero film rows, `video_url` absent, `job.StatusDone` + nil error. M3 red (outage treated as failure), N1 red (double publish). |
| R11 | **Outage: non-transient narration error → run failed** | PASS | else-branch :84-86; `TestPipeline_NarrationNonTransientFailureFailsRun` pins `failed {}` + `job.StatusError`. |
| R12 | **Exactly-once terminal events preserved (T10c once/panic structure)** | PASS | `generateJob`'s `sync.Once` publish wrapper untouched (diff confirms); panic → `failed` + re-raise; `narration_unavailable` is non-terminal, published once inside the transient branch (N1 red proves no duplicate tolerated). |
| R13 | **Wire: `pdf_url` + `video_url,omitempty` raw-JSON pinned** | PASS | `bookReadyEvent` tags (bookgen.go:157-160); `TestWirePayloadRawJSON` pins `{"pdf_url":"/media/pdf1","video_url":"/media/vid1"}` and outage `{"pdf_url":"/media/pdf1"}` (video_url omitted). M2 red. (Spec prose "video_url empty" is realised as key-omission under `omitempty` — deliberate, pinned both at unit and pipeline level; screen logic treats both as absent.) |
| R14 | **C3: cmd wiring + `newTestServer`** | PASS | main.go:310 `PDF: bookpdf.NewRenderer()`; main_test.go:78 in `newTestServer`; live test wired too (live_test.go:666). H1 mutant red → fix verified. |
| R15 | **go.mod: fpdf as DIRECT require** | FAIL → L1 | `go mod tidy -diff` moves it; working tree keeps it `// indirect`. |
| R16 | **PDF input plumbing: persisted illustration blobs read from disk at the media layout; page text + order correct** | PASS | `os.ReadFile(filepath.Join(MediaDir, ill.ID))` matches mediastore's one-file-per-id layout (`root.Create(id)`, media row id = file name); production wires `MediaDir = dataDir/media` = the store's dir (main.go:262-263, 303). Text from `st.Pages` in order (N=i+1 re-validated by bookpdf). Pinned end-to-end against the real store/blob dir by `TestPipeline_EndToEnd` (renderer inputs carry real page blobs in order) and `TestPipeline_NarrationTransientOutageProducesPDF`. |
| R17 | **L1 doc + L2 byline remediated** | PASS (with new L3/L4 notes) | Closures verified in §1; render-surface pin gap → L3; package-doc drift → L4. |
| R18 | **Reconciliation reading** | PASS | Pre-activation contract implemented (§Activation context; §5 below). |

---

## 4. Mutation table — round-1 equivalents and round-2 additions (md5 evidence)

Method: every mutant applied to the live file, pin run, then restored from the `/tmp/t10f-r2-mut/` backup and md5-verified byte-identical to the pre-mutation snapshot. Pre-mutation md5s: `pipeline.go df56ef6d0c59b8e59b5761cc783a3655`, `bookgen.go 2118f10f63e687f82bd854d3aff71589`, `bookpdf.go 323d7b40ce3152e70b8be9c4a01c3f66`, `main_test.go 17b6e56b6e64efec8c29944ff8e5d956`, `mediastore.go 56ed89a15132db86214898c173499016`. All restores md5-OK; final `git status --porcelain` = 16 lines, identical to the pre-review survey.

| # | Mutant | Pin | Result | Restore md5 |
|---|---|---|---|---|
| **M1** | Remove `"application/pdf": true` from `supportedTypes` | `TestPersistAndServePDF_RangeRequest` | **RED** — `mediastore_test.go:337`: `invalid content type: "application/pdf"` | `56ed89a1…` OK |
| **M2** | `json:"pdf_url"` → `json:"pdf"` on `bookReadyEvent` | `TestWirePayloadRawJSON` | **RED** — `bookgen_test.go:111`: wire `{"pdf":"…"}`, want `{"pdf_url":"…"}` | `2118f10f…` OK |
| **M3** | Transient branch disabled (`if false && errors.Is(…gmi.ErrTransient…)`) — outage treated as failure | `TestPipeline_NarrationTransientOutageProducesPDF` | **RED** — `pipeline_test.go:543`: `event = "failed" ({}), want "narration_unavailable"` | `df56ef6d…` OK |
| **M4** | PDF stage skipped on the success path (`pdfID, err = "", nil`) | `TestPipeline_EndToEnd` | **RED** — `pipeline_test.go:80`: `book_ready pdf_url = "/media/", want /media/<id>` | `df56ef6d…` OK |
| **M5** | Empty-title validation disabled | `TestRender_InputValidations` | **RED** — `empty_title` and `whitespace_title` subtests: `expected error …, got nil` | `323d7b40…` OK |
| **M6** (round-1 form) | Round-1's "fix" mutant is now the pristine state | `go test ./cmd/thutapi -race` | **GREEN** on the fixed tree (cmd 86.1% cov, 10.1 s) | n/a |
| **N1** | `narration_unavailable` published twice in the outage branch | `TestPipeline_NarrationTransientOutageProducesPDF` | **RED** — `pipeline_test.go:549`: `event = "narration_unavailable" ({}), want "book_ready"` (exactly-once pinned) | `df56ef6d…` OK |
| **N2** | `supersedePDFs` made a no-op (conflict-replace broken) | whole `internal/bookgen` suite | **GREEN** — nothing turns red; the PDF-supersede leg is unpinned → **L2** | `df56ef6d…` OK |
| **N3** | `formatByline` bypassed at the Render call site | whole `internal/bookpdf` suite | **GREEN** — render-level dedupe pin cannot observe text → **L3** | `323d7b40…` OK |
| **N4** | `go mod tidy -diff` (read-only) | n/a | fpdf moves to the direct require block → **L1** | tree untouched |
| Closure H1 | Remove bookpdf import + `PDF:` from `newTestServer` | `go test ./cmd/thutapi/...` | **RED** — 5 tests, `build generation handler: bookgen: handler not configured` | `17b6e56b…` OK |
| Closure L2 | Naive `"By " + TrimSpace(b)` `formatByline` | `TestFormatByline` | **RED** — 10 subtests incl. `formatByline("By Mira") = "By By Mira"` | `323d7b40…` OK |
| Closure L1 | Doc line 2 reverted to pre-fix text | inspection vs `supportedTypes` | **RED by inspection** — doc omits the PDF the map and persist path carry | `56ed89a1…` OK |

---

## 5. Reconciliation-activation statement

T10f **lands pre-activation**. The §T10g "film never skipped / both URLs always" text at PLAN.md:2146-2155 is activation-conditional on T10g's captioned-silent-film implementation, which does not exist in code (verified: `internal/bookvideo` unchanged since T10a; per-page narration still required; no silent-film path). The orchestrator's annotation (PLAN.md working-tree diff at :2150) states this. The code under review correctly implements the **pre-activation** contract — narration 503 → `narration_unavailable {}` published once, film skipped, PDF rendered/attached, `book_ready` with `pdf_url` and no `video_url`, job `StatusDone`; narration non-transient failure → `failed`. Nothing in T10f's delta pre-implements T10g's both-URLs behaviour, and nothing in it blocks T10g (the PDF stage is film-independent and sits before it). The reviewer holds T10f to the pre-activation contract only; the both-URLs wire becomes T10g's obligation under its own contract rows.

## 6. PDF-failure-semantics statement

Spec bullet "The PDF stage never fails the run. It needs only images and text, both of which exist by then" is a **design-rationale** claim, not a swallow-errors directive: the code fails the run on genuine render/persist/attach faults (pipeline.go:90-93), which is the coherent reading — an outage cannot starve the PDF (its inputs predate narration), while an infrastructure fault stays total per §T10c, and succeeding without a PDF would violate §T9/§T10b ("a PDF never is absent"). `TestPipeline_RenderFailureAndFilmFailureAreTotal` pins the "pdf render" row total. Spec and code are consistent; no finding.

## 7. go.mod statement

Settled: `github.com/go-pdf/fpdf v0.9.0` is directly imported by `internal/bookpdf` (non-test), so it must be a **direct require**. The working tree lists it under the second require block with `// indirect` (go.mod:12). `go mod tidy -diff` moves it to the direct block; the tree is not tidy-stable. **L1**, fix = `go mod tidy`.

## 8. Font-source statement

Fredoka data exists at two vendor points: the vendored variable TTF (static/vendor/fonts, with OFL.txt, commit 95cd577) and the embedded static Regular/Bold pair (internal/bookpdf/fonts, no OFL.txt beside). Different files (md5), different naming (the vendored default instance is "Fredoka Light"; the pair is Google-static-named "Fredoka Regular/Bold"), pair mtimes predating the vendor commit — i.e. **not** the same file and not derived from it by any recorded step. Duplication and licence-adjacency **L5**; the embed itself works (fonts render; suite green).

## 9. Gates table

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | **PASS** — 0 files |
| Vet | `go vet ./...` | **PASS** |
| Build | `go build ./...` | **PASS** |
| Targeted | `go test ./internal/bookpdf ./internal/bookgen ./internal/mediastore ./cmd/thutapi -race -count=1 -cover` | **PASS** — bookpdf **96.4%** (floor 75; remediation claimed 96.4), bookgen **81.7%** (75), mediastore **95.6%** (75), cmd/thutapi **86.1%** (75) |
| Coverage claim check | bookpdf ≥75 / bookgen 81.7 / mediastore 95.6 | **PASS** — claims exactly reproduced |
| Workspace | `go test ./... -race -count=1` | **FLAKY, external** — 15 packages; `internal/interview` `TestRestartedHandlerSeesEndedInterview` intermittently times out (5 s wait) under full-suite parallel `-race` load. **Not T10f-attributable**: `internal/interview` is untouched by the delta and does not import any T10f package; the identical flake reproduces at pristine base `c22696c` in a throwaway worktree (base: 1 fail in 3 full-suite runs; reviewed tree: 3 fails in 5, both trees also fully green — reviewed tree green 2×, base green 2×); the package passes in isolation 2/2 on both trees. Recorded here for honesty; the remediation record's "workspace PASS" reflects a green run, which this tree also produces. |

## 10. Zero-residue claim

Not approvable as-is: the round-1 remediation residue is **zero** (H1/L1/L2 closures verified in §1), but round-2 identifies **5 new L-level items** (L1 go.mod tidy state, L2 unpinned PDF supersede, L3 non-observing render-level byline pin, L4 bookgen package-doc drift, L5 duplicated font vendor point). All five are doc/hygiene/pin gaps — no functional defect was found in the implemented pre-activation behaviour. Verdict below requires one remediation round for the 5 L items, then a round-3 confirmation.

**VERDICT: REMEDIATE — 0 × C, 0 × H, 0 × M, 5 × L** (round-1 residue zero; new L1–L5 above).
