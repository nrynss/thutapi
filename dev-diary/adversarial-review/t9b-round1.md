# T9b round 1 — adversarial review

| | |
|---|---|
| **Target** | Track T9b uncommitted changes: landing shelf in `internal/web/**` (`web.go`, `templates/shelf.html`, `web_test.go`), `static/app.js`, `static/app.css`, wiring in `cmd/thutapi/main.go`, `cmd/thutapi/main_test.go`, `internal/audio/music.go`, and restored fixture under `data/prewarm/d625fd608be48227f08c33cf860e5de8/**`. |
| **Evidence read** | `AGENTS.md` (all Go style rules, testing rules, seam boundaries); `dev-diary/PLAN.md` §T9b ("The shelf actually has books on it", lines 1988-2047) and §T11/§T9/§T10b historical records; git diff against `main`; full fixture verification in `data/prewarm/d625fd608be48227f08c33cf860e5de8/`. |
| **Verification** | `go test -count=1 -race ./...` (PASS); `go vet ./...` (clean); `test -z "$(gofmt -l .)"` (clean); package test coverage: `internal/web` 84.2%, `cmd/thutapi` 83.6%, `internal/audio` 84.0% (all clearing the 75% floor); `node --check static/app.js static/book/catchup.js static/browser-test.js` (PASS). |
| **Date** | 2026-09-06. Reviewer: Adversarial Review Agent (fresh agent, independent of implementer). |

**Verdict: REMEDIATE — 1 C / 1 H / 1 M / 1 L.**

The SSR and client landing shelf implementation, along with the cold-boot fixture restore and download handlers, are functionally sound in local testing. However, the prewarm fixture files are blocked by `.gitignore`, leaving them untracked so fresh clones and CI will immediately break. Furthermore, the test suite contains dynamic generator code mutating the source repository tree, breaches closed track T12's seam by exporting `audio.MeasureDuration`, and leaves an exported function untested.

---

## Findings Table

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| C1 | **C** | `.gitignore:25` | `.gitignore` line 25 (`data/`) ignores the entire `data/` directory, including `data/prewarm/**`. As a result, the restored fixture `data/prewarm/d625fd608be48227f08c33cf860e5de8/` (`book.json` and all 20 media blobs totaling ~12 MB) is completely untracked and ignored by git. A normal commit or push omits it; on any fresh clone (including GitHub Actions CI running `verify.yml` on push/PR, or a fresh deployment on Hetzner), `data/prewarm` does not exist. `TestColdBootRestoresFixtureAndServesShelfBookAndDownloads` in `cmd/thutapi/main_test.go:946` and `TestPrewarmFixtureCompletedAndRestored` in `internal/web/web_test.go:497` both fail fatally (`restored = [], want [d625fd608be48227f08c33cf860e5de8]` and `read manifest: open .../book.json: no such file or directory`). The cold-boot landing shelf demo is broken on any clean environment. | Shell probe simulating clean clone: `mv data/prewarm data/prewarm.bak && go test ./internal/web ./cmd/thutapi`. Both tests fail with fatal errors. `git check-ignore -v data/prewarm/d625fd608be48227f08c33cf860e5de8/book.json` confirms `.gitignore:25:data/`. | Keeping `data/` in `.gitignore` without an unignore rule (`!data/prewarm/` or `!data/prewarm/**`) keeps the fixture untracked; fresh clones fail CI verification. |
| H1 | **H** | `internal/web/web_test.go:537-666` | `TestPrewarmFixtureCompletedAndRestored` embeds a 130-line generator inside `if !hasVideo || !hasPDF` that dynamically executes `bookpdf.NewRenderer().Render`, `bookvideo.Render`, generates random hex IDs, and writes directly into the source repository tree via `os.WriteFile` on `fixtureDir/media/<id>` and `fixtureDir/book.json`. This violates AGENTS.md §Testing rules ("tests must not mutate repository files or produce persistent side-effects in the repository tree") and PLAN.md §T9b Part 2 ("The artefacts already exist and cost nothing to install ... Persist them as video/mp4 and application/pdf media rows against this book id, then re-export the fixture so a restore carries them. Do not start a paid generation to close this, and do not export bookgen's unexported renderFilm / renderPDF to do it either"). Because the fixture files are now on disk, this block is dead code during normal test runs. If the fixture were absent on a runner without ffmpeg, tests fail on external binary execution. | Run `go test ./internal/web` against a temporary copy of the fixture directory with an incomplete manifest; observe that the test runs ffmpeg and mutates files in place. | Remove the 130-line dynamic generation block and assert that the static fixture already contains all 20 media blobs and valid video/PDF entries. |
| M1 | **M** | `internal/audio/music.go:568-570` | Seam boundary violation: Track T9b owns `internal/web/**`, `static/app.js`, `static/app.css`, and `data/prewarm/**`. It does NOT own `internal/audio/**` (owned by closed tracks T12/T13). The implementer exported `MeasureDuration` from `internal/audio/music.go` solely to supply the test-time video generator in `internal/web/web_test.go:598`. No production code in `cmd/thutapi` or any package calls `audio.MeasureDuration`. In addition, `audio.MeasureDuration` lacks a test in `internal/audio/music_test.go`, violating AGENTS.md §Testing rules ("Every exported function has a test"). | `git grep -n "audio\.MeasureDuration"` reveals the only caller in the entire repository is `internal/web/web_test.go:598`. | Revert `MeasureDuration` back to unexported `measureDuration` in `internal/audio/music.go`. Zero production code breaks; only the test-time generator in `web_test.go` fails. |
| L1 | **L** | `internal/web/web.go:79-81` | Untested exported function: `web.Shelf(w http.ResponseWriter, r *http.Request)` is an exported package-level function retained for backward compatibility, but no test in `internal/web/web_test.go` or across the repository calls it. All tests call `ShelfHandler.Shelf`, and `cmd/thutapi/main.go` wires `s.shelf.Shelf`. AGENTS.md §Testing rules mandates: "Every exported function has a test." | `git grep -n "web\.Shelf("` finds 0 callers in any test file. | Replace the body of `web.Shelf` with `http.Error(w, "broken", http.StatusInternalServerError)`. Run `go test ./...`. All tests remain 100% green. |

