# T-LiveBugs remediation round 1

| Finding | Fix | Pin | Mutation |
|---|---|---|---|
| C1 | Threaded the per-request `music` boolean through `generateJob` → `runBook` → `renderFilm`; the music-off branch now bypasses both bed generation and mixing. | `TestPipeline_MusicOffSkipsMusicBedAndMix` and `TestPipeline_MusicExplicitTrueInvokesMusicBedAndMix` exercise both request values. | Remove the `music` argument or restore an unconditional mix branch; the package no longer compiles or the music-off pin observes provider/mixer calls. |
| H1 | Replaced unanchored farewell substring matching with terminal-sentence matching; declarative ordinary prompts containing `goodbye` remain answerable. | `TestTurn_QuestionContainingFarewellWordDoesNotEnd` scripts `Tell me what Pip says when waving goodbye.` and asserts an open transcript plus a question event. | Restore `strings.Contains(lower, "goodbye")`; the pin emits `ended` instead of an answerable question. |
| M1 | Restricted the `/book/{id}` polling script to `data-book-state == "running"`; ready, failed, unknown, and not-started pages do not create an interval. | `TestBookPage_ReadyOmitsPollingScript`, `TestBookStateReportsFailedLatestRunWithoutInventingArtifacts`, `TestBookStateUnknownDoesNotExposePriorRunArtifacts`, and `TestBookStateDefaultsToNotStartedWithoutArtifacts`. | Change the template guard back to `ne .Book.Status "ready"`; the non-running page pins find `setInterval`. |

Focused verification passes: `go test ./internal/bookgen ./internal/interview ./internal/web -race -count=1`, `node --check static/app.js`, and `git diff --check`.

Round 2 must independently re-review all three remediations and the full T-LiveBugs delta.
