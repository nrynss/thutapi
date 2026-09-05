# T5c round 1 — remediation

| | |
|---|---|
| **Target** | All findings (0 C / 1 H / 0 M / 1 L) in `dev-diary/adversarial-review/t5c-round1.md` + sanctioned contract row C1. Verdict in round 1 was REMEDIATE (0C/1H/0M/1L). |
| **Remediator** | T5cRound1Remediator (remediation agent; distinct from reviewer and implementer). |
| **Date** | 2026-09-05. |
| **Commit** | Uncommitted at remediation time; working tree stays uncommitted — the orchestrator commits on APPROVE (AGENTS.md §Process step 5). |
| **Round file** | `dev-diary/adversarial-review/t5c-round1.md` is unchanged. |
| **Scope** | `internal/audio/narrate_test.go:132-136` (under Sanctioned Contract Row C1), `internal/story/validate_test.go:326-330` (L1), and this remediation record file. `internal/bookgen/`, `cmd/`, and all other packages untouched. |

---

## Dispositions

| # | Finding | Severity | Where | Disposition | Status |
|---|---|---|---|---|---|
| 1 | **H1** — `TestNarrateBook_ValidationFailsBeforeAnyCall` fails because `"calm"` is now an in-set emotion | H | `internal/audio/narrate_test.go:132-136` | **Sanctioned Contract Row C1.** Replaced `pages[0].Emotion = "calm"` with `pages[0].Emotion = "neutral"` and updated line 134 to assert `strings.Contains(err.Error(), "neutral")`. `"neutral"` is now the out-of-set emotion, preserving the exact validation contract intended by T8. | **DONE** |
| 2 | **L1** — `TestEmotionsSubsetOfProviderEnum` does not assert that `"auto"` is excluded from `story.Emotions` | L | `internal/story/validate_test.go:326-330` | Added an explicit check `if e == "auto"` inside the loop over `story.Emotions` in `TestEmotionsSubsetOfProviderEnum` asserting that `"auto"` must not be present in `story.Emotions` per PLAN.md §T5c. | **DONE** |

---

## Rows

### H1 — `internal/audio/narrate_test.go:132-136` fails because `"calm"` is now an in-set emotion

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `internal/audio/narrate_test.go:132-136` |
| **What was done** | When T8 was written, `story.Emotions` contained `"neutral"` and did not contain `"calm"`. `TestNarrateBook_ValidationFailsBeforeAnyCall` asserted that setting `pages[0].Emotion = "calm"` failed with `ErrInvalidPage` naming the invalid word and vocabulary. With T5c replacing `"neutral"` with `"calm"`, `"calm"` became valid and `"neutral"` became out-of-set. Under Sanctioned Contract Row C1, changed `pages[0].Emotion = "calm"` to `pages[0].Emotion = "neutral"` and updated line 134 to assert `strings.Contains(err.Error(), "neutral")`. |
| **Pin** | Run `go test -v -run TestNarrateBook_ValidationFailsBeforeAnyCall ./internal/audio/...`<br>Passes cleanly with code 0: `--- PASS: TestNarrateBook_ValidationFailsBeforeAnyCall (0.00s)`. |
| **Mutation check** | Reverting `pages[0].Emotion = "neutral"` to `pages[0].Emotion = "calm"` reproduces the exact test failure: `narrate_test.go:135: err = <nil>, want ErrInvalidPage naming the word and the vocabulary`. Restoring `"neutral"` returns the test to green. |

---

### L1 — `TestEmotionsSubsetOfProviderEnum` does not assert that `"auto"` is excluded from `story.Emotions`

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/story/validate_test.go:326-330` |
| **What was done** | In `TestEmotionsSubsetOfProviderEnum`, added an explicit check inside the iteration over `story.Emotions`: `if e == "auto" { t.Errorf("story.Emotions must not contain %q: provider-default is intentionally omitted to keep per-page emotion intentional (PLAN.md §T5c)", e) }`. This explicitly pins the invariant that `"auto"` is never in `story.Emotions`. |
| **Pin** | Run `go test -v -run TestEmotionsSubsetOfProviderEnum ./internal/story/...`<br>Passes cleanly with code 0: `--- PASS: TestEmotionsSubsetOfProviderEnum (0.00s)`. |
| **Mutation check** | Temporarily appending `"auto"` to `story.Emotions` in `internal/story/story.go:72` immediately causes `TestEmotionsSubsetOfProviderEnum` to fail RED: `validate_test.go:328: story.Emotions must not contain "auto": provider-default is intentionally omitted to keep per-page emotion intentional (PLAN.md §T5c)`. Restoring `story.Emotions` returns the test to green. |

---

## Mutation Table

| Mutant | Pin | Expected | Observed |
|---|---|---|---|
| H1: In `narrate_test.go:132`, set `pages[0].Emotion = "calm"` | `TestNarrateBook_ValidationFailsBeforeAnyCall` | **RED** (`err = <nil>`) | **RED** (`narrate_test.go:135: err = <nil>, want ErrInvalidPage naming the word and the vocabulary`) |
| L1: In `story.go:72`, append `"auto"` to `story.Emotions` | `TestEmotionsSubsetOfProviderEnum` | **RED** (`must not contain "auto"`) | **RED** (`validate_test.go:328: story.Emotions must not contain "auto": provider-default is intentionally omitted to keep per-page emotion intentional (PLAN.md §T5c)`) |

Both mutations were verified by hand and reverted cleanly.

---

## Gates

| Gate | Result | Notes |
|---|---|---|
| `go vet ./...` | **PASS** | Clean across entire repo. |
| `gofmt -l internal/story/ internal/audio/ internal/gmi/media/ cmd/` | **PASS** | Clean across all touched packages (internal/bookgen/ is pre-existing untracked and untouched per boundary rules). |
| `go test ./... -race` | **PASS** | All packages pass cleanly under race detector. |
| `go test ./internal/story/... -cover` | **PASS** | 100.0% statement coverage (floor 75%). |
| `go test ./internal/audio/... -cover` | **PASS** | 97.8% statement coverage (floor 75%). |

---

## Files Changed

- `internal/audio/narrate_test.go` — lines 132 and 134: updated out-of-set test case from `"calm"` to `"neutral"` (Sanctioned Contract Row C1).
- `internal/story/validate_test.go` — lines 326-330: added explicit assertion in `TestEmotionsSubsetOfProviderEnum` ensuring `"auto"` is not in `story.Emotions`.
- `dev-diary/adversarial-review/t5c-remediation-round1.md` — this remediation record.

`internal/bookgen/` and all other unowned files remain untouched.
