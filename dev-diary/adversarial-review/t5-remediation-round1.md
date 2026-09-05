# T5 round 1 — remediation

| | |
|---|---|
| **Target** | All 5 findings (0 × C, 0 × H, 3 × M, 2 × L) in `dev-diary/adversarial-review/t5-round1.md`. Verdict was REMEDIATE (0C/0H/3M/2L). |
| **Date** | 2026-09-05 |
| **Commit** | Uncommitted at remediation time; the working tree stays uncommitted — the orchestrator commits on APPROVE (AGENTS.md §Process step 5). |
| **Round file** | `dev-diary/adversarial-review/t5-round1.md` is unchanged. PLAN.md touched only where M2 sanctions it (§T5 Done-when amendment + the `T5b` Status row, mirroring the T2/T2b precedent); no other PLAN section touched. |
| **Verification methodology** | Every fix landed first, then the story suite re-run green before the mutation pass. Each of the five reviewer-prescribed mutations applied by hand, observed RED for the recorded reason, tree restored byte-identical (sha256 against a pre-mutation snapshot; all four mutated files verified identical post-revert), pin green again. Reviewer probes converted into permanent pins and re-run green under `-race -count=1`. Gate outputs in the aggregate section below; full suite green twice back-to-back as the flake check. Everything ran locally against scripted fakes and `httptest`; no live GMI or network calls. |
| **Scope note** | `internal/story/**` and this file, plus the M2-sanctioned PLAN.md edits. `internal/gmi/*`, `internal/store`, `internal/interview`, `cmd/` untouched; no main.go wiring exists (T5 owns the library only — the Phase-B call site is a later track's). |

## Rows

### M1 — The transport-error test asserts nothing: its errors.Is check has an empty body

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/story/structure_test.go` — `TestStructureTransportErrorNotRetried` (now :331-356), body restored per the reviewer's letter; `internal/story/structure_test.go` — the adopted probe `TestStructureTransportSentinelsPreserved` (now :358-377). |
| **What was done** | The hollow `if !errors.Is(err, gmi.ErrRateLimited) { }` guard is gone; the test now **fails** when the sentinel does not survive (`t.Fatalf` naming the untouched-surfaces contract) and additionally rejects a non-zero Story travelling alongside the error (a caller branching on the sentinel must never see a book with it), keeping the not-retried (1 call) and never-`ErrInvalidStory` assertions. The reviewer's probe was adopted as the permanent `TestStructureTransportSentinelsPreserved`: a subtest per transport sentinel (`ErrBadRequest`, `ErrPaymentRequired`, `ErrTransient`, `ErrUnauthorized`, `ErrRateLimited`, `ErrModelNotFound`), each asserting `errors.Is` through `Structure`'s `%w` wrap and never `ErrInvalidStory` — the classification seam T6/T8 branch on is now pinned for every sentinel, not just 429. The review's classification contract ("the text client's sentinels") is unchanged in production code, which was already correct. |
| **Test added** | `TestStructureTransportSentinelsPreserved` (the probe, suite-ised over all six sentinels); `TestStructureTransportErrorNotRetried` re-pointed at real assertions. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below): the swallow mutant turns the shipped test's `Fatalf` red and all six probe subtests red with `= <nil>, want the sentinel preserved untouched`. |

### M2 — The Done when's "twice running" is only pinned against a scripted reply; the live-model half has no owner

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `dev-diary/PLAN.md` §T5 Done-when (now :524-534); Status table `T5b` row (now :74, between T5 and T6, mirroring T2b's placement after T2). |
| **What was done** | The T2 round-3 amendment's structure, applied to T5: the Done-when's **scripted half is the mechanically-checkable criterion** — the same transcript through the extract-and-validate pipeline yields the same valid story twice, with the corrective retry bounded, pinned by `TestStructureDeterministicTwiceRunning` by name. The **live half is owned by a new `T5b` operator track** on the T2b precedent: two real M3 calls with `thinking` ON over a real transcript, each returning schema-valid JSON (8 pages, taught emotion vocabulary, consistent cast names), plus the corrective-system-message acceptance (MiniMax accepts a `system`-role corrective message mid-conversation) — recorded as the half no agent on this workstation can run (the operator key is rejected by both providers), running once a working key exists. No prose-only live claim remains in the Done when. |
| **Test added** | None — the round's own pin is the plan record itself: "Reviewer inspection of the Done when text vs the plan's track table". |
| **Pin re-run status** | The Done-when amendment and the `T5b` row verified by inspection after restore: §T5 names T5b as the live half's owner and the Status table carries the row. Mutation-checked by the reviewer's own prescription (see table below). |

### M3 — The system prompt demands "Exactly 8 pages"; nothing enforces or pins any count

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/story/story.go` — new `PageCount = 8` (now :72-77), the count contract's single value source in the Emotions style; `internal/story/structure.go` — the prompt's page rule (now :35) interpolates `%[1]d` from `PageCount`, and its doc comment names both interpolations (now :26-29); `internal/story/validate.go` — `Validate` enforces `len(s.Pages) != PageCount` (now :56-58, replacing the old ascending-only-accepted `len == 0` guard) and its doc states the number and the cut note (now :15-28). |
| **What was done** | The count contract is landed in one place: **`PageCount = 8`**, taught by the prompt and enforced by the validator from that one constant, so they cannot drift — the same single-source pattern the package already uses for `Emotions` (the reviewer's "either enforce the prompt's contract or document the chosen count … in one place, with a test", first horn chosen). The failure reason names the number both ways: `story has %d pages, want exactly %d`. The validator's doc comment states "exactly PageCount (8) pages" and that **the two-day cut to 6 pages would change exactly this rule** (and nothing else — the constant is the whole edit). The prompt text is reconciled to match: it still says "Exactly 8 pages numbered n = 1 to 8 in order", now rendered from the same constant. A model returning 3 or 6 pages now fails loudly into the corrective retry instead of passing silently toward §T6's "eight pages render". |
| **Test added** | `TestValidatePageCountContract` (now validate_test.go :243-267) — the premise check (`systemPrompt` contains "Exactly 8 pages") plus the count pin: 0, 2, 6, 7 and 9 pages all fail with `errors.Is(ErrInvalidStory)` naming `want exactly 8`, and the 8-page book validates clean. Table rows `no pages` / `too few pages` / `too many pages` pin the reason verbatim. Fixtures reconciled to the enforced contract: `validStory()` and `goodReply` are now eight-page books (the two-page fixtures the review probed with were themselves evidence of the unenforced divergence). |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below): disabling the rule turns the premise-plus-count pin and all three count table rows red. |

### L1 — The extractor takes the FIRST well-formed object, not the best; a complete decoy story is taken silently

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/story/extract.go` — `extractStory` (now :19-46) replaces `extractJSON`: the scan walks **all** brace-balanced candidates (`scanObjects`, now :57-73, the old loop with the `json.Valid` gate kept) and the visit applies the decode + `Validate` gates; `internal/story/structure.go` — `attempt` collapses to `return extractStory(reply)` (now :86-105, `encoding/json` import dropped with the moved decode). |
| **What was done** | The reviewer's letter, verbatim: **the first candidate that decodes AND validates wins; the fallback for error messages is the first candidate.** A well-formed but invalid decoy sitting in front of the real book is skipped — it can no longer win extraction or burn the corrective retry on its misleading reason. When no candidate survives, the error names the **first candidate's** failure — the model's primary attempt: a decode failure wraps `ErrInvalidStory` as `reply JSON did not decode as a story: …`, a validation failure carries `Validate`'s precise rule; a reply with no well-formed object at all keeps its own `reply contained no JSON object` failure. The doc comment states the selection contract and the fallback (the old doc honestly said "first" — the new one says "first that decodes and validates"). Both reviewer probes are permanent pins: the tail-tier silent take now loses end-to-end, the common-tier prose decoy no longer burns the retry. |
| **Test added** | `TestStructureFullStoryDecoyNotTaken` (structure_test.go :414-432) — the `TestProbeStructureFullStoryDecoyTakenSilently` probe inverted: a complete, schema-shaped story that fails only the page-count rule sits in the prose **before** the real book; `Structure` returns the real book in one call with no retry. `TestStructureProseDecoyDoesNotBurnRetry` (:434-448) — the common tier: `You asked for a book like {"title":"x"} — here it is: …` returns the real book on the **first** call. `TestExtractStoryFirstValidBookWins` (extract_test.go :109-121) — the unit-level inversion of `TestProbeExtractFirstObjectWinsOverBest`. `TestExtractStoryFallbackNamesFirstCandidate` (:123-137) — with two failing candidates, the error names the **first** one's reason (`cast is empty`), not the later decode failure. `TestExtractStoryLenientWrapping` / `TestExtractStoryDecodeFailureNamesDecode` / `TestExtractStoryNoObject` carry the old decode/no-object corpus onto the new surface; `TestScanObjects` (:24-63) keeps the leniency corpus at the scan level, with the nested-braces row updated for the honest all-candidates walk (inner objects are visited too, in order). |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below): re-pointing selection at the first well-formed candidate returns `The Decoy Book` end-to-end and `"x"` at the unit level — exactly the silent takes the round recorded. Note the tail-tier pin deliberately leans on M3's count rule (the decoy is the review-era "valid" two-page book), which is why the round's own exit criteria tie the two fixes: with M3 in place the decoy fails `Validate` and loses its slot. |

### L2 — Cast names differing only by case pass the uniqueness check

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/story/validate.go` — the cast loop (now :36-55): the uniqueness scan is `strings.EqualFold`-based over the cast in order; the page/line reference lookups use the unchanged exact-spelling map (now :80-88). Doc comment states both halves (now :15-23). |
| **What was done** | The uniqueness key is case-insensitive with `strings.EqualFold` semantics: `Mira` + `mira` (any casing pair) is one character with two spellings and is rejected loudly — T6 can no longer be handed two reference images for one character. The probed-correct asymmetry is preserved and now documented: page **references** stay exact-match, so a drifted-casing reference still fails loudly (`names character "mira", who is not in the cast`). The exact-duplicate reason is preserved verbatim (`cast name "Mira" appears twice` — the existing table row pins it); the case-only pair gets its own precise reason (`cast name "mira" appears twice, differing from "Mira" only by case`). The doc comment states the rule and the reference exact-match so the asymmetry reads as decided, not accidental. |
| **Test added** | `TestValidateCastUniquenessCaseFolded` (validate_test.go :273-288) — both halves: `Mira` + `mira` rejected with the case reason, and a `mira` page reference against an unchanged cast rejected as a cast miss (references unchanged). Table row `cast names differing only by case` pins the failure reason in the rules table. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below): reverting the key to the raw name lets `mira` slip uniqueness and the story surfaces a misleading cast-miss instead — the reviewer's recorded defect. |

