# T-LiveBugs round 2 — adversarial review

| | |
|---|---|
| **Target** | Track T-LiveBugs, the complete uncommitted delta in the paths owned at `PLAN.md:3143-3147`, including the round-1 remediation. |
| **Reviewer** | Fresh Review Agent (`gpt-5.6`), 2026-09-06. This reviewer did not implement or remediate this track. |
| **Method** | Re-read `AGENTS.md`, the T-LiveBugs requirements, relevant `project.md` decisions, and round-1/re-mediation history; inspected every changed source and test file; ran targeted race tests, vet, formatting, JavaScript syntax, diff integrity, and a direct JavaScript farewell-classifier probe. |
| **Verdict** | **REMEDIATE — 0 C / 2 H / 0 M / 0 L.** |

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:isFarewell` and `Interview` render branch | The client still uses broad, unanchored farewell substrings while the remediated server uses terminal-sentence classification. A normal declarative prompt such as `How would Pip say goodbye to a friend.` is not a server closing turn, but the client removes its answer form and shows **Make my book**. Tapping it advances to Screen 4; Generate then receives `409 not_ended`, leaving the child unable to answer the question or make the book. This is the exact false-terminal residue round 1 required to eliminate, only moved from Go to the browser. | A direct evaluation of the shipped `isFarewell` returns `true` for both `How would Pip say goodbye to a friend.` and `Pip says goodbye before dinner.` (expected `false`); add a browser/static pin covering those two ordinary prompts and an actual terminal phrase. | Restore / retain `lower.includes("goodbye")` (or any unanchored equivalent); the pin again hides the answer form and routes an open interview to a generation request that must fail. |
| H2 | H | `internal/interview/turn.go:questionTurn` | A full checklist still publishes a `question` event before immediately publishing `ended`. The freshly added `isClosingPhrasing` path avoids that only for recognized textual farewells; a model reply that fills the final slot but asks an ordinary confirmation still reaches `questionTurn`, so the client receives and renders a freeform answer form before `ended`. That violates the T-LiveBugs requirement to suppress it when **all slots are full**, and preserves the explicit farewell/completion flicker described in the live report. The existing test codifies the old behavior (`TestQuestionSpeaker_ChecklistEndPublishesQuestionAndAudioAndEnded`) rather than pinning the new requirement. | Add/run `TestTurn_FullChecklistPublishesEndedWithoutQuestion`: script a final non-`end` reply with all six slots and ordinary text, subscribe before answering, and assert `ended` is the only terminal UI event (no `question`/`question_audio` after the answer). It fails because `questionTurn` publishes first and calls `closeTurn` only afterwards. | Reintroduce `questionTurn` before the full-checklist check; the pin again receives an answerable question immediately followed by `ended`, exposing the child to a reply box for a completed interview. |

## Round-1 residue audit

- **C1 (music flag plumbing):** cleared. `music` now traverses `Generate → startRun → generateJob → runBook → renderFilm`; focused music-on and music-off pipeline pins show that false invokes neither the music provider nor mixer.
- **H1 (server farewell substring):** its server half is cleared by terminal-sentence matching and `TestTurn_QuestionContainingFarewellWordDoesNotEnd`. **The required client counterpart remains defective as H1 above.**
- **M1 (book-page polling):** cleared. The template emits its poll only for `data-book-state == "running"`; tests cover ready, failed, unknown, and not-started pages.

## Checks

| Check | Result |
|---|---|
| `GOCACHE=/tmp/thutapi-livebugs-gocache go test ./internal/bookgen ./internal/interview ./internal/web -race -count=1` | PASS (run with loopback permission; sandbox otherwise blocks `httptest` IPv6 listeners) |
| `GOCACHE=/tmp/thutapi-livebugs-gocache go vet ./...` | PASS |
| `test -z "$(gofmt -l .)"` | PASS |
| `node --check static/app.js` | PASS |
| `git diff --check` | PASS |
| direct `isFarewell` mutation/probe | **FAIL**, as recorded in H1 |

## Verdict

**REMEDIATE — 0 C / 2 H / 0 M / 0 L.** Round 1's compile and polling findings are resolved, but its false-farewell condition still exists in the client, and the full-checklist path still emits a question before completion. Therefore there is **not zero residue against round 1**. A remediation agent must fix both findings and write `t-livebugs-remediation-round2.md`; a new, fresh reviewer must then complete round 3.
