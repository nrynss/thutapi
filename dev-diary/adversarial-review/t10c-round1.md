# T10c round 1 — implementation record (review stub)

| | |
|---|---|
| **Target** | T10c — the generation pipeline (PLAN.md §T10c). Working tree on `main` at be12727; audited delta below. |
| **Recorded by** | Implementation agent (`T10cImplementer`), 2026-09-05. |
| **Owns** | `internal/bookgen/**` (all new) + its route lines in `newServer` (PLAN.md invariant 5), + the sanctioned main.go wiring this record lists. Every consumed package — `internal/job`, `internal/stream`, `internal/interview`, `internal/story`, `internal/illustrate`, `internal/audio`, `internal/bookvideo`, `internal/store`, `internal/mediastore` — is read at its surface and NOT edited. `dev-diary/PLAN.md` not touched. No live calls, no money. |
| **Files changed** | `internal/bookgen/{bookgen,pipeline,video}.go` (new), `internal/bookgen/{harness,pipeline,http,bookgen}_test.go` (new), `cmd/thutapi/main.go` (wiring below), `cmd/thutapi/main_test.go` (keep-green + route seam pin), this record. |

## What T10c does (the implemented contract, per PLAN.md §T10c)

A closed interview becomes a finished, playable book with no human step in
between, and screen 5 can watch it happen:

```
POST /interviews/{id}/generate     → 202 {"job_id","book_id","topic":"book:<bookID>",
                                          "events_url":"/interviews/<id>/generate/events","status":"running"}
GET  /interviews/{id}/generate/events → SSE on the book's topic ("book:"+bookID)
```

The pipeline runs as one `internal/job` job (short POST, invariant 6), stage
order fixed by PLAN.md §T10c:

