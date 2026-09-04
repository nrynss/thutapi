# T3 — Store and media, review round 2

- **Target:** the uncommitted working tree on `main` after the round-1
  remediation (`t3-remediation-round1.md`). Base for the delta: **0c60c6d**
  ("T2 closes at round 5"); the T3 work is uncommitted (main.go,
  main_test.go, go.mod modified; `internal/store/`, `internal/mediastore/`,
  go.sum new) — the orchestrator commits on APPROVE (AGENTS.md §Process
  step 5).
- **Date:** 2026-09-05.
- **Reviewer:** round-2 review agent — a fresh role per AGENTS.md
  §Agentic development; not the round-1 reviewer, not the implementer,
  not the remediator.
- **Evidence read in full:** `AGENTS.md`;
  `dev-diary/adversarial-review/README.md`; `t3-round1.md` (all three
  finding rows read untruncated, mutation sketches included);
  `t3-remediation-round1.md`; `dev-diary/PLAN.md` (§T3, §Architectural
  invariants 1–8, §Unowned seams, §T5/T6/T8/T10 as the media model's
  consumers); `dev-diary/project.md` (§1 Phase-B data model, §Pipeline,
  §4 Audio, §Architecture consequences); the whole changed code:
  `internal/store/store.go`, `media.go` and all six test files,
  `internal/mediastore/mediastore.go` and both test files,
  `cmd/thutapi/main.go` + `main_test.go` T3 hunks.
- **Method:** probe, don't trust prose. Round-1 pins re-run (the
  reviewer probes' conditions re-created as `zz_probe_test.go` per
  package and the remediation's permanent tests re-run inside the
  gates); the three mutations from the contract applied by hand
  (H1 re-introduction and one L1 branch deletion), observed red,
  restored byte-identical (`cmp`), re-run green; fresh-eyes probes
  against the new M1 design's edge cases. Probes ran once, output
  recorded verbatim below, probe files removed; at close the tree is
  byte-identical to the reviewed tree (md5 manifests over
  `internal/**`, `cmd/thutapi/**`, `go.mod`, `go.sum` compared before
  and after: identical) except this file. No live GMI or network calls;
  everything ran locally against temp dirs and `httptest`, as user
  `nryn` (so the two root-skipped pins genuinely executed).

## Verdict

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.** T3 can close.

All three round-1 findings are fixed with their letter honoured, all
pins are load-bearing (two re-proven red by hand this round), the M1
design is sound, sufficient and minimal for T6/T8/T10, and the gates
are clean. Zero residue against round 1, claimed explicitly below.

## Findings

None. (Empty table — recorded for schema completeness.)

| # | Severity | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| — | — | — | — | — | — |

## Zero residue against round 1, finding by finding

**1 (H) — `mediastore.ErrNotFound` documented but returned by nothing.**
Fixed, and the fix is exactly the round-1 mutation's letter, stricter:
`Delete` of an unknown id now wraps **both** sentinels
(`notFound(id, fmt.Errorf("delete: %w", err))` with a double-`%w`), the
handler's classification lives in `s.media()`, which classifies a
malformed id as `ErrNotFound` without touching the database and a
missing row as `ErrNotFound` **with** `store.ErrNotFound` in the chain,
and every other failure (closed DB, cancelled context) matches no
sentinel — "already gone" vs "broken now" stays distinguishable, per
the doc. The doc's three named shapes all classify: unknown and
malformed are returned by `Delete` and consumed by `ServeHTTP`; the
vanished-file shape is returned to the operator log classified
(`row without file` + the `fs.ErrNotExist` chain) and 404s to the
client — an `http.Handler` has no error return, so the log line is the
contract-carrying surface, and the permanent pin asserts it where a
branch is observable only through the log. Permanent pin:
`TestNotFoundSentinelContract` (4 subtests, asserting both sentinels on
the store-originating shapes). Round-1 probe re-run inverted: both
sentinels now match (transcript below). Mutation re-applied by hand
this round — see Mutation check.

