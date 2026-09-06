# T9b round 2 — adversarial review of the round-1 remediation

| | |
|---|---|
| **Target** | Track T9b round-1 remediation (`t9b-remediation-round1.md`), covering the four findings of `t9b-round1.md` (1 C / 1 H / 1 M / 1 L) across `.gitignore`, `internal/web/**` (`web.go`, `templates/shelf.html`, `web_test.go`), `static/app.js`, `static/app.css`, wiring in `cmd/thutapi/main.go` and `cmd/thutapi/main_test.go`, and restored fixture under `data/prewarm/d625fd608be48227f08c33cf860e5de8/**`. |
| **Evidence read** | `AGENTS.md` (all Go style rules, testing rules, seam boundaries); `dev-diary/PLAN.md` §T9b ("The shelf actually has books on it", lines 1988-2047) and §T11/§T9/§T10b historical records; git diff against `main`; full fixture verification in `data/prewarm/d625fd608be48227f08c33cf860e5de8/`. |
| **Verification** | `go test -count=1 -race ./...` (PASS repo-wide in ~16.5s); `go vet ./...` (clean); `test -z "$(gofmt -l .)"` (clean); coverage floor check per `verify.yml` (`internal/web` 84.2%, `cmd/thutapi` 83.6%, `internal/audio` 84.1%, clearing the 75% floor; `internal/gmi/media` 91.9%, `internal/gmi/text` 90.4%, clearing the 85% floor); `node --check static/app.js static/book/catchup.js static/browser-test.js` (PASS); `bash -n deploy/docker-run.sh` (clean); mutant probe execution on all pins. |
| **Date** | 2026-09-06. Reviewer: Adversarial Review Agent (fresh agent, independent of round 1 reviewer, round 1 implementer, and round 1 remediation agent). |

**Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L (zero residue).**

---

## Severity Counts

**0 C / 0 H / 0 M / 0 L**

Explicit zero-residue claim: All four findings from Round 1 (C1, H1, M1, L1) have been verified independently against the raw bytes, git index, and test suite. Every finding has been fully closed without residual defects, scope creep, or broken seams.

---

## Detailed Verification of Prior Findings

### 1. C1 Closure — Prewarm Fixture Git Tracking and `.gitignore` Rules
- **Verification of `.gitignore`**:
  Line 25 changed from `data/` to:
  ```gitignore
  data/*
  !data/prewarm/
  !data/prewarm/**
  ```
  Ran `git check-ignore -v data/prewarm/d625fd608be48227f08c33cf860e5de8/book.json` and all 20 media blobs. None are ignored.
  Ran `git check-ignore -v data/thutapi.db data/media data/media/test.png data/somefile`. All matched `.gitignore:25:data/*` and remain ignored.
- **Verification of staged files**:
  `git status` confirms `data/prewarm/d625fd608be48227f08c33cf860e5de8/book.json` and all 20 media files in `data/prewarm/d625fd608be48227f08c33cf860e5de8/media/` are staged (`Changes to be committed`).
- **Mutation Probe**:
  Temporarily moved `data/prewarm` out of tree (`mv data/prewarm data/prewarm.bak`).
  Ran `go test -run "TestPrewarmFixtureCompletedAndRestored|TestColdBootRestoresFixtureAndServesShelfBookAndDownloads" ./internal/web ./cmd/thutapi`.
  Both tests failed immediately with fatal errors:
  - `web_test.go:514: read manifest: open .../book.json: no such file or directory`
  - `main_test.go:946: restored = [], want [d625fd608be48227f08c33cf860e5de8]`
  Restored directory; both tests pass cleanly.
- **Status**: **CLOSED** (0 residue).

### 2. H1 Closure — Removal of Repository-Mutating Dynamic Generator in Tests
- **Verification of `internal/web/web_test.go`**:
  The 130-line dynamic generator block (`if !hasVideo || !hasPDF { ... }`) that invoked `bookpdf.NewRenderer().Render`, `bookvideo.Render`, generated random hex IDs, and mutated the working tree via `os.WriteFile` has been completely deleted.
  Unused generator imports (`crypto/rand`, `encoding/hex`, `thutapi/internal/audio`, `thutapi/internal/bookpdf`, `thutapi/internal/bookvideo`) were removed.
