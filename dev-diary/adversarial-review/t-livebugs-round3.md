# T-LiveBugs round 3 — adversarial review

| | |
|---|---|
| **Target** | The complete uncommitted T-LiveBugs delta in the paths owned at `PLAN.md:3143-3147`, including remediation rounds 1 and 2 and the user-authorized test-only update in `internal/interview/interview_test.go`. |
| **Reviewer** | Fresh independent Review Agent (`gpt-5.6-luna`), 2026-09-06. This reviewer did not implement or remediate the track. |
| **Method** | Re-read `AGENTS.md`, T-LiveBugs requirements, relevant project decisions, all round-1/round-2 review and remediation records, every changed production path and its tests. Re-ran full race/coverage tests, vet, formatting, JS syntax, deployment-script syntax and deployment-script tests; exercised the server/browser farewell classifiers and terminal-event mutations. No source implementation was changed. |
| **Verdict** | **APPROVE — 0 C / 0 H / 0 M / 0 L.** |

## Requirement and residue audit

| Requirement / prior finding | Review result | Pin / mutation |
|---|---|---|
| Farewell/completion never presents a reply box | Cleared. The server decides full-checklist, model-end, and terminal wording before `questionTurn`; the browser marks an `ended` event as closing and renders exactly the Make-my-book action instead of chips/form. | `TestTurn_FullChecklistPublishesEndedWithoutQuestion`, `TestTurn_ModelEndFinishesCleanly`, and `TestTurn_ClosingPhrasingWithoutModelEndFinishesCleanly` pass. Move the checklist decision after `questionTurn`: the no-question/audio pin fails. |
| Round-1/round-2 false-farewell H1 | Cleared. Go and shipped JavaScript use the same terminal-sentence contract: any question stays answerable, and a farewell word inside an ordinary declarative sentence is not terminal. | `TestTurn_QuestionContainingFarewellWordDoesNotEnd` and `TestAppFarewellClassifierMatchesTerminalClosingContract` pass. Restore unanchored `goodbye` matching: each ordinary farewell-word probe fails. |
| Secure-context and microphone guidance | Cleared. Recording exits before `getUserMedia` when `window.isSecureContext === false` with the required HTTPS/localhost and upload guidance; `NotAllowedError`, consent/token, and unavailable-recorder paths remain distinct. | Static source review plus `node --check static/app.js`. Remove the secure-context early return or merge `NotAllowedError` with the generic fallback: the explicit path is absent. |
| Music opt-out and pipeline propagation | Cleared. An enabled-by-default Screen-4 option sends a boolean field; the value reaches `Generate → startRun → generateJob → runBook → renderFilm`, and false bypasses both bed generation and mix. The no-body default still enables music. | `TestPipeline_MusicOffSkipsMusicBedAndMix`, `TestPipeline_MusicExplicitTrueInvokesMusicBedAndMix`, and existing no-body `TestPipeline_MusicOnMixesTheBedUnderTheFilm` pass. Restore unconditional `mixFilm`: the false pin observes music/mix calls. |
| Narration-progress copy | Cleared. At eight approved pages while drawing, the UI changes to “The animals are giving your characters voices.” | Static review of `attachBookStream` page counting and its render condition. Revert the `done === 8` branch: the narration status is unreachable. |
| Round-1 polling M1 and dynamic artifacts | Cleared. The server-rendered book template emits polling only for `data-book-state == running`; on ready it clears its interval and reloads, exposing the already-rendered PDF and video controls. | `TestBookStateAndColdPageExposeOnlyApprovedArtifacts`, `TestBookPage_ReadyOmitsPollingScript`, and failed/unknown/not-started non-poll tests pass. Broaden the template guard to non-ready: terminal-state pins find `setInterval`. |

## Checks

| Check | Result |
|---|---|
| `GOCACHE=/tmp/thutapi-livebugs-review3-gocache go test ./... -race -cover -count=1` | PASS — all packages; changed packages remain above their applicable coverage floors. |
| `GOCACHE=/tmp/thutapi-livebugs-review3-gocache go vet ./...` | PASS |
| `test -z "$(gofmt -l .)"` | PASS |
| `node --check static/app.js` | PASS |
| `bash -n deploy/docker-run.sh` | PASS |
| `./deploy/redeploy_test.sh` | PASS — 39 passed, 0 failed |
| `git diff --check` | PASS |

## Verdict

**APPROVE — 0 C / 0 H / 0 M / 0 L.** There is explicit **zero residue against every finding from rounds 1 and 2**: music flag plumbing and running-only polling remain correct; browser and server farewell classification are now parity-pinned; and a full checklist no longer produces a question/audio flicker before the terminal event. All T-LiveBugs requirements are covered inside the reviewed seams.
