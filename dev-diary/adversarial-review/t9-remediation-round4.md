# T9 round 4 — remediation

The two round-4 findings were remediated within the T9 seam, with the
authorized narrow `internal/interview` replay contract correction for M1.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `static/app.js` (`unlockAudio`, `play`); `static/browser-test.js` (`FakeAudio`, `testAudioUnlockContract`) | The shelf gesture attempted to play an `Audio` element with an empty source, so it did not establish a real mobile autoplay unlock and could falsely imply that later question audio would play automatically. | The shelf gesture now plays a valid, silent PCM WAV data URL while muted, waits for that gesture-time playback to settle before marking the shared player unlocked, then reuses the same player for question audio. Failed unlocks retain the speaker control as the cold-link tap affordance. | `static/browser-test.js:testAudioUnlockContract` makes `FakeAudio.play()` reject empty sources, asserts the shelf gesture plays the non-empty WAV source, delivers a later `question_audio` event, and asserts the control remains `Listen again` while `/media/audio` is played. `node --check static/app.js static/browser-test.js` passes. | Restore `new Audio()` with no source and immediate `pause()`; the fake rejects the unlock and the automatic question-audio assertion fails. |
| M1 | `internal/interview/http.go`, `internal/interview/turn.go`, `static/app.js`, `static/browser-test.js` | The opening question could publish before the 201 response exposed `events_url`; transcript text alone could not restore its chips for a clean tab. | Retain the latest question metadata (turn, text, chips, and optional audio URL) with the live interview session, expose it as `current` in the existing `GET /interviews/{id}` catch-up payload, and include a best-effort `opening` replay in the existing POST start response when the opening job has already completed. The UI consumes the POST replay when present and otherwise consumes the authoritative catch-up `current` before the legacy session cache fallback. | `internal/interview/http_test.go:TestTranscriptRouteServesTheCatchUpShape` waits for the opening turn over the HTTP seam and asserts the catch-up JSON contains turn 1, opening text, and both chips. `static/browser-test.js:testCatchupAndAudio` starts with an empty session cache and a `current` replay, then asserts the tappable chip survives. `go test ./internal/interview -race` passes. | Remove `current` from the catch-up response (or return only role/text); the clean-session endpoint/UI probes render no opening chip while the post-201 hand-off test still passes. |

## Checks

* `go vet ./...` — PASS
* `go test ./... -race` — PASS
* `test -z "$(gofmt -l .)"` — PASS
* `node --check static/app.js static/browser-test.js` — PASS
* `git diff --check` — PASS

The automated browser contract is present in `static/browser-test.js`; no
CUA browser surface was available in this environment for a live UI run.
