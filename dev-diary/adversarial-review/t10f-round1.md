# T10f round 1 — adversarial review of printable PDF book

| | |
|---|---|
| **Target** | Track T10f — printable PDF book (PLAN.md §T10f, contract rows C1: mediastore, C2: bookgen, C3: cmd/thutapi). Working tree on `main`. |
| **Reviewer** | Fresh adversarial Review Agent (`ReviewerT10fRound1`), 2026-09-05. Not the implementer of T10f. |
| **Owns** | `internal/bookpdf/**`, its own `application/pdf` line in `mediastore.supportedTypes`, and its own PDF stage in `internal/bookgen`. |
| **Files changed** | `internal/bookpdf/**` (new package, fonts), `internal/mediastore/mediastore.go`, `internal/mediastore/mediastore_test.go`, `internal/bookgen/**`, `cmd/thutapi/main.go`, `go.mod`, `go.sum`. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 0 × M, 2 × L** |

---

## Summary of the diff

Track T10f implements the printable PDF picture book stage and outage fallback per PLAN.md §T10f:

1. **`internal/bookpdf/` (New package)**:
   - Embeds Fredoka Regular and Bold TrueType fonts via `//go:embed` without external file dependencies.
   - Implements `Renderer.Render(ctx, Input) ([]byte, error)` generating A4 portrait (210mm × 297mm) PDFs.
   - Cover/title page: Title in Fredoka Bold (`--accent` `#d2703a`, 32pt), child's byline in Fredoka Regular (`--ink` `#1b1614`, 16pt).
   - Story pages: 4:5 JPEG image centered horizontally (`140mm × 175mm`), narrative text below the illustration in Fredoka Regular (`--ink` `#1b1614`, 15pt), subtle page numbers in `--muted` (`#8c827a`, 10pt).
   - Pass-through embedding of raw JPEG illustration bytes via `fpdf.RegisterImageOptionsReader` without re-encoding or resampling.
   - Validates non-empty title, ordered pages, and non-empty image bytes; honors `context.Context` cancellation.

2. **`internal/mediastore/` (Contract Row C1)**:
   - Added `"application/pdf": true` to closed `supportedTypes`.
   - Updated doc comment on `supportedTypes`.
   - Added `TestPersistAndServePDF_RangeRequest` pinning 200 OK full GET, 206 Partial Content sub-slice Range, and 206 Partial Content suffix Range.

3. **`internal/bookgen/` (Contract Row C2)**:
   - Declared consumer interface `pdfRenderer` in `bookgen.go`.
   - Added `PDF pdfRenderer` to `bookgen.Config` and enforced required dependency in `bookgen.New`.
   - Pipeline stage order: `structure → illustrate+judge+persist → narrate → PDF → film → ready`.
   - PDF stage (`renderPDF`): always runs for every book, reads persisted illustrations from disk, renders through `PDF.Render`, persists blob as `application/pdf`, attaches kindlessly to book via `SetMediaPlace`, and supersedes older PDF blobs via `supersedePDFs`.
   - Outage handling: When narration fails with `gmi.ErrTransient` (e.g. 503 capacity outage), logs warning, publishes `narration_unavailable {}`, skips film rendering, but continues to render & attach PDF; `book_ready` is emitted with `pdf_url` (and omitted/empty `video_url`), and job finishes with `job.StatusDone`.
   - Wire event payloads updated: `bookReadyEvent` carries `pdf_url` and `video_url,omitempty`; wire tests updated and pinned.

4. **`cmd/thutapi/main.go` (Contract Row C3)**:
   - Wired `PDF: bookpdf.NewRenderer()` into `bookgen.New` call in `run()`.

5. **`go.mod` / `go.sum`**:
   - Added pure Go dependency `github.com/go-pdf/fpdf v0.9.0` (MIT, pure Go), justified in PLAN.md §T10f.

---

## Severity counts

**0 C / 1 H / 0 M / 2 L**

---

