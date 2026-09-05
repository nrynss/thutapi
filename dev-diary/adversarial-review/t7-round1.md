# T7 round 1 — adversarial review of `internal/illustrate` verify/persist

| | |
|---|---|
| **Target** | T7, uncommitted working tree on `main` at base `44713bc`. `git status`: `?? internal/illustrate/verify.go`, `?? internal/illustrate/persist.go`, `?? internal/illustrate/verify_test.go`, `?? internal/illustrate/persist_test.go`, `?? dev-diary/adversarial-review/t7-round1.md`, plus `M internal/illustrate/types.go` and `M internal/illustrate/illustrate.go` (the six contract rows C1–C6 below). Nothing else changed — in particular **no file of the pre-existing T6 suite (decode.go, plan.go, prompt.go, and every `*_test.go` except the two new files) was edited**, verified by `git diff --name-only`. |
| **Evidence** | `AGENTS.md` in full; `PLAN.md` §T7, §T6, §T6b, §Unowned seams row "Illustration persistence", §Architectural invariants 1–9, §Status; `dev-diary/project.md` §2b (M3-as-judge, content-block shape, "cap retries (2 is plenty)"); `t6b-live-record.md` (item 2 — M3 verdicts on inline base64 jpegs, and the byte-identity assertion); `t6-round1.md` H1/M2 (the echo defect the guard exists for; the zero-Book contract; ruling 2's persistence reasoning); `t6-remediation-round1.md`; `t6-round2.md` (closures); the full diff (`/tmp/t7_diff.patch`, 201 lines); every edited and new file read in full; the consumed surfaces read at the surface T7 touches: `internal/gmi/text/client.go` (Message/TextPart/ImageURLRef/Chat semantics), `internal/store/media.go` + `store.go` (schema, `SetMediaPlace` UPDATE semantics, the three partial unique indexes, `classifyConstraint`), `internal/mediastore/mediastore.go` (`Persist`, `Delete` row-then-file), `cmd/thutapi/main.go` `run()` (untouched; no consumer of the new Config fields yet — T10 wiring is a later track). |
| **Method** | Probe, don't trust prose. A baseline md5 manifest of all thirteen `internal/illustrate/*.go` files was taken first. Four source mutations were applied one at a time and reverted from `/tmp` copies with md5 verified after every restore (M1–M4 below). Two reviewer mutations probed ordering claims the suite does not pin (P-A, P-B below); both survived the **whole package suite** and became findings. Gates run on the restored tree. No fixes applied, no formatter left running, no git mutation, no live GMI call and no money spent. Tree md5-identical to the review's start. |
| **Date** | 2026-09-05. Reviewer: fresh agent with no prior T7 round and no memory of the implementation's reasoning — this round's independence is the point. |

| **Method** | Probe, don't trust prose. A baseline md5 manifest of all fifteen `internal/illustrate/*.go` files was taken first. Four source mutations were applied one at a time and reverted from `/tmp` copies with md5 verified after every restore (M1–M4 below). Two reviewer mutations probed ordering claims the suite does not pin (P-A, P-B below); both survived the **whole package suite** and became findings. Gates run on the restored tree. No fixes applied, no formatter left running, no git mutation, no live GMI call and no money spent. Tree md5-identical to the review's start. |

**REMEDIATE — 0 × C, 0 × H, 2 × M, 1 × L.**

The closing loop PLAN.md §T7 asks for is implemented correctly and every load-bearing
behaviour the seam ruling lists is pinned: the drifted page is caught and regenerated
with the same prompt and sheets up to the cap, the echo guard runs before any verdict
and never regenerates (judge call count 0), retry-cap exhaustion is loud
(`ErrConsistency`, zero Book), persistence orders correctly (sheets immediate, pages
post-approval, `ErrConflict` replaced by delete-and-retry), zero-value Config leaves
the pipeline byte-for-byte T6 (no T6 test edited, whole pre-existing suite green), the
judge request is pinned on the raw wire (`MiniMaxAI/MiniMax-M3`, refs + page as
`data:` URIs, page last), every new sentinel is asserted with `errors.Is`, judge
transport errors surface unretried, and all gates pass.

The findings are the three places the *suite* does not hold the implementation to its
own documented contracts — the two mutations that survive the whole suite (P-A: the
"reference sheets persist immediately, per render" ordering is unpinned; P-B: "Done
counts pages that are final, not raw renders" is unpinned) and the closing loop's
never-fire error branches, which AGENTS.md §Testing rule 3 requires pinned and which
carry the no-second-retry property on the regeneration path. None is a wrong value
today; all three are pins that do not exist — the exact class T6 round-1 M2
established, and the class that let T2's wrong model constant and T6's H1 ship behind
green suites.

## Severity counts

0C / 0H / 2M / 1L

---

## Findings

### M1 — the closing loop's never-fire error branches are unpinned, and they carry the no-second-retry property on the regeneration path

**Severity:** M.

**Where:** `internal/illustrate/verify.go:229-234` (regeneration render and decode
failures, wrapped `regenerate: %w`) and `verify.go:273-278` (`judgePage`: a
no-choices reply and a textless assistant message, both → `ErrBadVerdict`).
Statements 229-230, 233-234, 273-274 and 277-278 measure **zero coverage**.

**What:** the new loop's error branches are asserted only on their *first* occurrence.
`TestIllustrate_JudgeTransportErrorSurfacesUnretried` pins the no-retry rule for the
judge error on call one; `TestIllustrate_NoSecondRetryLayer` (T6) pins it for the
initial render. Nothing pins the same rule on the regeneration path — a judge error on
the second call, a regeneration whose `EditImage` fails, or a regeneration whose decode
fails. These are exactly the branches AGENTS.md §Testing rule 3 says to pin
("including the branches you expect never to fire"). The no-retry structure is not
cosmetic here: the retry cap counts *false verdicts*, not errors, so the only thing
standing between the loop and unbounded regeneration is the `return err` path. A
one-line mutation that turns a judge error into a regeneration (`continue` instead of
`return err`) hangs the run forever — evidence below, M4.

**Pin** (reviewer probe, deleted after use): a scripted judge that errors on its second
call after scripting `false, true` — asserting the run surfaces the wrapped `gmi`
sentinel after exactly two renders and two judge calls with a zero Book; a
`fakeImager` whose edit fails on call two (judge `false, true`) — same assertions on
the regeneration render/decode failures; a judge replying with zero choices and one
replying with an empty assistant message — both `ErrBadVerdict` via `errors.Is`.

**Mutation:** M4 below (`judgePage` error → `continue`) turns the transport-error test
into a **hang** (test-level timeout at 8.009 s, unbounded regeneration; the earlier
unguarded run spun past 120 s). Deleting the `len(resp.Choices) == 0` guard or the
`Text()` error wrap would pass the whole suite green, because nothing exercises them.

### M2 — "reference sheets persist immediately, as they render" is unpinned: a deferred end-of-phase persist survives the whole suite

**Severity:** M. (The behaviour is correct today. The pin that protects it does not
exist — the T6 round-1 M2 shape.)

**Where:** `internal/illustrate/illustrate.go:412-416` — the per-member
`storeReference` hook inside the fan-out goroutine, against the contract sentences in
PLAN.md §T7 ("Same for the reference sheets: they are final as soon as they render, so
they persist immediately"), the renderReferences doc (illustrate.go:381-385), the
`Config.Persist` doc (types.go:70-76) and the seam ruling's C4.

**What:** every shipped persist test fails only *after* the whole reference phase or on
a single member, so none can distinguish "persisted per render, inside the goroutine"
from "persisted once, after the phase". The whole point of immediacy — a sheet that has
already rendered (and been paid for) survives a failure or crash later in the phase —
is asserted nowhere. A deferral mutant is green over the entire package suite (P-A
below).

**Pin** (deterministic; suggested): a two-member persist test with `Config.Limit: 1`
(serialises the phase) where the book has a cast row for member 1 only — member 1
renders and persists, member 2's `storeReference` fails with `store.ErrInvalid`, and
the test asserts `CastMedia(book, member1)` is present with the zero Book. Under a
deferred persist the phase aborts before anything is written and the assertion goes
red.

**Mutation:** P-A below (persist hook moved out of the goroutine into a post-`Wait`
loop over the completed refs) — **GREEN across the whole package suite**, which is the
finding.

### L1 — "Done counts pages that are final, not raw renders" is unpinned: reporting before the closing loop survives the whole suite

**Severity:** L. Doc-semantics claim with no consumer yet (nothing wires `Progress`
to a Judge/Persist run), so the regression consequence is cosmetic today — but the
claim is made in code (renderPages doc, illustrate.go:447-450) and the suite cannot
tell the difference.

**Where:** `internal/illustrate/illustrate.go:475-480` — the `r.report` call after the
`closePage` hook, versus the doc sentence "The loop is the page's last step before its
Progress event, so Done counts pages that are final, not raw renders". No test in the
patch wires `Config.Progress` together with `Config.Judge` or `Config.Persist`
(verified by grep), so the T6 progress test
(`TestIllustrate_ProgressReportsEveryRender`) exercises only the zero-value path — the
"didn't regress the T6 semantics" half of the requirement holds and is verified, but
the new semantics half is unpinned.

**Pin** (deterministic; suggested): in a drift test (judge `false, true`), have the
scripted judge record `len(progressEvents)` at each verdict: with the loop before the
report, the judge's first call sees only the sheet's event (Done 1 of 2); a
report-before-loop mutant makes it see the not-yet-final page's event too (Done 2 of
2).

**Mutation:** P-B below (report moved above the closing loop) — **GREEN across the
whole package suite**, which is the finding.

---

## The contract-change rulings the round was asked to make

PLAN.md §T7's `Owns` line is `internal/illustrate/verify.go` and
`internal/illustrate/persist.go` plus the `internal/mediastore` / `internal/store`
writes those make; the seam ruling permits the minimal set of edits to T6-owned files
that strictly hook the new logic in, every one listed as a contract row. Rulings on the
implementer's rows C1–C6:

| # | File (line) | Change | Ruling |
|---|---|---|---|
| C1 | `types.go` Config (:64-87) | Two fields: `Judge Judge`, `Persist *BookWriter`, nil = loop off / persistence off | **Sanctioned.** The seam ruling assigns the judge + persistence handles to Config, the single constructor surface the pipeline reads; the types live in T7-owned files; the zero value keeps every existing `Config{Imager: …}` construction compiling and green unedited (verified: no T6 test edited, whole suite green). |
| C2 | `illustrate.go` renderer (:259-266) | `judge`/`persist` fields mirroring Config | **Sanctioned.** renderer is "a resolved Config"; the T7 methods read the handles off it, so no signature of the fan-out closures changes. |
| C3 | `illustrate.go` resolve (:293) | Copy the two handles | **Sanctioned.** resolve is the single place Config becomes renderer; defaults are substituted there and nowhere else. |
| C4 | `illustrate.go` renderReferences (:412-416) | One guarded hook before `report`: `storeReference` when persist is set | **Sanctioned**, with a record correction: the stub's row names `r.persistReference` / `BookWriter.persistReference`, but the implemented method is `BookWriter.storeReference` (persist.go:45). The hook itself is the minimal insertion PLAN §T7's "final as soon as it renders" requires. The *immediacy* of that call is M2 above. |
| C5 | `illustrate.go` renderPages (:455-482) | (a) `sheetURLs`/`sheetBytes`/`lead` locals → `locked []Reference` (same refs, same order); (b) `closePage` hook after `out[i]` and before `report` | **Sanctioned.** (a) is a shape-preserving refactor — the three old locals are derived functions of `bases[i]`, and `locked` carries exactly the same data plus the `Name`/`ContentType` closePage needs for the verdict request and the echo guard; the zero-value path is byte-identical (T6's raw-wire, image-lock and ordering tests pass unedited, and M-a/M-d/M-g equivalents were not re-needed because those files are untouched). `locked[0]` cannot be empty: `buildPage` refuses a sheetless page with `ErrNoReference` before any render (prompt.go:155-157), so the new `lead := locked[0]` and its doc comment are safe. (b) is the single sanctioned branching point; the loop body lives in verify.go. |
Baseline manifest (full package, fifteen files, md5): `decode.go 312b2f7d…`,

All six rows are **sanctioned**: each is the minimal hook the ruling allows, the real
logic lives in verify.go/persist.go, and no change reaches `decode.go`, `plan.go`,
`prompt.go`, `internal/gmi/*`, `internal/store`, `internal/mediastore` or `cmd/`.

## Design decisions ruled

1. **Retry-cap arithmetic** — 2 regenerations after the initial render, 3 renders
   max, judge up to 3 calls, sentinel after the 3rd false verdict. Code trace:
   attempts 0/1/2 render, judge each; `attempt >= maxPageRegenerations` fires only
   after a false verdict at attempt 2. Matches PLAN.md §T7's "cap at 2 retries".
   Pinned three ways (F→T, F→F→T, F→F→F); cap mutation M2 below reddens both the
   success and the exhaustion pins. **Ruled correct.**
2. **Echo guard is exact-bytes, before every judge call and on regenerated pages,
   never folded into the regenerate path.** Code trace: `echoOfReference` sits at the
   top of every loop iteration (initial render included) and returns `ErrDecodeEcho`
   immediately; the judge is unreachable for an echo (call count 0 pinned) and no
   regeneration happens (edit renders = 1 pinned). Exact bytes are the plan-sanctioned
   cheapest form of its own "negative as well as positive" requirement — PLAN.md §T7
   says a byte/hash comparison catches the exact-echo case "for free and needs no model
   call". The perceptual near-echo gap is disclosed in the stub's decision 2 and is out
   of scope here (it would cost a second model call per page and nothing in the plan
   assigns it). **Ruled correct.**
3. **Slot replacement on re-run** — new blob first, then delete the old occupant,
   then place again. Code trace verified: `storeReference`/`storePage` call
   `blobs.Persist` (file fsynced, then the unplaced row) before `place`; on
   `store.ErrConflict` (an UPDATE into the occupied slot tripping the partial unique
   index, store.go schema) `occupant` reads the current holder, `blobs.Delete` removes
   row-then-file, and `SetMediaPlace` is retried. The old illustration keeps serving
   until the delete; a crash leaves at most an unplaced orphan blob (PLAN §T11's
   sweep) and an empty slot the next run fills — matching the doc claims exactly.
   Within one run no slot can collide (nothing persists before its verdict).
   Pinned by `TestPersist_ReRunReplacesTheSlotOccupant`. **Ruled correct.**
4. **Persist-without-Judge** — `Config.Persist` with nil Judge writes sheets and
   pages with no model call, a successful decode being the approval; the echo guard
   still runs (it is before the `r.judge == nil` break), so an H1 echo is never
   written. Code trace confirms; `TestPersist_WithoutJudgeTheRenderIsTheApproval` pins
   the happy half. **Ruled correct** (the echo-plus-persist-only combination shares the
   same guard statement the judge-path echo test covers).
5. **Judge transport errors are not retried here** — `judgePage` wraps every
   `internal/gmi` sentinel (incl. `ErrTransient`) and `closePage` surfaces it; the
   regeneration loop is verdict-driven only. Pinned by
   `TestIllustrate_JudgeTransportErrorSurfacesUnretried` (one render, one judge call,
   zero Book, not misclassified as a verdict condition). The deeper reason the pin
   matters is M4's evidence: the cap counts false verdicts, not errors, so retrying a
   judge error is an unbounded spin, not a bounded retry. **Ruled correct.**

## Mutation re-runs — by hand, on the pristine tree

Baseline manifest (full package, thirteen files, md5): `decode.go 312b2f7d…`,
`decode_test.go 7ca30051…`, `illustrate.go 16e72216…`, `illustrate_test.go
264f6006…`, `live_test.go 9320969d…`, `persist.go efc194ac…`, `persist_test.go
c76f3a20…`, `plan.go 42296b84…`, `plan_test.go 33aecc65…`, `prompt.go 0470cca7…`,
`prompt_test.go 4d7298e2…`, `types.go 7def1112…`, `verify.go 8dfa7bea…`,
`verify_test.go b3fd3f47…`, `wire_test.go 5e2ea722…`.

Method per mutation: copy the pristine file to `/tmp` → apply → run → restore from the
copy → md5-compare. **Every restore matched the manifest; the tree is byte-identical
to the state this review started from.**

| # | Mutation | Result | Restore md5 |
|---|---|---|---|
| M1 | Echo guard disabled (`echoOfReference` returns `false`) | **RED** — `TestIllustrate_EchoPageIsADecodeDefectNotConsistency`: `err = <nil>, want errors.Is(.., ErrDecodeEcho)`; the echo page is judged `true` and would ship as an approved page | `verify.go 8dfa7bea…` ✓, pin green after restore |
| M2 | `maxPageRegenerations` 2 → 1 | **RED** — `TestIllustrate_SecondRegenerationPasses` (cap fires at the 2nd render: "1 regenerations were all judged inconsistent") and `TestIllustrate_RetryCapExhaustedErrorsTheRun` (`edit renders = 2, want 3`). The 3-render budget and its sentinel are genuinely pinned | `verify.go 8dfa7bea…` ✓ |
| M3 | Page persisted before the verdict (storePage moved into the loop head) | **RED** — `TestPersist_SheetsImmediatePagesOnlyAfterApproval`: `placed rows = 4, want exactly the 2 sheets` — drifted, un-approved renders churned the illustration slot | `verify.go 8dfa7bea…` ✓, persist pins green after restore |
| M4 | Judge error → regenerate (`continue` instead of `return err`) | **HANGS — no cap fires.** The regeneration cap counts false verdicts, not errors, so the loop regenerates forever; the transport-error test timed out at 8.009 s (a first unguarded run spun past 120 s). The error-surfacing structure is what bounds the loop | `verify.go 8dfa7bea…` ✓, pin green after restore |
| P-A | Sheet persist deferred out of the goroutine into a post-`Wait` loop | **GREEN — whole package suite.** The "persist immediately, per render" ordering is unpinned → **M2** | `illustrate.go 16e72216…` ✓ |
| P-B | Page `Progress` report moved above the closing loop | **GREEN — whole package suite.** "Done counts pages that are final, not raw renders" is unpinned → **L1** | `illustrate.go 16e72216…` ✓ |

M1–M4 are the round's load-bearing pins and all four are red (or hang) on their
reversion. P-A and P-B are the findings.

## Gates (run by this reviewer, on the restored tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go test ./... -race -count=1` | ok, all packages (`cmd`, `gmi/media`, `gmi/text`, `illustrate`, `interview`, `job`, `mediastore`, `store`, `story`, `stream`) |
| `go test ./internal/illustrate/ -race -count=5` | `ok thutapi/internal/illustrate 3.209s` — no race, ordering stable |
| `gofmt -l .` | empty |
| coverage `internal/illustrate` | **95.0%** of statements (floor 75%) — and coverage is not the argument; the new files' uncovered statements are exactly M1's branches (persist.go's `Persist`/Delete/retry fault branches and `occupant`'s default arm are additionally uncovered, see below) |
| `go vet -tags live ./internal/illustrate/ ./internal/gmi/media/` | clean (live probes compile against the new types) |

## Requirement-by-requirement check

| T7 requirement | Where implemented | Pin | Verified |
|---|---|---|---|
| Drifted page caught and regenerated with the SAME prompt + sheets, ≤ 2 regenerations (3 renders max, sentinel after 3rd false verdict) | `closePage` (verify.go), regen via `EditImage(ctx, page.Prompt, r.model, refURLs(locked), …)` | `TestIllustrate_DriftedPageIsRegenerated`, `…_SecondRegenerationPasses`, `…_RetryCapExhaustedErrorsTheRun` + `assertSameRenderArgs` (prompt/model/refs identical across attempts, bytes tagged per attempt) | yes — M2 mutation red |
| Echo guard before the judge; fires on byte-identical-to-locked-sheet; NEVER regenerates; judge call count 0 | `echoOfReference` at the top of every loop iteration, `ErrDecodeEcho` returns before `judgePage` | `TestIllustrate_EchoPageIsADecodeDefectNotConsistency` (0 judge calls, exactly 1 edit) | yes — M1 mutation red |
| Retry cap exhaustion is loud (`ErrConsistency`), zero Book | `closePage` cap check | same tests + `assertZeroBook` | yes — M2 red |
| Sheets persist immediately on render; pages only post-approval; `ErrConflict` re-persist replaces the occupant (new blob first, old row+file deleted, retry) | `renderReferences` hook; `closePage` persist-after-approval; `place()` conflict path | `TestPersist_SheetsImmediatePagesOnlyAfterApproval`, `…_PageStoresTheApprovedRender`, `…_ReRunReplacesTheSlotOccupant`, `…_WithoutJudgeTheRenderIsTheApproval`, `…_AnchorValidationFailsLoudly` | post-approval & replacement: yes (M3 red); **per-render immediacy: NO — P-A green → M2** |
| Zero-value Config = byte-for-byte T6; no pre-existing test edited | nil-guarded hooks (`r.judge != nil \|\| r.persist != nil`; `r.persist != nil`) | `TestIllustrate_ZeroValueConfigKeepsTheT6Pipeline`; untouched T6 suite green; `git diff` shows only types.go/illustrate.go among T6 files | yes |
| Judge request raw wire: `MiniMaxAI/MiniMax-M3`, refs + page as `data:` URIs, page last | `judgeRequest` | `TestJudgeRequest_RefsAndPageAsDataURIsOnRawWire` (marshal-then-re-decode, block order, prefix + byte round-trip per block, model literal), `…_SingleRefNumbering` | yes |
| Every new error branch asserts its sentinel with `errors.Is`; no second retry layer above the judge | `ErrDecodeEcho`/`ErrConsistency`/`ErrBadVerdict` + pass-through wraps | `TestParseVerdict` table, transport test (classifies as `gmi.ErrTransient`, never a verdict condition), garbage-judge test, persist sentinel tests | yes on first occurrence; **no on the never-fire branches (judge no-choices, textless reply, regen render/decode failures) → M1** |
| Progress/ordering invariants: page Done counts only after the closing loop; T6 event semantics not regressed | `report` after `closePage`; zero-value path unchanged | T6 `TestIllustrate_ProgressReportsEveryRender` green unedited | T6 semantics: yes; **new semantics: NO — P-B green → L1** |

## Candidates examined and dismissed

- **`place()`'s DB-failure branches** (occupant read error, `blobs.Delete` error,
  retried-place error, `occupant`'s default-kind arm — persist.go:94-101, 116) and
  **`storeReference`/`storePage`'s `blobs.Persist` error branches** (persist.go:49, 65)
  are uncovered. Reaching them needs fault injection into the concrete `*store.DB` /
  `*mediastore.Store` (a store-wide failure), which no seam in this patch exposes —
  mediastore's own fault seam (`newBlob`) does not help from `illustrate`. Pinning them
  would demand a new test-only seam, rigor this patch's consumed packages do not
  themselves carry to that depth. Not findings; M1 does not include them.
- **Echo guard placement vs. a near-echo** — the perceptual near-echo (a page that is
  a *variation* of its sheet rather than its bytes) passes the guard by design;
  disclosed in stub decision 2 and sanctioned by PLAN §T7's "cheapest form" sentence.
- **A judge reply wrapped in markdown fences** would fail `parseVerdict` with
  `ErrBadVerdict` (the instruction demands "JSON only, no prose and no markdown", and
  the live M3 records returned clean JSON). Defensive-only; not a finding.
- **A second concurrent `Illustrate` run over the same book rows** (two live runs
  replacing the same slots) is outside the contract — `place()` serialises through the
  store's single connection and any interleaved conflict fails the run loudly.
- **`TestIllustrate_ProgressReportsEveryRender` semantics under the hooks** — the T6
  test runs a zero-value Config and is untouched; the judge-path event *granularity*
  (no event per regeneration) is a documented semantic of the new loop (renderPages
  doc), not a regression of the T6 event-per-render rule on the path that has no loop.
- **The progress *total* under regeneration** — `Total` counts sheets + pages, one per
  page regardless of regenerations; a successful run still converges to Done == Total.
  Coherent, and unchanged from T6's accounting.

## What this review did **not** examine

Stated so the remediation round's zero-residue claim has a boundary.

- **Anything live.** No GMI call was made and no money spent. The judge mechanism is
  pinned against the live-verified shapes in `t6b-live-record.md` (item 2's byte-identity
  assertion and the M3 verdict records) and project.md §2b, not against a new live call.
- **`internal/gmi/text`, `internal/store`, `internal/mediastore`** — read at the
  surface T7 consumes (message/content-block wire shapes, `SetMediaPlace` UPDATE +
  partial-unique-index conflict semantics, `Persist`/`Delete` ordering, content-type
  set). Their internals were not re-reviewed; all three are closed tracks.
- **`cmd/thutapi/main.go`** — verified untouched (`git status`) and read to confirm no
  consumer of `Config.Judge`/`Config.Persist` exists yet; wiring the handles into a
  real run is a later track (T10) and is out of T7's `Owns`.
- **Near-echo perceptual drift** — out of scope per decision 2 above.
- **Dev-diary prose** outside the sections cited in the Evidence row.

## Disposition

REMEDIATE. The three findings (M1, M2, L1) each have a deterministic pin and a
mutation that goes red once the pin exists; none requires a behaviour change. The
six contract rows C1–C6 are sanctioned and should land as recorded (with C4's method
name corrected to `storeReference`). No prior round has residue against this patch:
T6/T6b behaviour is untouched and the pre-existing suite is green unedited.

**REMEDIATE — 0C/0H/2M/1L**
