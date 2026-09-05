# T10f round 2 — remediation

| | |
|---|---|
|**Target**|The five round-2 findings (L1–L5) of `t10f-round2.md` (0 C / 0 H / 0 M / 5 L) across `go.mod`/`go.sum`, `internal/bookgen/**`, and `internal/bookpdf/**`. Verdict untouched: this file remediates, it does not re-verdict.|
|**Date**|2026-09-06. Remediation agent `T10fRemediator2`, fresh to the round.|
|**Status**|COMPLETE — all five findings remediated; the two new pins each demonstrated RED under their mutant and restored byte-identical (md5-verified); all gates green. Zero residue is claimed only after round 3's verdict.|

---

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **L1** — `go.mod` declares directly-imported `github.com/go-pdf/fpdf` as `// indirect` (go.mod second require block); `go mod tidy -diff` rewrites the file, so the tree is not tidy-stable. | Ran `go mod tidy`. fpdf now sits in the **direct** require block (`require (github.com/go-pdf/fpdf v0.9.0; golang.org/x/sync v0.22.0; modernc.org/sqlite v1.58.0)`), `// indirect` marker dropped. `go mod tidy -diff` after the run prints nothing (exit 0) — the tree is tidy-stable. `go.sum` md5 unchanged by tidy (`114e0f16…` before and after), i.e. the move needed no sum churn. | **DONE** — tidy-diff clean, direct block correct, go.sum untouched |
| 2 | **L2** — PDF supersede-on-regeneration (`supersedePDFs`, pipeline.go) implemented but unpinned: the no-op mutant left the whole bookgen suite green; `TestDoubleFireRefused` asserted only the surviving film row. | Extended `TestDoubleFireRefused` (internal/bookgen/pipeline_test.go) with the PDF mirror of the film assertions, exactly per the round-2 fix: after the second run over the same book, **exactly one** surviving `application/pdf` row, whose id equals the **second** run's `book_ready pdf_url` id, and the two runs produced **different** pdf ids (`ready1["pdf_url"] != ready2["pdf_url"]`). | **DONE** — pin GREEN pristine; RED under the N2 no-op mutant; restored byte-identical (md5) |
| 3 | **L3** — the render-level byline pin (`TestRender_BylinePrefixDeduplication`) asserted only `err == nil` + non-empty bytes; a call-site bypass of `formatByline` left the whole bookpdf suite green. | Replaced the decorative render test with a **content-observing** pin (chose assertion over deletion — the render surface is what round-1's L2 actually observed, and the helper pin alone cannot see a dropped/duplicated prefix at the call site). The pin decompresses every FlateDecode stream (`pdfContentText` helper) and asserts, per case, that the **UTF-16BE byte encoding of the exact expected byline** is present and that the doubled prefix **"By By"** is absent. Empirical discovery recorded for the record: fpdf v0.9.0 writes UTF-8-font text into page content streams as UTF-16BE code-unit strings — the visually clean `(By Mira)Tj` is the byte string `(\x00B\x00y\x00 \x00M\x00i\x00r\x00a)Tj` inside a zlib-compressed stream, *not* the raw-PDF UTF-16BE hex the round-2 record hypothesized; the pin therefore scans decompressed bytes (robust to operator/layout changes) and pins on the doubled prefix's absence, as instructed. | **DONE** — pin GREEN pristine; RED under the N3 call-site-bypass mutant *and* under the naive-helper mutant (round-1 L2's form, now also caught at the render level); restored byte-identical (md5) |
| 4 | **L4** — bookgen package doc describes the pre-T10f pipeline: wire catalogue missing `narration_unavailable` and `pdf_url`/`video_url,omitempty`; `book_ready` "fires after the film is rendered, persisted and attached"; stage list has no PDF stage between narrate and film; `renderFilm`'s doc numbers itself stage 4. | Rewrote the package doc (internal/bookgen/bookgen.go) to match the patched code exactly: (a) event catalogue now lists `narration_unavailable → {}` and `book_ready → {"pdf_url":…,"video_url":…}` with the note that a transient narration outage leaves `video_url` out of the payload; (b) the fires-paragraph and the stage list state `book_ready` fires once the run's finished artifacts are in place — the PDF always, the film whenever narration clips exist (PDF-only on the outage path) — never "only after the film row is placed"; (c) stage list renumbered narrate → **4. printable PDF (always runs once reached)** → **5. film (only when narration clips exist)**, with the transient-outage branch spelled out under stage 3 (outage ≠ error; any other narration failure is total and ends the run before the PDF stage); (d) the intro enumeration gained the PDF render. `renderFilm`'s header (pipeline.go:246) renumbered to **stage 5** (noting stage 4, the PDF, ran first). The `runBook` header's "narrate → PDF → film → ready" order and `renderPDF`'s "stage 4" were already correct. | **DONE** — docs read against code: wire shape (bookgen.go `bookReadyEvent` tags, outage branch at pipeline.go:79-87), stage order (pipeline.go:89-101) and success/outage semantics all match |
| 5 | **L5** — Fredoka exists as two independent vendor points: the canonical vendored variable face `static/vendor/fonts/Fredoka[wdth,wght].ttf` (commit 95cd577, OFL.txt beside) and the embedded static Regular/Bold pair in `internal/bookpdf/fonts/` (no OFL.txt, not the same file, no recorded derivation). | **Decision (with evidence, recorded here): keep the canonical vendor file where it is, and make the embedded pair derived, single-source instances of it.** Constraints that fix the shape: (a) `//go:embed` patterns cannot cross the package boundary, so `internal/bookpdf` cannot embed `static/vendor/fonts/…` in place; (b) the vendored face is a variable font (wght 300–700, **default 300 = Light** per its name table; wdth 75–125 default 100) with named instances at wght 400 and 700; embedding the variable file raw gives fpdf only its default-instance (Light) outlines — verified empirically: fpdf v0.9.0 parses and embeds the variable TTF without error, but one file cannot serve Regular **and** Bold through it, and fpdf performs no instancing; (c) the browser/ffmpeg consumers need the variable face where it is, under `static/`. Fix executed: derived the static pair from the **single canonical vendored file** with a recorded fonttools step — `python3 -m fontTools.varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' wght=400 wdth=100 --update-name-table` and the same with `wght=700` — replacing both TTFs in `internal/bookpdf/fonts/` (outputs: family "Fredoka" / subfamily Regular & Bold per updated name table, `fvar` dropped, `OS/2.usWeightClass` 400/700 — the same family/weight set bookpdf loads via `AddUTF8FontFromBytes("Fredoka","" / "B")`). Copied `OFL.txt` beside the pair (byte-identical to the canonical, md5 `21f5400b…`) so every redistributed copy carries the SIL OFL 1.1 licence text. Documented the single source, the recorded instancer commands, and the embed-boundary/instancing constraint in the embed comment block in `bookpdf.go`. Two copies of Fredoka data remain on disk by necessity (web/ffmpeg under `static/`, embed under `internal/bookpdf/fonts/`) — but provenance is now single-source with a recorded derivation, not two independent downloads. PLAN.md §T10f was **not** annotated: PLAN.md is outside this remediation's touch scope (batch constraint) — flagged for the orchestrator in the residue note. | **DONE** — pair is instanced from the vendored file (name-table + md5 + instancer log evidence), OFL.txt beside both copies, embed comment records the decision; full bookpdf suite green on the instanced pair |

---

## Mutation verification — new pins red under their mutants, restored byte-identical

Each mutant was applied to the live file, the new pin run, then the file restored from a `/tmp/t10f-r2-fix/` backup and md5-verified byte-identical. Pre-mutation md5s (pristine, matching round-2's survey): `pipeline.go df56ef6d0c59b8e59b5761cc783a3655`, `bookpdf.go 323d7b40ce3152e70b8be9c4a01c3f66` (pre-doc-edit baseline; the demo below ran against the post-embed-doc state `2436f30150b55dbec25a32058aeded46` and restored to it).

| Finding / new pin | Mutant applied | Result on pin | Restore |
|---|---|---|---|
| **L2** — `TestDoubleFireRefused` (extended: exactly one surviving `application/pdf` row = second run's pdf id, distinct from the first) | **N2**: `supersedePDFs` made a no-op (immediate `return` before any deletion) | **RED** — `pipeline_test.go:728: pdf rows after two runs = 2, want 1 (the old PDF is superseded)` | pipeline.go md5 `df56ef6d…` — **OK** (byte-identical); full bookgen suite green after restore |
| **L3** — `TestRender_BylinePrefixDeduplication` (content-observing: expected byline's UTF-16BE bytes present, `"By By"` UTF-16BE absent, over decompressed Flate streams) | **N3**: call-site bypass — `bookpdf.go:122` `formatByline(in.Byline)` → `in.Byline` | **RED** — `with_by_prefix` and `without_prefix` subtests: `cover byline "By Leo"/"By Mira" missing from the rendered text` (the bypass drops the prefix for unprefixed input) | bookpdf.go md5 `2436f301…` — **OK**; suite green |
| **L3** (same pin) | Naive-helper mutant (round-1 L2's form: `"By " + strings.TrimSpace(b)` with no stripping) — previously visible only to `TestFormatByline`, now also caught at the render surface | **RED** — `with_By_prefix` subtest: `cover text contains the doubled byline prefix "By By"` (absence pin); `with_by_prefix`: `cover byline "By Leo" missing` (presence pin) | bookpdf.go md5 `2436f301…` — **OK**; suite green |
| **L1** — tidy state | n/a (not a runnable mutant) | `go mod tidy -diff` after the fix prints nothing, exit 0 — fpdf sits in the direct require block | go.mod md5 `b189dbbf…` (post-tidy); go.sum md5 `114e0f16…` — unchanged from before tidy |

---

## Fredoka decision — evidence (L5)

| File | md5 | Size | Name table (family / subfamily / version) | fvar |
|---|---|---|---|---|
| `static/vendor/fonts/Fredoka[wdth,wght].ttf` (canonical, 95cd577) | `7b33fede…` | 159184 B | "Fredoka Light" / "Regular" / 2.001 | **variable**: wght 300–700 (**default 300**), wdth 75–125 (default 100); named instances at wght 400 ("Fredoka Regular") and 700 ("Fredoka Bold") |
| `internal/bookpdf/fonts/Fredoka-Regular.ttf` **before** (removed) | `96bf0807…` | 48856 B | "Fredoka" / "Regular" / 2.001 | no — static, Google-static naming, mtime 23:16 **predating** the vendor file's 23:30; not the vendored file, no recorded derivation, no OFL.txt beside |
| `internal/bookpdf/fonts/Fredoka-Bold.ttf` **before** (removed) | `c2493ce0…` | 48588 B | "Fredoka" / "Bold" / 2.001 | no — same provenance gap |
| `internal/bookpdf/fonts/Fredoka-Regular.ttf` **after** | `e980bc2a…` | 50196 B | "Fredoka" / "Regular" / 2.001 | no — **instanced from the canonical file** (wght=400, wdth=100) |
| `internal/bookpdf/fonts/Fredoka-Bold.ttf` **after** | `fdc9dae6…` | 49908 B | "Fredoka" / "Bold" / 2.001 | no — **instanced from the canonical file** (wght=700, wdth=100) |
| `OFL.txt` (canonical and embedded copy) | `21f5400b…` (identical at both paths) | 4388 B | — | — |

Recorded derivation (run with `python3 -m fontTools.varLib.instancer`, fontTools 4.64.0):

```
python3 -m fontTools.varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' wght=400 wdth=100 --update-name-table -o internal/bookpdf/fonts/Fredoka-Regular.ttf
python3 -m fontTools.varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' wght=700 wdth=100 --update-name-table -o internal/bookpdf/fonts/Fredoka-Bold.ttf
```

Instancer log confirms: axes restricted to the named-instance coordinates, `fvar`/`HVAR` dropped, `glyf` instantiated, `OS/2.usWeightClass` set to 400/700, name table updated. The pair is a drop-in replacement for the old one at the same embed paths; the full bookpdf suite (renders with the real fonts) is green on the instanced pair.

Why two copies remain (documented in the `bookpdf.go` embed comment): `//go:embed` cannot reach `static/` from `internal/bookpdf` (patterns may not leave the package directory), and fpdf reads only a font's default instance — the vendored variable face defaults to Light, and fpdf does no variable instancing — so bookpdf must carry its own static Regular/Bold instances. They are now derived from the single canonical vendor file by a recorded step, with the OFL 1.1 licence text beside every copy.

---

## Gates

All gates run from the remediated tree:

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | **PASS** — 0 files |
| Vet | `go vet ./...` | **PASS** |
| Build | `go build ./...` | **PASS** |
| Tidy | `go mod tidy -diff` | **PASS** — clean (fpdf in the direct require block; go.sum unchanged) |
| Targeted | `go test ./internal/bookpdf ./internal/bookgen ./internal/mediastore ./cmd/thutapi -race -count=1 -cover` | **PASS** — bookpdf **96.4%** (floor 75), bookgen **81.7%** (75), mediastore **95.6%** (75), cmd/thutapi **86.1%** (75) — coverage identical to round 2's claims; the new pins live in test code and change no production statements |
| Workspace | `go test ./... -race -count=1` | **PASS** — all 16 packages green. (Round 2 recorded an intermittent external flake in `internal/interview`'s `TestRestartedHandlerSeesEndedInterview` under full-suite `-race` load that reproduces at the pristine base and is not T10f-attributable; it did not fire this run.) |

---

## Files changed

| File | Change | Final md5 |
|---|---|---|
| `go.mod` | `go mod tidy`: fpdf moved to the direct require block, `// indirect` dropped | `b189dbbf…` |
| `go.sum` | untouched by tidy | `114e0f16…` (unchanged) |
| `internal/bookgen/bookgen.go` | package doc: event catalogue (`narration_unavailable`, `book_ready` payload), fires-paragraph semantics, stage list (PDF stage between narrate and film, outage branch), intro enumeration | `11ab80ad…` |
| `internal/bookgen/pipeline.go` | `renderFilm` doc renumbered stage 4 → stage 5 | `f337f00a…` |
| `internal/bookgen/pipeline_test.go` | `TestDoubleFireRefused` extended with the PDF-supersede assertions (L2 pin) | `29f23bc1…` |
| `internal/bookpdf/bookpdf.go` | embed comment block: canonical source, recorded instancer commands, embed/instancing constraint (L5 documentation); package doc touched to match | `2436f301…` |
| `internal/bookpdf/bookpdf_test.go` | `TestRender_BylinePrefixDeduplication` replaced with the content-observing pin + `pdfContentText`/`utf16be` helpers; imports updated (L3 pin) | `15144e3b…` |
| `internal/bookpdf/fonts/Fredoka-Regular.ttf` | replaced by the instanced instance of the vendored variable face (wght=400, wdth=100) | `e980bc2a…` |
| `internal/bookpdf/fonts/Fredoka-Bold.ttf` | replaced by the instanced instance of the vendored variable face (wght=700, wdth=100) | `fdc9dae6…` |
| `internal/bookpdf/fonts/OFL.txt` | added — SIL OFL 1.1 licence text beside the redistributed copies | `21f5400b…` |
| `dev-diary/adversarial-review/t10f-remediation-round2.md` | this record | — |

Touch scope respected: only `go.mod`/`go.sum`, `internal/bookgen/**` (docs + tests), `internal/bookpdf/**` (embed source + byline pin + tests), and the record file. PLAN.md, `internal/mediastore`, `cmd/`, and every other package were not modified.

---

## Residue note

- **L1–L5 remediated.** All restores byte-identical (md5-verified); the two new pins are RED under their mutants and GREEN on the restored tree.
- **PLAN.md §T10f "note the single source"** (round-2's L5 fix suggestion) was deliberately **not** performed: PLAN.md is outside this remediation's touch scope. The single-source decision, instancer commands and hashes are recorded here and in the `bookpdf.go` embed comment; the orchestrator may add the PLAN.md note when landing.
- **`internal/bookgen/live_test.go` stage labels left untouched** (observed, not a round-2 finding): its `Stage N: …` log strings and the STAGE TIMINGS BREAKDOWN use the live probe's own measured-phase row numbering (structure/illustrate/narrate/film-render/film-persist), and the PDF renderer is wired raw there and untimed, so the rows are not claims about pipeline stage numbers and adding a PDF row would require inventing timing metrics. Recorded so round 3 can rule on it deliberately.
- **`gofmt`-reformatted doc comments**: gofmt's doc-comment canonicalization reflowed the touched comment blocks in `bookgen.go`/`pipeline.go`/`bookpdf.go`/`bookpdf_test.go`; content is as reviewed above.
- Per AGENTS.md, the remediation agent does not alter the review verdict. Round 3 must independently audit the remediation and confirm zero residue before issuing an APPROVE.
