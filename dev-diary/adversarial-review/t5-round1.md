# T5 round 1 — adversarial review

- **Tree:** uncommitted work on `main`, base `eca7049` ("T4 closes at round 2"). Diff:
  `internal/story/**` (8 files, all new — story.go, structure.go, extract.go, validate.go and four
  test files). Nothing else in the tree changed; no `main.go` wiring exists yet (§T5 `Owns` is the
  library only — the Phase-B call site is a later track's).
- **Date:** 2026-09-05. Reviewer: T5Review1 (fresh agent; not the implementer, not a prior round's
  reviewer).
- **Evidence:** AGENTS.md; PLAN.md §T5 (schema JSON + Done when), §T6 (consumes `visual`, `prompt`,
  `characters`; "eight pages render"), §T8 (consumes per-page `emotion`; out-of-set silently ignored
  downstream), §T4 (producer of the transcript), §Architectural invariants, §Two-day cut list;
  project.md §Pipeline Phase B, §cast; the review loop README (per-finding schema); all of
  `internal/story/**`; the consumed seams read at the surface T5 touches (`internal/gmi/text`
  shapes: `ChatRequest`/`Reasoning`/`Choice`/`AssistantMessage` union decode + `Text()`,
  `internal/store/interviews.go` `Turn`); the MiniMax Speech 2.8 emotion vocabulary corroborated
  against independent provider docs (Replicate, MuleRouter, APIXO — the taught seven match).
- **Method:** probe, don't trust prose. A 20-probe throwaway (`internal/story/zz_probe_test.go`,
  `-race`, deterministic scripted fakes, no network) exercised the extractor against an adversarial
  corpus (prose-embedded decoy objects incl. a complete valid decoy story, array-wrapped objects,
  CRLF fences and a fence mispair, `\u007d` brace smuggling, escaped-backslash quote handling,
  40-deep nesting, balanced-but-invalid regions), the corrective-retry contract (failure text
  carried, second reply re-extracted and re-validated, bounded loop), the validator corners
  (n=0, case-differing duplicates, out-of-set and mis-cased emotions, empty lines/characters),
  the `ErrEmptyTranscript` boundary, and the excerpt/render bounds. Probes were recorded above,
  then removed. Two source mutations were tested and reverted sha256-verified byte-identical:
  (1) `Thinking: nil` → the raw-wire pin went RED on both calls; (2) swallowing the transport
  error in `attempt` → the shipped `TestStructureTransportErrorNotRetried` stayed GREEN while the
  reviewer probe went RED (finding M1). All five gates re-run by this reviewer on the restored
  tree. No fixes applied; no formatters run; no git mutations.

## Gates (run for this review, restored tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race -count=1` | ok, all packages |
| `gofmt -l .` | empty |
| `go test ./... -cover` | story **100.0%**, cmd 84.8%, all packages ≥ floor |
| `CGO_ENABLED=0 go build ./...` | clean |

## Verdict: REMEDIATE — 0 C / 0 H / 3 M / 2 L

The Phase-B contract is implemented faithfully: the wire shape matches the §T5/project.md JSON
key-for-key (audited), thinking-ON and the prefixed model id are pinned on the raw wire (and the
pin is load-bearing — mutation-tested), the corrective retry is bounded and carries the precise
failure text, the retry's reply goes through the same extract+validate, extraction leniency
survives every adversarial input tried, and the validator's loud-rejection posture matches §T5's
"fail loudly into a retry". But one shipped test pins nothing where its own doc comment names a
contract (proven by mutation), the Done when's live half has no owner, the prompt's only
page-count promise is unenforced and unpinned, and two validator/extractor edges let wrong books
through silently. 100% statement coverage did not catch any of these — they are decision gaps,
the exact T2 lesson.

## Findings

### M1 — The transport-error test asserts nothing: its errors.Is check has an empty body

