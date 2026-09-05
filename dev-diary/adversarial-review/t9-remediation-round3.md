# T9 round 3 — remediation

Round 3's three medium findings were audited against the T9-owned seam. M1
and M2 have executable browser-contract pins. M3 is deferred live
verification: it remains an operator-only acceptance requirement because this
environment has no real or remote phone. The probe and its exact missing
evidence are recorded without claiming a device run.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| M1 | `static/app.js` (`Byline`, `subscribeInterview`, `openInterview`); `static/browser-test.js` (`testOpeningQuestionHandoff`) | The opening EventSource was created only after the POST response had been handed to the route component, leaving a route-transition window in which the first live question and its chips could be dropped. | Create and attach a buffered EventSource immediately when the 201 returns, before navigating to the interview component. Adopt that same source in `Interview` before issuing the catch-up read; queued `question`, `question_audio`, `ended`, and `error` events retain their order and payload. The backend transcript still carries opener text only, so a provider event that was published before the 201 exists cannot be reconstructed within T9's permitted paths; that residual requires a T4 contract change to persist/replay chips. | `static/browser-test.js:testOpeningQuestionHandoff` uses a clean session and schedules the opener while the source is in the hand-off buffer; it asserts the recovered interview renders the offered `A fox` chip. `node --check static/app.js static/browser-test.js` passes. | Remove `pendingInterviewStreams`/`subscribeInterview` and return to opening the source in `Interview`'s effect; the pre-adoption opener dispatch loses its chip and the browser contract fails. |
| M2 | `static/app.js` (`Interview` catch-up effect); `static/browser-test.js` (`testEndedCatchupClosesInterviewStream`) | A catch-up response reporting `status:"ended"` or a persisted closing turn set `ended` but left the interview EventSource open while generation subscribed to the book stream. | Close `stream.current` in the terminal catch-up branch before setting `ended`; generation then owns the only active stream. | `static/browser-test.js:testEndedCatchupClosesInterviewStream` retains the first FakeEventSource, waits for generation to begin, and asserts the interview source has `closed === true`. | Remove the terminal catch-up `stream.current?.close()`; the lifecycle assertion fails while generation still starts. |
| M3 | T9 physical-phone acceptance; `static/browser-test.html:7-15`; this record | The required physical-phone smoke is deferred live verification. The pending checklist is preparation, not device evidence. | Read-only availability probe on 2026-09-06: CUA reported no browsers/tabs; `adb devices` reported no attached devices; `command -v adb` and `command -v firefox` found host tools only; `curl https://thutapi.nryn.dev/static/browser-test.html` returned HTTP 404, so no deployed browser-contract endpoint was available. No emulator, desktop responsive mode, or checklist result is claimed. | Authoritative live probe is `static/browser-test.html` over the deployed HTTPS origin, followed by the three listed observations (shelf/cold-link audio, keyboard/safe area, chips and event-driven race) and a row containing date, SHA, phone, browser/version, and pass/fail. It was unavailable here because there was no device/remote surface and the current HTTPS deployment does not expose the probe. | Remove the deferred-verification record once the operator captures the required device evidence; the live acceptance remains pending until then. |

## Checks

* `node --check static/app.js static/browser-test.js` — PASS
* `go vet ./...` — PASS
* `test -z "$(gofmt -l .)"` — PASS
* `git diff --check` — PASS
* `go test ./... -race` — FAIL only at the pre-existing dirty T10g fixture `internal/bookvideo.TestRender_RealFFmpeg`: `bookvideo: invalid input: page 1 has no words`; all other packages pass.

No commit was made.
