# T9 round 4 — adversarial re-review of the frontend shell and interview UI

| | |
|---|---|
| **Target** | T9 — Frontend shell and interview UI (`dev-diary/PLAN.md` §T9), after `t9-remediation-round3.md`. |
| **Reviewer** | Fresh independent review agent (`gpt-5.6-terra`), 2026-09-06. Not an implementation or remediation agent for this track. |
| **Owns audited** | `static/**`, `internal/web/**`, T9's page/static route lines, the expressly authorized narrow T4 byline repair, and the narrow T9a iframe bridge/theme hand-off. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 1 × M, 0 × L** |

## Scope and verification

The post-201 hand-off buffer from round 3 is present and correctly adopts its
one EventSource before the transcript read. The terminal catch-up path closes
the interview source before it starts generation. Distinct valid page numbers,
not their ordinals, drive the T9a iframe; the iframe bridge is same-origin,
bounded to 0–8, and the two T9a copies remain identical. The no-T13 path
correctly bypasses screen 4 rather than advertising inert voice controls.

Read-only checks at review time:

| Gate | Command | Result |
|---|---|---|
| Full Go suite and race detector | `go test ./... -race` | PASS |
| Focused T9 dependencies | `go test ./internal/interview ./internal/web ./static/race -race` | PASS |
| Static checks | `go vet ./...`; `node --check static/app.js static/browser-test.js`; `test -z "$(gofmt -l .)"`; `git diff --check` | PASS |

The previously reported `internal/bookvideo.TestRender_RealFFmpeg` dirty-T10g
fixture failure did not reproduce in this review: the complete suite passed.
That fixture is outside T9's owned seam in any event, so it is not a T9
finding. Physical-phone deployment evidence remains deferred operator work by
the explicit user decision; the pending record accurately makes no device
claim, and its absence is not reissued here as a T9 code-closure finding.

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (minimal reverting edit) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:36-46, 331`; `static/browser-test.js:11-16, 141-163` | The shelf gesture does not successfully unlock its sole `Audio` element. `unlockAudio` calls `play()` on a newly constructed element with no `src`, immediately calls `pause()`, and swallows the rejected/aborted promise. There is therefore no decodable media playback inside the gesture. When `question_audio` arrives later, its non-gesture `play()` may still be rejected, reducing the intended automatic spoken question to the cold-link fallback tap. The current fake always resolves `play()` and never checks a source, so it certifies the opposite of the browser contract. This is a code defect distinct from the deferred physical-phone evidence. | Make the audio fake reject `play()` while `src` is empty, then click the shelf CTA and deliver a `question_audio` event. Assert the CTA loads a committed silent playable asset (or equivalent decodable source), waits for that gesture-time play before pausing, and the later question plays without rendering `Tap to listen`. Current `unlockAudio` has an empty source and fails the first assertion. | After adding a decodable silent unlock source and an assertion for successful gesture-time playback, restore `new Audio()` with no source plus immediate `pause()`; the automatic-first-question assertion fails. |
| M1 | M | `internal/interview/http.go:77-127`; `internal/interview/turn.go:199-215`; `internal/stream/broker.go:115-121`; `static/app.js:85-94, 127-135, 234-244` | The round-3 buffer eliminates only the interval after the 201 is received. `startWithByline` starts the opening job before it returns that 201; a fast M3 response can persist and publish `question` while the broker has no subscriber. The broker is explicitly from-now-on and transcript turns persist only role/text. A clean tab then subscribes too late, and `recoverQuestion` reconstructs the opener as `{chips: []}`. The current question text survives, but its chips—and hence the tap-first opening—are lost under a valid pre-201 ordering. This cannot be repaired inside T9. The required T4/T3 contract edit is to persist the current interviewer question's offered chips with its transcript turn and return them, with its turn number, in `GET /interviews/{id}` (or provide an equivalently durable replay of the full `question` event); T9 can then recover those chips after subscribe-then-catch-up. A hand-off buffer alone cannot recover an event published before the response exposes `events_url`. | In an `internal/interview` integration test, gate the HTTP response after `startWithByline` has scheduled the opening job; let that job persist/publish its question before opening `/events`, then perform subscribe-then-`GET /interviews/{id}` from a clean session. Assert the authoritative catch-up payload contains the opener's `turn`, `text`, and chips, and a T9 browser test renders a tappable chip without `sessionStorage`. Current transcript contains only `{role,text}`, and the client renders zero chips. | After adding the durable chip-bearing transcript/replay contract, remove the persisted chips (or return only role/text); the clean-session pre-201 opener probe fails while the existing post-201 hand-off test still passes. |

## Boundary, safety, and prior-round residue audit

* Round-1 error recovery, same-document routing, local vendored Preact/htm,
  token palette, T9a consumption, and the no-inert-T13 boundary remain intact.
  The actual plan permits the direct no-sample path while T13 has no mount, so
  it is not a screen-4 regression.
* Round-2's not-found recovery, distinct approval counting, live-wins-equal
  catch-up ordering, and real child-iframe `--done` probe remain addressed.
* Round-3's terminal catch-up cleanup remains addressed. Its opener hand-off
  test proves events that arrive after the 201 and before component adoption;
  it does not and cannot cover the earlier server-side interval described in
  M1.
* The server-rendered shell remains present, API/page routes remain separate,
  and audited child-controlled values go through `html/template` or Preact
  escaping. No untrusted HTML sink, CDN dependency, route collision, or new
  T13-boundary/XSS defect was found.

## Zero-residue claim

All named findings from rounds 1–3 are materially addressed in the current
tree, except that round 3's opening-event issue has residual pre-201 ordering
not covered by its client-only hand-off fix (M1). The new audio finding (H1)
also remains. The deferred physical-phone operator evidence is recorded
accurately and is intentionally outside this code-closure verdict.

**VERDICT: REMEDIATE (0/1/1/0).**
