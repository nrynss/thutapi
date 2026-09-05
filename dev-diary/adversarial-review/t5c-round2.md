# T5c round 2 — adversarial re-review

| | |
|---|---|
| **Target** | Track T5c Round 1 remediation: `internal/audio/narrate_test.go:132-136` (sanctioned contract row C1, finding H1) and `internal/story/validate_test.go:326-330` (finding L1). |
| **Reviewer** | T5cRound2Reviewer (fresh adversarial review agent; not the implementer, not the round-1 reviewer, not the round-1 remediator). |
| **Date** | 2026-09-05. |
| **Evidence** | `AGENTS.md`; `dev-diary/PLAN.md` §T5c; `dev-diary/adversarial-review/README.md`; `dev-diary/adversarial-review/t8b-live-record.md`; `dev-diary/adversarial-review/t5c-round1.md`; `dev-diary/adversarial-review/t5c-remediation-round1.md`; `internal/story/**`; `internal/audio/**`; `internal/gmi/media/client.go`; `internal/illustrate/prompt.go:43`. |
| **Method** | Independent adversarial re-review. Code inspection across all modified files; live execution of repository test suite (`go test ./... -race`, `go vet ./...`, `gofmt -l .`); statement coverage verification; manual mutation testing of H1 and L1 pins with verified failure and recovery; boundary audit ensuring no unowned paths were touched. |

---

## Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L

Both findings from Round 1 (H1, L1) have been fully remediated and verified with robust mutation tests. All contract rows (C1, C2, C3) are satisfied. Zero unowned paths were modified. Repository gates pass cleanly with 100.0% statement coverage for `internal/story` and 97.8% for `internal/audio`.

There is **zero residue** across all severities against Round 1.

---

## Gates (Round 2)

| Gate | Result | Notes |
|---|---|---|
| `go test ./... -race` | **PASS** | Clean across all repo packages (`cmd/thutapi`, `internal/audio`, `internal/bookgen`, `internal/bookvideo`, `internal/gmi/media`, `internal/gmi/text`, `internal/illustrate`, `internal/interview`, `internal/job`, `internal/mediastore`, `internal/store`, `internal/story`, `internal/stream`, `static/race`). |
| `go test ./internal/story/... -race -cover` | **PASS** | 100.0% statement coverage (floor: 75%). |
| `go test ./internal/audio/... -race -cover` | **PASS** | 97.8% statement coverage (floor: 75%). |
| `go vet ./...` | **PASS** | Clean across entire repo. |
| `gofmt -l .` | **PASS** | Clean across entire repo (zero lines output). |

---

## Remediation Evaluation (Round 1 Findings)

### H1 — `TestNarrateBook_ValidationFailsBeforeAnyCall` pins out-of-set emotion validation

| Field | Value |
|---|---|
| **Round 1 Finding** | Setting `pages[0].Emotion = "calm"` caused `TestNarrateBook_ValidationFailsBeforeAnyCall` to fail because `"calm"` became a valid emotion in `story.Emotions`. |
| **Remediation** | In `internal/audio/narrate_test.go:132-136` (under Sanctioned Contract Row C1), `pages[0].Emotion` was updated from `"calm"` to `"neutral"`, and the assertion checks `strings.Contains(err.Error(), "neutral")`. |
| **Verification** | `go test -count=1 -v -run TestNarrateBook_ValidationFailsBeforeAnyCall ./internal/audio/...` PASSES cleanly. |
| **Mutation Check** | Temporarily mutating `pages[0].Emotion = "neutral"` back to `pages[0].Emotion = "calm"` reproduces the exact test failure: `narrate_test.go:135: err = <nil>, want ErrInvalidPage naming the word and the vocabulary`. Restoring `"neutral"` returns test to green. Pin is valid and robust. |
| **Status** | **CLOSED** |

---

### L1 — `TestEmotionsSubsetOfProviderEnum` pins exclusion of `"auto"`

| Field | Value |
|---|---|
| **Round 1 Finding** | `TestEmotionsSubsetOfProviderEnum` did not assert that `"auto"` is excluded from `story.Emotions`, leaving unpinned the invariant that `auto` must not be offered to M3. |
| **Remediation** | In `internal/story/validate_test.go:326-330`, an explicit check `if e == "auto"` was added to the loop over `story.Emotions` asserting that `"auto"` must not be present per PLAN.md §T5c. |
| **Verification** | `go test -count=1 -v -run TestEmotionsSubsetOfProviderEnum ./internal/story/...` PASSES cleanly. |
| **Mutation Check** | Temporarily appending `"auto"` to `story.Emotions` in `internal/story/story.go:72` immediately causes `TestEmotionsSubsetOfProviderEnum` to fail RED: `validate_test.go:328: story.Emotions must not contain "auto": provider-default is intentionally omitted to keep per-page emotion intentional (PLAN.md §T5c)`. Restoring `story.Emotions` returns test to green. Pin is valid and robust. |
| **Status** | **CLOSED** |

---

## Contract Rows Evaluation

| Row | Seam | Status | Evidence / Verification |
|---|---|---|---|
| **C1** | `internal/audio/narrate_test.go:132-136` | **SATISFIED** | Sanctioned in Round 1. Out-of-set test case updated from `"calm"` to `"neutral"`, asserting `strings.Contains(err.Error(), "neutral")`. Validated under mutation. |
| **C2** | `internal/gmi/media/client.go:300-310` | **SATISFIED** | Doc comment updated to cite GMI's model-details schema endpoint (`console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models/minimax-tts-speech-2.8-hd`) and confirm top-level placement per `t8b-live-record.md`. Verified. |
| **C3** | `internal/audio/audio.go:36, :46-56, :177` | **SATISFIED** | Doc comments updated to cite GMI's model-details enum for `minimax-tts-speech-2.8-hd` (calm in place of neutral; auto omitted) and confirm placement. Verified. |

---

## Seam and Boundary Audit

1. **`internal/bookgen/`**: Confirmed untouched by T5c / T5c remediation.
2. **`cmd/thutapi/main.go` and `cmd/thutapi/main_test.go`**: Confirmed untouched by T5c / T5c remediation.
3. **`internal/illustrate/prompt.go:43`**: Verified intact (`const referenceDirective = "Full body, front view, neutral pose, plain flat background..."`).
4. **All other unowned packages**: Confirmed completely untouched.
5. **No unauthorized file edits**: All touched files belong strictly to the track's `Owns` or sanctioned contract rows.

---

## Zero-Residue Claim (Against Round 1)

- **C (0):** None in Round 1, none in Round 2.
- **H (0):** Finding H1 remediated and verified with mutation pin in `narrate_test.go:132-136`. Zero residue.
- **M (0):** None in Round 1, none in Round 2.
- **L (0):** Finding L1 remediated and verified with mutation pin in `validate_test.go:326-330`. Zero residue.

Total findings: **0 C / 0 H / 0 M / 0 L**.
Track T5c is **APPROVED** and ready for the Orchestrator to commit to `main`.