## Mutation check (applied by hand, observed red, reverted byte-identical, pin green again)

| Mutation | Pin that goes red |
|---|---|
| M1: `attempt`'s transport branch → `return Story{}, nil` | `TestStructureTransportErrorNotRetried` (`Fatalf`: `Structure(transport failure) = <nil>, want errors.Is(gmi.ErrRateLimited)`) and all six `TestStructureTransportSentinelsPreserved` subtests (`= <nil>, want the sentinel preserved untouched`) |
| M2: `T5b` Status row deleted from PLAN.md | `grep -cF '\| **T5b** \|' dev-diary/PLAN.md` → 0 while §T5's Done-when still references T5b (the live half is unowned again — the reviewer's own prescription); restored, row present |
| M3: `if len(s.Pages) != PageCount` → `if len(s.Pages) < 0` | `TestValidatePageCountContract` (`Validate(0/2/6/7/9 pages) = <nil>`) and table rows `no_pages` / `too_few_pages` / `too_many_pages` |
| L1: selection re-pointed at the first well-formed candidate (gates dropped, `return` after `json.Unmarshal` into the story) | `TestStructureFullStoryDecoyNotTaken` (`Title = "The Decoy Book"`) and `TestStructureProseDecoyDoesNotBurnRetry` / `TestExtractStoryFirstValidBookWins` (`Title = "x"`) — the silent takes return |
| L2: `strings.EqualFold(seen, m.Name)` → `seen == m.Name` | `TestValidateCastUniquenessCaseFolded` and `TestValidateRules/cast_names_differing_only_by_case` (the case-drifted pair slips uniqueness; the failure surfaces as the wrong rule) |