## Findings table

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| **H1** | **H** | `cmd/thutapi/main_test.go:67-80` | `newTestServer` in `cmd/thutapi/main_test.go` builds `bookgen.New(bookgen.Config{...})` without setting the required `PDF` dependency (`Config.PDF`). Because `bookgen.New` strictly validates `cfg.PDF != nil`, `newTestServer` fails with `ErrNotConfigured`. This causes 5 tests in `cmd/thutapi` to fail (`TestHealthzReturnsOK`, `TestHealthzRejectsNonGET`, `TestMediaRouteServesThroughMux`, `TestInterviewRoutesServeThroughMux`, `TestGenerateRoutesServeThroughMux`), breaking the workspace test suite under `go test ./... -race` and failing the CI gate `verify.yml`. | `go test ./cmd/thutapi` fails with `build generation handler: bookgen: handler not configured` on 5 tests. | Providing `PDF: bookpdf.NewRenderer()` (or a `failPDFRenderer{}` fake) in `newTestServer` flips `go test ./cmd/thutapi` from FAIL to 100% green. |
| **L1** | **L** | `internal/mediastore/mediastore.go:1-3` | Package doc comment documents persisting "the images and audio GMI returns, and the MP4 book film", omitting the newly added printable PDF book (`application/pdf`). While the `supportedTypes` variable doc comment was updated, the package doc comment creates doc-level omission/drift contrary to AGENTS.md §Go style. | Inspection of `internal/mediastore/mediastore.go:1-3` vs lines 71-80. | Update `mediastore.go:2` to state "the MP4 book film, and the printable PDF book". |
| **L2** | **L** | `internal/bookpdf/bookpdf.go:107` | Cover byline unconditionally prepends `"By "`: `pdf.MultiCell(170, 8, "By "+byline, "", "C", false)`. If a child or user inputs `"by Mira"` or `"By Mira"`, the PDF renders duplicate prefix `"By by Mira"` or `"By By Mira"`. In contrast, `internal/bookvideo/command.go:91` (`formatByline`) strips any existing case-insensitive `"by "` prefix before formatting. | Calling `r.Render` with `in.Byline = "By Mira"` produces a cover page containing `"By By Mira"`. | Implement a `formatByline` helper in `bookpdf` matching `bookvideo`'s prefix-stripping behavior. |

---

## Contract Rows Evaluation

| Row | Target | Description | Status | Evaluation |
|---|---|---|---|---|
| **C1** | `internal/mediastore` | Add `application/pdf` to `supportedTypes` and Range tests | **PASS** (with L1 note) | Added `"application/pdf": true` to closed map `supportedTypes`. Updated `supportedTypes` doc comment. Added `TestPersistAndServePDF_RangeRequest` asserting 200 OK full GET, 206 Partial Content sub-slice Range, and 206 Partial Content suffix Range. Minor doc omission noted in L1. |
| **C2** | `internal/bookgen` | Add PDF renderer stage and outage fallback to pipeline | **PASS** | Declares `pdfRenderer` interface; adds `PDF` to `Config`; runs PDF stage for every book; persists and attaches `application/pdf` blob; supersedes prior PDFs; when narration 503s (`gmi.ErrTransient`), emits `narration_unavailable {}`, skips film, renders PDF, emits `book_ready {pdf_url}`, and completes with `job.StatusDone`. Wire formats and stage order pinned. |
| **C3** | `cmd/thutapi` | Wire `bookpdf.NewRenderer()` in `main.go` | **DEFECT** (H1) | `main.go:310` wires `PDF: bookpdf.NewRenderer()`. However, `main_test.go:67-80` (`newTestServer`) was not updated with a PDF handle, breaking 5 tests in `cmd/thutapi` and failing CI. |

---

