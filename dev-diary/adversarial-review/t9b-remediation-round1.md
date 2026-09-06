# T9b round 1 — remediation

| | |
|---|---|
| **Target** | The four round-1 findings of `t9b-round1.md` (1 C / 1 H / 1 M / 1 L) in `Track T9b`. |
| **Date** | 2026-09-06. Remediation agent fresh to the round. |
| **Status** | COMPLETE — all four findings remediated; all CI gates green. Zero residue is claimed only after round 2's verdict. |

---

## Dispositions Table

| # | Sev | Where | Disposition | Validation |
|---|---|---|---|---|
| C1 | **C** | `.gitignore:25` | Updated `.gitignore` to replace broad `data/` ignore rule with `data/*` followed by unignore rules `!data/prewarm/` and `!data/prewarm/**`. Staged the prewarm fixture directory `data/prewarm/d625fd608be48227f08c33cf860e5de8/` (`book.json` and all 20 media blobs) for git tracking. | `git check-ignore -v data/prewarm/d625fd608be48227f08c33cf860e5de8/book.json` returns `!data/prewarm/**`. `git status` verifies all 21 fixture files are tracked/staged. `git check-ignore -v data/thutapi.db data/media/x` confirms non-prewarm data files remain ignored. |
| H1 | **H** | `internal/web/web_test.go:537-666` | Removed 130-line dynamic generator block (`if !hasVideo || !hasPDF { ... }`) from `TestPrewarmFixtureCompletedAndRestored`. Added static assertions verifying that the on-disk manifest contains 20 media items, all 20 blobs exist on disk with non-zero size, and both `video/mp4` and `application/pdf` are present. Retained restore and handler verification (`prewarm.Import`, `db.BookMedia`, `ShelfHandler.Shelf`, `BookHandler.Book`, `BookHandler.State`, `DownloadHandler.Download`) without mutating repository files or invoking external ffmpeg processes. Cleaned up unused imports (`crypto/rand`, `encoding/hex`, `thutapi/internal/audio`, `thutapi/internal/bookpdf`, `thutapi/internal/bookvideo`). | `go test -v -run TestPrewarmFixtureCompletedAndRestored ./internal/web` passes in 0.02s without modifying repository files or spawning subprocesses. |
| M1 | **M** | `internal/audio/music.go:568-570` | Reverted export of `MeasureDuration` in `internal/audio/music.go` back to unexported `measureDuration`. `internal/audio` is now completely unmodified relative to origin, maintaining the seam boundary of closed tracks T12/T13. | `git diff internal/audio/` produces 0 diffs. `go test -v ./internal/audio` passes cleanly (84.1% coverage). |
| L1 | **L** | `internal/web/web.go:79-81` | Added unit test `TestShelf_PackageLevelFunction` in `internal/web/web_test.go` exercising package-level function `web.Shelf(w, r)`. Verified 200 OK, `text/html` Content-Type, default shelf hero CTA (`Make your own book`), shelf interview ID (`data-interview-id="shelf"`), and empty JSON books script. | `go test -v -run TestShelf_PackageLevelFunction ./internal/web` passes. `internal/web` statement coverage is 84.2% (exceeding the 75% floor). |

---

## Detailed Remediation Actions

### C1: Prewarm Fixture Git Tracking
- **Issue**: `.gitignore` ignored `data/`, blocking git from tracking `data/prewarm/**`. Fresh clones and CI verification lacked fixture files, causing `TestColdBootRestoresFixtureAndServesShelfBookAndDownloads` and `TestPrewarmFixtureCompletedAndRestored` to fail fatally.
- **Remediation**:
  - Modified `.gitignore` around lines 24-27:
    ```gitignore
    # Generated media (kept on the box, not in the repo)
    data/*
    !data/prewarm/
    !data/prewarm/**
    ```
  - Staged `data/prewarm/d625fd608be48227f08c33cf860e5de8/book.json` and all 20 media blobs in `data/prewarm/d625fd608be48227f08c33cf860e5de8/media/`.
  - Verified with `git status` that all 21 fixture files are staged for tracking, while untracked databases or live media under `data/` remain ignored.

### H1: Repository-Mutating Test Generator Removal
- **Issue**: `TestPrewarmFixtureCompletedAndRestored` in `internal/web/web_test.go` contained a 130-line code block executing `bookpdf.NewRenderer().Render`, `bookvideo.Render`, generating random IDs, and mutating `data/prewarm/...` on disk. This violated AGENTS.md §Testing rules prohibiting repo mutation and external binary dependencies during testing.
- **Remediation**:
  - Removed lines 537-666 containing `if !hasVideo || !hasPDF { ... }`.
  - Replaced with static assertions:
    - `len(manifest.Media) == 20`
    - Presence of `video/mp4` and `application/pdf`
    - On-disk presence and non-zero size for every media blob referenced in `manifest.Media`
  - Retained the restore testing logic using `t.TempDir()`, `prewarm.Import`, `db.BookMedia`, `ShelfHandler.Shelf`, `BookHandler.Book`, `BookHandler.State`, and `DownloadHandler.Download`.
  - Removed unused imports: `crypto/rand`, `encoding/hex`, `thutapi/internal/audio`, `thutapi/internal/bookpdf`, `thutapi/internal/bookvideo`.

### M1: Seam Boundary Restoration in `internal/audio`
- **Issue**: `internal/audio/music.go` exported `MeasureDuration`, violating seam boundaries for closed track T12 and introducing an untested exported function.
- **Remediation**:
  - Removed the `MeasureDuration` export and restored the unexported `measureDuration` function.
  - With the removal of H1's dynamic video generator in `web_test.go`, zero external callers require `audio.MeasureDuration`.
  - `internal/audio` has 0 uncommitted diffs against origin.

### L1: Test Coverage for `web.Shelf`
- **Issue**: Exported function `web.Shelf(w http.ResponseWriter, r *http.Request)` in `internal/web/web.go` had zero test callers, violating the requirement that every exported function has a test.
- **Remediation**:
  - Added `TestShelf_PackageLevelFunction` in `internal/web/web_test.go`.
  - Tested `web.Shelf(w, r)` with an HTTP GET request to `/`, asserting:
    - HTTP status 200 OK
    - `Content-Type: text/html; charset=utf-8`
    - Body contains `"Make your own book"` CTA
    - Body contains `data-interview-id="shelf"`
    - Body contains `<script type="application/json" id="shelf-books">[]</script>`

---

## Verification Gates

| Check | Result | Details |
|---|---|---|
| `go test -count=1 -race ./...` | **PASS** | Clean run across all packages in ~16.5s |
| `go vet ./...` | **PASS** | Clean, zero diagnostics |
| `test -z "$(gofmt -l .)"` | **PASS** | Clean, zero formatting issues |
| `go test -cover ./internal/web ./cmd/thutapi ./internal/audio` | **PASS** | `internal/web`: 84.2%, `cmd/thutapi`: 83.6%, `internal/audio`: 84.1% (all above 75% floor) |
| `node --check static/app.js static/book/catchup.js static/browser-test.js` | **PASS** | Clean syntax validation |
| `git check-ignore -v data/prewarm/d625fd608be48227f08c33cf860e5de8/book.json` | **PASS** | Unignored via `!data/prewarm/**` |
| `git diff internal/audio/` | **PASS** | Clean, 0 diffs |

---

## Residue Note

- All four findings (C1, H1, M1, L1) from Round 1 are completely remediated.
- No unreviewed scope creep was introduced.
- Review verdict in `t9b-round1.md` remains untouched (`REMEDIATE`).
- Ready for Round 2 adversarial review.
