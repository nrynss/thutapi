# T-LiveBugs round 1 — adversarial review

| | |
|---|---|
| **Target** | Track T-LiveBugs, uncommitted implementation delta across the paths declared in `PLAN.md:3141-3174`. |
| **Reviewer** | Fresh Review Agent (`gpt-5.6`), 2026-09-06. The reviewer did not implement or remediate this track. |
| **Method** | Read `AGENTS.md`, the track requirements, project decisions touching optional music, T1c/T9b/T10 history, and every changed file. Ran a clean-cache targeted build/test attempt, `node --check static/app.js`, `git diff --check`, and static contract probes. No source implementation was changed. |
| **Verdict** | **REMEDIATE — 1 C / 1 H / 1 M / 0 L.** |

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| C1 | C | `internal/bookgen/bookgen.go:609`; `internal/bookgen/pipeline.go:32` | `generateJob` now calls `h.runBook(ctx, bookID, ivID, music)`, but `runBook` still accepts only `(ctx, bookID, ivID)`. The package cannot compile, so the demo cannot build or deploy; additionally the music flag never reaches the music/mix branch. | `GOCACHE=/tmp/thutapi-review-gocache go test ./internal/bookgen -race -count=1` is red: `too many arguments in call to h.runBook; have (..., bool); want (... )`. | Remove the flag from `runBook`/`renderFilm` after fixing the compile mismatch (or hard-code use of `h.cfg.Music`): the same music-off request again calls `GenerateMusicBed`/`mixFilm`; `TestPipeline_MusicOffSkipsMusicBedAndMix` must go red. |
| H1 | H | `internal/interview/turn.go:274-303`; `static/app.js:300-318,625-626` | Farewell detection is an unanchored substring list and treats ordinary story questions as a completed interview. For example, a model reply `What does Pip say when waving goodbye?\\n[[filled:]]` has neither `end` nor a full checklist, yet the server publishes `ended`; the client independently hides the answer form for the same text. The child loses the interview before supplying the required details. | Add/run `TestTurn_QuestionContainingFarewellWordDoesNotEnd`: script the question above, start an interview, and assert a `question` event (not `ended`) plus an answerable open transcript. It fails now because `strings.Contains(lower, "goodbye")` makes `endNow` true. A browser/static counterpart must assert that a nonterminal question containing the word leaves the form rendered. | Widen a corrected terminal classifier back to `strings.Contains(lower, "goodbye")` (and the client equivalent); the pin must again receive `ended`/lose the form for an ordinary question. |
| M1 | M | `internal/web/templates/book.html:37-52` | The poll is emitted for every status except `ready`, rather than only `data-book-state == "running"` as required. A failed, unknown, or never-started book therefore makes an unbounded request every three seconds and can never become ready without a new generation/reload. The template test covers `running` and `ready`, but misses these terminal/non-running states. | Extend `TestBookStateReportsFailedLatestRunWithoutInventingArtifacts`, `TestBookStateUnknownDoesNotExposePriorRunArtifacts`, and `TestBookStateDefaultsToNotStartedWithoutArtifacts` to assert absence of `setInterval`; each fails now because `{{if ne .Book.Status "ready"}}` injects the timer. | Change a corrected `{{if eq .Book.Status "running"}}` guard back to `{{if ne .Book.Status "ready"}}`; all three non-running pins must become red. |

## Requirement audit

- **Bug 1b:** static inspection finds the required `window.isSecureContext === false` message before media access, separate `NotAllowedError` copy, and fallback copy for unavailable hardware/browser. This portion is not a finding.
- **Music UI/status:** the UI carries a checked-by-default toggle through `onContinue` and POSTs a boolean `music` field; the eight-page narration wording is present. These cannot pass end-to-end while C1 prevents compilation.
- **Dynamic artifact arrival:** the ready-state reload would remove the manual-refresh burden, but M1 must constrain the polling lifecycle to actual running books.
- **Seams:** all changed implementation paths are listed in the track's `Owns`; this review record is the mandated review artifact.

## Checks

| Check | Result |
|---|---|
| `GOCACHE=/tmp/thutapi-review-gocache go test ./internal/bookgen -race -count=1` | **FAIL — C1 compile error** |
| `go test ./internal/interview ./internal/web -race -count=1` | Could not complete in this sandbox: `httptest` IPv6 listener creation is denied (`operation not permitted`). This is environmental, separate from C1. |
| `node --check static/app.js` | Not reached in the combined command because the Go attempt ended at its timeout; source was subsequently parsed by inspection. Re-run after C1 remediation. |
| `git diff --check` | Not reached in that aborted combined command; re-run as part of remediation gates. |

## Verdict

**REMEDIATE — 1 C / 1 H / 1 M / 0 L.** This is round 1, so there is no prior-round residue to close. C1 blocks all build and deployment verification. H1 can end an ordinary interview merely for using a farewell word; M1 violates the running-only polling contract and indefinitely polls terminal states. Every finding needs a remediation row and a fresh round-2 review.