---

## Detailed Analysis and Evidence

### 1. Seam Boundaries and File Ownership
- Authorized paths for T9b per PLAN.md §T9b:
  - `internal/web/**` (`web.go`, `templates/shelf.html`, `web_test.go`)
  - `static/app.js`, `static/app.css`
  - Wiring in `cmd/thutapi/main.go`, `cmd/thutapi/main_test.go`
  - `data/prewarm/d625fd608be48227f08c33cf860e5de8/**`
- Modified unauthorized paths:
  - `internal/audio/music.go`: Modified to export `MeasureDuration`. T9b has no ownership of `internal/audio/**` (finding M1).
- Omitted / uncommitted authorized paths:
  - `data/prewarm/**`: Files exist in working tree but are ignored by `.gitignore:25:data/` (finding C1).

### 2. Functional Requirements Audit

#### Part 1: Landing Shelf (SSR + Client)
- **SSR (No-JS fallback)**:
  - `templates/shelf.html` renders `<main id="app" data-interview-id="shelf" data-books="{{.BooksJSON}}">`.
  - If `.Books` is non-empty, renders `<section class="shelf-books"><h2>From the shelf</h2><div class="book-list">...</div></section>`.
  - Links point to `/book/{{.ID}}`.
  - Byline renders `<span class="book-byline warm">By {{.Byline}}</span>` when present.
  - `<noscript><p>Turn on JavaScript to make a new book. Shared books still open here.</p></noscript>` is preserved.
  - Tested in `web_test.go:TestShelfHandler_WithBooks` and `main_test.go:TestShelfRouteServesLandingAndBooks`.
- **Client (Preact)**:
  - `static/app.js`: `readShelfBooks()` extracts books from `<script type="application/json" id="shelf-books">` or `#app.dataset.books`.
  - `Shelf` component renders structural twin of server markup when `books.length > 0`.
  - Card duplication avoided: `root.replaceChildren()` in `app.js:599` empties `#app` before mounting Preact. `#shelf-books` is outside `#app`, so it remains in the DOM and readable.
  - Audio unlock: CTA `<a class="primary" href="/interview/new" data-start onClick=${onStart}>` calls `onStart` which executes `event.preventDefault(); unlockAudio(); navigate("/interview/new", "new");`. Preserved and verified by `browser-test.js:testShelfGestureAndGeneration`.
- **Public & Ungated**:
  - `cmd/thutapi/main.go`: `GET /{$}` is registered directly on `s.mux` and omitted from the `gated` route slice.
  - `cmd/thutapi/main_test.go`: `srvGated` with passcode gate confirms `GET /` returns `200 OK`.

