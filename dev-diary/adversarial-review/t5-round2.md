# T5 round 2 — adversarial review

- **Tree:** uncommitted work on `main`, base `eca7049`. Diff unchanged in shape from round 1's
  scope: `internal/story/**` (8 files) plus the M2-sanctioned `PLAN.md` edits (§T5 Done-when
  amendment + `T5b` Status row) and this review trail. No `main.go` wiring exists (§T5 `Owns` is
  the library only).
- **Date:** 2026-09-05. Reviewer: T5Review2 (fresh agent; not the implementer, not the round-1
  reviewer).
- **Evidence:** AGENTS.md; the review loop README (per-finding schema); `t5-round1.md` (all five
  findings, exit criteria); `t5-remediation-round1.md` (incl. its stated deviations: M2 lands the
  T2-precedent pair, M3 chooses the enforce-the-prompt's-8 horn, L1's tail pin leans on M3's count
  rule); the `git diff` on PLAN.md (verified M2-scoped only); all of `internal/story/**` read in
  full; PLAN.md §T5 + the `T5b` row.
- **Method:** probe, don't trust prose. The five remediation pins plus the core round-1 pins were
  re-run by name under `-race -count=1` with recorded output (all GREEN — table below). A
  8-probe throwaway (`internal/story/zz_probe_test.go`, `-race`, scripted fakes, no network) then
  exercised the new corners: error-message precision of the L1 fallback when candidates decode but
  fail validation (first-candidate reason, in both decode-vs-validate and
  validation-vs-validation orders); two complete valid stories in one reply (first wins, one call,
  no retry, deterministic across five extractStory runs); `extractStory` over the
  corrective-retry reply (selection recovers the real book from prose+decoy, exactly one retry,
  correction carries the first candidate's precise reason); transport sentinel on call one not
  masked by a valid scripted reply two; the L2 asymmetry in both directions (reversed case pair
  `mira`+`Mira` rejected with the correct pair named; `mirA` line speaker and `MIRA` page
  character fail loud; a re-spelled cast with matching exact references stays legal); real book
  first / decoy after; nested fragments cannot win selection and the outer book's own failure is
  the named one. Probes were recorded above, then removed. Three source mutations were applied by
  hand, observed red, and reverted sha256-verified byte-identical: (1) the M1 swallow
  (`attempt`'s transport branch → `return Story{}, nil`) — the shipped
  `TestStructureTransportErrorNotRetried` went RED (the round-1 inversion: it passed blind under
  this exact mutant at round 1) along with all six `TestStructureTransportSentinelsPreserved`
  subtests; (2) the L1 first-valid reversion (Validate gate dropped from selection) — all four L1
  pins RED with the recorded silent takes ("The Decoy Book" end-to-end, `"x"` at the unit level);
  (3) the 8-page overfit audit (`PageCount = 8` → `6`) — 13 shipped tests break, every one a
  fixture or verbatim wantIn pin of the taught contract, production untouched beyond the constant
  (audit below). All five gates re-run by this reviewer on the restored tree. No fixes applied; no
  formatters run; no git mutations beyond reads.

## Gates (run for this review, restored tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race -count=1` | ok, all 9 test packages |
| `gofmt -l .` | empty |
| `go test ./... -cover` | story **100.0%**, cmd 84.8%, interview 93.4%, job 95.7%, mediastore 95.6%, store 86.9%, stream 94.4%, gmi/media 92.5%, gmi/text 90.4% — all ≥ floors |
| `CGO_ENABLED=0 go build ./...` | clean |

## Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L

Each round-1 finding is fixed along the reviewer's own letter, each fix is pinned by a permanent
test that this round re-proved load-bearing under its prescribed mutation, and the adversarial
corners the remediation's choices open (L1's best-candidate selection, M3's count contract, L2's
case-fold split) were probed and hold. No new findings.

## Findings

None.

## Pins (re-run by this reviewer under `-race -count=1`, recorded)

| Pin | Asserts | Status on this tree |
| --- | --- | --- |
| `TestStructureTransportErrorNotRetried` (M1) | transport sentinel survives via `errors.Is`, zero Story alongside, never `ErrInvalidStory`, exactly 1 call | GREEN; **RED under the re-applied swallow mutant** (Fatalf naming the untouched-surfaces contract) — the round-1 blind test now pins |
| `TestStructureTransportSentinelsPreserved` (M1) | all six text-client sentinels preserved through the `%w` wrap, never reclassified | GREEN (6 subtests); **all six RED under the same mutant**; reverted sha256-identical (`structure.go` `e12500b3…`), green again |
| `TestValidatePageCountContract` (M3) | `systemPrompt` teaches `Exactly 8 pages`; 0/2/6/7/9 pages fail `errors.Is(ErrInvalidStory)` naming `want exactly 8`; 8 validates clean | GREEN; premise-plus-count pin is the test the PageCount=6 flip turns red (n=6 becomes legal) |
| `TestStructureFullStoryDecoyNotTaken` (L1) | complete story-shaped decoy before the real book is skipped; real book, 1 call, no retry | GREEN; **RED under the re-applied first-valid reversion** (`Title = "The Decoy Book"`); reverted sha256-identical (`extract.go` `87724d90…`), green again |
| `TestStructureProseDecoyDoesNotBurnRetry` (L1) | small prose decoy does not win extraction nor burn the retry | GREEN; **RED under the same mutant** (`Title = "x"`) |
| `TestExtractStoryFirstValidBookWins` (L1) | unit level: first candidate that decodes AND validates wins | GREEN; **RED under the same mutant** (`Title = "x"`) |
| `TestExtractStoryFallbackNamesFirstCandidate` (L1) | when nothing validates, the error names the FIRST candidate's failure, not a later one's | GREEN; RED under the mutant (no fallback exists there) — re-probed green post-revert |
| `TestValidateCastUniquenessCaseFolded` (L2) | `Mira`+`mira` rejected naming the case pair; exact-match references unchanged (`mira` ref fails loud) | GREEN |
| `TestStructureDeterministicTwiceRunning` | Done-when scripted half: same transcript → same valid story twice, deep-equal | GREEN |
| `TestStructureRetriesOnceWithCorrection` | malformed-then-valid: exactly one retry, corrective `system` message carries the precise reason | GREEN |
| `TestStructureTwiceMalformedFailsLoudly` | twice-malformed: `ErrInvalidStory` after exactly 2 calls, naming reason two | GREEN |
| `TestStructureThinkingOnRawWire` | `thinking:{"type":"enabled"}` + full `MiniMaxAI/MiniMax-M3` id verbatim on both wire calls; emotion vocabulary reaches the wire | GREEN |
| Probe: `TestProbeErrorNamesFirstValidationReason` / `…FirstDecodeOverLaterValidation` | fallback precision holds in both failure orders (first candidate's reason wins; later candidates' reasons absent) | GREEN |
| Probe: `TestProbeTwoValidStoriesFirstWinsDeterministically` | two complete valid stories → first wins, 1 call, no retry, deterministic across 5 runs | GREEN |
| Probe: `TestProbeRetryReplyMinedWithSelection` | `extractStory` over the corrective-retry reply recovers the real book from prose+decoy; exactly 2 calls; correction carries the first candidate's reason | GREEN |
| Probe: `TestProbeTransportSentinelStillFirst` | transport error on call one surfaces untouched though reply two would validate | GREEN |
| Probe: `TestProbeCaseFoldCorners` | reversed pair `mira`+`Mira` rejected with the pair named; `mirA` line speaker fails loud; `MIRA` page character fails loud; re-spelled cast with matching exact refs legal | GREEN |
| Probe: `TestProbeRealBookFirstDecoyAfter` / `TestProbeNestedFragmentCannotWin` | selection stops at the first valid book; nested fragments cannot win and the outer book's own failure is the named one | GREEN |
| Mutation 3 (audit): `PageCount = 8` → `6` | see overfit audit below | 13 shipped tests RED, all fixture/wantIn pins; reverted sha256-identical (`story.go` `6ae05956…`) |

## 8-page overfit audit (M3 follow-up: is "the cut = one constant" true?)

Applied `PageCount = 6` by hand and ran the story suite. **13 shipped tests break**:
`TestValidateRules`, `TestValidatePageCountContract`, `TestValidateWrapsSentinel`,
`TestValidateCastUniquenessCaseFolded`, `TestStructureValidReply`,
`TestStructureDeterministicTwiceRunning`, `TestStructureRetriesOnceWithCorrection`,
`TestStructureTwiceMalformedFailsLoudly`, `TestStructureFullStoryDecoyNotTaken`,
`TestStructureProseDecoyDoesNotBurnRetry`, `TestStructureThinkingOnRawWire`,
`TestExtractStoryLenientWrapping`, `TestExtractStoryFirstValidBookWins`.

Judgement: **this is not overfitting — it is the pins working.** Every breakage is a fixture
(`validStory()`, `goodReply`/`badReply` — the latter two are one hand-written literal plus its
derived form) or a verbatim `wantIn` string (8 sites in the validate table: three
`want exactly 8` rows, five `got [1 … 8]` number-list rows) pinning the *taught contract
itself*; `TestValidatePageCountContract`'s loop even self-demonstrates (n=6 flips legal). Zero
production lines change beyond the constant: prompt and validator both read it, so the cut really
is one edit on the production surface, and the red suite names every fixture site to update.
The doc claims ("that cut would change exactly this constant, nothing else" / "exactly this
rule") are production-scoped and hold. Cut cost if the two-day list ever fires: `PageCount`, two
fixtures, eight wantIn strings — all mechanical, all discovered by the red suite.

## Zero residue against round 1

**Zero, severity by severity.**

- **M1 (transport test asserts nothing):** fixed twice over — the hollow `errors.Is` guard is
  replaced by a real `Fatalf` plus zero-Story/never-`ErrInvalidStory`/1-call assertions, and the
  round-1 probe is permanent as `TestStructureTransportSentinelsPreserved` over all six
  sentinels. This round re-applied the exact round-1 swallow mutant: the shipped test now goes
  RED where round 1 recorded it PASS — the load-bearing inversion is demonstrated, revert
  sha256-identical. No residue.
- **M2 (live half unowned):** §T5's Done-when now states the scripted half as the
  mechanically-checkable criterion with `TestStructureDeterministicTwiceRunning` named, and the
  live half (two real M3 calls, thinking ON, corrective-system-message acceptance) is owned by
  the `T5b` Status row on the T2b precedent — verified in the PLAN.md delta, which touches
  nothing else. No residue.
- **M3 (count unenforced/unpinned):** `PageCount = 8` is the single source; the prompt
  interpolates it, `Validate` enforces it from it, the doc comments state the contract and the
  cut note, the pin asserts premise plus 0/2/6/7/9 plus the clean 8, and the fixtures were
  reconciled onto the enforced contract (the round-1 two-page fixtures were themselves evidence
  of the old divergence). Overfit audited above. No residue.
- **L1 (first wins, not best):** selection is first-decode-AND-validate with the first-candidate
  fallback, exactly the round-1 letter; the two silent-take probes are inverted into permanent
  pins; the re-applied first-valid reversion reproduces both recorded silent takes and is
  reverted byte-identical; the new corners the selection opens (fallback precision in both
  failure orders, deterministic two-story tie-break, retry-reply mining, nested fragments) were
  probed and hold. No residue.
- **L2 (case-differing duplicates):** uniqueness is `EqualFold` with the case-pair reason;
  references stay exact-match and documented; the permanent pin covers both halves; this round
  probed the remaining directions (reversed pair order, `mirA` line speaker, `MIRA` page
  character, legal re-spelling with matching refs). No residue.

## Boundary

**Reviewed:** the five fixes end-to-end (M1 transport preservation incl. mutation re-proof; M2's
Done-when amendment + `T5b` row against the PLAN.md delta; M3's count contract incl. the 6-page
cut experiment; L1's selection and fallback incl. the adversarial corners its choice opens; L2's
case-fold split in both directions), plus regression over the whole round-1 surface: the
leniency corpus at scan and story level, the corrective-retry contract (bounded, reason-carrying,
second reply re-extracted and re-validated), degenerate-reply classification, the
`ErrEmptyTranscript` boundary, excerpt/UTF-8 bounds, the thinking-ON raw-wire pin, and the AGENTS
style audit of the touched code (doc comments on every exported identifier, single-source
constants, no `**T`/`map[string]any`, ctx-first, sentinels + `%w` + `errors.Is`, consumer-declared
`Chatter`).

**Not reviewed / out of scope:** live GMI behaviour and the T5b operator track (no network; no
working key — T5b owns it); `internal/gmi/*` and `internal/store` internals beyond the seams T5
consumes (T2/T3 closed); the Phase-B HTTP/job call site (does not exist yet); spend gating and
input-size caps (T11); deploy artifacts (T1).

**T5 can close.** The scripted half of the Done when is pinned and green; the live half is owned
by T5b, which is the plan's recorded path for it. Verdict APPROVE, 0/0/0/0, zero residue against
round 1.
