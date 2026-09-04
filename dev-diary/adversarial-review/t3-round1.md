# T3 — Store and media, review round 1

- **Target:** the uncommitted working tree on `main` (reviewed as-is, no stash,
  no fixes applied). Base for the diff: **0c60c6d** ("T2 closes at round 5").
- **Date:** 2026-09-05.
- **Reviewer:** round-1 review agent (fresh role per AGENTS.md §Agentic
  development; not the implementer).
- **Evidence read in full:**
  - `AGENTS.md` (binding; §Go style, §Testing, §CI and images)
  - `dev-diary/PLAN.md` (§T3, §T0 conventions, §Architectural invariants
    1–8, §Unowned seams, §T1/T1b check-5 re-scope, §T8/T9/T10/T11/T13
    assumptions about media and store)
  - `dev-diary/project.md` (§1 Phase-B data model, §Architecture
    consequences, §Pipeline, §Deployment)
  - `dev-diary/adversarial-review/README.md` (per-finding schema, severity key)
  - `internal/store/*.go` + `*_test.go` (store.go, books.go, pages.go,
    cast.go, interviews.go, media.go and all six test files)
  - `internal/mediastore/*.go` + `*_test.go` (mediastore.go,
    mediastore_test.go, restart_test.go)
  - `cmd/thutapi/main.go` + `main_test.go` (T3 hunks and their context)
  - `go.mod` / `go.sum`; `Dockerfile`, `deploy/docker-run.sh`,
    `.gitignore` (consumption only — they are T1/T0-owned — to answer
    whether the restart-survival promise holds in production)
- **Method:** probe, don't trust prose. Full seam read first; then
  reviewer-written probe tests in `zz_probe_test.go` (one per package)
  for everything the implementation's tests do not pin, run under
  `-race`, output recorded verbatim below, **probe files then removed**.
  At close the tree is byte-identical to the reviewed tree except this
  file (`git status --porcelain` shows only the pre-existing T3 entries
  plus `dev-diary/adversarial-review/t3-round1.md`). No live GMI or
  network calls; everything ran locally against temp dirs and
  `httptest`. Gates re-run in full (below).

## Verdict

**REMEDIATE** — 0 × C, **1 × H**, **1 × M**, **1 × L.**

Every severity lands a remediation row (AGENTS.md: no severity is
exempt). Exit criteria for round 2 are at the bottom.

## Findings

