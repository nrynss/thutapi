# T5c round 1 — adversarial review

| | |
|---|---|
| **Target** | Track T5c, `internal/story/story.go` (the `Emotions` constant and its doc), three test sites (`wire_test.go`, `validate_test.go`, `structure_test.go`), and two doc-only contract rows in `internal/gmi/media/client.go` and `internal/audio/audio.go`. |
| **Reviewer** | T5cRound1Reviewer (adversarial review agent; not the implementer). |
| **Date** | 2026-09-05. |
| **Evidence** | `AGENTS.md`; `dev-diary/PLAN.md` §T5c; `dev-diary/adversarial-review/README.md`; `dev-diary/adversarial-review/t8b-live-record.md`; `internal/story/**`; `internal/audio/**`; `internal/gmi/media/client.go`; `internal/illustrate/prompt.go`. |
| **Method** | Line-by-line inspection of changes against `origin/main` at `864bc8d`; verification of seam boundary adherence (no unowned paths touched by T5c); repo gate execution (`go test ./internal/story/... -race -cover`, `go vet`, `gofmt`); audit of upstream schema citation; validation of wire pin on raw JSON; reproduction of test failure in `internal/audio/narrate_test.go:132-136`; mutation analysis for all findings. |

---

## Gates (run for this review)

| Gate | Result | Notes |
|---|---|---|
| `go test ./internal/story/... -race -cover` | **PASS** | 100.0% statement coverage. All 3 test sites pass, new enum pin passes. |
| `go vet ./internal/story/... ./internal/audio/... ./internal/gmi/media/...` | **PASS** | Clean. |
| `gofmt -l internal/story/ internal/audio/audio.go internal/gmi/media/client.go` | **PASS** | Clean across all T5c-modified files. |
| `go test ./internal/audio/...` | **FAIL** | `TestNarrateBook_ValidationFailsBeforeAnyCall` fails at `narrate_test.go:135` (Finding H1). |

---

## Verdict: REMEDIATE — 0 C / 1 H / 0 M / 1 L

The core implementation of T5c is clean and faithful to `dev-diary/PLAN.md` §T5c and `dev-diary/adversarial-review/t8b-live-record.md`:
1. `story.Emotions` successfully replaced `"neutral"` with `"calm"`.
2. The doc comment on `story.Emotions` cites GMI's model-details schema (`console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models/minimax-tts-speech-2.8-hd`, `t8b-live-record.md`) rather than MiniMax native docs, and accurately documents why `auto` is omitted.
3. `internal/story/wire_test.go:87` pins `..., calm` on raw JSON.
4. `validStory()` in `internal/story/validate_test.go:65` and `goodReply` in `internal/story/structure_test.go:28` update page 5 to `"calm"`, while `"curious"` in `badReply` remains intact as an intentional out-of-set test case.
5. `TestEmotionsSubsetOfProviderEnum` was added to `internal/story/validate_test.go:295-331`, checking `story.Emotions` against GMI's `minimax-tts-speech-2.8-hd` provider enum.
6. `internal/illustrate/prompt.go:43` ("neutral pose") was confirmed untouched.
7. Contract rows in `internal/gmi/media/client.go:300-310` and `internal/audio/audio.go:36, :46-56, :177` are properly updated (doc-only).
8. Seam boundaries were respected: `internal/bookgen/` and other unowned packages were NOT touched by T5c.

However, a verdict of **REMEDIATE** is mandatory under `AGENTS.md` due to finding **H1**: `internal/audio/narrate_test.go:132-136` tests an out-of-set emotion using `Emotion = "calm"`. Because `calm` is now valid, `TestNarrateBook_ValidationFailsBeforeAnyCall` fails under `go test ./internal/audio/...`. The implementer correctly identified this and refrained from editing outside the seam; contract row C1 is hereby formally sanctioned to allow remediation of `narrate_test.go`. In addition, finding **L1** notes that `TestEmotionsSubsetOfProviderEnum` does not assert that `"auto"` is excluded from `story.Emotions`.

---

## Findings

### H1 — `internal/audio/narrate_test.go:132-136` fails because `"calm"` is now an in-set emotion

