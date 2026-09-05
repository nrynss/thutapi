# T9 round 2 — remediation

The six findings in `t9-round2.md` were remediated only in T9-owned static
paths and this record. No interview, generation, audio, or T13 implementation
path was changed. The T13 capture surface is intentionally absent until T13
mounts its complete implementation, as required by `PLAN.md` §Screen 4.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `static/app.js` (`generate`) and `static/browser-test.js` (`testShelfGestureAndGeneration`) | Concurrent page completion was represented by the greatest ordinal instead of real approval count. | Keep a per-generation `Set` of approved page numbers, reset it for each generation, and set `done` to the set size only for a new valid `1..8` page. | The browser contract sends page 8 then page 1 and requires the iframe titles `1 of 8` then `2 of 8`. | Restore `setDone(current => Math.max(current, event.n))`; the out-of-order approval assertion fails. |
| H2 | `static/app.js` (`json`, stream/catch-up handlers, missing-story render) and `static/browser-test.js` (`testMissingCatchupRecovery`) | A 404 catch-up was warm but let the child repeat a failing answer POST. | Preserve the typed API error class, close the terminal stream, and render a new-story/shelf recovery card with no answer form for `not_found`. | The browser contract makes `GET /interviews/missing` return `404 {"error":"not_found"}` and requires warm copy, a `Start a new story` door, and no `.answer` form. | Remove the `missing` render branch or typed 404 handling; the recovery-door assertion fails. |
| M1 | `static/app.js` (`applyRecoveredQuestion`) and `static/browser-test.js` (`testLiveQuestionWinsCatchup`) | An equal-turn transcript could overwrite a live SSE question and erase its chips. | Treat a recovered turn equal to the active live turn as already supplied; catch-up only fills strictly later turns. | The browser contract delays catch-up, delivers live turn 1 with `A fox`, then resolves the same transcript turn and requires the chip to remain. | Change the guard back to `q.turn < activeTurn.current`; the ordering assertion fails. |
| M2 | `static/app.js`, `static/app.css`, and `static/browser-test.js` (`testShelfGestureAndGeneration`) | Screen 4 advertised record/upload controls that had no T13 implementation behind them. | Removed the inert T9 capture controls and their styling. Until T13 provides both working modes, an ended interview takes the existing no-sample path straight to generation; this is the meaningful skip path and does not touch T13's owned implementation seam. | The browser contract ends an interview, requires a generate request and race iframe, and rejects any record/upload/capture element. | Restore `AdultStep` or its mode-only handlers; the bypass assertion fails. |
| M3 | `static/browser-test.js` (`testShelfGestureAndGeneration`) | The T9a iframe receiver was only source-audited, so the parent-to-child one-number hand-off could silently break. | Extended the real browser contract to wait for `/static/race/race.html?embed=1`, then read the child `.race` inline `--done` style after parent progress reaches two approvals. | Serve the static files and run `/static/browser-test.html`; it requires the real child `.race` to receive `--done: 2`. | Remove T9a's `message` listener while leaving the parent iframe title update; the child-style assertion fails. |
| M4 | `static/browser-test.html`, `static/browser-test.js`, `static/app.css`, and this record | The required phone acceptance had neither a reproducible probe nor an honest record of its physical-device limit. | The browser contract now exercises the deterministic phone-critical conditions (shelf audio-unlock gesture, cold-link speaker fallback, tapping chips, 100dvh/safe-area/focus behavior presence, and event-driven race progress). Its rendered manual checklist gives the required on-phone audio, keyboard, and event checks and requires the operator to record phone/browser/SHA/date/pass-fail here. | Run `/static/browser-test.html` from the HTTPS deployment on the physical phone, then execute its three listed observations and append the device result below. Desktop emulation is expressly insufficient. | Remove the manual checklist or its deterministic browser cases; a future operator no longer has a reproducible probe or the required recorded observation fields. |

## Physical-phone acceptance record

* **Automated environment, 2026-09-06:** no browser surface was available to
  the agent, and `adb devices` reported no connected device. No physical-phone
  execution, iOS-audio assertion, or keyboard assertion is claimed.
* **Reproducible operator probe:** deploy the checked tree over HTTPS, open
  `/static/browser-test.html` on the physical phone, wait for its automated
  contract result, and complete the displayed shelf/audio, keyboard/safe-area,
  and event-driven-race observations. Record the device fields below before a
  reviewer can treat T9's real-phone `Done when` as satisfied.

| Date | Deployment SHA | Phone | Browser/version | Shelf/audio | Keyboard/safe area | Chips and real-event race | Result |
|---|---|---|---|---|---|---|---|
| Pending physical-device operator run | — | — | — | — | — | — | Not yet claimed |

## Checks

* `node --check static/app.js static/browser-test.js` — PASS
* `go test ./... -race` — PASS
* `go vet ./...` — PASS
* `gofmt -l .` — clean
* `go test ./static/race -race` — PASS
* `cmp -s static/race/race.html static/race/index.html` — PASS
* `git diff --check` — PASS

**REMEDIATED — H1–H2 and M1–M3 have runnable behavioral pins. M4 now has its
reproducible phone probe and an explicit, non-fabricated pending device record;
the physical-device row must be completed before T9 can satisfy its `Done
when`.**