| # | Severity | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| 1 | **H** | `internal/mediastore/mediastore.go:47-50` (declaration); return sites `:224-245`, `:194-204` | `ErrNotFound` is declared and documented as the package's boundary sentinel — *"ErrNotFound is returned when a media id has no stored blob: unknown id, a malformed one, or a row whose file has vanished. Match with errors.Is."* — **and no code path ever returns it.** `ServeHTTP` maps every not-found shape to HTTP codes without the sentinel; `Delete` on an unknown id wraps only `store.ErrNotFound`. The package contradicts itself: its own test (`mediastore_test.go:334`) asserts `errors.Is(err, store.ErrNotFound)` on a second `Delete`, while the doc sells `mediastore.ErrNotFound`. Any consumer following the doc — T8 audio, T11's retention sweep, a future handler — matches the wrong sentinel and misclassifies "already gone" as an unexpected failure. Invariant 8 (errors cross a boundary as a sentinel matched with `errors.Is`) plus AGENTS.md's docs-about-behaviour rule; same shape as `t2-round3.md` H2 (contract claimed in `errors.go`, unimplemented). | Probe `TestProbeDeleteUnknownIDSentinelContract` (verbatim below): `Delete(unknown 32-hex)` → `errors.Is(err, mediastore.ErrNotFound) = false`, `errors.Is(err, store.ErrNotFound) = true`; grep: `ErrNotFound` appears only at its declaration (line 50) and its doc (47). | Remediate either way the doc and code agree: make not-found paths return it (e.g. `Delete`: `fmt.Errorf("mediastore: delete: %w: %w", ErrNotFound, err)`), **or** delete the declaration and re-point the doc at `store.ErrNotFound`. Re-introduction is the current line 196 (`fmt.Errorf("mediastore: delete: %w", err)`) — with a permanent pin asserting `errors.Is(err, ErrNotFound)` on Delete-of-unknown, that line fails the pin. |
| 2 | **M** | `internal/store/store.go:180-186` (`media` DDL); `internal/store/media.go` (API surface) | **A blob cannot be bound to a page (or a cast member / role), and the store cannot list a book's media at all.** Every Phase-B column project.md §1 names is present and correct for T5 (title; page `text/prompt/characters/emotion/lines`; cast `visual/pitch/sound_effects`) — but T6 must attach one reference image per cast member and one illustration per page, T8 must attach narration (and per-line audio) per page, and T10's flipbook renders by asking "page n → image URL + audio URL". The `media` table carries only `book_id`; `pages` carries no media reference; the exported API (probe below: 23 methods) has no media-listing or page-binding query. As shipped, "which blob is page 3's illustration?" is unanswerable by any query. The workaround — a later track migrates the schema — is real but costly: `CREATE TABLE IF NOT EXISTS` never alters an existing table, so the first deployed data dir cannot be migrated in place. Review brief: a column T5/T6 will need that is missing is a finding. | Probe `TestProbeExportedStoreAPISurface` (verbatim below): the complete `*store.DB` method set — only `CreateMedia / Media / SetMediaBook / DeleteMedia`; nothing enumerates a book's media or binds one to a page. Plus the DDL: `media(id, book_id, content_type, size_bytes, created_at)` — no `page_n`, no `kind`. | Add the binding, e.g. `ALTER TABLE media ADD COLUMN page_n INTEGER` + `kind TEXT` (or `pages.media_id` + a cast-image reference), a `Media(bookID)`-style listing, and permanent tests that attach per-page blobs and read the mapping back through a restart. Reverting those columns re-introduces the gap; the probe question becomes unanswerable again. |
| 3 | **L** | `internal/mediastore/mediastore.go:229-232`, `:243-245` (both ServeHTTP 500 branches); `:183-184`, `:186-187` (`writeBlob` fail "sync"/"close"); `:174-175` (fail-closure Remove-error log) | **Five error branches ship with no test** — exactly the "branches you expect never to fire" AGENTS.md §Testing rule 3 says must be asserted, because that is where the wrong classification hides. Coverprofile proves it: counts are 0 on all five blocks. The two ServeHTTP 500s are triggerable by honest black-box means (a closed/corrupt DB for the metadata branch; an unopenable blob for the open branch), so the gap is a missing test, not an untestable design. | `go test ./internal/mediastore -coverprofile` (verbatim below): `mediastore.go:229.3,232.1 3 0`, `:243.3,245.9 3 0`, `:183.3,184.1 1 0`, `:186.3,187.1 1 0`, `:174.4,175.1 1 0`. | Add table-driven pins: serve after `db.Close()` → assert 500; make the blob unopenable (`chmod 000`, skipped under root like `TestPersistIntoReadOnlyDirFails`) → assert 500; fault-inject `Sync`/`Close` (a failing `io.Writer` wrapper is not enough for close — wrap the file handle) → assert the partial file is removed. Deleting either 500 branch must turn a pin red; today deleting both leaves the suite green. |

## Probe transcripts (verbatim)

Reviewer-written `zz_probe_test.go` per package; run
`go test -race -count=1 -v -run 'Probe' ./internal/store ./internal/mediastore`;
files removed after recording.

### DSN pragma claims hold (store.go doc: "the DSN carries the pragmas")

Driver-level confirmation first: modernc.org/sqlite v1.58.0 documents and
parses the exact shorthand keys used (`driver.go:113-115`,
`sqlite.go:307-328`: `_busy_timeout`, `_foreign_keys`/`_fk`,
`_journal_mode`/`_journal`). Empirically:

```
=== RUN   TestProbeDSNPragmasInEffect
    zz_probe_test.go:27: PRAGMA foreign_keys -> 1
    zz_probe_test.go:27: PRAGMA journal_mode -> wal
    zz_probe_test.go:27: PRAGMA busy_timeout -> 5000
--- PASS: TestProbeDSNPragmasInEffect (0.02s)
```

### Two-writer contention (busy_timeout is real, not decorative)

Writer B is a separate `sql.DB` pool on the same file — the two-process
case. A holds a write txn; B inserts; A commits after 300 ms:

```
=== RUN   TestProbeSecondConnectionHonoursBusyTimeout
    zz_probe_test.go:73: B blocked for 334ms, err = <nil>
--- PASS: TestProbeSecondConnectionHonoursBusyTimeout (0.35s)
```

B blocked and then succeeded — with `busy_timeout` inert this returns
SQLITE_BUSY immediately.

### Context cancellation crosses the boundary `errors.Is`-ably

