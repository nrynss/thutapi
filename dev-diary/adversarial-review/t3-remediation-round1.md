# T3 round 1 — remediation

| | |
|---|---|
| **Target** | All 3 findings (1 × H, 1 × M, 1 × L) in `dev-diary/adversarial-review/t3-round1.md`. Verdict was REMEDIATE (0C/1H/1M/1L). |
| **Date** | 2026-09-05 |
| **Commit** | Uncommitted at remediation time; the working tree stays uncommitted — the orchestrator commits on APPROVE (AGENTS.md §Process step 5). |
| **Round file** | `dev-diary/adversarial-review/t3-round1.md` is unchanged. PLAN.md is untouched — the T3 status row flips on APPROVE, as with T2. |
| **Verification methodology** | Every Pin re-run under `-race -count=1` against the remediated tree. All six code mutations from the round's exit criteria applied by hand (sed edits), each observed RED for the recorded reason, tree restored byte-identical after each (`cmp`/byte-compare in the harness), re-run green. Gate outputs in the aggregate section below. Everything ran locally against temp dirs and `httptest`; no live GMI or network calls. |
| **Scope note** | `internal/store/**` (store.go DDL + package doc, media.go, media_test.go), `internal/mediastore/**` (mediastore.go, mediastore_test.go, restart_test.go), and this file. Nothing else moved. `cmd/thutapi/main.go` + `main_test.go` needed no change: the M1 binding is entirely below the `/media/{id}` route line, which is unchanged — confirmed by the untouched round-1 pin `TestMediaRouteServesThroughMux` passing inside the full suite. |

## Rows

### H1 — `mediastore.ErrNotFound` documented as the boundary sentinel, returned by nothing

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `internal/mediastore/mediastore.go` — the `ErrNotFound` declaration (was :47-50), `Delete` (was :194-204), `ServeHTTP`'s not-found classification (was :224-245). |
| **What was done** | The docs are now true. Every not-found path the doc names returns an error matching `mediastore.ErrNotFound` via `errors.Is`, wrapping `store.ErrNotFound` where the missing row originates (Go 1.27 multi-`%w`): `Delete` on an unknown or malformed id wraps both sentinels; the handler's classification was extracted into an unexported `s.media()` lookup that (a) classifies a malformed id as `ErrNotFound` without touching the database, (b) classifies a missing row as `ErrNotFound` **with** `store.ErrNotFound` in the chain, and (c) returns every other failure — a closed database, a cancelled context — with no sentinel, so `errors.Is(err, ErrNotFound)` stays false for server faults and the 500 branch keeps its meaning; and the vanished-file (row-without-file) branch classifies through the same `notFound(id, err)` helper that lands in the operator log while the client still sees the same 404. Two deliberate refinements beyond the mutation's letter: the sentinel wrap in `Delete` is **conditional** on `errors.Is(err, store.ErrNotFound)` — the mutation's unconditional `fmt.Errorf("...: %w: %w", ErrNotFound, err)` would have made a closed-DB failure match `ErrNotFound`, i.e. "already gone" misclassifying "broken now", the exact defect class invariant 8 forbids; and the sentinel text moved from `"mediastore: not found"` to `"not found"` (store's spelling) — matching is `errors.Is`, never text, and the double wrap otherwise reads `mediastore: … : mediastore: not found: …`. All doc comments updated to match the behaviour (AGENTS.md docs-about-behaviour). |
| **Test added** | `TestNotFoundSentinelContract` — permanent, table-driven, four probes: `Delete` of an unknown id, `Delete` of a malformed id, `s.media()` (the lookup `ServeHTTP` classifies on) of an unknown id, and of a malformed id. The unknown-id rows assert **both** `errors.Is(err, mediastore.ErrNotFound)` and `errors.Is(err, store.ErrNotFound)` — either consumer contract classifies correctly. The existing `TestDeleteRemovesRowAndFile` store-sentinel assertion and `TestServeHTTPUnknownAndMalformedIDs` HTTP-code pin continue to hold unchanged. |
| **Pin re-run status** | Green under `-race -count=1` (all four subtests). Mutation-checked: the reviewer's re-introduction — `Delete` wrapping only `store.ErrNotFound` again — turns `TestNotFoundSentinelContract/delete_unknown_id` red; restored byte-identical, green again. |

