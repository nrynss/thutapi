# T9 round 3 — adversarial re-review of the frontend shell and interview UI

| | |
|---|---|
| **Target** | T9 — Frontend shell and interview UI (`dev-diary/PLAN.md` §T9), after `t9-remediation-round2.md`. |
| **Reviewer** | Fresh independent review agent (`gpt-5.6-terra`), 2026-09-06. Not an implementation or remediation agent for this track. |
| **Owns audited** | `static/**`, `internal/web/**`, T9's human-page/static route lines, the expressly authorized narrow T4 byline repair, and the narrow T9a iframe bridge/theme hand-off. |
| **Verdict** | **REMEDIATE — 0 × C, 0 × H, 3 × M, 0 × L** |

## Scope and verification

Round 2's distinct-page `Set`, typed not-found recovery, equal-turn
live-before-catch-up preservation, deliberate no-T13 screen-4 bypass, and
real iframe receiver probe are present. The byline change remains confined to
the question-zero contract: it trims an optional request value, writes it to
the book before the opening turn, and does not change the M3 prompt or the six
story slots.

Read-only checks passed:

| Gate | Command | Result |
|---|---|---|
| Go suite and race detector | `go test ./... -race` | PASS |
| Vet, formatting, and whitespace | `go vet ./...`; `test -z "$(gofmt -l .)"`; `git diff --check` | PASS |
| Race contract | `go test ./static/race -race`; `cmp -s static/race/race.html static/race/index.html` | PASS |
| Browser-module syntax | `node --check static/app.js`; `node --check static/browser-test.js` | PASS |

No browser or connected physical phone is available in this environment.
The static browser contract was read and its syntax checked, but its runtime
and the required device smoke are not claimed as having run here.

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (minimal reverting edit) |
|---|---|---|---|---|---|
| M1 | M | `static/app.js:56-64,196-215`; `internal/interview/http.go:77-127`; `internal/stream/broker.go` | A newly started interview can miss its very first live `question`. `POST /interviews` starts the opening turn before returning the id; `job.Runner.Start` immediately releases its goroutine, and the broker has no replay. If that turn publishes before `Byline.submit` receives the 201 and `Interview` opens its EventSource, the catch-up transcript supplies only text. `recoverQuestion` has no cached chips in this fresh session, so the question renders with zero tap choices and the child must type. This violates T9's tap-first interview contract under a valid ordering; the text box is only a workaround. | Make the opening chatter complete before the POST response is released, then complete question zero in a clean session with no `sessionStorage` entry. Assert the recovered first question has its offered chips and can be answered by tapping. The current client recovers `{chips: []}`. Existing T4 event-route testing deliberately gates the opening turn before subscribing, so it does not cover this inverse ordering. | After adding a durable chip-bearing catch-up contract or other ordering-safe hand-off, restore `recoverQuestion`'s empty-cache return and let the opening event publish before subscription; the clean-session tap assertion fails. |
| M2 | M | `static/app.js:196-225` | A terminal interview discovered by the catch-up read does not close the interview EventSource. When `GET /interviews/{id}` returns `status:"ended"` (or a persisted closing turn), the client sets `ended` and starts generation, but leaves `stream.current` subscribed until the page redirects. This contradicts the two-stream lifecycle — the interview stream ends at `ended` and screen 5 owns the separate book stream — and leaves a needless live SSE subscription during the entire generation wait. | Mock a cold ended interview, retain the first `FakeEventSource`, and wait for generation to start. Assert the interview source is closed before or when the book source opens. Current code leaves `eventSources[0].closed` unset. | Add the close in the terminal catch-up branch, then remove it; the lifecycle assertion fails while all non-terminal catch-up behavior remains unchanged. |
| M3 | M | T9 `Done when`; `static/browser-test.html:7-15`; `t9-remediation-round2.md:17-43` | Physical-phone acceptance remains genuinely outstanding. The manual checklist and its pending table are useful preparation, but they are not evidence that an interview was completable by tapping on a real phone, nor that the one-Audio gesture, cold-link speaker fallback, keyboard/safe-area layout, and real-event race work on the deployment. T9's own Done-when expressly requires that observation; desktop syntax/tests cannot satisfy it. | From a clean deployed SHA over HTTPS, run `/static/browser-test.html` on a physical phone and record date, SHA, device, browser/version, shelf/cold-link audio result, keyboard/safe-area result, chip completion, and real-event race result in the T9 record. The only existing row says `Pending physical-device operator run`. | Remove a completed device transcript or reproducible device probe after it exists; the acceptance evidence is absent again. |

## Boundary, safety, and prior-round residue audit

* Round-2 H1 is fixed at behavior level: only new valid page numbers enter the
  per-run `Set`, so page 8 followed by page 1 yields one then two markers.
  The real child iframe receives `--done: 2`; the bridge is not merely a source
  search. T9 consumes the T9a widget and changes only its documented number.
* Round-2 H2 is fixed for a terminal `not_found`: the stream is closed and the
  response has a new-story/shelf door with no repeat-failing answer form.
  A recoverable opening/turn error remains warm and usable through the answer
  form, which matches T4's persisted-error contract. M2 above is the distinct
  terminal-*ended* lifecycle case.
* Round-2 M1's live-before-catch-up equality case is fixed: a same-turn
  transcript cannot erase a live question's chips. M1 above is the opposite
  no-live-event ordering at first start, for which transcript data cannot
  reconstruct chips.
* Screen 4 correctly does not render before T13 supplies its complete mount:
  there are no inert record/upload controls, and the ended path takes the
  defined no-sample route. This stays inside the T9/T13 boundary.
* The one `Audio` allocation remains in the shelf gesture and cold links use
  the same element through a speaker gesture. The child-facing values render
  through Preact or `html/template`; no untrusted HTML sink, CDN dependency,
  or API/page route collision was found. Static serving is rooted at `static/`.

## Zero-residue claim

Round-1 H1–H3 and M1–M4, and round-2 H1–H2 and M1–M3, are materially
addressed by the present tree: opening/not-found failure recovery, same-document
audio, T13-boundary behavior, actual approval counting, equal-turn ordering,
and the iframe bridge all have behavioral coverage. Residue remains outside
those named fixes: M1 loses a fast opening question's chips before first
subscription; M2 leaks the terminal catch-up interview stream; and M3 is the
unmet required physical-phone acceptance, not a checklist-only condition.

**VERDICT: REMEDIATE (0/0/3/0).**