Every mutation was reverted from the pre-mutation snapshot (`/tmp/t5-pre-mutation/`) and sha256-verified byte-identical: `structure.go` e12500b3…, `validate.go` 792aabb2…, `extract.go` 87724d90…, `PLAN.md` 3a4d850f…; each affected pin re-ran green after the revert.

## Residue against round 1

**Zero.** Each of the five findings has a row above following its mutation's letter, with the reviewer-sanctioned choices stated in place: (1) M2's fix is the exact pair the round named first — a `T5b` Status row **and** the §T5 Done-when amendment mirroring T2's round-3 structure; (2) M3 enforces the prompt's existing 8 rather than re-documenting 6, with the count as one constant both artifacts read (`PageCount`), and the fixtures moved to the enforced contract; (3) L1's extractor follows the round's "first that decodes **and validates**" with the first-candidate fallback, and the two fixtures the review had used as *evidence* of the unenforced divergence (`validStory()`, `goodReply`) became eight-page books so the suite cannot silently re-absorb the old divergence. The round's non-finding note (`storiesEqual` line-count asymmetry, "not a finding, noted for the remediator's tidiness") was left as-is: it is a non-load-bearing test helper explicitly recorded as a non-finding, and remediation does not fix things nobody found.

## Aggregate verification

```
$ go vet ./...                       clean
$ gofmt -l .                         empty
$ CGO_ENABLED=0 go build ./...       ok
$ go test ./... -race -count=1       ok — all packages, twice back-to-back (flake check)
$ go test ./... -cover
story          100.0%  (floor 75%; was 100.0%)
interview       93.4%  (floor 75%)
cmd/thutapi     84.8%  (floor 75%)
gmi/media       92.5%  (floor 85%)
gmi/text        90.4%  (floor 85%)
job             95.7%  (floor 75%)
mediastore      95.6%  (floor 75%)
store           86.9%  (floor 75%)
stream          94.4%  (floor 75%)
```