| | |
| --- | --- |
| **Severity** | M |
| **Where** | `internal/story/structure_test.go:329-330` (`if !errors.Is(err, gmi.ErrRateLimited) {` / `}`), test named at `:323-326` |
| **What** | `TestStructureTransportErrorNotRetried` claims to pin "transport failures … surface untouched" (its own doc comment), but the `errors.Is` guard that is its only preservation assertion has an **empty body**. Mutation-tested: changing `attempt`'s transport branch to `return Story{}, nil` — swallowing every transport failure into a zero Story with a nil error — leaves this test (and the whole suite) GREEN; the reviewer probe `TestProbeTransportErrorPreserved` went RED under the same mutant. The production code is currently correct (probe GREEN on the real tree: the `%w` wrap preserves the sentinel); the defect is that the test pins nothing, so the classification seam T6/T8 will branch on is unprotected against exactly the regression its doc comment names. AGENTS §Testing rule 3: every error branch has a test asserting the sentinel with `errors.Is` — this one exists and doesn't. |
| **Pin** | Mutation demonstration recorded in this review's method section: mutant `return Story{}, nil` in `attempt` → `TestStructureTransportErrorNotRetried` PASS (blind), `TestProbeTransportErrorPreserved` RED. Mechanical inspection: `structure_test.go:329-330`. |
| **Mutation** | Re-delete the assertion body once restored (back to `{ }`) — or re-apply the swallow mutant above — and the suite goes green while transport errors are destroyed; the probe is the load-bearing replacement. |

### M2 — The Done when's "twice running" is only pinned against a scripted reply; the live-model half has no owner

| | |
| --- | --- |
| **Severity** | M |
| **Where** | PLAN.md:524-525 (Done when: "a transcript yields valid JSON that validates against the schema, twice running"); `internal/story/structure_test.go:120-140` (the twice-running pin uses one canned reply through one fake); PLAN.md status table (`T5 | Not started` — no T5b row) |
| **What** | `TestStructureDeterministicTwiceRunning` proves the *pipeline* is deterministic — the same scripted bytes extract and validate twice — which is the only clean-tree-checkable reading. It cannot speak to the claim a judge will actually test: that M3, with thinking ON, returns schema-valid JSON (8 pages, taught emotion vocabulary, consistent cast names) on two real calls, and that MiniMax accepts a `system`-role corrective message mid-conversation. No agent on this workstation can run that probe (the operator key is rejected by both providers — the T2b precedent), and no b-suffixed track owns it, so §T5's Done when as written cannot be closed mechanically. This is the same finding class T2's round 3 recorded ("the raw-bytes/no-live-test `Done when` gap") — and T2 shipped the wrong model constant behind exactly this shape of gap. AGENTS §Testing rule 6: a live-only check goes in a b-suffixed track, not in prose. |
| **Pin** | Reviewer inspection of the Done when text vs the plan's track table (no live-probe owner exists; scripted fake is the only "twice" evidence). The scripted half is GREEN (`TestStructureDeterministicTwiceRunning`); the live half is unrunnable by construction here. |
| **Mutation** | Delete the `T5b` row (or the T2b scope note) once added — the Done when's live half is unowned again and the gap re-opens. |

### M3 — The system prompt demands "Exactly 8 pages"; nothing enforces or pins any count

| | |
| --- | --- |
| **Severity** | M |
| **Where** | `internal/story/structure.go:34` (`- Exactly 8 pages numbered n = 1 to 8 in order…`) vs `internal/story/validate.go:53-59` (enforces only ascending-from-1; doc at `:15-25` claims nothing more) |
| **What** | Two T5 artifacts disagree about a schema rule. The prompt tells the model 8 pages are required; `Validate` — the component §T5 charges with "validate before use" — accepts any ascending-from-1 count, and its doc comment says so, so the divergence is deliberate-looking but recorded nowhere. Probed: `TestProbeValidatePageCountUnenforced` — the test suite's own two-page `validStory()` validates green while `systemPrompt` contains "Exactly 8 pages". Consequence: a model that returns 3 or 6 pages passes silently, and §T6's Done when ("eight pages render") fails downstream with nothing in T5 to catch it — the half-rendered-book class §T5's own "fail loudly" rule exists to prevent. The two-day cut list allows trimming the count to 6, so hardcoding 8 in `Validate` may be wrong — the defect is the unrecorded, unpinned divergence: either enforce the prompt's contract or document the chosen count (and enforce that) in one place, with a test. |
| **Pin** | `TestProbeValidatePageCountUnenforced` — GREEN under the defect (asserts `Validate(two-page book) == nil` while `systemPrompt` says "Exactly 8 pages"). |
| **Mutation** | Remove the count rule from the prompt (or the validator/doc) once reconciled — the probe's premise check (`systemPrompt` contains "Exactly 8 pages") and the chosen-count pin go red again. |