```
=== RUN   TestProbeContextCancelPropagates
    zz_probe_test.go:89: Books: err = store: books: context canceled, errors.Is(context.Canceled) = true
    zz_probe_test.go:94: DeleteBook: err = store: delete book aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: context canceled, errors.Is(context.Canceled) = true
--- PASS: TestProbeContextCancelPropagates (0.01s)
```

### H1 pin — the sentinel the doc promises is never returned

```
=== RUN   TestProbeDeleteUnknownIDSentinelContract
    zz_probe_test.go:233: Delete(unknown): err = mediastore: delete: store: delete media ffffffffffffffffffffffffffffffff: not found
    zz_probe_test.go:234:   errors.Is(err, mediastore.ErrNotFound) = false
    zz_probe_test.go:235:   errors.Is(err, store.ErrNotFound)      = true
--- PASS: TestProbeDeleteUnknownIDSentinelContract (0.01s)
```

### M1 pin — the API surface cannot express page ↔ media

```
=== RUN   TestProbeExportedStoreAPISurface
    zz_probe_test.go:106: exported methods of *store.DB: 23
    zz_probe_test.go:108:   Book
    zz_probe_test.go:108:   Books
    zz_probe_test.go:108:   Cast
    zz_probe_test.go:108:   Close
    zz_probe_test.go:108:   CreateBook
    zz_probe_test.go:108:   CreateCastMember
    zz_probe_test.go:108:   CreateInterview
    zz_probe_test.go:108:   CreateMedia
    zz_probe_test.go:108:   CreatePage
    zz_probe_test.go:108:   DeleteBook
    zz_probe_test.go:108:   DeleteCastMember
    zz_probe_test.go:108:   DeleteInterview
    zz_probe_test.go:108:   DeleteMedia
    zz_probe_test.go:108:   DeletePage
    zz_probe_test.go:108:   Interview
    zz_probe_test.go:108:   Media
    zz_probe_test.go:108:   Page
    zz_probe_test.go:108:   Pages
    zz_probe_test.go:108:   SetMediaBook
    zz_probe_test.go:108:   UpdateBook
    zz_probe_test.go:108:   UpdateCastMember
    zz_probe_test.go:108:   UpdateInterview
    zz_probe_test.go:108:   UpdatePage
--- PASS: TestProbeExportedStoreAPISurface (0.01s)
```

### Range protocol edges the implementation's table does not pin

All correct by delegation to `http.ServeContent`; recorded so the
behaviour is pinned knowledge, not assumption. (The malformed-Range 416
is stdlib's long-standing deliberate answer — the plan pinned the
mechanism to `http.ServeContent`, so this is not a finding.)

```
=== RUN   TestProbeRangeProtocolEdges/multi-range
    zz_probe_test.go:53: status=206 content-type="multipart/byteranges; boundary=4d9ab2141912140958a1bd3d7fd222721977790dae0cf05e370e4b025947" content-range="" content-length="320" body-bytes=320
=== RUN   TestProbeRangeProtocolEdges/unsatisfiable
    zz_probe_test.go:53: status=416 content-type="text/plain; charset=utf-8" content-range="bytes */1024" content-length="" body-bytes=33
=== RUN   TestProbeRangeProtocolEdges/malformed
    zz_probe_test.go:53: status=416 content-type="text/plain; charset=utf-8" content-range="" content-length="" body-bytes=14
=== RUN   TestProbeRangeProtocolEdges/if-range_mismatch
    zz_probe_test.go:53: status=200 content-type="audio/mpeg" content-range="" content-length="1024" body-bytes=1024
=== RUN   TestProbeRangeProtocolEdges/empty_blob
    zz_probe_test.go:68: status=200 content-length="0" body-bytes=0
--- PASS: TestProbeRangeProtocolEdges (0.02s)
```

### Path traversal, HEAD, mux-level crafted ids — triple defense holds

`validID` (32-hex only) + `os.Root` confinement + ServeMux path cleaning;
nothing outside the blob dir was touched:

```
=== RUN   TestProbeTraversalHeadAndMethod
    zz_probe_test.go:86: HEAD: status=200 content-length="64" body-bytes=0
    zz_probe_test.go:106: handler id ".."                                     -> 404
    zz_probe_test.go:106: handler id "../secrets"                             -> 404
    zz_probe_test.go:106: handler id "%2e%2e"                                 -> 404
    zz_probe_test.go:106: handler id "../../../etc/passwd"                    -> 404
    zz_probe_test.go:106: handler id "59F4E43B9B4AAED70CECCAECEDC2D108"       -> 404
    zz_probe_test.go:106: handler id "59f4e43b9b4aaed70ceccaecedc2d10"        -> 404
    zz_probe_test.go:106: handler id "59f4e43b9b4aaed70ceccaecedc2d1080"      -> 404
    zz_probe_test.go:106: handler id "0000000000000000000000000000000g"       -> 404
    zz_probe_test.go:124: mux "/media/"                                     -> 404
    zz_probe_test.go:124: mux "/media/.."                                   -> 307
    zz_probe_test.go:124: mux "/media/..%2F..%2Fetc%2Fpasswd"               -> 404
    zz_probe_test.go:124: mux "/media/%2e%2e%2f%2e%2e%2fetc%2fpasswd"       -> 404
    zz_probe_test.go:124: mux "/media/59f4e43b9b4aaed70ceccaecedc2d108%2F"  -> 404
    zz_probe_test.go:124: mux "/media/59f4e43b9b4aaed70ceccaecedc2d108/extra" -> 404
    zz_probe_test.go:137: blob dir contains only valid ids (1 entries)
--- PASS: TestProbeTraversalHeadAndMethod (0.01s)
```

(`/media/..` → 307 is ServeMux cleaning to `/`, which is unregistered —
the followed redirect 404s. HEAD works with correct Content-Length.)

### Concurrency under `-race`

```
=== RUN   TestProbeConcurrentPersistAndServe
    zz_probe_test.go:179: 16 concurrent persist+read+serve cycles clean under -race
--- PASS: TestProbeConcurrentPersistAndServe (0.04s)
```

(`os.Root` is documented safe for concurrent use; the single-connection
pool serialises SQLite — confirmed empirically.)

### Corrupted blob file across a restart — recorded behaviour, no finding

No integrity mechanism exists or is promised anywhere in the plan; the
row-without-file case (worse) *is* handled honestly (404 + log). A
file that exists but holds wrong bytes serves as-is:

```
=== RUN   TestProbeCorruptBlobFileOnRestart
    zz_probe_test.go:221: corrupted-blob serve: status=200 content-type="audio/mpeg" content-length="7" body="GARBAGE" etag="\"8ae73c48b0cf82013e74104a78730367\""
--- PASS: TestProbeCorruptBlobFileOnRestart (0.02s)
```

## Pins run

Reviewer probes were run once (transcripts above) and removed. The
implementation's load-bearing pins were re-run in full via the gates; the
ones this verdict rests on:

| Pin | What it proves | Result |
|---|---|---|
| `TestBookSurvivesRestartWithMedia` (internal/mediastore/restart_test.go) | PLAN §T3's Done when end to end: book + pages + cast + interview + blob written, DB closed, fresh store reopens the dir, blob serves with a 206 Range | PASS (`-race`) |
| `TestServeHTTPRangeRequests` (5-case table incl. `bytes=-5` suffix and open-ended) | Range-capable serving — the T1b check-5 seam T3 owns | PASS |
| `TestDeleteBookCascades` (internal/store/books_test.go) | FK cascade actually removes pages, cast, interview, media rows | PASS |
| `TestPersistWithCancelledContextFailsClean` | cancelled ctx → no row, no orphan file | PASS |
| `TestPersistRejectsUnsupportedContentTypes` / `...AcceptsSupportedTypes` / `...NormalizesContentType` | the closed content-type set, both directions, plus parameter/case normalisation | PASS |
| `TestMediaRouteServesThroughMux` (cmd/thutapi) | the one sanctioned route line: 404 unknown, 404 malformed, 405 non-GET | PASS |
| `TestNewIDIsUnguessable`, `TestCreateWithDeadRandSourceIsInternal` | 128-bit crypto/rand ids; dead entropy → `ErrInternal` | PASS |
| reviewer probe `TestProbeDSNPragmasInEffect` | foreign_keys=1, wal, busy_timeout=5000 live on the connections used | PASS (removed) |
| reviewer probe `TestProbeSecondConnectionHonoursBusyTimeout` | cross-connection writer blocks 334 ms then succeeds | PASS (removed) |
| reviewer probe `TestProbeDeleteUnknownIDSentinelContract` | H1 pin: `mediastore.ErrNotFound` unmatched | evidence (removed) |
| reviewer probe `TestProbeExportedStoreAPISurface` | M1 pin: 23 methods, no page/media binding or listing | evidence (removed) |
| coverprofile | L1 pin: five branches at count 0 | recorded (above) |