Done-when tests re-run by name, green under `-race`: `TestStructureDeterministicTwiceRunning` (twice-valid), `TestStructureRetriesOnceWithCorrection` (malformed-then-valid), `TestStructureTwiceMalformedFailsLoudly` (twice-malformed), `TestStructureThinkingOnRawWire` (the thinking pin, both wire calls). New pins re-run green by name: `TestStructureTransportErrorNotRetried`, `TestStructureTransportSentinelsPreserved`, `TestStructureFullStoryDecoyNotTaken`, `TestStructureProseDecoyDoesNotBurnRetry`, `TestExtractStoryFirstValidBookWins`, `TestExtractStoryFallbackNamesFirstCandidate`, `TestValidatePageCountContract`, `TestValidateCastUniquenessCaseFolded`.

## Files touched

* `internal/story/story.go` — `PageCount` constant with doc (M3)
* `internal/story/structure.go` — prompt interpolates `PageCount` (M3); `attempt` via `extractStory`, `encoding/json` import dropped (L1); Structure/attempt docs updated
* `internal/story/extract.go` — `extractStory` selection + first-candidate fallback; `scanObjects` iterator; `objectEnd` unchanged (L1)
* `internal/story/validate.go` — exactly-`PageCount` rule + doc cut note (M3); `EqualFold` uniqueness with exact-match references + doc (L2)
* `internal/story/structure_test.go` — 8-page `goodReply`; transport test real assertions + sentinel probe (M1); decoy pins + `decoyBook` (L1)
* `internal/story/validate_test.go` — 8-page `validStory()`; count + case-drift table rows; `TestValidatePageCountContract` (M3), `TestValidateCastUniquenessCaseFolded` (L2)
* `internal/story/extract_test.go` — corpus moved onto `scanObjects`; `extractStory` selection/fallback/leniency tests (L1)
* `dev-diary/PLAN.md` — §T5 Done-when amendment + `T5b` Status row, exactly per M2 (M2)
* `dev-diary/adversarial-review/t5-remediation-round1.md` — this file
