# T7 round 2 — adversarial re-review of the round-1 remediation

| | |
|---|---|
| **Target** | T7 round-1's three findings (M1, M2, L1) and their remediation in `t7-remediation-round1.md` — the review record I audit. Working tree on `main` at base `44713bc` with the T7 files uncommitted, exactly as round 1 left them: `?? internal/illustrate/{verify,persist}.go`, `?? {verify,persist}_test.go`, `?? t7-round1.md`, `?? t7-remediation-round1.md`, plus `M internal/illustrate/{illustrate,types}.go` (the sanctioned T7 hook edits) and `M dev-diary/PLAN.md` — the last is an **external in-flight T10 sibling edit, out of T7's scope** (PLAN.md §T10 reshape, `t10-video-record.md`); recorded as external, not reviewed, not counted. |
| **Evidence** | `AGENTS.md` in full; `PLAN.md` §T7 (contract sentences: "regenerate on `false`. Cap at 2 retries", the echo blind-spot section, "Persistence lands here": *"they are final as soon as they render, so they persist immediately"*), §Unowned seams "Illustration persistence", §Status; `t7-round1.md` in full (findings, C1–C6 rulings, design decisions 1–5, baseline md5 manifest, mutation table, requirement table); `t7-remediation-round1.md` in full; the production files in full — `verify.go` (280 lines), `persist.go` (118 lines), `illustrate.go` (515 lines, hook regions and all bodies), `types.go` (151 lines); the test files in full — `verify_test.go` (748 lines, all pins old and new), `persist_test.go` (360 lines), plus the T6 helpers the new pins reuse (`fakeImager`, `scriptedImager`, `queueEnvelope`, `fixtureMedia`, `pngBytes`, `twoCastStory`, `assertZeroBook`, `assertSameRenderArgs` in `illustrate_test.go` / `verify_test.go`). |
| **Method** | Fresh-agent re-verification: trust neither the round-1 verdict nor the remediation record — re-read every file and re-run every mutant myself. (1) md5 manifest of all fifteen `internal/illustrate/*.go` files against the round-1 baseline + remediation test-file hashes. (2) All seven mutants re-applied by hand, one at a time, each from a pristine `/tmp` copy with md5-verified restore — M4 (judge error → `continue`), the regen render-failure `continue`, the regen decode-failure `continue`, the `Choices==0` guard deletion, the `Text()` wrap removal, P-A (persist deferred to a post-`Wait` loop — run in **both** the `ctx` and `gctx` shapes), and P-B (report moved above the closing loop). (3) Gates run on the restored tree. (4) M2 pin-shape question answered on the evidence, not the record's assertion. No fixes applied, no git mutation, no live GMI call, no money spent. |
| **Date** | 2026-09-05. Reviewer: fresh agent with no prior T7 round (neither round-1 review nor remediation) — every claim below was re-derived from the code and the re-runs, not taken from either record. |

**APPROVE — 0C/0H/0M/0L.**

Round 1's findings were all PIN-ONLY, and the remediation is exactly that: **no production behaviour changed**. The four production files are byte-identical to the round-1 manifest (`verify.go 8dfa7bea…`, `persist.go efc194ac…`, `illustrate.go 16e72216…`, `types.go 7def1112…`), so the round-1 sanctioned rulings (C1–C6, retry-cap arithmetic, echo-guard semantics, slot replacement, persist-without-judge, no-second-retry) hold by construction, and I re-verified each at the code level anyway. The only changes are the two T7-owned test files (`verify_test.go b3fd3f47… → fa5d9bca…`, `persist_test.go c76f3a20… → 467629e6…`), which add the pins round 1 demanded. I re-applied every named mutant by hand: **each new pin goes RED (or hangs) under its mutant and GREEN on the pristine tree**, and the deferral mutant reddens exactly the one new M2 pin and nothing else in the whole package. The M2 pin-shape deviation from round-1's suggested assertion is correct, honestly recorded, and tests the documented contract — answered in detail below. No new findings; no round-1/round-2 closure is wrong.

