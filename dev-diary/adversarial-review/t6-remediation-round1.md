# T6 round 1 — remediation

| | |
|---|---|
|**Target**|All 8 findings of t6-round1.md (H1 escalated to Critical by T6b) + the sanctioned media.EditImage contract change.|
|**Date**|2026-09-05|
|**Status**|COMPLETE — every row landed; gates green; M-k re-run red as required. Residue note below.|

## Process deviation (recorded per AGENTS.md, orchestrator-sanctioned)

Five delegated remediation passes (agents 1a–1f) were aborted mid-run when the
session's harness wedged on a phantom pending `ast_edit` preview: every
non-`write` tool — and eventually `write` itself — was rejected at the turn
boundary, with both `xd://resolve` and `xd://reject` reporting nothing pending.
The session was restarted; the working tree survived. The remaining
remediation (test migration, record completion, gates) was finished by the
**orchestrator directly**, with the review independence of round 2 intact: a
fresh reviewer agent, with no prior T6 round and no memory of this
remediation's reasoning, runs next. This deviation is recorded here so the
audit trail shows who fixed what without a remediation round.

The half-remediated tree the cancelled agents left was audited hunk by hunk
against the dispositions before the orchestrator touched tests; the audit of
rows 3–9 (H2–L2) is recorded in the round's review under "Residue" and was
independently confirmed by a read-only audit pass before round 2.

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **H1/C** echoed payload beats result | decode keyed on `outcome.media_urls` — array of objects `{"id","url"}`, take `.url`; `outcome.thumbnail_image_url` never chosen; POST-sync and polled records decode identically; missing/empty `media_urls` → loud sentinel. Echoing fixture in the REAL live shape: t2i variant (payload echoes prompt) + i2i variant (payload echoes `{}`), media_urls array-of-objects, thumbnail present and never picked, page bytes ≠ reference bytes. | **DONE.** `decode.go` `queueRecord`/`outcomeMedia`/`decodeOutcome`/`hasOutcome`/`sentImages` (keyed decode, thumbnail never consulted, loud `ErrNoImage` on missing/empty `media_urls`); walk half excludes `sent` bytes/URLs. Pins: `decode_test.go` `TestDecodeImage_FetchesAURL` (media_urls fetch, thumbnail 404-tripwire hit-count 0), `TestDecodeImage_EnvelopeKeysOnMediaURLs` (echo carries sheet bytes+URL beside outcome; decoded bytes ≠ sheet; render fetched exactly once), `TestDecodeImage_UnsupportedCandidateIsDeferred` outcome subtest, `TestDecodeImage_ErrorBranches` outcome-no-media_urls + empty-list rows, `TestDecodeImage_QueuedRecordHasNoPicture`; wire level `wire_test.go` `TestWire_EchoedPayloadNeverBecomesThePage` (server echoes payload verbatim, media_urls result, thumbnail-fetch count 0, page bytes = PAGE ≠ SHEET). Polled vs POST-sync: identical record shape by construction, covered by the same fixtures. |
| 2 | **Contract change** media image methods | `media.EditImage(ctx, prompt, model, refImages []string, opts ImageOptions)`; both image methods take named `ImageOptions` (Size `1792x2240`, Format `jpeg`, Watermark false, MaxImages 1 — zero value usable, default-path tests, raw-wire pins on the pinned values). `DefaultModel` (illustrate) = seedream-5.0-lite. Clean cutover, every caller migrated. | **DONE.** `client.go` `ImageOptions`/`imagePayload`/`payload` (zero-value-usable defaults, `image` as URL array, no-omitempty `watermark`/`max_images`), `EditImage` validates prompt/model/ref-count(≤14)/empty-ref, `GenerateImage` empty-model default = seedream-5.0-lite. Pins: `client_test.go` `TestGenerateImage`/`TestEditImage` payload-field assertions (size/output_format/max_images/watermark/image array verbatim), `TestGenerateImage_DefaultModelPinnedInRawJSON` (bytes pin seedream-5.0-lite), `TestEditImage_EmptyModelRejected`, `TestEmptyInputs` new-signature rows; every caller migrated (`illustrate.go` refs/sheets as `[]string` URLs, `Reference.URL` chaining). |
| 3 | **H2** variant rule fuses different characters | Fusion only same-entity: "the same \<head-noun\> as \<Anchor\>" / alias; no species tokens; no visualTokens half. Corpus: Mum both phrasings OWN; Blue/Green Dragon OWN; Bramble OWN; Grumpy+Happy River FUSE (deterministic anchor); Narrator non-visual. Pages carry ALL named characters' sheet URLs. | **DONE.** `plan.go` `variantGroup`/`opensWithBackReference`/`variantNamesTheAnchor`/`samePhraseHead`/`namesAreAliases`/`identityTokens`/`speciesWords`; the `visualTokens`/`mine[tok]` second-signal half is gone (grep-clean). Corpus pins in `plan_test.go`: Mum marker-first own sheet (180–187), Mum mid-visual own sheet (`NoBackReferenceKeepsSeparateSheets` 144–161), Blue/Green Dragon separate (188–195), Bramble/Mira separate (196–203), Grumpy+Happy River fuse deterministically (204–212, 111–139), alias The Sock fuses (213–221), every row asserts `Skipped` empty. Pages carry every named character's sheet URLs (`prompt.go` `buildPage` multi-sheet branch; pinned by `illustrate_test.go` `TestIllustrate_PagesChainTheirOwnReferenceSheetURLs`). |
| 4 | **H3** NeedsReferenceSheet false positives | True-absence narrowing; ghost/doll/snowman/boots-boy drawable; Narrator free; defect-pinning table row fixed; both directions one table. | **DONE.** `plan.go` `NeedsReferenceSheet` clause-anchored (`absenceSegments` matched only at segment head 234–240); `noAppearancePhrases` 109–127 drops invisible/faceless/no face/never seen; `noAppearanceExact` whole-visual placeholders. Pins: `plan_test.go` `TestNeedsReferenceSheet` both directions (false: both live narrator phrasings, clause-head, whole-visual; true: ghost/faceless doll/snowman/invisible-cloak boy), the old `{"documented false positive", "an invisible boy in a blue coat", false}` row flipped to true (65); whole-book pins `illustrate_test.go` `TestIllustrate_DrawableCharactersWithAbsenceWordsRender` (Boo/Dolly/Snowy render, no skips) and `TestIllustrate_LiveHazardStoryEndToEnd` (narrator free in both live phrasings). |
| 5 | **M1** forbidden-model guard | Case-folded last path segment + video stems; forbidden gains Flux2-Klein/Z-Image (never-generate); seedream-5.0-lite/gemini-2.5-flash-image allowed; decorated Qwen refused. | **DONE.** `illustrate.go` `IsForbiddenModel` over `normaliseModelID` (trim, trailing `/`, last path segment, strip `:tag`) + case-folded stem containment `forbiddenModelStems` {h3, hailuo, video}; `forbiddenModels` = {Qwen-Image-2512, Flux2-Klein, Z-Image}; refusal in `resolve` pre-call. Pins: `illustrate_test.go` `TestIsForbiddenModel` (decorated Qwen 7 spellings, H3/H3-2/Hailuo, **minimax-video-01 and decorated video id** — the bare "video" stem's refusal row, added by the orchestrator after the hunk audit flagged it unpinned —, Flux2-Klein + registry-prefixed, Z-Image + tagged, allowed gemini-2.5-flash-image and empty), `TestIllustrate_ForbiddenModelRefusedBeforeAnyCall` (7 ids, zero calls), `TestDefaultModelIsNotForbidden`. |
| 6 | **M2** zero-Book unpinned on render paths | Zero-Book assertions in render-failure tests; M-k re-applied → red. | **DONE.** `assertZeroBook` (`illustrate_test.go` 1027–1032) asserted in `TestIllustrate_GMISentinelsSurvive` from-reference-sheet (806) and from-page (816) subtests and `TestIllustrate_UndecodableResponses` both call kinds (1048/1058) — i.e. on paths that genuinely reach a render failure, which is where the mutant hides. **M-k re-run by the orchestrator: mutant applied to both render-phase error returns → RED at 806/816/1048/1058; tree restored md5-identical (`3585f1da…`), suite green.** (The full M-a..M-k table re-run is round 2's exit criterion 5 and belongs to the round-2 reviewer's record.) |
| 7 | **M3** unsupported candidate aborts good render | Deferred unsupported-type; single-candidate GIF still surfaces. | **DONE.** `decode.go` walk records `firstErr` (base64 137–139, URL fetch/type 156–166, non-image 169–171) and surfaces it only when nothing usable is found (176–178); `decodeOutcome` fails immediately on a bad answer (the subtree is the answer). Pins: `decode_test.go` `TestDecodeImage_UnsupportedCandidateIsDeferred` (gif-then-png 338–349, dead-URL-then-good 350–368, outcome-subtree gif fails immediately 369–379) + `TestDecodeImage_ErrorBranches` gif-in-a-data-URI single-candidate row. |
| 8 | **L1** notes misstates go.mod edit | Notes sentence corrected (added, not promoted). | **DONE.** `t6-round1-notes.md` now reads the require line is **new** — `golang.org/x/sync` appears nowhere in pre-T6 `go.mod` (`git show aaf33bc:go.mod | grep -c golang.org/x/sync` = 0) — while `go.sum` already carried the hashes via `modernc.org/sqlite`'s graph. Prose fix only; no behaviour. |
| 9 | **L2** fetch follows redirect anywhere | CheckRedirect scheme allow-list per hop + cap 3. | **DONE.** `decode.go` `safeRedirectPolicy` re-applies the http(s) scheme check on every hop and caps the chain; `fetchImage` uses the default client carrying it. Pin: `decode_test.go` `TestFetchImage_RedirectPolicy` — same-scheme chain cut at the cap with exactly `maxRedirectHops+1` fetches (initial + allowed redirects, never the refused target's) and a `file:` redirect refused. One off-by-one corrected during migration: the policy refused at `len(via) >= maxRedirectHops` (two allowed hops despite the doc's "three is generous"); now `>` — three redirects followed, the fourth refused — matching the pin and the doc. |

## Mutation re-runs

Exit criterion 4's re-run (M-k) is recorded under disposition 6 above: red on
the mutant, tree restored byte-identical, suite green. The complete M-a..M-k
table is re-run by the round-2 reviewer per exit criterion 5; this
remediation's record states which mutations it re-ran and their result, and
the tree is byte-identical to the state round 2 reviews.

## Gates

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go build ./...` (CGO_ENABLED=0 not needed for tests; build clean) | clean |
| `go test ./... -race -count=1` | ok, all packages |
| `go test ./internal/illustrate/ -race -count=5` | ok — no race, ordering stable |
| `gofmt -l .` | empty |
| coverage `internal/illustrate` | **95.7%** of statements (floor 75%) |
| coverage `internal/gmi/media` | **91.9%** of statements (floor 85%) |
| `go vet -tags live ./internal/illustrate/ ./internal/gmi/media/` | clean (live probes compile) |

Coverage is not the argument (the package sat at 100.0% with three H findings
in round 1); the decision-level pins are the dispositions above.

## Residue

* All nine disposition rows are DONE with pins that fail on reversion of the
  fix; the M1 "video" stem row and M-k re-run were added by the orchestrator
  after the hunk audit flagged the gaps (one unpinned stem, one unpinned
  render-path zero-Book contract) — both are now red-on-revert.
* The remediation's own audit of rows 3–9 was confirmed by an independent
  read-only audit pass (fresh agent, no conversation memory) before this
  record was closed: all seven findings FIXED against their dispositions,
  six fully pinned, L1 prose-only by nature, one unpinned stem — now pinned
  above.
* **Zero residue is claimed only after round 2's verdict**: this record's
  dispositions are the round-2 reviewer's input, not its conclusion. Round 2
  must re-run M-a..M-k, re-check the exit criteria of t6-round1.md, and issue
  its own APPROVE with an explicit zero-residue claim against this round.
