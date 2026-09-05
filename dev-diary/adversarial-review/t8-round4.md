# T8 round 4 — the question-audio persistence amendment (wire contract 2)

| | |
|---|---|
| **Target** | T8, working tree on `main` at `8b7066b` (T8 closed at round-3 APPROVE, 0/0/0/0, against the round-1–3 spec). The audited delta vs `8b7066b`: modified `internal/audio/audio.go` and `internal/audio/question_test.go`; untracked this file. |
| **Recorded by** | Implementation agent (`T8Amendment`), 2026-09-05. |
| **Scope** | Only `internal/audio/**` and this record. No `cmd/`, no `internal/store`, `internal/mediastore`, `internal/gmi/*`, `internal/interview`, no SSE/flow code, no `dev-diary/PLAN.md` (concurrent operator work). |

## Why this round exists

T8 reached round-3 APPROVE against the round-1 spec, in which round-1's
design decision **D3 ruled question audio SOUND as "a plain function
returning downloaded bytes, no store write"** (`t8-round1.md`, ruling
`"D3 — question shape (plain bytes-returning function, turbo, no store
write) — SOUND"`, pinned by `TestSynthesizeQuestion_NoStoreNeeded`'s "no
row appears"). An outside review of the T9 spec then found that this exact
shape breaks the flow: **there is no `/media` URL the UI could play**.
`audio.SynthesizeQuestion` returns bytes and touches no store by design
(PLAN.md §The flow, wire contract 2, `dev-diary/PLAN.md:1106-1125`), so a
`question_audio` SSE event would name a URL that does not exist.

The ruling and its remedy are recorded in PLAN.md, which the operator owns:
wire contract 2 (`PLAN.md:1106-1125`) and decision 19 (`PLAN.md:1762`)
decide question audio arrives as a **second SSE event**
`question_audio → {"turn":1,"audio_url":"/media/<id>"}` — never a field on
`questionEvent`, because a field would hold the text until TTS returned —
and state the precondition this round implements: *"This makes question
audio persist to `mediastore` like narration does, so the URL is real and
serves over the proven `GET /media/{id}` with Range. **That is a contract
change against T8 … it belongs in T8's next round file, not in a quiet
edit.**"*

Decision 19's *event shape* is untouched by this round (that is T9's
wiring). What changes is the mechanism behind it: **the question clip now
persists, so the URL exists before any event can name it.**

## Contract-change row — supersedes round-1 D3's question shape

| | |
|---|---|
| **Supersedes** | Round-1 D3 ("question audio shape: a plain function returning downloaded bytes, no store write — SOUND", `t8-round1.md`), its export-surface row `SynthesizeQuestion(ctx, cfg, text) ([]byte, error)`, and its requirement-table row "no store write … pure parallel-fire shape" pinned by `TestSynthesizeQuestion_NoStoreNeeded`. |
| **New contract** | `SynthesizeQuestion` synthesises, downloads on receipt (unchanged), **persists the clip (blob + metadata row) when a store is configured**, and returns **both the audio bytes and the persisted media id** (the `/media/<id>` URL). The persist is on the parallel path: the seam stays a pure function the caller fires when it likes, so text never waits on audio. A Config with no store keeps returning plain bytes (the round-1 questions-without-store mode). |
| **Where recorded upstream** | PLAN.md §The flow wire contract 2 (`1106-1125`) and decision 19 (`1762`), 2026-09-05. |

## Design decisions (implementation record — reviewer to rule)

### D1 — Persist shape: an UNPLACED media row; no store contract row needed

**Decision:** the question clip is `mediastore.Persist`'d like a narration
clip — blob file first, then one `store` row — but the row is left
**unplaced**: `CreateMedia` with no `BookID`, no `Kind`, no anchor. The
returned id serves over `GET /media/{id}` exactly like a placed blob's.

**Evidence (read, not assumed):**

* The store's kinds are a closed, book-anchored set: `MediaKind` names
  "the role a blob plays in its **book**" — reference (cast member),
  illustration (page), narration (page) (`internal/store/media.go:11-27`);
  `SetMediaPlace` errors on any kind outside them (`media.go:164-166`,
  "unknown kind … want reference, illustration or narration").