## Gates (re-run at review, probe files removed)

```
go vet ./...                      -> clean
go test ./... -race -count=1      -> ok (all packages)
gofmt -l .                        -> clean
go test ./... -cover              -> cmd 81.5%, mediastore 87.4%, store 86.6%,
                                     gmi/media 95.1%, gmi/text 90.4% (floors 75/85 hold)
CGO_ENABLED=0 go build ./...      -> ok
```

## Invariant audit

1. **`internal/gmi` only talks to GMI** — holds; neither new package makes network calls.
2. **Config struct, no env reads in packages** — holds for both new packages (grep: the only `os.Getenv` hits are pre-existing T2 `internal/gmi`, a recorded deviation); `DATA_DIR` resolves in `main` and flows down through `store.Config` / `mediastore.Config`.
3. **Concrete returns; consumers declare interfaces** — `Open` returns `*DB` / `*Store`; no exported interfaces anywhere. `newServer` takes the concrete handler — acceptable for the composition root; the handler *is* the product here.
4. **Queue is a queue** — untouched, n/a.
5. **One route line in `newServer`** — exactly one added (`s.mux.Handle("GET /media/{id}", s.media)`); handler lives in the track's package; `main` stays free of business logic (the `run()` wiring is composition).
6. **Nothing blocks > 100 s** — n/a for T3 (no long work yet); `Persist`'s copy is un-bounded by ctx and the doc says so honestly; callers own deadlines.
7. **Persist GMI output on receipt** — the mechanism ships (`Persist(bytes) → id`, fsync-before-row, orphan cleanup); URL strings are never stored. Callers arrive with T6/T8.
8. **Sentinels across boundaries** — holds in `internal/store` (four sentinels, `%w`, `errors.Is`, extended-result-code classification empirically proven by the conflict/FK tests) — and is the exact place it fails in `internal/mediastore` (finding 1).

## Examination boundary

- Reviewed the whole T3 `Done when` and every seam T3 touches — not just
  the diff: schema vs project.md's data model, restart survival at the
  store level *and* the blob level, Range at the HTTP level, the
  content-type gate, id guessability, traversal, concurrency, cascade,
  orphan behaviour, wiring, and the deployment consumption (bind mount +
  `DATA_DIR=/data` in `deploy/docker-run.sh`, `VOLUME /data` in the
  Dockerfile — the restart promise holds in production).
- Not re-reviewed (pre-existing, outside T3's patch, closed with T2):
  `internal/gmi`'s env reads and `interface{}` spellings; T0's
  `errShutdownTimeout` doc wording.
- Out of scope by method: live GMI/network probes (all probes local per
  the brief); power-loss durability beyond the process-restart claim
  (blob fsync but no directory fsync — the process-restart claim as
  written holds); corrupt-blob integrity (no mechanism promised; recorded
  above); stdlib's 416-on-malformed-Range (deliberate stdlib contract).
- Notes, not findings: PLAN.md's status table still says "T3 Not started"
  while T3 code is on the tree — the orchestrator updates that row at
  land time, as with T2. `go.sum` is new and must land with the track's
  commit.

## Exit criteria for round 2

1. **H1** — `mediastore.ErrNotFound` either returned by every not-found
   path its doc names, or gone, with the doc re-pointed at
   `store.ErrNotFound`. Permanent table-driven pin: for Delete of an
   unknown id (and any other not-found-returning API added), assert
   `errors.Is` on the sentinel the doc advertises. Doc and code must
   agree (AGENTS.md docs-about-behaviour).
2. **M1** — the page ↔ blob binding is representable and queryable
   (e.g. `media.page_n` + `media.kind`, or `pages.media_id`), with a
   media-listing API for a book; permanent tests attach per-page blobs
   and read the mapping back through a restart; state the fresh-data-dir
   migration assumption explicitly or make the migration real for
   existing files.
3. **L1** — every error branch asserted per AGENTS.md §Testing rule 3:
   the two ServeHTTP 500s (closed-DB metadata failure; unopenable blob)
   and `writeBlob`'s sync/close failure stages (fault-injected), with
   mutations that turn each new pin red.
4. Gates re-run clean: `go vet ./...`, `go test ./... -race -count=1`,
   `gofmt -l .`, per-package coverage ≥ 75% (85% for `internal/gmi/*`),
   `CGO_ENABLED=0 go build ./...`.