## Verification Gates Table

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | **PASS** (clean, exit 0) |
| Vet | `go vet ./...` | **PASS** (clean, exit 0) |
| Format | `gofmt -l .` | **PASS** (clean, 0 unformatted files) |
| Package tests & coverage: `bookpdf` | `go test ./internal/bookpdf/... -race -cover` | **PASS** (**97.0%** statement coverage; floor is 75%) |
| Package tests & coverage: `mediastore` | `go test ./internal/mediastore/... -race -cover` | **PASS** (**95.6%** statement coverage; floor is 75%) |
| Package tests & coverage: `bookgen` | `go test ./internal/bookgen/... -race -cover` | **PASS** (**81.7%** statement coverage; floor is 75%) |
| Workspace tests | `go test ./... -race` | **FAIL** — `cmd/thutapi` fails with 5 errors due to `newTestServer` missing `PDF` (Finding H1). |

---

## Mutation Testing

All mutations were applied to the working tree, verified against the test suites, and reverted cleanly to verify that the test pins are load-bearing.

| # | Mutation | Target | Command | Result |
|---|---|---|---|---|
| **M1** | Remove `"application/pdf": true` from `supportedTypes` in `internal/mediastore/mediastore.go` | `TestPersistAndServePDF_RangeRequest` | `go test ./internal/mediastore -run "TestPersistAndServePDF_RangeRequest"` | **RED** — fails at `mediastore_test.go:337`: `persist application/pdf: mediastore: persist: mediastore: invalid content type: "application/pdf"` |
| **M2** | Change `PDFURL string json:"pdf_url"` to `PDFURL string json:"pdf"` in `internal/bookgen/bookgen.go` | `TestWirePayloadRawJSON` | `go test ./internal/bookgen -run "TestWirePayloadRawJSON"` | **RED** — fails at `bookgen_test.go:111`: `book_ready wire = {"pdf":"/media/pdf1","video_url":"/media/vid1"}, want {"pdf_url":"/media/pdf1","video_url":"/media/vid1"}` |
| **M3** | In `internal/bookgen/pipeline.go`, disable transient error check: `if false && errors.Is(err, gmi.ErrTransient)` | `TestPipeline_NarrationTransientOutageProducesPDF` | `go test ./internal/bookgen -run "TestPipeline_NarrationTransientOutageProducesPDF"` | **RED** — fails at `pipeline_test.go:543`: `event = "failed" ({}), want "narration_unavailable"` |
| **M4** | In `internal/bookgen/pipeline.go`, skip PDF rendering: `pdfID, err = "", nil` | `TestPipeline_EndToEnd` | `go test ./internal/bookgen -run "TestPipeline_EndToEnd"` | **RED** — fails at `pipeline_test.go:80`: `book_ready pdf_url = "/media/", want /media/<id>` |
| **M5** | In `internal/bookpdf/bookpdf.go:76`, disable empty title validation | `TestRender_InputValidations` | `go test ./internal/bookpdf -run "TestRender_InputValidations"` | **RED** — fails at `bookpdf_test.go:172`: `expected error for case "empty title", got nil` |
| **M6** | In `cmd/thutapi/main_test.go`, wire `PDF: bookpdf.NewRenderer()` in `newTestServer` | `TestHealthzReturnsOK` & other cmd tests | `go test ./cmd/thutapi -race` | **GREEN** — flips `cmd/thutapi` from 5 failures to 100% PASS (10.1s). Proves H1 root cause. |

---

## Verdict and Required Remediation

**VERDICT: REMEDIATE (0 × C, 1 × H, 0 × M, 2 × L)**

Track T10f cannot be approved until a remediation round addresses all findings:
1. **Fix H1**: Update `newTestServer` in `cmd/thutapi/main_test.go` to provide a PDF fake (e.g. `failPDFRenderer{}`) or `bookpdf.NewRenderer()`, ensuring `go test ./... -race` and `verify.yml` pass.
2. **Fix L1**: Update package doc comment in `internal/mediastore/mediastore.go` to document the printable PDF book.
3. **Fix L2**: Update `internal/bookpdf/bookpdf.go` byline formatting to strip existing `"by "` / `"By "` prefixes before prepending `"By "`.

Once remediation is completed and recorded in `dev-diary/adversarial-review/t10f-remediation-round1.md`, Round 2 review must be performed.