#### Part 2: Restored Prewarm Fixture
- `data/prewarm/d625fd608be48227f08c33cf860e5de8/`:
  - `book.json`: Valid JSON, carries 8 pages, 4 cast members, and 20 media blobs.
  - `media/`: 20 files present:
    - 3 reference images (Bo, Grumpy Cloud, Pip)
    - 8 illustration images (pages 1–8)
    - 7 narration audio clips (pages 1, 2, 3, 5, 6, 7, 8; page 4 silent captioned tier)
    - 1 `video/mp4` (`59a3934071824cebf453e2da868d432c`, 3,477,486 bytes, 1080×1620 h264/aac, ~81.4s)
    - 1 `application/pdf` (`12c186127107e759e93917e2ad5d3b56`, 3,533,551 bytes, 9 pages)
  - Tested on cold boot by `TestColdBootRestoresFixtureAndServesShelfBookAndDownloads` in `main_test.go`.
  - State endpoint `/book/{id}/state` returns `ready` with both URLs.
  - PDF and MP4 downloads serve with `Content-Disposition` attachments and slugified filenames.
  - Direct film playback at `/media/{id}` serves video stream with 3,477,486 bytes.

### 3. Mutation Checks Executed
1. **Template range mutation**: Mutated `{{range .Books}}` to empty slice range in `shelf.html`.
   - Result: `TestShelfHandler_WithBooks`, `TestShelfRouteServesLandingAndBooks`, and `TestColdBootRestoresFixtureAndServesShelfBookAndDownloads` all failed immediately.
2. **JSON serialization mutation**: Mutated `BooksJSON` generation in `web.go` to always return `[]`.
   - Result: `TestShelfHandler_WithBooks` failed immediately (`len(parsed) = 0, want 2`).
3. **Store error handling mutation**: Mutated `ShelfHandler.Shelf` to return 500 on store error instead of empty shelf.
   - Result: `TestShelfHandler_StoreError` failed immediately (`status = 500, want 200`).
4. **Ungated route mutation**: Wrapped `GET /{$}` in gate passcode protection.
   - Result: `TestShelfRouteServesLandingAndBooks` Part 3 failed immediately on gated server.
5. **Absent fixture mutation**: Moved `data/prewarm` out of tree to simulate clean git clone.
   - Result: `TestPrewarmFixtureCompletedAndRestored` and `TestColdBootRestoresFixtureAndServesShelfBookAndDownloads` both failed immediately.

---

## Verification Commands Output

### 1. `go test -count=1 -race ./...`
```console
ok  	thutapi/cmd/thutapi	11.413s
ok  	thutapi/internal/audio	5.500s
ok  	thutapi/internal/bookgen	5.377s
ok  	thutapi/internal/bookpdf	2.132s
ok  	thutapi/internal/bookvideo	8.141s
ok  	thutapi/internal/gate	1.074s
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	6.103s
ok  	thutapi/internal/gmi/text	1.858s
ok  	thutapi/internal/illustrate	2.037s
ok  	thutapi/internal/interview	3.360s
ok  	thutapi/internal/job	1.435s
ok  	thutapi/internal/mediastore	14.845s
ok  	thutapi/internal/prewarm	2.042s
ok  	thutapi/internal/store	2.168s
ok  	thutapi/internal/story	1.036s
ok  	thutapi/internal/stream	16.441s
ok  	thutapi/internal/web	1.490s
ok  	thutapi/static/race	1.049s
```

### 2. `go vet ./...`
```console
(clean, exit 0)
```

### 3. `test -z "$(gofmt -l .)"`
```console
(clean, exit 0)
```

### 4. Coverage Floors (75% floor)
```console
thutapi/internal/web:   84.2% of statements
thutapi/cmd/thutapi:    83.6% of statements
thutapi/internal/audio: 84.0% of statements
```

### 5. Frontend Syntax Check
```console
node --check static/app.js static/book/catchup.js static/browser-test.js
(clean, exit 0)
```

---

## Remediation Plan

To reach **APPROVE (0/0/0/0, zero residue)** in Round 2:
1. **Fix C1**: Update `.gitignore` to unignore `data/prewarm/` (e.g., change `data/` to `data/*` with `!data/prewarm/`), and stage the full prewarm fixture `data/prewarm/d625fd608be48227f08c33cf860e5de8/` into git.
2. **Fix H1**: Remove the 130-line `if !hasVideo || !hasPDF` code block from `internal/web/web_test.go:TestPrewarmFixtureCompletedAndRestored`. The test should assert that the static fixture on disk is valid and complete without mutating repository files.
3. **Fix M1**: Revert the export of `MeasureDuration` in `internal/audio/music.go` back to unexported `measureDuration`. Remove the call to it from `internal/web/web_test.go`.
4. **Fix L1**: Add a test in `internal/web/web_test.go` that directly invokes `web.Shelf(w, r)` and verifies it serves `200 OK` with landing CTA.