- **Static Assertions**:
  `TestPrewarmFixtureCompletedAndRestored` now asserts statically:
  - `len(manifest.Media) == 20`
  - `hasVideo` and `hasPDF` are present in manifest
  - All 20 blob files exist on disk with `info.Size() > 0`
  - Restore into an isolated temporary store (`t.TempDir()`) successfully restores the book, populates 20 media rows, serves the shelf card, answers the `/book/{id}/state` ready state, and serves both PDF and MP4 downloads.
  Tests run in 0.02s without spawning ffmpeg or mutating repository files.
- **Mutation Probe**:
  Grep confirmed zero occurrences of `bookpdf.NewRenderer` or `bookvideo.Render` in `internal/web/`.
- **Status**: **CLOSED** (0 residue).

### 3. M1 Closure — Restored Seam Boundary in `internal/audio`
- **Verification of `internal/audio/music.go`**:
  `MeasureDuration` has been reverted to unexported `measureDuration(b []byte) (time.Duration, error)`.
  `git diff main -- internal/audio/` produces 0 diffs.
  The ownership boundary of closed tracks T12/T13 is strictly preserved.
- **Caller Audit**:
  Repo-wide search for `MeasureDuration` confirms zero callers outside internal test fixtures in `internal/audio/music_test.go`.
- **Status**: **CLOSED** (0 residue).

### 4. L1 Closure — Test Coverage for `web.Shelf` Package-Level Function
- **Verification of `internal/web/web_test.go`**:
  `TestShelf_PackageLevelFunction` was added, directly testing `web.Shelf(w, r)`.
  Asserts HTTP 200 OK, `Content-Type: text/html`, CTA `Make your own book`, `data-interview-id="shelf"`, and empty JSON books script `<script type="application/json" id="shelf-books">[]</script>`.
- **Mutation Probe**:
  Mutated `web.Shelf(w, r)` to return `http.Error(w, "broken", http.StatusInternalServerError)`.
  Ran `go test -run TestShelf_PackageLevelFunction ./internal/web`.
  Test failed immediately (`web_test.go:430: status = 500, want 200`).
  Restored original implementation; test passed.
- **Status**: **CLOSED** (0 residue).

---

## Verification of Functional Requirements

### Part 1: Shelf UI (SSR and Preact)
- **SSR Fallback (`internal/web/templates/shelf.html`)**:
  - Contains standalone markup under `<main id="app" data-interview-id="shelf" data-books="{{.BooksJSON}}">`.
  - Loops over `.Books` with `{{if .Books}}<section class="shelf-books"><h2>From the shelf</h2><div class="book-list">{{range .Books}}<a class="book-card" href="/book/{{.ID}}"><span class="book-title">{{.Title}}</span>{{if .Byline}}<span class="book-byline warm">By {{.Byline}}</span>{{end}}</a>{{end}}</div></section>{{end}}`.
  - `<noscript>` banner is preserved for JavaScript-disabled clients.
  - Links to `/book/{{.ID}}` are public and ungated.
- **Client Twin (`static/app.js`)**:
  - `readShelfBooks()` safely extracts books from `<script type="application/json" id="shelf-books">` (placed outside `#app`) or `#app.dataset.books`.
  - `Shelf` component mirrors the server structure with `.shelf-books`, `<h2>From the shelf</h2>`, and `.book-card` links.
  - Card duplication is prevented: `root.replaceChildren()` at line 599 clears `#app` before mounting Preact. Since `#shelf-books` is outside `#app`, it remains intact and accessible.
  - Audio unlock gesture on `Make your own book` CTA is preserved (`event.preventDefault(); unlockAudio(); navigate("/interview/new", "new");`).
- **Ungated Routing**:
  - `GET /{$}` is registered directly on `s.mux` in `cmd/thutapi/main.go` and excluded from the `gated` slice.
  - Verified by `main_test.go:TestShelfRouteServesLandingAndBooks` Part 3 with passcode gate active: returns 200 OK without requiring passcode header.