### M1 — A blob cannot be bound to a page or cast member; a book's media is unlistenable

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/store/store.go` — the `media` DDL (was :180-186) and the package doc's schema listing; `internal/store/media.go` — the whole media API surface. |
| **What was done** | The binding is representable and queryable, shaped by project.md's model (one reference sheet per cast member, one illustration + one narration per page, §1/§2/§Pipeline). **DDL:** `media` gains `kind TEXT NOT NULL DEFAULT ''`, `page_n INTEGER`, `cast_name TEXT`; a CHECK constrains the row to exactly one of four states — unplaced (`book_id` NULL, `kind` ''), attached-without-role (`book_id` set, no kind/anchor — the pre-placement state `CreateMedia` has always produced), reference (names a cast member, no page), illustration/narration (names a page ≥ 1, no cast member); two composite foreign keys `(book_id, page_n) → pages(book_id, n)` and `(book_id, cast_name) → "cast"(book_id, name)`, both `ON DELETE CASCADE`, make a dangling anchor impossible and extend the book-cascade doctrine to page/cast deletion (rows go; the files stay PLAN.md §T11's sweep); three partial unique indexes (`media_page_illustration`, `media_page_narration`, `media_cast_reference`) make one-blob-per-slot the database's rule, not a convention. **API (minimal, exactly the T6/T8/T10 queries):** `MediaKind` with `MediaReference` / `MediaIllustration` / `MediaNarration`; `MediaPlace{BookID, Kind, PageN, CastName}`; `SetMediaPlace` (replaces `SetMediaBook` — it subsumes attach, move and detach; the one caller was migrated); `BookMedia(bookID)` — the listing by book; `PageMedia(bookID, n, kind)` — the page-n-asset-by-kind lookup (T6's page illustration, T8's page narration, T10's "page n → image + audio"); `CastMedia(bookID, name)` — the reference sheet T6's image lock generates every page against. `CreateMedia` keeps its contract but rejects a pre-placed row (`ErrInvalid`) so the place machine has one door. Nothing speculative: per the remediation contract the kind set is exactly the three roles the pipeline produces — T8's per-line character audio is **not** modelled (its query was not in the contract's enumeration; adding a fourth kind or a line anchor now would be scope creep arriving unreviewed). `classifyConstraint` maps the new violations for free: duplicate slot → `ErrConflict`, dangling anchor → `ErrInvalid`. **Migration:** none needed, and the omission is safe by fact, not by hope — the schema is not deployed anywhere (the box runs T1's binary, which has no store), so the DDL was edited in place; the first deployed data dir is born under the new schema, and `CREATE TABLE IF NOT EXISTS` reopen stays the no-op the doc claims. |
| **Test added** | Six pins. `TestMediaRowLifecycle` (updated): place → read back → detach → delete, unplaced invariants asserted. `TestSetMediaPlaceBranches`: 10 subtests over the invalid/not-found branch set (unknown media/book/page/cast member, unknown kind, kind without anchor, anchor without kind, detach carrying fields). `TestSetMediaPlaceConflict`: a second blob in a taken slot is `ErrConflict` while the sibling kind on the same page places independently, and re-placing moves the blob (old slot vacated). `TestBookPageAndCastMediaQueries`: the three read shapes with their not-found and invalid branches, and the unplaced blob provably never listed. `TestAnchorCascadesRemovesRows`: page deletion takes its illustration, cast deletion takes its reference, neighbours survive. `restart_test.go` `TestBookSurvivesRestartWithMedia` (extended): three placed blobs — page-1 narration + illustration, the Dragon's reference — survive close/reopen and answer `BookMedia` (3 rows), `PageMedia` (both kinds), `CastMedia`; the not-found branch (page 2 narration → `store.ErrNotFound`) and bad-kind branch (`"trailer"` → `store.ErrInvalid`) are asserted **after** the restart, per the round's exit criteria. |
| **Pin re-run status** | Green under `-race -count=1` (store 86.9%, mediastore 95.6% statement coverage — floors 75% hold, both above the round-1 numbers). Mutation: reverting the `kind`/`page_n`/`cast_name` columns re-introduces the gap by construction — the pins call `PageMedia`/`CastMedia`/`BookMedia`, which cease to exist, so the revert fails compilation and the attach → restart → read-back pin fails with it; no live re-run needed to prove a compile error. |

### L1 — Five error branches ship with no test

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/mediastore/mediastore.go` — both `ServeHTTP` 500 branches (were :229-232, :243-245), `writeBlob`'s `fail("sync")` / `fail("close")` stages (were :183-184, :186-187), and the fail-closure's remove-error log branch (was :174-175); `internal/mediastore/mediastore_test.go` — the new pins and the injection seams. |
| **What was done** | All five branches asserted, per AGENTS.md §Testing rule 3. Two seams were required: `writeBlob` now creates the file through a `newBlob func(id string) (blobFile, error)` field on `Store` (bound to the `os.Root` handle in `Open`), behind the unexported `blobFile` interface (`io.WriteCloser` + `Sync`) — the contract's note that "a failing `io.Writer` wrapper is not enough for close" is honoured by wrapping the real `*os.File` with injected `Sync`/`Close` failures (`failingBlob`); and a `capture` `slog.Handler` records operator lines, because two of the five branches are observable only through the log. The handler tests exercise the branches by honest black-box means as the round prescribed: a closed database for the metadata 500, a `chmod 000` blob for the open 500 (both root-skipped exactly like the existing `TestPersistIntoReadOnlyDirFails`). |
| **Test added** | Five. `TestServeHTTPWithClosedDBIs500` (500 **and** the `metadata lookup failed` line). `TestServeHTTPUnopenableBlobIs500` (500 **and** the `blob open failed` line). `TestPersistSyncFailureRemovesPartialBlob` and `TestPersistCloseFailureRemovesPartialBlob` (injected stage failure → persist fails with the stage's error, blob directory left empty — no partial file, and no row since the insert never ran). `TestPersistSyncFailureWithFailingRemoveLogsOrphan` (the same injection with the directory made unremovable-from mid-persist → the remove failure is exactly the one `partial blob not removed` line, not a swallowed error). |
| **Pin re-run status** | Green under `-race -count=1`. All five mutations applied by hand and observed red (table below). The mutation run caught a subtlety worth recording: deleting the open-500 branch alone leaves `http.ServeContent` answering **500 anyway** on the unusable handle (the nil-safe `Seek` errors, stdlib 500s), so a status-only pin survives that mutation. The pin therefore asserts the branch's full documented contract — "a 500 **and one log line**" — and the log assertion is what turns it red. The closed-DB pin carries the log assertion for symmetry and the same reason (status-only passed only because the branch deletion redirects the flow into a 200 serve). |

## Mutation check (applied by hand, observed red, reverted byte-identical, re-run green)

| Mutation | Pin that goes red |
|---|---|
| `Delete` reverts to wrapping only `store.ErrNotFound` (the round's re-introduction line) | `TestNotFoundSentinelContract/delete_unknown_id` |
| `ServeHTTP` metadata-500 branch deleted (`log` + `http.Error` + `return`) | `TestServeHTTPWithClosedDBIs500` |
| `ServeHTTP` open-500 branch deleted (`log` + `http.Error` + `return`) | `TestServeHTTPUnopenableBlobIs500` (via the missing log line; status alone stays 500 through stdlib) |
| `writeBlob` sync branch deleted (`if err := f.Sync() …`) | `TestPersistSyncFailureRemovesPartialBlob` |
| `writeBlob` close branch deleted (`if err := f.Close() …`) | `TestPersistCloseFailureRemovesPartialBlob` |
| Fail-closure remove-error log line deleted | `TestPersistSyncFailureWithFailingRemoveLogsOrphan` |
| `media` kind/`page_n`/`cast_name` columns reverted | M1 pins' compile units — `PageMedia`/`CastMedia`/`BookMedia` and the DDL CHECK/indexes cease to exist; the restart attach → read-back pin fails with them |

## Residue against round 1

**Zero.** Each of the three findings has a row above following its mutation's letter (with the two refinements in H1 and the log-line strengthening in L1 argued in place, both making the fix *stricter* than the mutation's sketch, not narrower). The round-1 pins that already passed — restart survival with Range, the content-type gate, traversal, cascade, orphan cleanup, the mux route — all run green inside the full suite below, untouched except where a row says otherwise.

## Aggregate verification

```
$ go vet ./...                       clean
$ gofmt -l .                         empty
$ CGO_ENABLED=0 go build ./...       ok
$ go test ./... -race -count=1
ok  thutapi/cmd/thutapi              10.1s
ok  thutapi/internal/gmi/media        1.8s
ok  thutapi/internal/gmi/text         1.8s
ok  thutapi/internal/mediastore       1.5s
ok  thutapi/internal/store            1.8s
$ go test ./... -cover
cmd/thutapi   81.5%  (floor 75%)
gmi/media     95.1%  (floor 85%)
gmi/text      90.4%  (floor 85%)
mediastore    95.6%  (floor 75%; was 87.4% at round 1)
store         86.9%  (floor 75%; was 86.6% at round 1)
```

## Files changed

* `internal/store/store.go` — package doc's media line; `media` DDL with kind/page/cast columns, composite cascading FKs, the four-state CHECK, three partial unique indexes.
* `internal/store/media.go` — `MediaKind` + constants, `MediaPlace`, `Media`'s three new fields, `CreateMedia`'s unplaced-row guard, `scanMedia`, `SetMediaPlace` (replacing `SetMediaBook`), `BookMedia`, `PageMedia`, `CastMedia`.
* `internal/store/media_test.go` — updated lifecycle/branch pins; five new permanent M1 pins.
* `internal/mediastore/mediastore.go` — `ErrNotFound` made real and documented as wrapping `store.ErrNotFound`; `media()` + `notFound()`; conditional sentinel wrap in `Delete`; `ServeHTTP` classification through `media()`; `blobFile` + `newBlob` seam in `writeBlob`; docs matching behaviour.
* `internal/mediastore/mediastore_test.go` — sentinel-contract pin (H1); five L1 branch pins; `capture`, `failingBlob`, `emptyDir` helpers.
* `internal/mediastore/restart_test.go` — three placed blobs; restart read-back through the new queries incl. the not-found and bad-kind sentinels.
* `dev-diary/adversarial-review/t3-remediation-round1.md` — this file.