**2 (M) — a blob cannot be bound to a page or cast member; a book's
media is unlistenable.** Fixed: `media` gains `kind`/`page_n`/
`cast_name` with a four-state CHECK, two composite cascading FKs to
`pages(book_id, n)` and `"cast"(book_id, name)`, three partial unique
indexes (one blob per slot), and the API gains `MediaKind`,
`MediaPlace`, `SetMediaPlace` (replacing `SetMediaBook`; its one caller
migrated — round-1's 23-method surface is now 26, `SetMediaBook` gone),
`BookMedia`, `PageMedia`, `CastMedia`. Round-1's exit criteria are met
line by line: the binding is representable and queryable
(`PageMedia` answers "page n → image/audio" directly); permanent tests
attach per-page blobs and read the mapping back through a restart
(`restart_test.go`: three placed blobs, close, reopen, all three query
shapes answered, plus a 206 Range on the reopened blob); the migration
assumption is stated explicitly in the remediation row ("Migration:
none needed… the schema is not deployed anywhere (the box runs T1's
binary, which has no store), so the DDL was edited in place; the first
deployed data dir is born under the new schema"). Six permanent store
pins cover lifecycle, the place-machine branch set, slot conflicts,
the three read shapes with their not-found/invalid branches, anchor
cascades, and duplicate-id conflicts.

**3 (L) — five error branches ship with no test.** Fixed: all five
asserted (two ServeHTTP 500s with 500 **and** log line; `writeBlob`
sync/close faults injected through the `newBlob`/`blobFile` seam
against the real `*os.File`; the fail-closure remove-error line via a
`capture` slog handler), coverprofile counts now non-zero, and the
remediation's mutation table shows all five load-bearing. My spot
re-check of the trickiest one (below) reproduces the recorded
discovery: deleting the open-500 branch leaves stdlib answering 500
anyway, and the **log-line assertion** is what turns the pin red.

## Probe transcripts (verbatim)

Reviewer-written `zz_probe_test.go` per package; run
`go test -race -count=1 -v -run 'Probe' ./internal/store ./internal/mediastore`;
files removed after recording (md5-verified byte-identical tree).

### Round-1 probe conditions, re-run

DSN pragma claims still hold on the connections actually used:

```
zz_probe_test.go:26: PRAGMA foreign_keys -> 1
zz_probe_test.go:26: PRAGMA journal_mode -> wal
zz_probe_test.go:26: PRAGMA busy_timeout -> 5000
```

Two-writer contention (writer B is a separate pool on the same file —
the two-process case):

```
zz_probe_test.go:85: B blocked for 335ms, err = <nil>
```

Context cancellation still crosses the boundary `errors.Is`-ably:

```
zz_probe_test.go:97: Books: err = store: books: context canceled, errors.Is(context.Canceled) = true
zz_probe_test.go:102: DeleteBook: err = store: delete book aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa: context canceled, errors.Is(context.Canceled) = true
```

H1 pin — the round-1 probe's conditions, now inverted (both probes
PASS; the sentinel the doc promises is real, and it rides with
`store.ErrNotFound` wherever the row lookup originates in the store):

```
zz_probe_test.go:28: Delete(unknown): errors.Is(err, ErrNotFound)      = true
zz_probe_test.go:29: Delete(unknown): errors.Is(err, store.ErrNotFound) = true
zz_probe_test.go:35: Delete(malformed): errors.Is(err, ErrNotFound)      = true
zz_probe_test.go:36: Delete(malformed): errors.Is(err, store.ErrNotFound) = true
zz_probe_test.go:46: media(malformed): errors.Is(err, store.ErrNotFound) = false
```

(The last line is correct per the doc: a malformed id never touches
the store, so `store.ErrNotFound` cannot be in the chain. The
malformed-delete line above does carry it — the DELETE ran.)

M1 pin — the exported surface, re-counted: 26 methods; `SetMediaPlace`
/ `BookMedia` / `PageMedia` / `CastMedia` present, `SetMediaBook`
absent (asserted, not just logged):

```
exported methods of *store.DB: 26
  Book  BookMedia  Cast  CastMedia  Close  CreateBook  CreateCastMember
  CreateInterview  CreateMedia  CreatePage  DeleteBook  DeleteCastMember
  DeleteInterview  DeleteMedia  DeletePage  Interview  Media  Page
  PageMedia  Pages  SetMediaPlace  UpdateBook  UpdateCastMember
  UpdateInterview  UpdatePage
```

Range protocol edges — still stdlib's, recorded as pinned knowledge:

```
multi-range: status=206 content-type="multipart/byteranges; boundary=136b…" content-range="" body-bytes=521
unsatisfiable: status=416 content-type="text/plain; charset=utf-8" content-range="bytes */1024" body-bytes=33
malformed: status=416 content-type="text/plain; charset=utf-8" content-range="" body-bytes=14
if-range_mismatch: status=200 content-type="audio/mpeg" content-range="" body-bytes=1024
empty_blob: status=200 content-length="0" body-bytes=0
```

Path traversal, HEAD, mux-level crafted ids — triple defense holds
(`validID` + `os.Root` + ServeMux cleaning); blob dir holds only valid
ids afterwards:

```
HEAD: status=200 content-length="64" body-bytes=0
handler id ".."          -> 404      handler id "%2e%2e"            -> 404
handler id "../secrets"  -> 404      handler id "../../../etc/passwd" -> 404
handler id <32-hex upper> -> 404     handler id <31 chars>          -> 404
handler id <33 chars>    -> 404      handler id <non-hex>           -> 404
mux "/media/"                     -> 404
mux "/media/.."                   -> 307   (ServeMux cleaning; followed redirect 404s)
mux "/media/..%2F..%2Fetc%2Fpasswd"     -> 404
mux "/media/%2e%2e%2f%2e%2e%2fetc%2fpasswd" -> 404
mux "/media/<id>%2F"              -> 404   mux "/media/<id>/extra" -> 404
blob dir contains only valid ids (1 entries)
```

Concurrency under `-race`, and the recorded no-integrity-mechanism
behaviour (a file with wrong bytes serves as-is; the row-without-file
case is the handled one):

```
16 concurrent persist+read+serve cycles clean under -race
corrupted-blob serve: status=200 content-type="audio/mpeg" content-length=7 body="GARBAGE"
```

### Fresh-eyes M1 probes — the edges the new tests did not pin

All PASS; recorded because they are design knowledge, not accidents.

**Occupied-slot move is non-destructive.** A rejected move
(`ErrConflict`) leaves the occupying row byte-identical **and** the
moving row on its old page — the UPDATE is all-or-nothing:

```
move onto occupied slot: err = ErrConflict ✓
occupying blob unchanged (book, kind=illustration, page 1) ✓
moving blob still on page 2 ✓
```

**Detach → re-place cycle, and the self-place.** A blob can be
re-placed onto its own slot without conflict (no self-conflict), the
slot empties on detach, and accepts its old tenant back:

```
self-place (same blob, same slot): nil, BookMedia still 1 row ✓
detach: PageMedia → ErrNotFound ✓
re-place onto the vacated slot: the original id answers ✓
```

**Cross-book move.** `SetMediaPlace` moves a blob between books; the
old book's slot vacates with it:

```
move book A page 1 → book B page 1: nil ✓
PageMedia(A,1) → ErrNotFound ✓   PageMedia(B,1).ID = the blob ✓
```

**kind '' book-level rows** (the CHECK's attached-without-kind state).
Both doors reach it — `CreateMedia` with a `BookID`, and
`SetMediaPlace` with a book and no kind — the rows are book-visible
(`BookMedia`), invisible to the typed slots (`PageMedia`/`CastMedia`
still ErrNotFound), and promotable into a real slot:

```
create pre-attached: nil ✓      kindless attach: nil ✓
row = {book set, kind '', page 0, cast ''} ×2 ✓   BookMedia = 2 rows ✓
PageMedia/CastMedia → ErrNotFound ✓   promote to reference: ✓
```

**A page cascade takes the anchor, not just the tenant.** After
`DeletePage`, placing against the deleted page is `ErrInvalid` (the
composite FK to `pages`), and the slot reopens only when the page is
re-created; anchored-elsewhere rows (narration on page 2, the cast
reference) survive untouched:

```
DeletePage(1): illustration row gone ✓
place illustration page 1 → ErrInvalid (anchor integrity) ✓
re-create page 1: slot accepts a new tenant ✓
page 2 narration ✓   reference ✓
```

**One slot, eight racing placements, under `-race`.** The partial
unique index is the arbiter under concurrency: exactly one winner,
seven `ErrConflict`, and the winner is what the slot answers with:

```
8 concurrent placements: exactly 1 winner, 7 ErrConflict ✓
```

**Vanished-file shape** (the one not-found shape without its own
permanent HTTP pin): row present, file removed → client 404, operator
gets the classified `row without file` line, and the classifier itself
wraps `fs.ErrNotExist` into the bargain:

```
vanished-file serve → 404 ✓   "mediastore: row without file" logged ✓
notFound(vanished): ErrNotFound=true fs.ErrNotExist=true ✓
```

(The `capture` handler records message strings only, so the sentinel
chain was verified by calling `notFound` directly — it lives in the
log attr a real handler renders.)

## Mutation check (applied by hand, observed red, restored byte-identical)

| Mutation | Pin that went red | Restoration |
|---|---|---|
| H1 re-introduction: `Delete` line 220 reverted to `fmt.Errorf("mediastore: delete: %w", err)` (wrapping only the store error) | `TestNotFoundSentinelContract` — **two** subtests red (`delete_unknown_id`, `delete_malformed_id`): `err = mediastore: delete: store: delete media ffff…: not found, want match on mediastore.ErrNotFound` | `cmp` byte-identical; re-run green |
| L1 branch deletion: `ServeHTTP`'s open-500 branch removed (log + `http.Error` + `return`, lines 295–297) | `TestServeHTTPUnopenableBlobIs500` — status check **passed** (stdlib answers 500 on the nil-safe handle, exactly as the remediation recorded) and the failure is `no blob-open-failed line was logged`: the log assertion is the load-bearing half | `cmp` byte-identical; re-run green (together with `TestServeHTTPWithClosedDBIs500` and `TestNotFoundSentinelContract`) |

The remaining mutations in the remediation's table (metadata-500
branch, sync/close branches, remove-error log line, column reversion →
compile failure) were each observed red by the remediator and are of
the same two shapes I re-proved; no reason to doubt them survived this
round's reading — every pin asserting them reads the observable
contract (error stage string, empty blob dir, log line), none reads
implementation text.

## Pins run

| Pin | What it proves | Result |
|---|---|---|
| `TestNotFoundSentinelContract` (4 subtests) | H1 permanent pin: every doc-named not-found shape matches `ErrNotFound`; store-originating shapes match `store.ErrNotFound` too | PASS (`-race`), red under mutation |
| `TestServeHTTPWithClosedDBIs500` / `TestServeHTTPUnopenableBlobIs500` | the two 500 branches: 500 **and** the operator log line | PASS (non-root; red under branch deletion) |
| `TestPersistSyncFailureRemovesPartialBlob` / `…CloseFailure…` / `…WithFailingRemoveLogsOrphan` | writeBlob's fault stages leave no partial file; the unremovable case is a log line, not a swallow | PASS |
| `TestMediaRowLifecycle`, `TestCreateMediaValidations`, `TestCreateMediaInUnknownBookIsInvalid`, `TestCreateMediaDuplicateIDConflicts` | M1 create/read/place/detach/delete lifecycle, the pre-placed-row rejection (one door), FK/UNIQUE classification | PASS |
| `TestSetMediaPlaceBranches` (10 subtests), `TestSetMediaPlaceConflict` | the place machine's invalid/not-found branch set; one-per-slot as a database rule; move vacates the old slot | PASS |
| `TestBookPageAndCastMediaQueries` | the three read shapes incl. not-found and invalid branches; unplaced blobs never listed | PASS |
| `TestAnchorCascadesRemovesRows` | page cascade takes its blobs, cast cascade its reference, neighbours survive | PASS |
| `TestBookSurvivesRestartWithMedia` | PLAN §T3's Done when end to end, now incl. the three placements answering T6/T8/T10 queries after reopen + a 206 Range | PASS (`-race`) |
| `TestDeleteBookCascades` | book cascade removes pages, cast, interview, media rows | PASS |
| `TestServeHTTPRangeRequests`, `TestServeHTTPIfNoneMatchPins304`, `TestPersistAndServeFullBody` | Range/ETag/base serving contract | PASS |
| `TestPersistRejects/Accepts/Normalizes…ContentTypes`, `TestPersistIDsAreUnguessableAndDistinct`, `TestPersistWithCancelledContextFailsClean` | the closed content-type set both directions; 128-bit ids; cancelled ctx leaves no row and no file | PASS |
| `TestMediaRouteServesThroughMux` (cmd/thutapi) | the one sanctioned route line: 404 unknown, 404 malformed, 405 non-GET | PASS |
| reviewer probes (this round) | DSN pragmas; 335 ms cross-pool block; ctx-cancel propagation; sentinel conditions; 26-method surface; Range edges; traversal/HEAD/mux; 16-cycle concurrency; corrupt-blob behaviour; the six M1 edge probes above | PASS (removed) |

## Gates (re-run at review, probes removed)

```
go vet ./...                      -> clean
gofmt -l .                        -> empty
go test ./... -race -count=1      -> ok: cmd 10.0s, gmi/media 1.8s,
                                     gmi/text 1.8s, mediastore 1.5s, store 1.8s
go test ./... -cover -count=1     -> cmd 81.5%, gmi/media 95.1%,
                                     gmi/text 90.4%, mediastore 95.6%,
                                     store 86.9%  (floors 75 / 85 hold)
CGO_ENABLED=0 go build ./...      -> ok
```

## The M1 design itself — fresh-eyes assessment

Brief: is the kind set + anchor model + partial unique indexes sound,
minimal, and sufficient for T6/T8/T10? Judged against project.md §1/§2/
§4/§Pipeline and PLAN §T6/T8/T10 — **yes**, on all three counts.

- **Sufficient.** T6's image lock is `CastMedia(book, member)` for the
  reference sheet and `PageMedia(book, n, illustration)` per page; T8's
  narration is `PageMedia(book, n, narration)`; T10's flipbook ask —
  "page n → image URL + audio URL" — is exactly two `PageMedia` calls.
  Per-line character audio is deliberately **not** modelled, and that
  is correct: multi-character voices are in AGENTS.md's cut-first list,
  T8's own row specifies per-page narration with per-page emotion, and
  the remediation states the scoping explicitly instead of smuggling a
  fourth kind in.
- **Sound.** The CHECK's four states map one-to-one onto the place
  machine's switch, so no API path can write a state the schema
  forbids — probed from every direction I could find (occupied-slot
  moves, detach/replace cycles, cross-book moves, kindless rows,
  cascades, an 8-way placement race). The composite FKs make a dangling
  anchor unrepresentable and extend the cascade doctrine coherently:
  deleting an anchor deletes its blobs' rows *and their anchor*
  (place-against-deleted-page is `ErrInvalid`, the slot reopens when
  the page returns). Uniqueness lives in the database, not in
  convention — the race probe shows it arbitrating concurrent writers.
- **Minimal.** Every query has a named consumer (T6/T8/T10 above,
  `BookMedia` for T11's sweep); `SetMediaPlace` subsumes
  attach/move/detach so the old `SetMediaBook` door is gone, and
  `CreateMedia` rejects pre-placed rows so the machine has one door.
  Nothing speculative found: no unused kind, no query without a
  consumer, no knob.

Two notes, recorded as design observations, **not findings** — neither
has an observable defect attached:

1. The CHECK's attached-without-kind state (book set, `kind` '') has no
   consumer in the sanctioned pipeline (`Persist` produces unplaced
   rows; T6/T8 always place with a kind). It is documented honestly
   (`Media.Kind`'s doc names both ways to be kindless), it preserves
   the pre-existing `CreateMedia` BookID contract, and it gives
   callers an attach-now/role-later option — a defensible state, and
   the store never confused anything with it in probing.
2. `BookMedia`'s `WHERE book_id = ?` has no plain index on
   `media(book_id)` — it scans the table. At pipeline scale (tens of
   rows per book, T11's retention sweep capping the total) that is
   noise; if a sweep ever walks a large table, the fix is one index.

## Invariant audit (delta since round 1)

1. **`internal/gmi` only talks to GMI** — holds; no network calls added.
2. **Config struct, no env reads in packages** — holds; `DATA_DIR`
   resolves in `main` and flows down through `store.Config` /
   `mediastore.Config`.
3. **Concrete returns; consumers declare interfaces** — holds; the
   unexported `blobFile` interface is declared by its consumer
   (`writeBlob`), the one exported seam stays concrete.
4. **Queue is a queue** — untouched, n/a.
5. **One route line in `newServer`** — still exactly one
   (`s.mux.Handle("GET /media/{id}", s.media)`); the composition-root
   signature change (media handler injected) is wiring, not business
   logic.
6. **Nothing blocks > 100 s** — unchanged (Persist's copy is unbounded
   by ctx and documented so; callers own deadlines).
7. **Persist GMI output on receipt** — holds, end-to-end pinned.
8. **Sentinels across boundaries** — now holds in *both* new packages:
   round-1's H1 was the failure; the re-run probe conditions and the
   permanent pin prove it fixed.

## Examination boundary

- Reviewed the whole T3 surface again post-remediation — schema vs
  project.md's data model, the place machine's full state space, the
  three queries, the HTTP not-found/500 classification, the
  restart-survival promise (store + blob levels), Range edges,
  content-type gate, id guessability, traversal at handler and mux
  levels, cascade (book, page, cast), orphan cleanup, concurrency
  (handler-level and placement-race level), and the round-1 probe
  conditions re-run.
- Probes were local only: temp dirs and `httptest`; no live GMI or
  network probes; no fixes applied by this review; no formatter or git
  state changes beyond reads (mutations were hand-applied, observed,
  and `cmp`-restored; final md5 manifests identical).
- Not re-reviewed (outside T3's patch, closed earlier): `internal/gmi`
  (T2, closed at round 5), T0's `errShutdownTimeout` doc wording,
  T1-owned deployment artifacts (unchanged on the tree since round 1
  read them; `git status` shows no modifications to them).
- Standing notes, not findings: PLAN.md's T3 status row flips at land
  time (orchestrator, as with T2); `go.sum` is new and must land with
  the track's commit; the tree stays uncommitted until APPROVE by
  design.

**Zero residue against round 1, severity by severity: H — fixed and
mutation-proven; M — fixed, restart-pinned, migration assumption
stated; L — all five branches pinned and load-bearing. Nothing from
round 1 remains open, softened, or half-landed. T3 can close.**