### Part 2: Restored Prewarm Fixture
- **Fixture Contents (`data/prewarm/d625fd608be48227f08c33cf860e5de8/`)**:
  - `book.json`: Valid manifest with 8 pages, 4 cast members, and 20 media items.
  - `media/`: Exactly 20 media blobs present:
    - 3 reference sheets (JPEG, 1792×2240)
    - 8 page illustrations (JPEG, 1792×2240)
    - 7 narration audio clips (MP3, 32 kHz, 128 kbps; page 4 silent captioned tier)
    - 1 `video/mp4` (`59a3934071824cebf453e2da868d432c`, 3,477,486 bytes, 1080×1620, ~81.4s)
    - 1 `application/pdf` (`12c186127107e759e93917e2ad5d3b56`, 3,533,551 bytes, 9 pages)
- **Cold Boot Restore**:
  - Verified by `cmd/thutapi/main_test.go:TestColdBootRestoresFixtureAndServesShelfBookAndDownloads`.
  - In an empty `t.TempDir()`, `prewarm.Import` restores the book.
  - `GET /` serves the shelf listing with title "Bo and Pip's Moon Mango Dance" and link `/book/d625fd608be48227f08c33cf860e5de8`.
  - `GET /book/{id}/state` returns status `ready` with non-empty `pdf_url` and `video_url`.
  - `GET /book/{id}/download/pdf` serves `application/pdf` with `Content-Disposition` filename `bo-and-pips-moon-mango-dance.pdf`.
  - `GET /book/{id}/download/video` serves `video/mp4` with `Content-Disposition` filename `bo-and-pips-moon-mango-dance.mp4`.
  - Direct video playback (`GET /media/{id}`) serves all 3,477,486 bytes with `video/mp4` Content-Type.

---

## Verification Gates Output

1. **`go test -count=1 -race ./...`**:
   Passed across all packages in ~16.5s.
   ```
   ok  	thutapi/cmd/thutapi	10.760s
   ok  	thutapi/internal/audio	5.737s
   ok  	thutapi/internal/bookgen	5.600s
   ok  	thutapi/internal/bookpdf	2.001s
   ok  	thutapi/internal/bookvideo	8.625s
   ok  	thutapi/internal/gate	1.089s
   ok  	thutapi/internal/gmi/media	6.178s
   ok  	thutapi/internal/gmi/text	1.870s
   ok  	thutapi/internal/illustrate	1.971s
   ok  	thutapi/internal/interview	3.336s
   ok  	thutapi/internal/job	1.425s
   ok  	thutapi/internal/mediastore	14.936s
   ok  	thutapi/internal/prewarm	2.158s
   ok  	thutapi/internal/store	2.251s
   ok  	thutapi/internal/story	1.050s
   ok  	thutapi/internal/stream	16.463s
   ok  	thutapi/internal/web	1.589s
   ok  	thutapi/static/race	1.047s
   ```

2. **`go vet ./...`**:
   Clean (exit code 0).

3. **`test -z "$(gofmt -l .)"`**:
   Clean (exit code 0).

4. **Coverage Floors (`verify.yml` evaluation)**:
   - `thutapi/internal/web`: 84.2% (floor: 75.0%) — **PASS**
   - `thutapi/cmd/thutapi`: 83.6% (floor: 75.0%) — **PASS**
   - `thutapi/internal/audio`: 84.1% (floor: 75.0%) — **PASS**
   - `thutapi/internal/gmi/media`: 91.9% (floor: 85.0%) — **PASS**
   - `thutapi/internal/gmi/text`: 90.4% (floor: 85.0%) — **PASS**
   All packages satisfy coverage requirements.

5. **Frontend Syntax Check**:
   `node --check static/app.js static/book/catchup.js static/browser-test.js`
   Clean (exit code 0).

6. **Shell Syntax Check**:
   `bash -n deploy/docker-run.sh`
   Clean (exit code 0).

---

## Findings Table

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| — | — | — | *No findings. All prior findings remediated; zero new findings.* | — | — |

---

## Verdict

**APPROVE** (0 C / 0 H / 0 M / 0 L, zero residue).
Track T9b satisfies all functional requirements and architectural invariants, strictly adheres to file ownership seams, and passes all verification gates.