### L1 — The extractor takes the FIRST well-formed object, not the best; a complete decoy story is taken silently

| | |
| --- | --- |
| **Severity** | L |
| **Where** | `internal/story/extract.go:13-28` (first `json.Valid` candidate returned at `:22-25`; doc comment at `:5-12` honestly says "first") |
| **What** | Probed both tiers. Common tier: a prose-embedded decoy object before the real book (`You asked for a book like {"title":"x"} — here it is: {book}`) is returned verbatim; it decodes, fails `Validate` ("cast is empty"), and burns the single corrective retry on a misleading reason while the real book sat in the same reply. Tail tier: a decoy that is itself a **complete valid story** in the prose is taken end-to-end with a nil error and one call — `TestProbeStructureFullStoryDecoyTakenSilently` shows `Structure` returning `The Decoy Book` as the title, no retry, no error. The leniency contract exists precisely because the model wraps JSON in prose, and prose can contain JSON; scanning all brace-balanced candidates and returning the first that decodes **and validates** (falling back to the first candidate for the error message) is a small, discrete fix that keeps the leniency and removes both failure modes. |
| **Pin** | `TestProbeStructureFullStoryDecoyTakenSilently` (end-to-end silent take) and `TestProbeExtractFirstObjectWinsOverBest` (first-wins at the unit level) — both GREEN under the defect. |
| **Mutation** | Re-point the selection at `return cand, true` for the first `json.Valid` candidate (drop the validate-all-candidates preference) — the silent-take probe goes red again. |

### L2 — Cast names differing only by case pass the uniqueness check

| | |
| --- | --- |
| **Severity** | L |
| **Where** | `internal/story/validate.go:38-41` (case-sensitive `names` map) |
| **What** | Probed: a cast of `Mira` + `mira` with consistently-cased page references validates clean (`TestProbeValidateCaseDifferingDuplicateCastNames`, GREEN under the defect). The uniqueness rule that keeps one visual per character (§T6's whole consistency lock, and §T8's voice per member) is defeated by a casing drift *within the cast array itself* — T6 would render two reference images for one character and split the lock. Note the asymmetry the map also produces: a page *reference* whose casing drifts from the cast name fails loudly (`TestProbeValidateCaseDifferingLineSpeakerFailsLoudly` — correct, per the fail-loud spec), so only the duplicate-pair edge slips. Requires the model to case-drift inside the cast array, hence L. A case-insensitive uniqueness check (or lowercased key in the `names` map, keeping references exact) is the discrete fix. |
| **Pin** | `TestProbeValidateCaseDifferingDuplicateCastNames` — GREEN under the defect (asserts `Validate(Mira + mira with consistent refs) == nil`). |
| **Mutation** | Revert the uniqueness key to the raw name — the case-duplicate probe goes red again. |

## Pins