| | |
|---|---|
| **Severity** | H |
| **Where** | `internal/audio/narrate_test.go:132-136` |
| **What** | When T8 was authored, `story.Emotions` contained `"neutral"` and did not contain `"calm"`. `TestNarrateBook_ValidationFailsBeforeAnyCall` explicitly tested the out-of-set emotion error reporting by setting `pages[0].Emotion = "calm"` and asserting that `NarrateBook` returned `ErrInvalidPage` containing `"calm"`. Now that T5c has updated `story.Emotions` to include `"calm"`, `"calm"` is accepted as valid, causing `NarrateBook` to succeed (`err == nil`). Consequently, `TestNarrateBook_ValidationFailsBeforeAnyCall` fails: `narrate_test.go:135: err = <nil>, want ErrInvalidPage naming the word and the vocabulary`. This breaks `go test ./internal/audio/...` and CI verification (`verify.yml`). |
| **Pin** | Run `go test -v -run TestNarrateBook_ValidationFailsBeforeAnyCall ./internal/audio/...`<br>Output:<br>`=== NAME TestNarrateBook_ValidationFailsBeforeAnyCall`<br>` narrate_test.go:135: err = <nil>, want ErrInvalidPage naming the word and the vocabulary`<br>`--- FAIL: TestNarrateBook_ValidationFailsBeforeAnyCall (0.00s)` |
| **Mutation** | In `internal/audio/narrate_test.go:132`, changing `pages[0].Emotion = "calm"` to `pages[0].Emotion = "neutral"` and updating line 134 to assert `strings.Contains(err.Error(), "neutral")` causes `TestNarrateBook_ValidationFailsBeforeAnyCall` to PASS green. Reverting to `"calm"` reproduces the test failure. |

---

### L1 — `TestEmotionsSubsetOfProviderEnum` does not assert that `"auto"` is excluded from `story.Emotions`

| | |
|---|---|
| **Severity** | L |
| **Where** | `internal/story/validate_test.go:310-330` |
| **What** | `dev-diary/PLAN.md` §T5c ("`auto` stays out") and `internal/story/story.go:69-70` ("auto is provider-default and deliberately omitted to keep per-page emotion intentional") specify an invariant: `"auto"` must NOT be present in `story.Emotions`. However, `TestEmotionsSubsetOfProviderEnum` populates `providerEnum` with `"auto": true` alongside the 7 explicit emotions, and only checks subset inclusion (`if !providerEnum[e]`). If an agent or refactor mistakenly appends `"auto"` to `story.Emotions`, `TestEmotionsSubsetOfProviderEnum` remains GREEN. The invariant that `auto` is omitted is currently unpinned by any unit test. |
| **Pin** | Mutation check: temporarily appending `"auto"` to `story.Emotions` in `internal/story/story.go:72` leaves `TestEmotionsSubsetOfProviderEnum` PASSING green. |
| **Mutation** | Adding an explicit check in `TestEmotionsSubsetOfProviderEnum` asserting that no member of `story.Emotions` is `"auto"` (e.g., `if e == "auto" { t.Errorf(...) }`) fails RED if `"auto"` is in `story.Emotions`, and stays GREEN when `"auto"` is omitted. |

---

## Contract Row Evaluation

| Row | Seam | Status | Ruling / Details |
|---|---|---|---|
| **C1** (New) | `internal/audio/narrate_test.go:132-136` | **SANCTIONED** | Raised by implementation agent. `PLAN.md` §T5c `Owns` omitted `internal/audio/narrate_test.go` from the list of test sites that spell out the vocabulary. The implementation agent correctly stopped at the seam boundary. This contract change authorizes the remediation agent to modify `internal/audio/narrate_test.go:132-136` to replace `"calm"` with `"neutral"` (the emotion that is now out-of-set) and assert `strings.Contains(err.Error(), "neutral")`. |
| **C2** (PLAN §T5c #6) | `internal/gmi/media/client.go:300-310` | **SATISFIED** | Doc comment updated to confirm emotion payload key placement against provider schema per `t8b-live-record.md`. Doc-only, verified. |
| **C3** (PLAN §T5c #7) | `internal/audio/audio.go:36, :46-56, :177` | **SATISFIED** | Doc comments updated to cite GMI's model-details enum for `minimax-tts-speech-2.8-hd` and confirm placement. Doc-only, verified. |

---

## Seam & Boundary Audit

1. **`internal/bookgen/`**: Confirmed untouched by T5c. (Pre-existing untracked files from another track remain intact in the working tree).
2. **`cmd/thutapi/main.go` and `cmd/thutapi/main_test.go`**: Confirmed untouched by T5c.
3. **`internal/illustrate/prompt.go:43`**: Confirmed untouched (`"Full body, front view, neutral pose, plain flat background..."`).
4. **All other packages**: Untouched by T5c.

---

## Remediation Instructions for Round 1

The Remediation Agent for T5c Round 1 must:
1. **Remediate H1**: In `internal/audio/narrate_test.go:132-136` (under sanctioned contract row C1), replace `pages[0].Emotion = "calm"` with `pages[0].Emotion = "neutral"` and assert `strings.Contains(err.Error(), "neutral")`.
2. **Remediate L1**: In `internal/story/validate_test.go`, update `TestEmotionsSubsetOfProviderEnum` (or add an assertion) to explicitly assert that `"auto"` is not in `story.Emotions`.
3. Record fixes in `dev-diary/adversarial-review/t5c-remediation-round1.md`.
