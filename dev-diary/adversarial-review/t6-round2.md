# T6 round 2 — adversarial review of the T6 remediation

| | |
|---|---|
| **Target** | T6 remediation, uncommitted working tree on `main` at base `37d7631`. `git status`: 17 modified files — `internal/illustrate/*` (decode.go, illustrate.go, plan.go, prompt.go, types.go + six test files), `internal/gmi/media/*` (client.go, client_test.go, live_test.go, polling.go, polling_test.go — the T2-closed package's sanctioned contract change), `dev-diary/adversarial-review/t6-round1-notes.md` — plus untracked `dev-diary/adversarial-review/t6-remediation-round1.md`. |
| **Evidence** | `AGENTS.md` in full; `PLAN.md` §T6, §T6b, §T7; `adversarial-review/README.md` conventions via AGENTS.md; `t6-round1.md` (H1–H3, M1–M3, L1–L2, the M-a..M-k table, rulings, round-2 exit criteria); `t6b-live-record.md` (the media_urls shape, the echo, seedream-5.0-lite, multi-reference); `t6-remediation-round1.md` (the record under audit, including its process deviation note); the full working-tree diff (captured to `/tmp/t6r2_diff.patch`, 3717 lines); the current state of every changed file read in full. |
| **Method** | Re-run, don't trust prose. A baseline md5 manifest of all eleven `internal/illustrate/*.go` files was taken first. All eleven mutations **M-a..M-k were applied by hand one at a time** against the pristine tree, the package suite run, and the file restored byte-identical from a `/tmp` copy with md5 verified before and after every mutation. Five reviewer-written throwaway probes (`zz_r2probe_test.go`) probed the remediation's own claims (envelope-fails/walk-defers, i2i echo → render, variant-page single-sheet/no-t2i-fallback), ran, then were **deleted**; the tree md5s were re-verified after deletion. The gates were run on the restored tree. No fixes applied, no formatter run, no git mutation, no live GMI call. |
| **Date** | 2026-09-05. **Reviewer: a fresh agent with no prior T6 round and no memory of this remediation's reasoning — the independence of this round is the point.** |

## Verdict

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**

Every finding of round 1 (H1, escalated to Critical by T6b, H2, H3, M1, M2, M3,
L1, L2) is closed, and each was closed by **re-running the pin myself**, not by
crediting the remediation record. The three locks survive the new
URL-array payload shape and the multi-reference i2i change. All eleven
mutations in the round-1 table are red against the remediated tree — M-k
included, red as the M2 exit criterion demands. The sanctioned
`media.EditImage`/`GenerateImage` contract change is a clean cutover with no
stale caller anywhere in the tree. All gates green. The tree is byte-identical
to the state this review started from.

The verdict's boundary is stated at the end: one stale PLAN.md paragraph
contradicting the model decision is recorded there as an orchestrator note,
not a finding, because it predates this patch.

## Gates (run by this reviewer, on the restored tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go test ./... -race -count=1` | ok, all packages (`cmd`, `gmi/media`, `gmi/text`, `illustrate`, `interview`, `job`, `mediastore`, `store`, `story`, `stream`) |
| `go test ./internal/illustrate/ -race -count=5` | `ok  thutapi/internal/illustrate  2.293s` — no race, phase ordering stable |
| `gofmt -l .` | empty |
| coverage `internal/illustrate` | **95.7%** of statements (floor 75%) |
| coverage `internal/gmi/media` | **91.9%** of statements (floor 85%) |
| `go vet -tags live ./internal/illustrate/ ./internal/gmi/media/` | clean (live probes compile against the new signatures) |

Coverage is not the argument — round 1 sat at 100.0% with three H findings in
the package. The decision-level pins below are the argument.

---

## Closure of the round-1 findings (each closed because this reviewer ran the pin)

### H1/C — the echoed request payload beats the result URL — **CLOSED, verified**

**Where:** `internal/illustrate/decode.go` `queueRecord`/`decodeOutcome`/
`hasOutcome`/`sentImages` (decode keyed on `outcome.media_urls`, an array of
objects `{"id","url"}`), against the walk half that excludes what the call
itself sent.

**What the fix now is:** a queue envelope (JSON with a non-null `outcome`)
decodes from that subtree alone, by name; `outcome.thumbnail_image_url` is
never consulted; a missing/empty `media_urls` is a loud `ErrNoImage`, never an
invitation to go walk the payload echo; a `queued` record's `outcome:null` is
"no picture", not a signal to look elsewhere. The walk (schemas GMI never
published) still exists but steps over candidates byte- or URL-equal to what
this call sent.

**Pins I verified:** the echoing fixture lands in the suite in the REAL live
shape. `wire_test.go` `wireServer` echoes the submitted `payload` verbatim
(the prompt on t2i calls, the reference URL array on i2i calls) beside an
`outcome` whose `media_urls` names the render and whose thumbnail names an
unregistered path, and `TestWire_EchoedPayloadNeverBecomesThePage` asserts
every page's decoded bytes are **different from both reference sheets** and
equal to the render (`PAGE`) bytes, with the render URL dereferenced once per
page and the thumbnail zero times. `TestDecodeImage_EnvelopeKeysOnMediaURLs`
does the same at decode level against an echo carrying both the sheet bytes
and the sheet URL. My own probe (an end-to-end `Illustrate` over the real
media client against the echoing wire server) passed: pages decode to the
render bytes, the i2i echo demonstrably carried the reference URL array, the
thumbnail was never fetched. The exit criterion's "page bytes ≠ reference
bytes" assertion — the one round 1 said the suite had never made — now exists
in both fixtures.

### H2 — the variant rule fuses two genuinely different characters — **CLOSED, verified**

**Where:** `internal/illustrate/plan.go` `variantGroup`/
`opensWithBackReference`/`variantNamesTheAnchor`/`samePhraseHead`/
`namesAreAliases`.

**What the fix now is:** fusion requires the visual to OPEN with a
back-reference marker AND identity evidence of exactly two kinds — the
"the same \<head-noun\>" phrase heads with the anchor's own head noun
("the same wide blue river" → river = Grumpy River's kind), or the two names
are aliases of one referent (identical identifying-token sets, species words
removed, possessive names never aliases). The round-1 second-signal half
(`visualTokens[tok]`/`mine[tok]`) is gone from the code; only comments and
tests still mention it.

**Pins I verified:** `plan_test.go` `TestPlanReferences_VariantNeedsIdentityEvidence`
covers the whole corpus in one table: Mum phrased marker-first owns her sheet;
Blue/Green Dragon own separate sheets; Bramble (the dog "the same height as
Mira's knee") owns its sheet; Grumpy+Happy River fuse deterministically onto
the earlier member; "The Sock"/"Sock" fuse by alias.
`TestPlanReferences_NoBackReferenceKeepsSeparateSheets` still passes (Mum's
mid-visual "the same red hair" fuses nothing), and my mutation M-i below
(marker gate + possessive guard removed) reddens exactly those two tests —
so the pin is load-bearing, not prose.

### H3 — `NeedsReferenceSheet` classifies drawable characters as having no appearance — **CLOSED, verified**

**Where:** `internal/illustrate/plan.go` `noAppearancePhrases`/
`absenceSegments`/`noAppearanceExact` and `NeedsReferenceSheet`.

**What the fix now is:** the behaviour changed, not the comment. The
absence phrases are matched only at segment heads (each punctuation-delimited
clause, one optional subordinator and article stripped), and the
ordinary-description fragments (`invisible`, `faceless`, `no face`, `never
seen`, `not seen`) are gone from the phrase list; whole-visual placeholders
("", "-", "n/a", "none", "unknown", …) are handled separately.

**Pins I verified:** both directions live in one table,
`TestNeedsReferenceSheet`: false for both live narrator phrasings, placeholders,
clause-head and whole-visual assertions; true for the ghost with no face, the
faceless rag doll, the snowman never seen without his scarf, the boy in an
invisible cloak — and the old defect-pinning row `{"documented false
positive", "an invisible boy in a blue coat", false}` is **flipped to true**
(line 65). The whole-book pins hold: `TestIllustrate_DrawableCharactersWithAbsenceWordsRender`
produces a three-sheet book for Boo/Dolly/Snowy, and
`TestIllustrate_LiveHazardStoryEndToEnd` still costs the Narrator nothing.
Mutation M-h below reddens the table, both plan tests, the end-to-end story
and the prompt/error pins.

### M1 — the forbidden-model guard is exact-match — **CLOSED, verified**

**Where:** `internal/illustrate/illustrate.go` `IsForbiddenModel`/
`normaliseModelID`/`forbiddenModels`/`forbiddenModelStems`, called in
`Config.resolve` before any request.

**What the fix now is:** the id is normalised (trim, trailing `/`, last path
segment, `:tag` stripped) and compared case-folded; `forbiddenModels` is
{Qwen-Image-2512, Flux2-Klein, Z-Image} — the last two because they accept and
never generate (t6b-live-record.md §1) — and `forbiddenModelStems` {h3,
hailuo, video} implements "H3 / any video model" by family.

**Pins I verified:** `TestIsForbiddenModel` refuses seven decorated Qwen
spellings, H3/H3-2/Hailuo/video stems (bare and decorated), Flux2-Klein and
Z-Image (bare, registry-prefixed, tagged), and admits seedream-5.0-lite and
gemini-2.5-flash-image. `TestIllustrate_ForbiddenModelRefusedBeforeAnyCall`
runs seven ids through full `Illustrate` and asserts **zero calls recorded**.
M-c below reddens exactly those. The "decorated video id" row the remediation
record says the orchestrator added is present. And the model decision is
grep-clean: no Go source anywhere carries "Flux2-Klein" or "Z-Image" as a
default or as an allowed model — `media.defaultImageModel` and
`illustrate.DefaultModel` are both `seedream-5.0-lite`, and
`TestDefaultModelOnRawWire`/`TestGenerateImage_DefaultModelPinnedInRawJSON`
pin that literal on the wire through the empty-model default path. (`Z-Image`
survives only as an *explicit* model argument in T2-era retry/classifier
tests, which is not a default and not an allow-list, and in dev-diary prose.)

### M2 — zero `Book` unpinned on the render paths — **CLOSED, verified (M-k red)**

**Where:** `illustrate_test.go` `assertZeroBook`, asserted in
`TestIllustrate_GMISentinelsSurvive` from-reference-sheet and from-page
subtests and in `TestIllustrate_UndecodableResponses` both call kinds — the
paths that genuinely reach a render failure.

**Pins I verified:** the assertions exist at the four render-failure sites.
My re-run of **M-k** (partial `Book{References: refs, Skipped: plan.Skipped}`
returned beside both render-phase errors) went **RED**: every
`GMISentinelsSurvive/*/from_a_page` subtest (all six sentinels) and
`UndecodableResponses/page` fail in `assertZeroBook`, because there the
mutant really does carry the two completed sheets beside the error. The
from-reference-sheet legs cannot catch this mutant on their fixture — a
reference-phase failure returns nil refs, and with `twoCastStory`'s empty
`Skipped` the mutant's book is still zero there — but the zero-Book assertion
exists on that path too, and the criterion "M-k must go red" is met.

### M3 — one unsupported candidate anywhere fails a render with a usable image — **CLOSED, verified**

**Where:** `internal/illustrate/decode.go` — the walk records `firstErr` and
surfaces it only when nothing usable is found; `decodeOutcome` fails
immediately because the outcome subtree is the answer.

**Pins I verified:** `TestDecodeImage_UnsupportedCandidateIsDeferred` pins all
three directions (gif-then-png in the walk succeeds; dead-URL-then-good
succeeds; a gif at the outcome's `media_urls[0]` fails immediately), and the
single-candidate gif rows of `TestDecodeImage_ErrorBranches` still surface the
deferred error. My own probes confirmed the asymmetry independently:
an envelope whose `media_urls[0]` serves a GIF fails loudly with
`ErrUnsupportedImage` while the identical candidate in a walked body is
stepped past a later PNG, and a valid `media_urls` entry beside an
*unsupported* thumbnail still decodes the render (the thumbnail is never even
consulted).

### L1 — the notes misstate the go.mod edit — **CLOSED, verified**

The notes diff now reads that the require line is **new** — `golang.org/x/sync`
appears nowhere in pre-T6 `go.mod` — with `go.sum` carrying the hashes via
`modernc.org/sqlite`'s graph. Prose only; verified in the diff.

### L2 — `fetchImage` follows a redirect anywhere — **CLOSED, verified**

**Where:** `internal/illustrate/decode.go` `safeRedirectPolicy` +
`maxRedirectHops`, carried by the default client `resolve` builds.

**Pins I verified:** `TestFetchImage_RedirectPolicy` — a same-scheme redirect
chain is cut with exactly `maxRedirectHops+1` fetches (initial plus three
followed redirects, never the target of the refused hop), and a `file:`
redirect is refused. The off-by-one noted in the remediation record checks
out: the policy refuses at `len(via) > maxRedirectHops`, so three redirects
are followed and the fourth refused, matching the doc and the pin.

---

## Mutation re-runs — the full M-a..M-k table, by hand, on the current tree

Method per mutation: restore the file from the pre-review `/tmp` copy → apply
the mutation → `go test ./internal/illustrate/` → restore → md5-compare. The
baseline manifest (all eleven package files) was taken before the first
mutation: `decode.go 312b2f7d…`, `illustrate.go 3585f1da…`, `plan.go
42296b84…`, `prompt.go 0470cca7…`, `types.go 414715e9…`, plus the six test
files (`decode_test 7ca30051…`, `illustrate_test 264f6006…`, `live_test
6287925d…`, `plan_test 33aecc65…`, `prompt_test 4d7298e2…`, `wire_test
5e2ea722…`). **Every file's md5 after every mutation and restore matched the
manifest; nothing in the tree differs from the state this review started in.**

| # | Mutation (as applied to the remediated tree) | Result |
|---|---|---|
| M-a | `DefaultModel = "Z-Image"` | **RED.** Note the semantic shift: Z-Image is now on the forbidden list (the T6b change), so the constant makes `resolve` refuse and the whole suite reddens — including the two pins round 1 named, `TestDefaultModelIsNotForbidden` and `TestDefaultModelOnRawWire`. To verify the round-1 *intent* (a wrong-but-legal default) separately I also ran `DefaultModel = "gemini-2.5-flash-image"`: **RED on exactly `TestDefaultModelIsNotForbidden` + `TestDefaultModelOnRawWire`** — the pin pair round 1 named. |
| M-b | delete `if model == "" { model = DefaultModel }` from `Config.resolve` | **RED** — `TestIllustrate_DefaultModelReachesBothCallKinds` + `TestDefaultModelOnRawWire` + all five raw-wire tests (`TestVisualVerbatimInRawJSON`, `…_AwkwardCharacters`, `TestStyleSuffixVerbatimOnRawWire`, `TestImageLockOnRawWire`, `TestWire_EchoedPayloadNeverBecomesThePage`). The real `media.EditImage` refuses the empty model. **The T2 H1 trap is genuinely closed: default asserted through the default path on the marshalled bytes, both call kinds.** |
| M-c | delete the `IsForbiddenModel` block from `resolve` | **RED** — `TestIllustrate_ForbiddenModelRefusedBeforeAnyCall` (all 7 ids) + `TestIllustrate_ErrorBranches/forbidden_model`, with the zero-calls assertion holding |
| M-d | page rendered with `GenerateImage` instead of `EditImage` (the t2i fallback) | **RED** — 25 subtests incl. `TestIllustrate_NeverCallsGenerateImageForAPage`, `TestImageLockOnRawWire`, `TestIllustrate_ReferencesFinishBeforeAnyPageStarts`, `TestIllustrate_LiveHazardStoryEndToEnd`, `TestWire_EchoedPayloadNeverBecomesThePage`, `GMISentinelsSurvive/…/from_a_page`, `UndecodableResponses/page`. There is no t2i path for a page anywhere |
| M-e | `strings.TrimSpace(m.Visual)` in both prompt builders | **RED** — `TestReferencePrompt_VisualIsVerbatim` (awkward row), `TestPagePrompt_VisualsAreVerbatim`, `TestVisualVerbatimInRawJSON_AwkwardCharacters`. Lock 1 is pinned through the awkward visual on the raw wire |
| M-f | one word changed inside `StyleSuffix` ("thick" → "thin") | **RED** — `TestStyleSuffixWording` (the literal pin; the raw-wire comparisons compare against the constant and self-heal, exactly as round 1 recorded) |
| M-g | `renderReferences` returns without waiting on its errgroup | **RED** — 35 subtests. `TestIllustrate_ReferencesFinishBeforeAnyPageStarts` (the ordering pin) reddens, and the wire tests cascade (page renders read not-yet-written sheet URLs; the real client refuses the empty references). Phase ordering is pinned |
| M-h | `NeedsReferenceSheet` always true | **RED** — `TestNeedsReferenceSheet` (11 false-direction rows: both live narrator phrasings, the placeholders, clause-head and whole-visual assertions), `TestPlanReferences_LiveNarratorGetsNoSheet` (both live phrasings), `TestPlanReferences_SkippedOrderIsCastOrder`, `TestIllustrate_LiveHazardStoryEndToEnd`, `TestIllustrate_ErrorBranches` (2 rows), `TestPagePrompt_NonAppearanceMemberIsLeftOut`, `TestPagePrompt_ErrNoReference` row |
| M-i | variant fusion no longer requires a back-reference (marker gate and `namesAreAliases` possessive guard removed) | **RED** — `TestPlanReferences_NoBackReferenceKeepsSeparateSheets` (Mum fuses onto Mira → 3 sheets instead of 4) and `TestPlanReferences_VariantNeedsIdentityEvidence/mum_phrased_marker-first_is_her_own_character`. The H2 corpus pins are load-bearing |
| M-j | `collectStrings` walks map keys unsorted | **RED** — the straight deletion leaves `"sort"` unused and the package fails to build; with the import removed so the pins actually run: **`TestDecodeImage_IsDeterministic` + `TestCollectStrings`**, the same pair round 1 recorded |
| M-k | return `Book{References: refs, Skipped: plan.Skipped}` beside a render-phase error | **RED — the M2 exit criterion is met.** `TestIllustrate_GMISentinelsSurvive/*/from_a_page` (all 6 sentinels, via `assertZeroBook`) + `TestIllustrate_UndecodableResponses/page`. Round 1's "GREEN — nothing sees it" is gone |

Eleven of eleven mutations reddened the shipped suite; M-k — the one mutation
round 1 found unpinned — is now red. Every mutation was reverted byte-identical
(md5-verified) before the next ran.

## Reviewer probes of the remediation's own claims

Written into throwaway `internal/illustrate/zz_r2probe_test.go`, run, then
deleted; package md5s re-verified identical after deletion.

1. **An envelope whose `media_urls` first entry is an unsupported type fails
   loudly while the walk defers.** `TestR2Probe_EnvelopeUnsupportedFailsLoudly`
   (outcome `media_urls[0]` serves a GIF → `ErrUnsupportedImage`, immediate),
   `TestR2Probe_WalkDefersUnsupportedCandidate` (same candidate under a walked
   key is deferred past a usable PNG), `TestR2Probe_ThumbnailNeverConsultedEvenWhenGif`
   (a valid `media_urls` entry beside a GIF *thumbnail* decodes the render). **3/3 PASS.**
2. **An i2i page response whose echoed payload contains the reference URL
   array decodes to the render.** `TestR2Probe_EchoedRefArrayDecodesToRender` —
   full `Illustrate` over the real `media.Client` against the echoing wire
   server; both pages decode to the render bytes, the i2i echo demonstrably
   carried the URL array, the thumbnail was never fetched. **PASS.**
3. **No t2i fallback for a page naming a variant.** `TestR2Probe_VariantPageLocksToAnchorSheetNoT2iFallback` —
   Happy River renders against Grumpy River's single sheet URL (`edit` refs =
   exactly `[grumpyURL]`), exactly one `GenerateImage` call runs for the whole
   book, the prompt explains the shared sheet ("Grumpy River, who is the same
   character as Happy River") and carries Happy River's own visual verbatim.
   **PASS.**

## The sanctioned contract change — clean cutover, verified by grep

Old forms grepped across the whole tree (Go and prose):
`EditImage(ctx, refImage []byte, …)` — no Go caller anywhere; the old signature
exists only in dev-diary history. `GenerateImage` without `ImageOptions` — no
caller. "Flux2-Klein" or "Z-Image" as a **default** — none in Go source
(`media.defaultImageModel = "seedream-5.0-lite"`, `illustrate.DefaultModel =
"seedream-5.0-lite"`, both pinned on the raw wire through the empty-model
path). "Z-Image" as an **allowed** model — none; it appears only as an
explicit argument in T2-era retry/classifier tests (exercising the client's
retry/classifier, never a default) and in historical prose. Every caller
migrated: `media` client + polling tests, `illustrate` types/interface, code,
wire tests, and both live probes compile (`go vet -tags live` clean). The
payload defaults (`size` `1792x2240`, `output_format` `jpeg`, `max_images` 1,
`watermark` false — the last two with no omitempty so they stay on the wire at
their defaults) are asserted through the default path in
`TestGenerateImage`/`TestEditImage`, and `EditImage`'s new guards (explicit
model or `ErrBadRequest`, non-empty refs, ≤ `MaxReferenceImages` refs) are in
place; its empty-model and empty-refs branches are pinned.

## Multi-reference i2i did not regress the three locks

Lock 2's content half is now multi-URL: `TestImageLockOnRawWire` asserts page 2
carries `[brambleURL, miraURL]` in named order on the raw bytes, and
`TestIllustrate_PagesChainTheirOwnReferenceSheetURLs` asserts the attached URLs
are exactly each page's own sheets'. Lock 1 and lock 3 survive verbatim on the
same wire (M-e, M-f red). The variant prompt text that explains a shared sheet
is pinned (`TestPagePrompt_VariantNamesBothCharacters`, probe 3). Ordering
still holds (M-g red). Mutations M-a..M-k above were run on the new
URL-array shape and all eleven went red.

## Zero-residue claim, severity by severity

- **C:** zero residue. H1/C (the echoed-payload decode, escalated by T6b) is
  closed: the decode keys on `outcome.media_urls[].url` by name, the echoing
  fixtures are in the suite in the live shape, and the page-bytes-≠-sheet-bytes
  assertion exists and passes.
- **H:** zero residue against H1 (as rated), H2 and H3: the echoing fixture
  set, the H2 corpus table, and the H3 both-directions table all exist and
  each reddens its reversion (verified: M-d/M-i/M-h red).
- **M:** zero residue against M1, M2 and M3: the normalised forbidden-model
  guard, the render-path zero-Book assertions (M-k red), and the deferred
  unsupported-type walk (probe-verified) are all pinned.
- **L:** zero residue against L1 and L2: the notes sentence is corrected, and
  the redirect policy is capped and scheme-checked per hop with a pin.
- The complete M-a..M-k table re-run above is the load-bearing evidence: ten
  of eleven mutations redden the same named pins round 1 cited, and M-k — the
  one that was green — is red.

---

## Boundary — what this round did not examine (so the claim above is precise)

- **Anything live.** No GMI call was made and no money spent. The wire shape
  (media_urls array-of-objects, echo, thumbnail) is taken from
  `t6b-live-record.md` and reproduced in fixtures; the live file
  (`live_test.go`, `//go:build live`) compiles but was not run.
- **`internal/gmi/media` internals beyond the sanctioned change.** The
  T2-closed package was reviewed only at the diff hunks: the
  `payload any`/`ImageOptions`/URL-array contract change, its tests and its
  migration. The retry/poll machinery itself was not re-reviewed.
- **Orchestrator note (NOT a finding): PLAN.md §T6 still carries a stale
  paragraph.** Lines 708–712 ("Provider behind a one-line switch. Start on
  **Flux2-Klein** or **Z-Image**…") directly contradict the same section's
  forbidden table (line 669, which forbids exactly those two ids) and the
  shipped guard whose error message cites "PLAN.md §T6's forbidden table" as
  authority. `git blame` shows the paragraph dates from PLAN.md's creation and
  was never updated when T6b flipped the model decision (3c58534) — it
  **predates this patch's base 37d7631**, so under this review's
  patch-introduced criterion it is not a finding and does not count against
  the verdict. It is the kind of doc/behaviour contradiction AGENTS.md makes a
  finding of the next time a track touches §T6's model prose, and the
  orchestrator should delete or rewrite those five lines before T7 greps the
  section.
- **`ImageOptions` override half and `EditImage`'s >14 / whitespace-ref
  guards.** The non-default paths (non-empty Size/Format/Watermark/MaxImages;
  a 15th reference URL; an empty string inside the refs array) are implemented
  correctly but only the all-default path is pinned today. No current defect is
  provable, so no finding; a future track that starts passing non-zero options
  should add one override row to the media wire tests.
- **Whether the prompts work** (the `Done when`) remains T6b items 2–3's
  business, exactly as round 1 ruled.
- Dev-diary prose outside the files cited in the Evidence row.

---

**APPROVE — 0C/0H/0M/0L**