| Pin | Asserts | Status on this tree |
| --- | --- | --- |
| `TestProbeTransportErrorPreserved` | a transport sentinel survives `Structure` intact (`errors.Is`) | GREEN on real code; **RED under the M1 swallow mutant** — the assertion the shipped test forgot |
| `TestProbeValidatePageCountUnenforced` | prompt's 8-page rule is unenforced by `Validate` | GREEN (defect demonstrated) |
| `TestProbeStructureFullStoryDecoyTakenSilently` | a complete valid decoy story in prose is returned, nil error, no retry | GREEN (defect demonstrated) |
| `TestProbeExtractFirstObjectWinsOverBest` | first well-formed object wins over the later real one | GREEN (defect demonstrated) |
| `TestProbeValidateCaseDifferingDuplicateCastNames` | `Mira`+`mira` cast passes uniqueness | GREEN (defect demonstrated) |
| `TestProbeRetryCorrectiveMessageCarriesReason` | corrective message carries the first attempt's precise reason (probed on the no-JSON class) | GREEN (contract holds) |
| `TestProbeRetrySecondReplyAlsoValidated` | retry's reply is re-extracted and re-validated; clean-but-invalid second reply fails naming reason two | GREEN (contract holds) |
| `TestProbeNoRetrySpinOnAlwaysMalformed` | always-malformed model → exactly 2 calls, then `ErrInvalidStory` | GREEN (bounded) |
| `TestProbeExtractArrayWrappedObject` / `…CRLFAndFenceMispair` / `…UnicodeEscapedBrace` / `…EscapedBackslashBeforeQuote` / `…DeepNesting` / `…BalancedInvalidThenReal` | leniency corpus: array-wrapped objects, CRLF + stray-fence replies, `\u007d` brace smuggling, `\\"` before closing quote, 40-deep nesting, balanced-invalid regions skipped | all GREEN (leniency exactly as documented) |
| `TestProbeValidateCaseDifferingLineSpeakerFailsLoudly` | drifted-casing line speaker fails loudly into `ErrInvalidStory` | GREEN (judged correct) |
| `TestProbeValidateOutOfSetEmotionRejected` | out-of-set emotion ("calm", taught-set strictness) rejected loudly | GREEN (judged correct) |
| `TestProbeValidateEmptyLinesAndCharactersLegal` | a narrative page with no characters/lines is legal | GREEN (schema-faithful) |
| `TestProbeEmptyTranscriptBoundary` | `ErrEmptyTranscript` at exactly zero turns, 0 model calls; one voiceless turn still goes to the model | GREEN (boundary exact) |
| `TestProbeExcerptBoundaries` | 512-byte reply echoed whole without marker; rune crossing 513 stays valid UTF-8 with marker | GREEN (bounds exact) |
| `TestProbeRenderTranscriptMultiline` | multi-line turn text rides unprefixed after the role prefix | GREEN (benign, model-facing) |
| Mutation 1: `Thinking: nil` in `attempt` | `TestStructureThinkingOnRawWire` must go red | **RED on both calls** (initial + corrective retry) under mutation; reverted sha256-identical — the thinking pin is load-bearing |
| Mutation 2: `return Story{}, nil` for transport errors in `attempt` | `TestStructureTransportErrorNotRetried` must go red | **PASS under mutant** (pins nothing — finding M1); reviewer probe RED; reverted sha256-identical |
| Existing suite | `go test ./... -race -count=1` | GREEN |

Probes ran under `-race`, deterministic (scripted chatter only), no live network.
`zz_probe_test.go` removed at close; the eight `internal/story` files sha256-verified byte-identical
to the reviewed state; the tree differs from base only by `internal/story/**` (pre-existing) plus
this file.

## Boundary