1. **Structure.** The interview's transcript + its book row are read back
   through the store; `story.Structure` validates the Story; the book row is
   updated to the authored title with the byline preserved; every page and
   cast member is upserted as a store row (upsert, not create, so a retry
   over a failed run's rows replaces them) — T7's `BookWriter` and T8's
   `NarrateBook` both require book/page/cast rows to exist before their
   place calls fire the anchor FKs (t6b-live-record.md item 3; verified
   against `illustrate/persist.go:22-24` and `audio/audio.go:338-342`).
2. **Illustrate** with BOTH `Config.Judge` (the real *text.Client) and
   `Config.Persist` (a `NewBookWriter` bound to the book) set — T7's
   closing loop: sheets persist on render, pages post-approval.
3. **Narrate** — `audio.NarrateBook`, one persisted clip per page in order.
4. **The film** — `bookvideo` render (title card + page segments + end card
   + concat) from the persisted page images/narration; the MP4 is persisted
   and attached to the book (kindless book media), the row that makes
   GET /book/{id} serve cold and that `book_ready` names.
5. `book_ready` is published only after the film row is placed.

Book-topic events (exact wire, pinned by raw-JSON tests):

```
event: page_approved  → {"n":3,"image_url":"/media/<id>"}
event: book_ready     → {"video_url":"/media/<id>"}
event: failed         → {}
```

`page_approved` fires the moment a page's illustration is approved and
persisted (Progress is reported AFTER the closing loop — t7-round2.md L1 —
so the bridge reads the just-placed `MediaIllustration` row back for the id);
`failed` fires on ANY error including a panic and means the whole run; a
terminal run never blocks the next POST (a retry is a tap — decision 17), a
RUNNING run does (double-fire refused, 409 `busy` — the busy check consults
`job.Runner.Result`'s exactly-once terminal states under `internal/job`, per
T3b's cancel-registry/terminal semantics; a per-book id map guards the
check-and-start window under a mutex). No automatic retry anywhere.

## Contract-change rows

### C1 — main.go wiring (file:line, why)

`newServer` is the shared seam (invariant 5) and the pipeline's handler needs
the process handles run() builds; wiring them through is recorded here per the
T10c seam ruling ("main.go wiring beyond one route line per endpoint is a
sanctioned contract row"). All lines in `cmd/thutapi/main.go` unless noted:


| Line | Change | Why |
|---|---|---|
| main.go 25-27 | import `internal/bookgen`, `internal/bookvideo`, `internal/gmi/media` | the two GMI clients (text for structure/judge, media for images/TTS) and the film renderer adapter |
| main.go 139-140 | `server` struct gains `generate *bookgen.Handler` | newServer must reach the handler |
| main.go 142-166 | `newServer` signature gains `generate *bookgen.Handler`; two route lines `POST /interviews/{id}/generate` → `s.generate.Generate` (line 164), `GET /interviews/{id}/generate/events` → `s.generate.Events` (line 165) | T10c's sanctioned route lines (invariant 5) — the pipeline handlers live in internal/bookgen, not main |
| main.go 260 | `mediaDir` hoisted from inline `filepath.Join(cfg.dataDir, "media")` (was inside mediastore.Open's Config) | the film stage reads blob files by id from the media dir (see C5) |
| main.go 261-267 | mediastore local renamed `media` → `blobs` | the name now collides with the `internal/gmi/media` package import |
| main.go 274-275 | hoist `textCli := text.New()` and `mediaCli := media.New()` | one text client serves interview chat + structure + judge (same instance satisfies `story.Chatter`, `illustrate.Judge`); one request-queue client serves imager + TTS |
| main.go 282-283 | hoist `runner := job.New(broker)` | both the interview handler and the generation handler share one bound runner |
| main.go 284-290 | interview.New now receives `Chat: textCli` (285), `Jobs: runner` | the same hoisted handles (no behaviour change) |
| main.go 294-310 | construct `bookgen.New(bookgen.Config{…})` with db, blobs, mediaDir, textCli, mediaCli, broker, runner, `bookgen.NewFFmpegRenderer(bookvideo.Config{})`, film = blobs | the pipeline's full dependency set; config flows down from main (invariant 2) |
| main.go 312 | `newServer(log, blobs, interviews, generate)` | extended call |
| main_test.go 39-96 | `newTestServer` builds a bookgen handler with loud-fail stage fakes (`failImager`/`failTTS`/`failRenderer`/`failFilmStore`) reusing the existing `echoChatter` as structure chat + judge | keep-green of the newServer signature; the fakes never fire in cmd's route-seam tests |
| main_test.go 336-383 | `TestGenerateRoutesServeThroughMux` | pins the two route lines per the T3/T4 main_test convention (unknown interview → 404 not_found; method mismatch → 405) |

### C2 — the film's content type is not yet storable (gap against internal/mediastore, recorded not edited)

`mediastore.Persist` validates against its **closed** content-type set
(`mediastore.go:66-75`) which lacks `video/mp4`. The pipeline's film persist
goes through a `filmStore` seam (Persist/Delete) whose production value is
the real `*mediastore.Store`, so a production run fails **loudly** at the
film step (`failed {}`, terminal error naming "persist film") until the owner
adds `video/mp4` to `supportedTypes`. The bookgen test suite drives the seam
with a fake that replicates mediastore's own Persist shape (blob file named
by a fresh id + one real `store.CreateMedia` row) so the whole film lifecycle
(row, placement, serving, supersession) is pinned against the real store.
**Owner to land:** T3/mediastore (closed track). The event shape
`book_ready {"video_url":"/media/<id>"}` is then real: unplaced video row →
`SetMediaPlace(book)` (kindless book media — a legal store shape today,
`media.go:147-151`) → BookMedia lists it, §T11's sweep (book_id IS NULL) never
eats it, GET /media/{id} serves it cold with Range.

### C3 — there is no book "ready" flag (recorded, not invented)
The store has no book state column and no video media kind. T10c therefore
does NOT invent schema: readiness is the **book-attached video/mp4 media
row** (BookMedia lists it), and `book_ready` is published only after that row
is placed. **Owner:** T10b (GET /book/{id}) decides how "ready" surfaces on
its route; T10c's contribution is that the row exists and is the event's URL.

### C4 — the book state HTTP read for late subscribers does not exist (recorded)

Screen 5's reload at minute four cannot replay the SSE stream (from-now-on
by design); it must read the book's current state the way GET /interviews/{id}
catches up on turns. **No such route exists today.** T10c records the gap
rather than inventing a second book-state surface that would collide with
T10b's: the read must expose pages approved (count of placed
MediaIllustration rows), the video_url (the book's video/mp4 row), and the
run state — and may need a persisted run-outcome row (failed runs leave only
a partial store + the job registry, which is in-memory) — a T10b/T9 decision.
What IS pinned here: the current state is fully readable from the store rows
the events describe (TestPipeline_CatchUpStateIsInTheStore: from-now-on
verified, pages/title rows asserted).

### C5 — bookgen reads blob files by mediastore's documented layout (recorded)

The film stage needs the persisted page images and narration clips as bytes.
mediastore offers no programmatic read API (its file access is a private
`os.Root`), and its documented layout is "each blob is one file named by its
id" under the configured dir (`mediastore.go:5-11`). bookgen's Config carries
`MediaDir` (the dir main opened mediastore on) and reads `<MediaDir>/<id>`.
Alternative surfaces (a read API) are the owner's call; not invented here.

### C6 — byline: interpretation of "CreateBook with the byline" (recorded)

The batch instruction says "the byline lands on the book row BEFORE the run
proceeds — store.CreateBook with the byline". The store has no
CreateBook-with-byline: `CreateBook` starts byline empty
(`books.go:26-29`) and `UpdateBook` is the only writer
(`books.go:98-118`), and the book row already exists (the interview start
created it with WorkingTitle — `interview/http.go:74-75`). T10c therefore
realises the instruction as: read the book row (byline from question zero),
structure, then `UpdateBook{Title: story.Title, Byline: book.Byline}` — the
byline is on the row before any page/persist work proceeds, and the title
card renders it. Note: **no caller writes `books.byline` anywhere in the
repo today** (wire contract 1 — POST /interviews `{"byline"}` — is T9/T4's
unimplemented surface); bookgen reads whatever is there; empty is the normal
case (decision 14: the card drops the line, not the card). Fixtures set the
byline directly to pin the carry chain to the film's title card.

## Design decisions (implementation record — reviewer to rule)

### D1 — Topic and route surface: `book:` prefix, interview-keyed events route
Topic = `BookTopicPrefix("book:") + bookID` (pinned by TestTopicConvention),
served by `GET /interviews/{id}/generate/events`. The events route is
interview-keyed because screen 5's own URL is `/interview/{id}`: a reload can
derive the stream URL with no extra fetch, and interview↔book is 1:1, so the
keying is equivalent. The generate response carries topic + events_url in the
same shape as the interview start response (`interview/http.go:21-28`).

### D2 — Exactly-once terminal + busy registry
`generateJob` (bookgen.go) is the only publisher of the book topic's terminal
event (book_ready xor failed), via a `sync.Once`; panic-safe (a panicking
stage still publishes `failed {}` before re-raising for job.call's ErrPanic
boundary — pinned by TestPipeline_PanicStillPublishesFailed). `startRun`
holds a per-book job-id map and consults `job.Runner.Result`; only
`StatusRunning` refuses (409 busy). The map entry is never deleted — a
terminal entry lets the next POST start fresh and overwrite (pinned by
TestDoubleFireRefused: busy during the run, fresh job id after terminal,
old film superseded).

### D3 — Judge adapter decision: none needed — *text.Client satisfies illustrate.Judge
`internal/illustrate` already declares `Judge` (`verify.go:44-52`, one method
`Chat(ctx, text.ChatRequest)` — invariant 3), and `*text.Client` satisfies it
directly (same for `story.Chatter`). bookgen adds no adapter of its own:
main passes one `text.New()` instance as both `Config.Chat` (structure) and
`Config.Judge`. Config keeps them as separate fields so tests script the two
roles independently; production passes the same client twice.

### D4 — Progress bridge: read-back of the just-placed row
`illustrate.Config.Progress` reports each page AFTER the closing loop
approved AND persisted it (t7-round2.md L1; verified `illustrate.go:475-480`
report-after-closePage). Progress carries no media id, so the bridge
(`approvalBridge`) reads `store.PageMedia(book, n, illustration)` back and
publishes `{"n":N,"image_url":"/media/<id>"}`. A read-back failure after a
successful persist is recorded and fails the run after Illustrate returns —
the race must never silently lose a page (this branch is not fault-injectable
through the real store; disclosed, not silently ignored).

### D5 — Rows are upserted so a retry replaces a failed run's partial state
Stage 1 reads-then-creates/updates pages and cast (upsertPage,
upsertCastMember; pinned by TestUpsert_RowsAreReplacedNotDuplicated). Slot
replacement on re-runs is then owned by the consumers: BookWriter replaces
illustration/reference occupants (`persist.go:84-103`), NarrateBook replaces
narration occupants, and the film stage supersedes older book films AFTER
the new film is placed (failure to delete an old film is logged, never a
failed run whose book is actually ready — `supersedeFilms`).

### D6 — Failure shape: total, terminal, no retry
Any stage error fails the run: `failed {}` on the book topic, job terminal
error in the runner, zero film rows (pinned for structure, narrate, render,
film-persist, panic). Partial pages that WERE approved stay persisted (they
are real pages) and are replaced by the next run. A cancelled context
(no cancel path exists yet in this flow) lands `job.StatusCancelled` on the
job topic and `failed {}` on the book topic via the same once.

### D7 — The store rows the stages require, verified against their docs
Stage 1 creates all rows BEFORE illustrate (BookWriter doc
`persist.go:22-38`: "book whose pages and cast rows exist, or every place
fails with store.ErrInvalid") and before narrate (audio doc `audio.go:338-342`:
"store.CreateBook and store.CreatePage are the caller's"). Film inputs come
from `store.PageMedia` (illustration rows) + the `[]audio.Clip` NarrateBook
returns (narration rows), read as blob files per C5; the renderer receives
`bookvideo.Input{Title, Byline, Pages, OutputPath}` with bytes in page order
(`bookvideo/types.go:78-109`), so the title card carries the authored title
and byline.

## Event bridge shape (as implemented)

```
illustrate.Config.Progress (page, post-approval+persist)
  → approvalBridge.progress
      → db.PageMedia(book, n, MediaIllustration)          [real store read-back]
      → json {"n":N,"image_url":"/media/<row.ID>"}
      → broker.Publish("book:"+bookID, event page_approved)
job fn terminal: once → broker.Publish(book topic, book_ready {"video_url":…})
                 or, on any error/panic → failed {}
```

## Files planned/landed

- `internal/bookgen/bookgen.go` — package doc (full contract), Config, New,
  Handler, Generate/Events handlers, busy registry, classify/writeError,
  sentinels, wire payload types.
- `internal/bookgen/pipeline.go` — runBook (stages), structure/upserts,
  approvalBridge, renderFilm, supersedeFilms.
- `internal/bookgen/video.go` — `FFmpegRenderer` adapter over bookvideo.Render.
- `internal/bookgen/bookgen_test.go` / `pipeline_test.go` / `http_test.go` /
  `harness_test.go` — unit + e2e pins (list below).
- `cmd/thutapi/main.go` + `main_test.go` — C1 rows.

## Tests (what pins what)

| Test | Pins |
|---|---|
| TestPipeline_EndToEnd | whole run over real HTTP: 202 shape; 8 page_approved + book_ready on the book topic; job done; authored title + byline preserved; page/cast rows; sheets/illustrations/narrations per slot; exactly one book-attached film row whose bytes serve over real GET /media/{id}; renderer input carries the persisted bytes in page order |
| TestPipeline_StageOrder | stage order structure → sheets → pages → narration → render → film, counts (2 sheets, 8 pages, 8 clips) |
| TestPipeline_ApprovalEventsCarryRealIds | each page_approved's image_url == the persisted illustration row of that page |
| TestPipeline_FailureIsTotalAndTerminal | narrate failure: 8 approvals then failed {}; no film row; job terminal error; re-POST starts a fresh run that completes |
| TestPipeline_StructureFailurePublishesOnlyFailed | invalid structure: failed {} only, zero imager/tts calls (nothing spent) |
| TestPipeline_PanicStillPublishesFailed | panicking stage still publishes failed {} once; job terminal wraps ErrPanic |
| TestPipeline_RenderFailureAndFilmFailureAreTotal | render error and film persist error both fail the run with no film row |
| TestDoubleFireRefused | second POST while running → 409 busy; after terminal → fresh job id; old film superseded (one video row remains) |
| TestGenerateRefusals | 404 not_found / 409 not_ended / 500 internal (bookless) |
| TestPipeline_CatchUpStateIsInTheStore | stream is from-now-on; state (pages, title) readable from store rows (C4's read is the recorded gap) |
| TestUpsert_RowsAreReplacedNotDuplicated | upsert twice keeps one row per slot |
| TestEventsRouteStreamsSSEOverHTTP | full SSE over a real connection: event:/data: framing, 8 page_approved + book_ready with correct payload keys |
| TestEventsRoute404 / TestMethodMismatches | events 404; 405 on wrong methods |
| bookgen_test.go unit pins | New nil-config refusals, topic format, classify mapping, raw JSON wire shapes, ended detection, events path |
| TestGenerateRoutesServeThroughMux (cmd) | route seam per invariant 5 |

## Gates (scoped — full-tree validation is the orchestrator's post-landing run; T5c is in flight in internal/story)

| Gate | Result |
|---|---|
| `go vet ./internal/bookgen/ ./cmd/thutapi/` | clean |
| `go test ./internal/bookgen/ -race -count=1` | ok (2.65 s) |
| `go test ./cmd/thutapi/ -race -count=1` | ok (9.99 s) |
| `go test ./internal/bookgen/ -cover` | **81.7%** of statements (floor 75%) |
| `gofmt -l internal/bookgen cmd/thutapi` | empty |

## Boundaries / residue

- No file outside `internal/bookgen/**`, `cmd/thutapi/{main.go,main_test.go}`
  and this record was created or modified (verified: `git status` shows only
  T5c's in-flight edits to `internal/story/*`, `internal/audio/audio.go`,
  `internal/gmi/media/client.go` — none touched here — plus this delta).
- PLAN.md untouched (operator's). Job/stream/interview/story/illustrate/
  audio/bookvideo/store/mediastore untouched (consumed at the surface only).
- Emotion vocabulary: fixtures use only "happy" (valid before AND after
  T5c's neutral→calm change); bookgen never enumerates the vocabulary and
  passes `story.Page.Emotion` through unmodified.
- Disclosed non-fault-injectable branches: the approval read-back failure
  inside the Progress callback (D4) and the film row attach failure after a
  successful film persist (both would need a mid-run real-store fault to
  fire; both fail the run loudly rather than silently degrading).

---

# T10c round 1 — adversarial review verdict

| | |
|---|---|
| **Reviewer** | Fresh agent (`T10cRound1Reviewer`), no prior T10c round. Review of the working tree at HEAD `cfee82e` (= be12727 + the landed T5c commit) plus T10c's uncommitted delta: `cmd/thutapi/{main.go,main_test.go}`, `internal/bookgen/**`, this record. |
| **Shared-tree note** | The batch context anticipated T5c's edits to `internal/story/*`, `internal/audio/audio.go`, `internal/gmi/media/client.go` as in-flight. They LANDED as HEAD `cfee82e` before this review began; the tree shows no uncommitted T5c delta (`git status` lists only the T10c files). T5c's commit was inspected for signature drift against bookgen's consumption: it changes only prose and the emotion vocabulary (`story.Emotions`: `neutral`→`calm`) — no consumed signature changed, so there is no T5c/T10c reconciliation issue. No test failure in story/audio/media was observed; full-tree `go test ./... -count=1` is green. |
| **Scope** | T10c's diff only: `internal/bookgen/**` read in full (bookgen.go, pipeline.go, video.go, harness/pipeline/http/bookgen tests); full git diff of `cmd/thutapi/main.go` + `main_test.go`; consumed surfaces read at the surface bookgen calls (internal/job, internal/stream, internal/interview, internal/story, internal/illustrate, internal/audio, internal/bookvideo, internal/store, internal/mediastore). Nothing edited; mutations applied and reverted byte-identical (md5-verified). |

## Verdict

**APPROVE — 0/0/0/0 (C/H/M/L). Zero findings.**

Every spec contract in PLAN.md §T10c and §The flow is implemented as specified
and pinned by a load-bearing test; all six contract rows C1–C6 are sanctioned
(two with cross-track dependencies recorded for their owning lanes, below);
every stated gate passes; and five reviewer-run mutations confirm the pins are
load-bearing (table below). No patch-introduced defect meeting the review
criteria (provable impact, actionable, unintentional, introduced in the patch)
was found.

## Findings

None. Candidate issues examined and rejected (each fails at least one review
criterion):

| Candidate | Why not a finding |
|---|---|
| `sync.Once` around the terminal publish is not load-bearing (removing it leaves the suite green — M2) | Not a defect: with the two defers as written, a single run can reach only one failure path (plain error → err-defer; panic → recover-defer with `err` still nil), so exactly-once is structural. Defensive, harmless. M2b (removing the panic-recover guard) IS load-bearing → red. |
| `mediastore` lacks `video/mp4`, so a production run fails at the film persist | Recorded, deliberate, loud (C2). Cross-track dependency for the mediastore owner, not a bookgen defect; the seam + fake pin the whole film lifecycle against the real store today. |
| No HTTP catch-up read for a late book-state subscriber | Recorded (C4), the code never claims the read exists, and the store-side state is pinned. T10b/T9's surface. |
| Approval read-back failure branch not fault-injectable (D4) | Disclosed in the record; matches the house compiler-honesty pattern for branches a fixed struct / real store cannot fail in (cf. `job.marshalCompletion`, `interview.writeJSON`). |
| Film staged in the OS temp dir rather than under `/data` (pipeline.go `os.CreateTemp`) | Matches the renderer seam's own default workdir (bookvideo zero-Config WorkDir); transient, removed in a defer, ~MB-scale. No provable container failure (distroless `/tmp` is writable). |
| Generate 500 vs Events 404 for a bookless interview | Unreachable via the normal start path (1:1 book per interview); both refuse loudly; pinned as intentional (TestGenerateRefusals). |

## Contract-row rulings

### C1 — main.go wiring — SANCTIONED

Verified against the diff: imports (main.go:30-32), `server.generate` field
(main.go:140), `newServer` signature + the two sanctioned route lines
(main.go:145, 164-165 — `POST /interviews/{id}/generate` → `s.generate.Generate`,
`GET /interviews/{id}/generate/events` → `s.generate.Events`), the hoisted
`mediaDir`/`blobs` rename (main.go:260-267), shared `textCli`/`mediaCli`
(main.go:274-275), shared `runner` (main.go:283), and the bookgen.New
dependency set (main.go:294-307). Route-seam pin `TestGenerateRoutesServeThroughMux`
passes (404 not_found from the handler, 405 from the mux). (Note: the stub's
C1 file:line pins were measured on the pre-T5c-commit tree and are shifted by
~10 lines; the lines above are the current-tree anchors.)

### C2 — the film's content type is not yet storable — SANCTIONED as a recorded cross-track dependency (bookgen honest; owning lane must land the type)

Verified: `mediastore.supportedTypes` (mediastore.go:69-75) is a closed set
without `video/mp4`; `Persist` rejects it with `ErrInvalidContentType`
(mediastore.go:142-146). bookgen's `filmStore` seam (bookgen.go:238-241) is the
production `*mediastore.Store` (main.go:305), so today a production run fails
**loudly** at `persist film` → `failed {}` terminal — never a `book_ready`
naming an unstoreable URL. The test suite drives the seam with a fake
(harness_test.go:371-420) that mirrors Persist's real shape (blob file named by
a fresh id + one real `store.CreateMedia` row) and pins row, placement,
serving and supersession against the real store.

**What the owning lane must land:** add `"video/mp4"` to
`internal/mediastore`'s `supportedTypes` (a declared contract amendment
against closed T3, in the shape of the 847e9b6 byline change), with a raw
Persist/ServeHTTP pin. No bookgen change is needed: the moment the closed set
gains the type, the production film lands, `SetMediaPlace` attaches it
kindlessly (a legal shape, media.go:147-151), BookMedia lists it, §T11's
unplaced-blob sweep never touches it, and GET /media/{id} serves it (the
mediastore handler is content-type agnostic once the row exists).

### C3 — readiness as a book-attached film row — SANCTIONED (no invented schema)

Verified: the store has no book-state column and no video media kind
(`MediaKind` = reference/illustration/narration only, media.go:17-27); a film
row is placed with `SetMediaPlace{BookID}` and empty Kind — explicitly a legal
shape ("a blob attached without a kind has no anchor", media.go:147-151).
`book_ready` is published only after that row is placed (runBook returns the
video id only after renderFilm's attach). `TestPipeline_EndToEnd` pins exactly
one book-attached video/mp4 row whose bytes serve over the real GET /media/{id}.
T10b owns how "ready" surfaces on GET /book/{id}.

### C4 — the book-state HTTP read for late subscribers does not exist — SANCTIONED as a recorded cross-track dependency (honest; owning lane must land the route)

Verified: no route reads book state today (newServer registers only healthz,
media, the four interview routes and T10c's two); the broker is from-now-on by
design. bookgen's doc claims only what the store provides: approved pages are
MediaIllustration rows, the film is the book's video/mp4 row — and
`TestPipeline_CatchUpStateIsInTheStore` pins that state (8 page rows, authored
title, no stream replay) from the store. What the record says is NOT there — a
run-outcome read (failed runs leave only partial store rows + the in-memory job
registry) — is stated as T10b/T9's decision, not papered over.

**What the owning lane must land:** T10b's GET /book/{id} (or a book-state JSON
surface) exposing pages-approved (count of placed MediaIllustration rows), the
film URL (the book's video/mp4 row), and the run state; the run-state half may
require a persisted run-outcome row, which is a T10b/T9 schema decision to make
before screen 5's failure doors can be rebuilt correctly on reload.

### C5 — blob-file reads by mediastore's documented layout — SANCTIONED

Verified against the docs and the code: mediastore's own package doc states
"each blob is one file named by its id" under the configured dir
(mediastore.go:7-11), and `writeBlob` creates exactly `<Dir>/<id>`
(`root.Create(id)`, mediastore.go:128). main hoists `mediaDir` and passes it
down (main.go:260, 296); the film stage reads `<MediaDir>/<id>` for
illustrations and narration. `TestPipeline_EndToEnd` proves the read bytes are
the persisted ones (renderer input carries them in page order). A future
programmatic read API is noted as the owner's call; the coupling is physical
but documented and test-covered.

### C6 — byline realisation — SANCTIONED

Verified: `CreateBook(ctx, title)` has no byline parameter and starts byline
empty (books.go:30, 40-41); `UpdateBook` writes title + byline and is the only
writer (books.go:98-118); the book row already exists at interview start
(interview/http.go:75). bookgen reads the row, structures, then updates
title-with-byline-preserved BEFORE any page/persist work (pipeline.go:91-128),
so the byline is on the row before the title-card-consuming stages run.
Confirmed no production caller writes `books.byline` (grep: only tests and this
preserve path) — the POST /interviews `{"byline"}` surface is T9/T4's
unimplemented wire contract 1, exactly as recorded. Fixtures set the byline
directly and the render input pin carries it to the title card.

## Spec contracts — verified against code and pins

| Contract | Verified by |
|---|---|
| POST /interviews/{id}/generate → job id; second POST while running → 409 busy, no second run; re-POST after terminal → fresh run | TestDoubleFireRefused + M1 (guard removed → red: second POST became 202) |
| Stage order: structure → page/cast rows → illustrate (judge+persist) → narrate → bookvideo → film row + ready | TestPipeline_StageOrder + M3 (narrate-before-illustrate → red) |
| Events on the BOOK topic: page_approved {n,image_url} per approved+persisted page (Progress bridge reads the persisted row back) | TestPipeline_EndToEnd, TestPipeline_ApprovalEventsCarryRealIds, TestEventsRouteStreamsSSEOverHTTP + M4 (unbound URL → red) |
| book_ready {video_url} only after the film row is placed | TestPipeline_EndToEnd + M5 (attach removed → red: 0 film rows) |
| failed {} exactly-once, no code/prose; failure total + terminal; no auto retry | TestPipeline_FailureIsTotalAndTerminal, TestPipeline_StructureFailurePublishesOnlyFailed; M2b (panic guard removed → red) |
| Panic → failed (exactly-once), ErrPanic terminal | TestPipeline_PanicStillPublishesFailed |
| Late-subscriber catch-up: store-side state readable (pinned); HTTP read gap recorded | TestPipeline_CatchUpStateIsInTheStore (from-now-on verified; title/pages in store); C4 |
| *text.Client satisfies story.Chatter AND illustrate.Judge; one production instance for both | Compile-proven: main.go passes `textCli` as both `Chat` and `Judge`; interfaces identical one-method shape (story/structure.go:21-23, illustrate/verify.go:45-47) |
| Persist-before-progress ordering (the bridge's read-back is of the just-placed row) | illustrate.go:475-480 (`closePage` incl. `storePage` persist, then `report`) — closed T7 seam, own pins + t7-round2.md L1; report() serialised under renderer mutex (illustrate.go:299-307) |

## Mutation table — reviewer re-runs (apply → run → revert, byte-identical)

All mutations applied to `internal/bookgen/{bookgen.go,pipeline.go}` from a
snapshot of the pristine tree and reverted by copy afterwards; md5 verified
identical before and after (baseline `9bc8499b… bookgen.go`,
`7a99c21c… pipeline.go`, `8a7e3a5d… video.go`; every revert re-checks all
three as OK). Final `git status` shows only the T10c delta — no residue.

| # | Mutation | Run | Result | md5 after revert |
|---|---|---|---|---|
| M1 | Remove the busy check in `startRun` (bookgen.go:388-393) so a running book never refuses a second POST | `go test ./internal/bookgen/ -run TestDoubleFireRefused -count=1` | **RED** — `second POST = 202 ({}), want 409 busy` (pipeline_test.go:504) | `bookgen.go: OK` (9bc8499b…) |
| M2 | Remove the `sync.Once` guard on the terminal publish (publish directly) | failure/panic pins (FailureIsTotalAndTerminal, PanicStillPublishesFailed, Render/FilmFailure, StructureFailure, EndToEnd) | **GREEN** — the defer structure alone serialises the failure paths (plain error → err-defer only; panic → recover-defer only, `err` still nil). The once is defensive, not load-bearing; recorded, not a finding. | `bookgen.go: OK` |
| M2b | Remove the panic-recover defer (a panicking stage no longer publishes `failed`) | `TestPipeline_PanicStillPublishesFailed` | **RED** — `timed out waiting for "failed" event` (pipeline_test.go:421) — the panic→failed exactly-once pin IS load-bearing | `bookgen.go: OK` |
| M3 | Swap stages in `runBook`: narrate before illustrate | StageOrder + EndToEnd + FailureIsTotalAndTerminal | **RED** — `stage "tts" at 0 is not after the previous stage at 10` (pipeline_test.go:230); failure test also red (failed before any page_approved) | `pipeline.go: OK` |
| M4 | `approvalBridge.progress` publishes `page_approved` with a fixed `/media/fake-id` instead of the persisted row's id | ApprovalEventsCarryRealIds + EndToEnd + EventsRouteStreamsSSEOverHTTP | **RED** — `page_approved 1 = "/media/fake-id", want the persisted illustration row "/media/4c6f…"` (pipeline_test.go:295); EndToEnd `events = 1, want 8` | `pipeline.go: OK` |
| M4a | Reorder publish before the read-back inside the callback and silently drop read failures | ApprovalEventsCarryRealIds + EndToEnd + StageOrder | **GREEN** — the persist-then-event timing is enforced inside the closed T7 seam (illustrate.go:475-480); bookgen's pins guard the observable contract (event bound to the placed row; a post-approval read failure fails the run, D4) | `pipeline.go: OK` |
| M5 | Drop the film `SetMediaPlace` attach in `renderFilm` (film row never attaches; book_ready would name an unplaced blob) | EndToEnd + TestDoubleFireRefused + Render/FilmFailure | **RED** — `film rows = 0, want exactly 1` (pipeline_test.go:145); DoubleFire `film rows after two runs = 0, want 1` | `pipeline.go: OK` |

## Gates table (reviewer re-run, 2026-09-05)

| Gate | Result |
|---|---|
| `go build ./...` | clean (exit 0) |
| `go vet ./internal/bookgen/ ./cmd/thutapi/` | clean |
| `gofmt -l internal/bookgen cmd/thutapi` | empty |
| `go test ./internal/bookgen/ -race -count=5` | ok (9.15 s) |
| `go test ./cmd/thutapi/ -race -count=1` | ok (10.03 s) |
| `go test ./internal/bookgen/ -cover` | **81.7%** of statements (floor 75%) — matches the implementer's claim |
| `go test ./... -count=1` (full tree) | all packages ok — **no external failures to attribute**; the anticipated T5c in-flight lane landed at HEAD `cfee82e` and its packages (story, audio, gmi/media) pass |

## Zero-residue claim

T10c has no prior review round. This round reports zero findings
(0 C / 0 H / 0 M / 0 L) and therefore carries zero residue into
round 2. The two recorded cross-track dependencies (C2: `video/mp4` in
mediastore's closed content-type set; C4: the book-state HTTP read) are NOT
T10c findings — they are owning-lane deliverables the bookgen code is honest
about, and their absence fails T10c's production film step loudly rather than
silently.

---

**VERDICT: APPROVE (0/0/0/0) — zero findings, zero residue.**
