# T10f round 1 — remediation

| | |
|---|---|
|**Target**|The three round-1 findings of `t10f-round1.md` (0 C / 1 H / 0 M / 2 L) across `cmd/thutapi`, `internal/mediastore`, and `internal/bookpdf`.|
|**Date**|2026-09-05. Remediation agent fresh to the round.|
|**Status**|COMPLETE — all three findings remediated; each pin verified red under its re-applied mutant; all CI gates green. Zero residue is claimed only after round 2's verdict.|

---

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **H1** (`cmd/thutapi/main_test.go:67-80`): `newTestServer` built `bookgen.New(bookgen.Config{...})` without setting the required `PDF` dependency (`Config.PDF`). Because `bookgen.New` strictly validates `cfg.PDF != nil`, `newTestServer` failed with `ErrNotConfigured`, causing 5 tests in `cmd/thutapi` to fail (`TestHealthzReturnsOK`, `TestHealthzRejectsNonGET`, `TestMediaRouteServesThroughMux`, `TestInterviewRoutesServeThroughMux`, `TestGenerateRoutesServeThroughMux`). | Imported `"thutapi/internal/bookpdf"` in `cmd/thutapi/main_test.go` and supplied `PDF: bookpdf.NewRenderer()` in `newTestServer`'s `bookgen.Config`. | **DONE** — red on mutant (below), green on pristine tree |
| 2 | **L1** (`internal/mediastore/mediastore.go:1-3`): Package doc comment documented persisting "the images and audio GMI returns, and the MP4 book film", omitting the newly added printable PDF book (`application/pdf`). | Updated package doc comment in `internal/mediastore/mediastore.go:2` to state "the images and audio GMI returns, the MP4 book film, and the printable PDF book". | **DONE** — verified documentation matches `supportedTypes` and package capabilities |
| 3 | **L2** (`internal/bookpdf/bookpdf.go:107`): Cover byline unconditionally prepended `"By "`: `pdf.MultiCell(170, 8, "By "+byline, "", "C", false)`. If a child or user input already started with `"by "` or `"By "`, a duplicate prefix (`"By by ..."` or `"By By ..."`) was rendered. | Added `formatByline(b string) string` helper in `internal/bookpdf/bookpdf.go` that trims whitespace, handles bare `"by"`/`"By"` inputs cleanly, and strips any leading case-insensitive `"by "` prefix before prepending `"By "`. Updated cover page rendering to use `formatByline(in.Byline)`. Added unit tests `TestFormatByline` (testing `"By Mira"`, `"by Leo"`, `"Mira"`, `"BY Leo"`, etc.) and `TestRender_BylinePrefixDeduplication` in `internal/bookpdf/bookpdf_test.go`. | **DONE** — red on mutant (below), green on pristine tree |

---

## Mutation verification — re-running mutants against pins

Each mutant was applied individually to verify that the corresponding pin test goes RED on reversion, then restored to verify that the pin returns to GREEN.

| Finding / Pin | Mutant applied | Result on pin | Restore verification |
|---|---|---|---|
| **H1**<br>`go test ./cmd/thutapi/... -race` | Omitted `PDF: bookpdf.NewRenderer()` in `newTestServer` (`cmd/thutapi/main_test.go`) | **RED — failed:** `build generation handler: bookgen: handler not configured` across 5 tests (`TestHealthzReturnsOK`, `TestHealthzRejectsNonGET`, `TestMediaRouteServesThroughMux`, `TestInterviewRoutesServeThroughMux`, `TestGenerateRoutesServeThroughMux`). | Restored `PDF: bookpdf.NewRenderer()`: pin **GREEN (PASS, 10.02s)** |
| **L1**<br>Inspection of `internal/mediastore/mediastore.go:1-3` | Reverted `mediastore.go:2` to omit the printable PDF book | **RED — documentation drift:** Package doc comment claims to only store images, audio, and MP4 film, contradicting `supportedTypes` mapping for `application/pdf`. | Restored "the MP4 book film, and the printable PDF book": doc comment aligned with code and `supportedTypes`. |
| **L2**<br>`TestFormatByline` & `TestRender_BylinePrefixDeduplication` in `internal/bookpdf` | Reverted `formatByline` in `internal/bookpdf/bookpdf.go` to unconditionally prepend `"By " + strings.TrimSpace(b)` | **RED — failed:** `TestFormatByline` failed on subtests `with_By_prefix` (`formatByline("By Mira") = "By By Mira", want "By Mira"`), `with_by_prefix` (`formatByline("by Leo") = "By by Leo", want "By Leo"`), `case-insensitive_BY`, etc. | Restored prefix-stripping `formatByline`: pins **GREEN (PASS, 0.00s / 0.20s)** |

---

## Gates

All automated verification checks run from a clean tree pass cleanly:

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | **PASS** (clean, exit 0) |
| Vet | `go vet ./...` | **PASS** (clean, exit 0) |
| Format | `gofmt -l .` | **PASS** (clean, 0 unformatted files) |
| Workspace tests | `go test ./... -race` | **PASS** (all packages pass under race detector) |
| Package tests & coverage: `bookpdf` | `go test ./internal/bookpdf/... -race -cover` | **PASS** (**96.4%** statement coverage; floor is 75%) |
| Package tests & coverage: `mediastore` | `go test ./internal/mediastore/... -race -cover` | **PASS** (**95.6%** statement coverage; floor is 75%) |
| Package tests & coverage: `bookgen` | `go test ./internal/bookgen/... -race -cover` | **PASS** (**81.7%** statement coverage; floor is 75%) |
| Package tests & coverage: `cmd/thutapi` | `go test ./cmd/thutapi/... -race -cover` | **PASS** (**86.1%** statement coverage; floor is 75%) |

---

## Residue note

- All 3 findings (H1, L1, L2) are fully remediated inside the track's Owned paths and declared contract rows.
- No changes made outside the track's boundaries (zero scope creep).
- In accordance with AGENTS.md, the remediation agent does not alter the review verdict. Round 2 review must independently audit the remediation and confirm zero residue before issuing an APPROVE verdict.