**Reviewed:** the whole of §T5's Done when and the schema's fidelity to the PLAN/project.md JSON —
struct/tag audit key-for-key (title, cast, name, visual, voice, pitch, sound_effects, pages, n,
text, prompt, characters, emotion, lines, character) with decode-side renames shown to fail loudly
through validation or field assertions; the lenient extractor against an adversarial corpus
(incl. the first-vs-best judgement the brief asked for: it is first-wins, probed at both the
harmless-prose tier and the silent-complete-story tier); the corrective retry end to end (failure
text carried raw, exactly one retry, second reply re-extracted and re-validated, malformed-then-
malformed loud, no spin, transport errors excluded from the trigger); the emotion-set decision
(seven values corroborated against independent Speech 2.8 provider docs; exact-lowercase matching
judged **correct** — the prompt teaches from the same slice `Validate` enforces so they cannot
drift, and T8 passes the value straight to MiniMax where out-of-set is silently ignored, so
`Happy` must be rejected into the retry, not passed silently); ascending-from-1 rejection vs
normalization (judged **correct**: the spec's own words are "numbered n = 1 to 8 in order" and §T5
mandates loud failure; four table rows pin gap/duplicate/order/zero); degenerate-reply
classification (no choices / nil / textless → `ErrInvalidStory`: judged correct and documented —
the corrective retry is the right recovery and the text client's sentinels stay about transport);
`ErrEmptyTranscript` vs `ErrInvalidStory` separation (boundary probed at exactly zero turns);
excerpt and render bounds; `thinking`-ON raw-wire pin mutation-tested load-bearing; AGENTS style
audit (ctx-first, sentinels + `errors.Is`, `%w` wrapping, doc comments on every exported
identifier, no `map[string]any`, no `**T`, consumer-declared `Chatter` interface per invariant 3,
no env reads in the package); `Turn` divergence from `store.Turn` (two-field mirror, field-copy
seam documented at `story.go:72-78` with the invariant-3 rationale; store carries json tags for
persistence, story's copy is in-memory-constructed — consistent); the wire test's `t.Setenv` use
(internal/gmi's recorded deviation, confined to a test).

**Not reviewed / out of scope:** live GMI behaviour (no network — scripted fakes only; the live
half of the Done when is finding M2); `internal/gmi/text` and `internal/store` internals beyond
the seams T5 consumes (T2/T3 closed); T6/T8 consumption beyond the documented fields (`visual`
verbatim, `prompt`, `characters`, `emotion`); the Phase-B HTTP/job call site (does not exist yet —
T5 `Owns` the library only); spend gating and input-size caps (T11); deploy artifacts (T1).

## Exit criteria for round 2

1. All five findings fixed; no severity exempt. M1's fix restores a real `errors.Is` assertion
   (the reviewer probe may be converted into that regression test); M3's fix lands the count
   contract in exactly one place — prompt and validator agreeing, whichever count is chosen —
   with a test; M2's fix records the live twice-running probe's owner (a `T5b` row or an explicit
   T2b scope note in PLAN.md — a contract change recorded, not reached around); L1/L2 land their
   probes as regression tests.
2. Zero residue claimed against this round, severity by severity, in `t5-round2.md`.
3. Gates re-run green: `go vet ./...`, `go test ./... -race -count=1`, `gofmt -l .`,
   `go test ./... -cover`, `CGO_ENABLED=0 go build ./...`.

## Candidates examined and dismissed

- **Emotion vocabulary wrong or incomplete** — the taught seven (happy, sad, angry, fearful,
  disgusted, surprised, neutral) match independent Speech 2.8 provider docs verbatim; some API
  wrappers list extra values (`calm`, `fluent`, `whisper`), and being a safe subset costs only a
  retry if the model ever emits one (probed: `calm` rejected loudly). Taught-set ⊆ accepted-set is
  the safe direction; not a defect.
- **Case-sensitivity of emotions wrong for model output** — judged correct, not leniency-worthy:
  a capitalized `Happy` passed through would be silently ignored by the TTS provider (T8's
  documented behaviour), which is exactly the silent degradation §T5's "fail loudly" rule forbids.
  `Happy` is table-pinned to fail.
- **Rejecting out-of-order pages instead of normalizing** — correct loud behaviour per the spec's
  own wording; four table rows pin the failure reasons; normalizing would hide a model defect.
- **No-choices replies classified as story problems** — documented and defensible: the model
  answered; a corrective retry is the right recovery, the second miss is loud, and stealing the
  transport sentinels would misclassify a real story failure. Probed choiceless and textless.
- **`extractJSON` rescans inside failed candidates (O(n²) worst case)** — bounded by reply length,
  no realistic trigger (prose rarely carries `{`); correctness unaffected (probed: failed
  candidates with interior braces are skipped correctly).
- **`finish_reason` ignored** — a length-truncated reply yields an unbalanced object → extraction
  fails → loud retry; correct failure direction without modelling the field.
- **`voice` omitted → zero-value `Voice{}`** — schema-faithful (narrator pitch is 0 by project.md's
  own table, so omission is indistinguishable from a legitimate narrator); prompt teaches it;
  unenforceable without noise. Dismissed.
- **`n: 1.0` decodes as float** — `encoding/json` rejects `1.0` into `int` loudly → retry; correct.
- **`storiesEqual` helper doesn't compare line-count symmetrically** — an extra line in the second
  story is unseen; both stories come from the same canned reply, so nothing rides on it; non-
  load-bearing test helper, noted for the remediator's tidiness, not a finding.
- **Multi-line transcript turns ride unprefixed after the first line** — model-facing formatting
  only (probed); the transcript is prose context, not a protocol. Benign.
- **Transport errors wrapped as `story: chat: %w` despite "returned untouched"** — `errors.Is`
  preserves the sentinel (probed); the prose prefix changes no classification. Doc phrasing holds.
- **Coverage 100% but decisions unpinned** — covered by M1/M3: statement coverage counted the
  lines and missed the decisions, exactly the AGENTS §Testing preamble's warning.