## Severity counts

0C / 0H / 0M / 0L

---

## Mutant re-runs — round 2's own table, md5 evidence per restore

Method per mutant (the round-1 reviewer's own, re-run by hand): pristine source copied to `/tmp/t7r2/` and md5'd → mutant applied → the named pin run → restored from the copy → md5-compared. **Every restore matched the round-1 manifest; the tree after all seven mutants is byte-identical to this review's start.** All "pin green after restore" checks re-run and passing.

| # | Mutant (file:line) | Pin | Round-2 result | Restore md5 |
|---|---|---|---|---|
| 1 | **M4** — `judgePage`'s error return in `closePage` → `continue` (`verify.go:246`): a mid-loop judge error regenerates instead of surfacing | `TestIllustrate_JudgeErrorMidLoopSurfacesUnretried` | **RED — hangs.** The scripted judge's `errOn: 2` call never advances `seen` past the erroring call, so every regeneration's verdict errors again and no cap fires (the cap counts false verdicts, not errors). `-timeout 8s`: `FAIL thutapi/internal/illustrate 8.011s` — matching round-1 M4's own 8.009 s and the remediation's 8.010 s evidence. The `return err` path is what bounds the loop | `verify.go 8dfa7bea…` ✓, pin green after restore |
| 2 | Regen **render** error return → `continue` (`verify.go:229`): a failed regeneration `EditImage` regenerates anyway | `TestIllustrate_RegenerationRenderFailureSurfacesUnretried` | **RED** — `verify_test.go:426: err = illustrate: page 1: … still drifted after the regeneration cap …, want errors.Is(.., gmi.ErrTransient)`: the swallowed render error burned the retry budget to `ErrConsistency` instead of surfacing | `verify.go 8dfa7bea…` ✓, pin green after restore |
| 3 | Regen **decode** error return → `continue` (`verify.go:233`): an undecodable regeneration regenerates anyway | `TestIllustrate_RegenerationDecodeFailureSurfacesUnretried` | **RED** — `verify_test.go:453: err = … still drifted after the regeneration cap …, want errors.Is(.., ErrUnsupportedImage)` | `verify.go 8dfa7bea…` ✓, pin green after restore |
| 4 | `len(resp.Choices) == 0` guard deleted (`verify.go:272-274`) | `TestIllustrate_NoChoicesReplyIsABadVerdict` | **RED — panic** (`index out of range [0] with length 0` from inside the page goroutine): the guard is what stops an empty/nil `Choices` from reaching `Choices[0]`; the reply is no longer a loud `ErrBadVerdict` | `verify.go 8dfa7bea…` ✓, pin green after restore |
| 5 | `Message.Text()` error wrap → `return false, nil` (`verify.go:277`): a textless reply reads as a drift verdict | `TestIllustrate_TextlessAssistantReplyIsABadVerdict` | **RED** — `verify_test.go:502: err = … still drifted after the regeneration cap …, want errors.Is(.., ErrBadVerdict)`: the unverdictable reply silently burned three renders instead of failing the run loudly | `verify.go 8dfa7bea…` ✓, pin green after restore |
| 6 | **P-A** — sheet persist hooks moved out of the fan-out goroutine into a post-`Wait` loop over the completed refs (`renderReferences`), **`ctx` shape** (the shape whose evidence the remediation recorded) | `TestPersist_ReferenceSheetSurvivesSiblingStoreFailure` | **RED** — `persist_test.go:343: member 2's render saw member 1's sheet placed 0 times, want 1 — the sheet must persist inside member 1's goroutine, before the phase can fail`. The failure is the non-fatal `t.Errorf`, and the test then ran the end-state assertions (Mira placed with the right bytes, Bramble `ErrNotFound`, run already matched `store.ErrInvalid` at line 334) with **no further error** — empirically confirming the record's central claim: a deferred persist is indistinguishable from immediacy by end state alone | `illustrate.go 16e72216…` ✓, pin green after restore |
| 6′ | **P-A**, `gctx` shape (mechanical move keeping the goroutine's context) | same pin | **RED differently** — errgroup cancels its derived context the moment `Wait` returns, so the deferred persist loop ran on a cancelled `gctx` and the run failed at the *first* persist with `persist_test.go:334: err = … persist reference sheet for "Mira": … context canceled, want errors.Is(.., store.ErrInvalid)`. Also red — the pin is robust to either deferral shape | *(variant of mutant 6; same restore)* |
| 7 | **P-B** — the page `Progress` report moved above the `closePage` hook (`renderPages`) | `TestIllustrate_DoneCountsFinalPagesNotRawRenders` | **RED** — `verify_test.go:598: verdict 2 saw 2 Progress events, want 1` (verdict 1 likewise): the not-yet-final page's event (Done 2 of 2) exists before the closing loop approved it | `illustrate.go 16e72216…` ✓, pin green after restore |

Also re-verified on the pristine tree, as the remediation claimed: under the ctx-shaped P-A the **whole package suite is green minus the one new M2 pin** (`go test ./internal/illustrate/ -skip '^TestPersist_ReferenceSheetSurvivesSiblingStoreFailure$'` → ok) — the deferral mutant reddens exactly the pin built for it and nothing else. All seven pins plus the two spot pins (`TestIllustrate_RetryCapExhaustedErrorsTheRun`, `TestJudgeRequest_RefsAndPageAsDataURIsOnRawWire`) re-run green, verbatim, on the restored tree.

## Gates (round 2, on the restored tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go test ./... -race -count=1` | ok, all packages (`cmd`, `gmi/media`, `gmi/text`, `illustrate`, `interview`, `job`, `mediastore`, `store`, `story`, `stream`) |
| `go test ./internal/illustrate/ -race -count=5` | `ok thutapi/internal/illustrate 3.453s` — no race, ordering stable (restored tree, final run) |
| `gofmt -l .` | empty |
| coverage `internal/illustrate` | **95.5%** of statements (floor 75%); `closePage` **100.0%**, `judgePage` **100.0%** — round-1 M1's zero-coverage statements (229-230, 233-234, 273-274, 277-278) are exercised |
| `go vet -tags live ./internal/illustrate/ ./internal/gmi/media/` | clean |

## Full md5 manifest (round 2, restored tree)

`decode.go 312b2f7d…`, `decode_test.go 7ca30051…`, `illustrate.go 16e72216…`, `illustrate_test.go 264f6006…`, `live_test.go 9320969d…`, `persist.go efc194ac…`, `persist_test.go 467629e6…`, `plan.go 42296b84…`, `plan_test.go 33aecc65…`, `prompt.go 0470cca7…`, `prompt_test.go 4d7298e2…`, `types.go 7def1112…`, `verify.go 8dfa7bea…`, `verify_test.go fa5d9bca…`, `wire_test.go 5e2ea722…` — every file matches the round-1 baseline except the two test files, which match the remediation record's post-pin hashes. `git status` after all re-runs shows exactly the review-start set (the T7 files, plus the external T10 `PLAN.md` / `t10-video-record.md` entries). Tree byte-identical to start.

## The M2 pin-shape question — answered

**Does `TestPersist_ReferenceSheetSurvivesSiblingStoreFailure` pin the documented contract ("sheets persist immediately, per render, inside the fan-out") or merely an implementation detail?**

**It pins the documented contract.** Findings of fact first, all re-derived this round:

1. **Round-1's suggested assertion does not redden a faithful P-A.** A deferred post-`Wait` persist loop (ctx shape) writes member 1's sheet before it hits member 2's failing `storeReference`, so the end state — `store.ErrInvalid`, zero Book, Mira present — is identical under immediacy and deferral. I confirmed this empirically: under the ctx-shaped P-A the pin fails *only* at the probe (line 343) and the end-state assertions pass. Round-1's red-claim ("the phase aborts before anything is written") was wrong for the deferral shape it itself defined; the remediation's surprise is real and correctly recorded.
2. **The remediation pin's red condition is the probe** — from inside member 2's render (the phase is serialised by `Config.Limit: 1`, so member 1's goroutine, including its `storeReference`, has fully completed before member 2's begins), the real sqlite `CastMedia(book, "Mira")` must already succeed. Deterministic; observes the **store**, not renderer internals — there is no counter on `storeReference`, no hook on `BookWriter`, no peek at goroutine scheduling.
3. **The contract sentences are placement sentences.** PLAN.md §T7: reference sheets "are final as soon as they render, so they persist **immediately**"; the `renderReferences` doc (illustrate.go:382-386): "each sheet is written **as soon as it finishes rendering** — a reference sheet is final the moment it renders, so it persists immediately rather than waiting for the pages"; the `BookWriter` doc (persist.go:16-17): "a reference sheet is final as soon as it renders and persists **immediately**"; and round-1's own seam ruling C4 records that "the *immediacy* of that call is M2". The deferral mutant contradicts each of these sentences on their plain reading, and the probe rejects exactly the class of implementations that defer member 1's write past member 2's render — the class M2 named as unpinned.
4. **The pin is the only possible non-crash pin of this ordering.** The end state cannot distinguish immediate from deferred persist in *any* scenario where the deferred loop gets to run (member 1 is written first either way), so the ordering is observable only mid-run — exactly parallel to the L1 pin, which round 1 and the remediation both treat as a legitimate pin of the documented sentence "The loop is the page's last step before its Progress event, so Done counts pages that are final, not raw renders": under P-B the *final* event sequence is identical to the correct implementation, and only the judge's mid-run view of the event log distinguishes. L1's mid-run observer is the judge callback; M2's is a sibling render reading the store. Both observe a real module-boundary fact at a real execution point.
5. **Precision verified.** Under the ctx-shaped P-A, the whole package suite is green minus the one new pin — the deferral reddens the M2 pin and nothing else. On the pristine tree the pin is green under `-race -count=5`.

Residual observation, not a finding: the pin's scenario fails member 2 at *persist* time, where immediacy and deferral have identical end-state survival, so the crash/no-re-spend consequence the round-1 finding text emphasised is protected *derivatively* (any implementation passing the probe has already written member 1 before the phase can fail in any way) rather than exercised in-scenario. Round-1's suggested assertion had the same limitation and was additionally wrong about deferral; the remediation's documented deviation is an improvement over the suggestion, keeps the review's scenario, sentinel, zero-Book and end-state assertions, and adds the only deterministic probe that separates the two behaviours. **The pin tests the documented contract; the deviation is not a finding.**

## Requirement spot-check — round-1 rulings still hold

All four production files are byte-identical to the round-1 manifest, so every ruling round 1 made by reading is undisturbed; the remediation touched only test files. Re-verified at the code level anyway (nothing round 1 read could have been disturbed, but each was re-read this round):

| Ruling | Where it lives now | Round-2 check |
|---|---|---|
| C1 Config fields / C2 renderer mirrors / C3 resolve copies | `types.go:64-84`; `illustrate.go:263-264`; `illustrate.go:293` | present, nil-guarded, defaults substituted once — zero-value path untouched (T6 suite green unedited) |
| C4 `storeReference` hook in the fan-out | `illustrate.go:412-416` (method `BookWriter.storeReference`, persist.go:45 — round-1's C4 name correction) | present; its immediacy is M2, closed (mutant 6/6′ red) |
| C5 `locked []Reference` refactor + `closePage` after `out[i]`, before `report` | `illustrate.go:451-488` (475-480) | present; ordering pinned by L1's pin (mutant 7 red) |
| Retry-cap arithmetic (2 regenerations, 3 renders, sentinel after 3rd false) | `closePage` `verify.go:224-259`, `maxPageRegenerations` `verify.go:36` | code trace + F→T / F→F→T / F→F→F pins green; cap mutation red in round 1, unregressed |
| Echo guard exact-bytes, before every verdict and every regeneration, never regenerates | `verify.go:238-240` | `ErrDecodeEcho` pin green (0 judge calls, 1 edit) |
| Slot replacement (new blob first, delete occupant, retry) | `persist.go:84-103`, `occupant` 109-118 | `TestPersist_ReRunReplacesTheSlotOccupant` green |
| Persist-without-Judge, decode is the approval | `verify.go:241-243` | `TestPersist_WithoutJudgeTheRenderIsTheApproval` green |
| No second retry layer above the judge; judge transport errors surface unretried | `verify.go:245-247`, `267-271` | first-occurrence pin green; mid-loop pin red on M4 (mutant 1) |
| Judge request raw wire (model literal, refs + page as `data:` URIs, page last) | `judgeRequest` `verify.go:89-103` | `TestJudgeRequest_RefsAndPageAsDataURIsOnRawWire` + `…_SingleRefNumbering` green; shape matches project.md §2b / t6b-live-record.md item 2 |

The two probes the batch suggested "only if unpinned" are both already pinned and green on the current code: the judge-page data-URI ordering (raw-wire test above) and `ErrConsistency` with zero Book under a false-false-false script (`TestIllustrate_RetryCapExhaustedErrorsTheRun`, plus the real-store `TestPersist_SheetsImmediatePagesOnlyAfterApproval`). No throwaway probe was needed.

## Zero-residue claim (severity by severity)

- **C (0):** none claimed in round 1, none found in round 2. Race suite clean (`-race -count=5` and full-tree `-race -count=1`); no demo-breaking or data-integrity path introduced or left open by the remediation — it changed no production code.
- **H (0):** none claimed in round 1, none found in round 2.
- **M (0):** M1 closed — five pins, each red under its named mutant (judge-error mid-loop hangs, regen render/decode failures surface instead of burning the cap, no-choices and textless replies are loud `ErrBadVerdict`), and the formerly zero-coverage statements (verify.go 229-230, 233-234, 273-274, 277-278) are exercised (`closePage`/`judgePage` 100.0%). M2 closed — the M2 pin goes red under both shapes of the deferral mutant and under nothing else in the package. No M residue.
- **L (0):** L1 closed — the L1 pin goes red under P-B (report above the closing loop) and green on the pristine tree. No L residue.

## Record notes

- **Round-1 C6 print discrepancy (no code impact):** `t7-round1.md`'s prose says "six contract rows C1–C6" (lines 5, 138, 293) but its rulings table prints five rows, C1–C5 (lines 142-146); no C6 row exists in the file. This is a completeness defect in the *round-1 record*, not introduced by the remediation patch, and nothing is left unexamined by it: the full diff of T6-owned files (the only files a C-row could govern) is `types.go` Config fields + docs and `illustrate.go` renderer fields / resolve / the two hooks — all read in full this round, all sanctioned content. Recorded for the audit trail; not counted as a finding.
- **What round 2 did not examine:** anything live (no GMI call, no money spent — the judge mechanism rests on the live-verified shapes in t6b-live-record.md item 2 and project.md §2b, as in round 1); the internals of `internal/gmi/*`, `internal/store`, `internal/mediastore` (closed tracks, read only at the surface T7 consumes); and the external in-flight T10 edit (`dev-diary/PLAN.md` §T10 and `dev-diary/adversarial-review/t10-video-record.md`) — untouched by this review and by the remediation, excluded from this round's residue.

## Disposition

APPROVE. All three round-1 findings are closed by pins that genuinely go red under their named mutations (re-run by hand, md5-verified restores, seven mutants plus a shape variant), the M2 pin tests the documented contract rather than an implementation detail, no production behaviour changed in remediation (four source files byte-identical to the round-1 manifest), all gates pass, and there is zero residue at every severity against both prior rounds. T6/T6b behaviour is untouched and the pre-existing suite is green unedited.

**APPROVE — 0C/0H/0M/0L**