* Question audio is interview-scoped: it exists *before* Phase B creates
  any book/page/cast row (question zero is turn 1 of the interview), so
  there is no book row to anchor to and none of the three kinds fits.
  Inventing a fourth kind or an interview anchor would be a store contract
  change this round does not need.
* The store already offers the honest shape for "bytes persisted with no
  book": `CreateMedia`'s doc — "A new row is always unplaced: the role and
  anchor are set afterwards with `SetMediaPlace`" (`media.go:73-80`) — is
  exactly the state `mediastore.Persist` leaves (`mediastore.go:132-166`),
  and `Media.BookID` is "empty until the blob is attached to a book"
  (`media.go:52-53`). `BookMedia` never lists such rows ("Unplaced blobs
  have no book and are never listed", `media.go:183-186`) — correct,
  because a question clip is nobody's book media.
* Serving needs no placement: `mediastore.ServeHTTP` resolves the row by
  id alone (`mediastore.go:246-308`); there is no placed-only gate, so an
  unplaced row answers `GET /media/{id}` with `http.ServeContent` (Range,
  ETag, correct `audio/mpeg` — the type `fetchAudio` already gates into
  mediastore's closed set, `mediastore.go:69-75`).
* Retention: unplaced rows are the same orphan class PLAN.md §T11's
  retention sweep already owns (the crash-window orphans of narration's
  and illustration's replace paths, and `mediastore.Delete`'s row-first
  ordering). No new cleanup mechanism is introduced.

**Consequence:** no `internal/store` / `internal/mediastore` change, no
contract row against the store.

### D2 — API shape: `SynthesizeQuestion(ctx, cfg, text) ([]byte, string, error)`

**Decision:** the function returns the audio bytes **and** the persisted
media id; an empty id means the Config carried no store and nothing was
persisted. The URL is the fixed route `"/media/" + id` — cmd/thutapi's
`newServer` registers exactly that pattern (PLAN.md invariant 5;
`cmd/thutapi/main.go:149`) — so the id is the store-shaped value the
package owns and the caller builds the event's `audio_url` from it. No
read-back query: `mediastore.Persist` already returns the id, and nothing
here needs `SizeBytes`/`CreatedAt` (a caller that does can `db.Media(id)`).

**Evidence:** repo-wide grep finds **no caller of `SynthesizeQuestion` or
`audio.Config` outside `internal/audio`** (the interview never wired it;
that is T9/T10c's future work), so the signature change is
package-internal and breaks nothing — the round-3 tree's `go build ./...`
stays green. The pure-function parallel-fire shape is unchanged: the
caller still decides when to fire the call, and the text path never waits
on it.

### D3 — The persist conditional: both halves set → persist; neither → plain bytes; one → ErrNoStore before any paid call

**Decision:** exactly mirroring how illustrate gates on a configured
persist writer (`Config.Persist *BookWriter` — nil writes nothing) and how
this package already refuses half-configs:

* `DB != nil && Blobs != nil` → synthesise, download, persist, return
  `(bytes, id)`.
* `DB == nil && Blobs == nil` → the legal round-1 questions-without-store
  mode: synthesise, download, return `(bytes, "")`, write nothing. All
  pre-amendment no-store callers/tests keep their behaviour.
* exactly one set → `ErrNoStore` before the TTS call, the message naming
  the missing half. A silently half-wired store would silently drop the
  persist — the "no silent half-config" rule this package's round-1
  requirement table already enforced (`ErrNoStore`/`ErrNoTTS` before any
  call) and AGENTS.md's loud-failure rule.

### D4 — A persist failure fails the call; every question is its own row

**Decision:** a store-write failure after a successful download returns
`(nil, "", err)` wrapped as `audio: persist question clip: …` — mirroring
narration's persist-failure-fails-the-page (`TestNarrate_PersistFailure
FailsThePage`, `narrate_test.go:356-372`). A clip that never reached the
store is not a playable question; the caller treats the failed call as
"question_audio never arrives", which wire contract 2 already names as a
normal state ("The second may **never arrive** … the UI must treat that as
normal and stay silent"). No second retry layer is added (invariant: the
media client's single retry is the only retry). Repeated questions each
persist a fresh unplaced row — there is no slot to conflict or replace on,
so no `ErrConflict` machinery applies.

### D5 — Doc corrections (behaviour-matching, per AGENTS.md "Docs about behaviour must match the behaviour")

Every now-wrong claim about the question path, found by grep and corrected
in the same change:

| Stale claim (pre-amendment) | Location | Now states |
|---|---|---|
| "Questions are not book narration; nothing here anchors them into store rows" | `audio.go` package doc, §The two paths | Question clips persist like narration — blob + row — as **unplaced** rows (no book anchor exists); the seam stays a pure function |
| "Nothing is persisted: questions are not book narration and the interview keeps no audio rows" | `audio.go` `SynthesizeQuestion` doc | Full new contract: persist-on-store conditional, unplaced-row shape, id/URL, persist-failure and never-arrives semantics |
| "The question path never needs them" | `audio.go` `ErrNoStore` doc | Covers both paths: one half without the other is `ErrNoStore`; both nil is the legal question plain-bytes mode |
| "Required for NarrateBook … and never touched by SynthesizeQuestion" | `audio.go` `Config.DB`/`Blobs` doc | Both halves set → question clips persist too (unplaced); neither → plain bytes; one → `ErrNoStore` |
| "…the question path needs neither" | `audio.go` `NarrateBook` `ErrNoStore` wrap | Dropped (narration still needs both) |
| "questions are not book narration and write no store rows" / "the interview keeps no audio rows" | `question_test.go` test docs | Rewritten to the conditional contract (see D6) |

### D6 — Tests (what each pins)

| Test | Pins |
|---|---|
| `TestSynthesizeQuestion_PersistsOnConfiguredStore` (new) | **Persist-on-configured-store**, end to end: `(bytes, id)` returned; `db.Media(id)` exists and is **unplaced** (empty `BookID`/`Kind`/`PageN`/`CastName`), `audio/mpeg`, exact `SizeBytes`; blob on disk byte-equal to the served clip; `BookMedia` stays empty (never book media); **`GET /media/{id}` through the real mux route (`mux.Handle("GET /media/{id}", blobs)` — the cmd/thutapi registration, illustrate live-test precedent) serves 200 `audio/mpeg`, byte-identical**; a second call persists a fresh distinct id (no slot, no replace) |
| `TestSynthesizeQuestion_NoStoreConfigReturnsPlainBytes` (repurposed from round-1's `…NoStoreNeeded`) | **No-store Config still returns plain bytes** and id `""`; one download; nothing written — the Config holds no store handle |
| `TestSynthesizeQuestion_HalfStoreIsErrNoStore` (new) | DB-without-Blobs and Blobs-without-DB are both `ErrNoStore` **before any TTS call**, the message naming the missing half |
| `TestSynthesizeQuestion_PersistFailureFailsTheCall` (new) | Closed store → the call fails naming `persist question clip`, returns nil bytes and empty id |
| `TestSynthesizeQuestion_DefaultsAndBytes` (updated) | Turbo + `DefaultVoice` + no emotion at the seam, downloaded bytes returned — now also asserting id `""` on the no-store Config |
| `TestSynthesizeQuestion_EmptyTextIsErrNoText` / `NilTTSIsErrNoTTS` / `UpstreamErrorPassesThrough` / `DecodeFailureIsLoud` / `FetchFailureIsErrNoAudio` (updated) | The pre-amendment error branches, all `errors.Is`-asserted, adapted to the three-value signature |

The persist/no-store/half-store tests *are* the doc-vs-behaviour alignment
check: the corrected doc claims (D5) name exactly the three store states
D3 defines, and each state has a pin.

## Files changed

* `internal/audio/audio.go` — package-doc §The two paths; `ErrNoStore`
  doc; `Config.DB`/`Blobs` doc; `NarrateBook`'s `ErrNoStore` message;
  `SynthesizeQuestion` doc + signature `([]byte, string, error)` + body
  (store-conditional persist, half-store refusal, persist wrap).
* `internal/audio/question_test.go` — rewritten for the new contract
  (D6).
* `dev-diary/adversarial-review/t8-round4.md` — this record.

Not touched: `internal/audio/decode.go`, `audio_test.go`,
`narrate_test.go`, `persist_test.go`, `decode_test.go`, `wire_test.go`
(narration code and its tests are byte-identical to the round-3 tree); no
file outside `internal/audio/**` and this record.

## Gates (scoped — the project-wide full-suite run is the orchestrator's single post-landing validation)

| Gate | Result |
|---|---|
| `go vet ./...` | clean (whole tree — compile-safe across the signature change) |
| `go build ./...` | clean |
| `go test ./internal/audio/ -race -count=1` | ok (1.775 s) |
| `go test ./internal/audio/ -race -count=5` | ok (4.958 s) |
| `go test ./internal/audio/ -cover` | **97.8%** of statements (floor 75%); `SynthesizeQuestion` 100% — the only uncovered statements are the four round-1-D5-disclosed non-fault-injectable narration wraps (`storeClip` read-back, `place` replace-path), unchanged from rounds 1–3 |
| `gofmt -l .` | empty |

## Boundaries / residue

* No store contract row was needed: the store's own unplaced-row shape
  sufficed (D1). No `internal/store`, `internal/mediastore`,
  `internal/interview`, SSE or `cmd/` change exists to review.
* The `question_audio` SSE event itself, the question-persist caller, and
  PLAN.md remain the operator's/T9's concurrent work, untouched here.
* The round-1 requirement-table row "`SynthesizeQuestion`: downloaded
  bytes returned, **no store write**, turbo default, pure parallel-fire
  shape" and round-2's "**D3** question shape … no store write" HOLDS
  lines are superseded by the contract-change row above; rounds 1–3's
  remaining closures (L1/L2/C1/H1 records, decode.go byte-identity) are
  untouched by this delta.

---

# Reviewer verdict — T8 round 4 (fresh reviewer)

| | |
|---|---|
| **Reviewed by** | Review agent (`T8Round4Reviewer`), 2026-09-05 — fresh, no prior T8 round (AGENTS.md: a fresh agent per role per round). |
| **Base** | Working tree on `main` at `8b7066b`; the audited delta is the amendment record above plus `internal/audio/audio.go` and `internal/audio/question_test.go` (the only working-tree changes vs `8b7066b`). No file outside `internal/audio/**` and this record was reviewed as changed — there is nothing else changed to review. |
| **Evidence** | The amendment record above (D1–D6, contract-change row, gates, boundaries) — every claim re-verified by hand, not trusted; `PLAN.md:1106-1125` (wire contract 2) and `PLAN.md:1762` (decision 19); `t8-round3.md` (the round-3 APPROVE this builds on); `t8-round1.md` D3 (`:211-218`, the ruling this supersedes) and its requirement row (`:250`); `internal/audio/audio.go` (package doc, `ErrNoStore`/`Config` docs, `SynthesizeQuestion` `:397-471`); `internal/audio/question_test.go` in full + the shared harness (`audio_test.go`); `internal/store/media.go` (kinds `:11-27`, `CreateMedia` `:73-113`, `SetMediaPlace` `:133-181`, `BookMedia` `:183-210`); `internal/mediastore/mediastore.go` (supported types `:66-75`, `Persist` `:132-166`, `Delete` `:212-230`, `ServeHTTP` `:260-309`); `cmd/thutapi/main.go:149` (the `GET /media/{id}` registration); `internal/interview/http.go` `start` (book row exists from interview start); `PLAN.md` §T11 (`:1527-1556`, sweep ownership); repo-wide greps (importers of the audio package, stale doc phrases). |
| **Method** | Contract-2 mapping (persist/bytes/conditional/no-store mode/supersession); unplaced-row honesty check against the store's own docs; every pin read against the code it pins; all four load-bearing mutants re-applied by hand (apply → target pin observed RED → restore, `md5sum -c` OK over the 8-file manifest after every restore); all gates re-run by this reviewer on the amended tree; the full-suite `-race` flake class attributed by isolation runs, a dependency proof, and pristine-base runs. |

## Verdict

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**

The amendment implements the operator's wire-contract-2 change exactly as PLAN.md
prescribes: question audio now persists (blob + one unplaced metadata row) when a
store is configured, so `/media/<id>` is a real URL serving the exact persisted
clip over the registered route; the bytes are still returned (text never waits);
the persist is conditional on the store, so a no-store Config keeps round 1's
plain-bytes mode intact; and the half-config state is refused loudly before any
paid call. Round-1 D3's question shape is properly superseded and recorded in the
contract-change row above. Every behavioural claim in the corrected docs matches
the code (verified claim-by-claim against the store's own docs and the pins), the
signature change is package-internal with zero external callers (build green),
and all four mutants bite. Details: rulings, mutation table, gates, zero-residue
claim below. The full-suite `-race` flake class observed today is environmental
and outside the patch (see the dedicated note).

## Findings

None. No issue meets the review bar (provable impact on a patch-introduced code
path). **0 × C, 0 × H, 0 × M, 0 × L.** Two points were examined and resolved as
non-findings, recorded here so the reasoning is on the audit trail:

* The "no book row to anchor to" phrasing (record D1, `audio.go:16-18`, `:408-410`)
  read against `internal/interview/http.go` `start`, which creates the book row
  before the opening turn streams: a (placeholder) book row does exist at question
  time. Under the ownership reading the claim holds — the media schema anchors
  only books, a question clip is interview-scoped, and the store's kinds are
  book-deliverable roles — and the *behavioural* claims are all exact: the row is
  created with no book and no kind (`mediastore.Persist` → `CreateMedia` with no
  `BookID`), `BookMedia` selects by `book_id` and so never lists it, and serving
  needs no placement. The unplaced-no-book choice is what keeps a question clip
  from surfacing as its interview-book's media. No behaviour is misdescribed, so
  no finding.
* The full-suite `-race` poll-timeout flakes in `internal/interview` /
  `internal/job` (see the note below): they execute in test binaries containing
  zero audio code (no import, no dependency), do not reproduce in isolation, and
  today's pristine-base run flaked in `cmd/thutapi` with the patch absent — the
  round-3-recorded environmental class, outside this amendment's seam.

## Rulings on the amendment's design decisions (record D1–D6) — re-verified, not trusted

Rows use the house Where / What / Pin / Mutation shape; "green pins" are the tests
listed in the record's D6 table, re-run green by this reviewer.

### D1 — unplaced-row persist shape — **SOUND**

* **Where:** `audio.go` `SynthesizeQuestion` + record D1; **What:** question clips
  persist via `mediastore.Persist` as rows with no book and no kind; **Pin:**
  `TestSynthesizeQuestion_PersistsOnConfiguredStore` (unplaced row: empty
  `BookID`/`Kind`/`PageN`/`CastName`; `BookMedia` empty; id serves 200
  `audio/mpeg`, byte-identical through the registered route pattern); **Mutation:**
  M3 (id dropped) → RED.
* Verified against the store, not assumed: `MediaKind` is a closed, book-anchored
  set ("the role a blob plays in its book", `media.go:11-27`) and `SetMediaPlace`
  refuses any kind outside it (`media.go:164-166`) — adding a question kind or an
  interview anchor would be a store contract change this round does not need.
  `CreateMedia`'s doc is explicit that a new row is always unplaced
  (`media.go:73-80`); `mediastore.Persist` inserts exactly that shape with no
  `BookID` (`mediastore.go:155`), so the row is unattached as well as unplaced.
  `BookMedia` never lists such rows — it selects `WHERE book_id = ?`
  (`media.go:191-193`) — and `ServeHTTP`/`media` resolve by id alone with no
  placement gate (`mediastore.go:246-309`). The served content type is safe: the
  closed set includes `audio/mpeg`/`audio/wav` (`mediastore.go:69-75`) and
  `fetchAudio` already gates into exactly those (`decode.go:148-153`, `:172-175`).
  Retention: `mediastore.Delete` is row-first and its own doc assigns the
  crash-window unreferenced file to PLAN §T11's sweep (`mediastore.go:212-214`;
  §T11 owns the retention sweep, `PLAN.md:1527`, item 4), and `Persist` removes
  the file when the row insert fails (`mediastore.go:155-163`) — so question rows
  introduce no new orphan class and no store doc contradicts the shape.

### D2 — API shape `([]byte, string, error)` — **SOUND**

* **Where:** `audio.go:434` signature; **What:** bytes + persisted media id; empty
  id = no store; the caller builds `/media/<id>`; **Pin:** the id is asserted
  non-empty on a configured store and `""` on a no-store Config, and the
  `GET /media/{id}` mux test proves the id is the store-shaped value the fixed
  route serves (`main.go:149` registers exactly `GET /media/{id}`); **Mutation:**
  M3 → RED.
* Verified: repo-wide grep finds zero importers of `thutapi/internal/audio` and
  zero `SynthesizeQuestion`/`audio.Config` callers outside the package, so the
  signature change breaks nothing; `go build ./...` clean; no read-back query is
  needed because `Persist` returns the id.

### D3 — persist conditional (both → persist; neither → plain bytes; one → ErrNoStore before any paid call) — **SOUND**

* **Where:** `audio.go:442-450`; **What:** the three store states the contract
  names, with the missing half named in the error; **Pin:**
  `TestSynthesizeQuestion_NoStoreConfigReturnsPlainBytes` (no-store mode: bytes,
  id `""`, one download, zero book rows), `TestSynthesizeQuestion_DefaultsAndBytes`
  (same mode through the default path), `TestSynthesizeQuestion_HalfStoreIsErrNoStore`
  (both directions, message names the nil half); **Mutation:** M1 (persist made
  unconditional) and M2 (half-config refusal removed) → RED.
* "Before any paid call" verified structurally: the switch precedes
  `sp.tts.SynthesizeSpeech` (`audio.go:451`), and M2's red demonstrates the
  alternative — with the guard removed the half-config call reaches the TTS seam
  ("audio: synthesize question: …" instead of `ErrNoStore`). The no-store mode is
  round 1's behaviour preserved: both-nil Configs are legal, return the downloaded
  bytes, and hold no store handle for any write to go through.

### D4 — persist failure fails the call; every question its own fresh row — **SOUND**

* **Where:** `audio.go:466-470`; **What:** a store-write failure after a successful
  download returns `(nil, "", err)` wrapped `audio: persist question clip: …`; no
  retry layer added; repeated questions persist distinct rows; **Pin:**
  `TestSynthesizeQuestion_PersistFailureFailsTheCall` (closed store: error naming
  the persist step, nil bytes, empty id) and the second-call half of
  `TestSynthesizeQuestion_PersistsOnConfiguredStore` (`id2` fresh and distinct);
  **Mutation:** M4 (persist error swallowed) → RED, and M3 → RED.
* Matches the package's own narration precedent (`TestNarrate_PersistFailure
  FailsThePage`) and the "never return a non-nil value alongside a non-nil error"
  rule (nil bytes on the error path). The "question_audio never arrives" consumer
  semantics are exactly what wire contract 2 already names as a normal state.

### D5 — doc corrections — **SOUND**

* **Where:** every stale claim site listed in the record's D5 table; **What:** the
  package doc, `ErrNoStore` doc, `Config.DB`/`Blobs` doc, `NarrateBook`'s wrap and
  `SynthesizeQuestion`'s doc now state the persist-on-store contract; **Pin:** the
  doc-vs-behaviour alignment is the D3/D4 test set — each of the three store
  states the docs name has a pin; **Mutation:** M1–M4 each contradict a doc claim
  and each goes RED.
* Verified by grep + read: no "touches no store", "the question path needs
  neither", "never touched by SynthesizeQuestion", "no store write" or
  "Nothing is persisted" claim remains anywhere in `internal/audio` — the only
  surviving mention of the old test name is the supersession reference inside the
  new test's doc comment (`question_test.go:110`), which is the record of the
  change, not a stale claim. No doc in the delta claims behaviour the code lacks.

### D6 — tests — **SOUND**

* **Where:** `question_test.go`; **What:** every contract clause has a pin (see
  D1–D5); **Pin/Mutation:** four mutants red, five-count race shake green,
  `SynthesizeQuestion` 100.0% covered.
* Conventions checked: real sqlite + blob store via the shared narration harness
  (never a fake for persistence), the `GET /media/{id}` mux round-trip mirrors the
  registration pattern the box actually uses (`main.go:149`; the illustrate
  live-test precedent), no live calls added, no new `//go:build` files, all
  assertions `errors.Is`-based where sentinels are involved.

## Supersession of round-1 D3 — verified recorded

Round-1 D3 ("question audio shape: plain bytes-returning function, no store
write — SOUND", `t8-round1.md:211-218`) and its requirement-table row
(`t8-round1.md:250`) are superseded by the contract-change row above, which names
the ruling, its export-surface row and its pin, and records the upstream home of
the change (PLAN.md wire contract 2 + decision 19 — the operator's mandate that
the change "belongs in T8's next round file"). That is the house channel for a
contract change; the round-1 file itself is history and is not rewritten, the
same way C1's rows were carried forward in rounds 1–3. The residue section above
records the round-1/round-2 HOLDS lines as superseded. The round-1 mode the
amendment keeps (no-store Config → plain bytes) is preserved and pinned, so the
supersession narrows D3 rather than overturning it.

## Mutation table — re-applied by this reviewer, observed RED, restored byte-identical (md5-verified)

Baseline manifest of all 8 `internal/audio/*.go` files (`/tmp/t8r4-baseline.md5`)
recorded before the first mutation; shipped md5s: `audio.go`
`6f5f9ea9206a7bee65b59a16479c87bd`, `question_test.go`
`5278a645063e710cb17c2b69fb9f2516`, and `decode.go`
`57d22e7886b066fd56cb8b357c405204` — byte-identical to the round-1 remediation
manifest, confirming this amendment never touched the decode path. `md5sum -c`
reported **OK for all 8 files after every restore and once more at the end**;
`git status --porcelain` at the end is identical to the start snapshot (modified
`audio.go` + `question_test.go`, untracked this record — nothing else). Each
mutant was applied to `audio.go` alone, the target pin(s) run, then the file
restored from the pristine copy.

| # | Mutant | Result | Evidence (red reason) |
|---|---|---|---|
| M1 | Persist made unconditional — the store-state `switch` deleted (`audio.go:443-450`) so a no-store Config also persists | **RED** | `TestSynthesizeQuestion_NoStoreConfigReturnsPlainBytes` + `TestSynthesizeQuestion_DefaultsAndBytes`: nil-pointer panic at `cfg.Blobs.Persist` on the no-store Config (test binary FAIL) — the conditional is what keeps the no-store mode legal |
| M2 | Half-config refusal removed — the two `ErrNoStore` cases deleted (`audio.go:446-449`) | **RED** | `TestSynthesizeQuestion_HalfStoreIsErrNoStore` both subtests: `err = audio: synthesize question: fakeTTS: …` — the call reached the paid TTS seam instead of `errors.Is(.., ErrNoStore)`; the silent half-config would spend money then drop the persist |
| M3 | Persisted id not returned — `return b, id, nil` → id discarded, `""` returned (`audio.go:470`) | **RED** | `TestSynthesizeQuestion_PersistsOnConfiguredStore`: `media id = "", want a persisted id` — the clip persisted but no `/media/<id>` exists to name, which is exactly the URL-contract failure mode |
| M4 | Persist failure swallowed — `id, err := …` error check deleted (`audio.go:466-469`) | **RED** | `TestSynthesizeQuestion_PersistFailureFailsTheCall`: `err = nil, want the persist failure to fail the call` — a clip that never reached the store was handed back as a playable question |

The four target pins also verified **GREEN on the shipped tree** before and after
the mutation cycle, together with the full audio suite (`go test ./internal/audio/
-race -count=5`, five runs green).

## Gates — re-run by this reviewer

| Gate | Result |
|---|---|
| `gofmt -l .` | empty |
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go test ./internal/audio/ -race -count=5` | ok (4.795 s) — no intermittent behaviour in isolation |
| `go test ./internal/audio/ -cover` | **97.8%** of statements (floor 75%); `SynthesizeQuestion` **100.0%** (per `-coverprofile`/`-func`) — the record's coverage claim confirmed |
| `go test ./internal/audio/ -count=1` | ok, green after the mutation cycle |
| `go test ./... -race -count=1` | see the flake note; green in the final runs and in every audio row of every run |
| Tree byte-identity | `md5sum -c` 8/8 OK after every restore and at the end; `git status --porcelain` identical to the start snapshot; nothing outside `internal/audio/**` and this record was created or modified |

## Full-suite `-race` flake note (environmental; not patch-attributable)

Four of the first six amended-tree full-suite runs failed in **untouched**
packages — `internal/interview` (`TestRestartedHandlerSeesEndedInterview`,
`waitTranscriptRole` poll timeout) ×3 and `internal/job`
(`TestProgressAndTerminalExactlyOnce`, poll timeout) ×1; the final two amended
runs were fully green. Attribution evidence gathered by this reviewer:

* Neither flaking package contains any code from the patch: repo-wide grep finds
  **zero importers** of `thutapi/internal/audio`, and `go list -deps` puts
  `internal/audio` **0 times** in the dependency closure of `internal/interview`
  or `internal/job` — Go builds each package's test binary separately, so nothing
  amended executes inside the failing binaries.
* Neither failure reproduces in isolation (`internal/interview` ok, `internal/job`
  ok, single-package `-race` runs).
* The class predates this patch: round 3 recorded identical full-suite flakes on
  clean trees ("internal/interview poll timeout 1× … internal/job poll timeout
  1×"), and today a pristine `main@8b7066b` copy in `/tmp` (no amendment) flaked
  in a *third* different package (`cmd/thutapi`,
  `TestShutdownLogRecordsSignalName`) while passing the rest.
* Full-suite-minus-audio runs on the amended tree were 2/2 green, and pristine
  full-suite runs were 5/6 green — the flake wanders between packages under
  parallel `-race` load on this box and never reproduces alone.

This is the load-sensitive poll-bound class already on record for this machine,
not a defect of the amendment and not in this review's seam (`internal/interview`,
`internal/job`, `cmd/thutapi` are outside the audited delta; the operator owns the
interview seam). Flagged here for the operator's track, not counted against this
verdict.

## Contract-2 obligation — assessment

**The operator's wire-contract-2 obligation for T8 is met by this amendment.** The
URL half is now real: a question clip synthesised on a configured store is
persisted (blob + unplaced media row) and its id serves the exact bytes with the
correct content type over the registered `GET /media/{id}` route (`main.go:149`,
Range/ETag via `http.ServeContent`), pinned end-to-end by
`TestSynthesizeQuestion_PersistsOnConfiguredStore` and red under M3. The
bytes-are-still-returned clause holds (text never waits: the seam stays a pure
function the caller fires in parallel). The conditional-persist clause holds
(no-store Config → plain bytes, round-1 mode preserved and pinned; half-config →
`ErrNoStore` before any paid call). **What still stands is the SSE event half —
the `question_audio` event itself, its `/media/<id>` payload, and the caller that
fires `SynthesizeQuestion` on the events stream — which is the operator's/flow
track's (T9) work, explicitly untouched here** and still owed on top of this
seam, exactly as decision 19 assigns it.

## Zero-residue claim

**Against round 1** (D3 question shape) — zero residue within this round's reach:
the superseding contract is implemented, documented (D5) and pinned (D6); the
round-1 no-store mode the amendment keeps is preserved and pinned; decode.go is
byte-identical to the round-1 manifest (`57d22e78…`), so the L1/L2/C1 closures
carried through rounds 1–3 are undisturbed.

**Against round 2** (H1) — zero residue: the asserted-but-unverified emotion notes,
the committed settlement probe and the 503/open-until-pass record live in files
this amendment did not touch (no delta vs `8b7066b` outside `audio.go` and
`question_test.go`); H1's remaining live-pass obligation is unchanged and stays in
the T8b record.

**Against round 3** — zero residue: round 3 APPROVE'd the round-2 remediation; the
round-3-reviewed code is untouched by this delta except for the two files this
round audits, whose round-3 behaviour (narration path) is unchanged — the
`audio.go` diff is confined to question-path docs/`SynthesizeQuestion` plus one
`NarrateBook` error-message clause, and the narration tests are byte-identical
(md5 manifest, 8/8).

**No new findings** in this amendment.

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**
