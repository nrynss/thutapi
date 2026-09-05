# T9 round 1 — adversarial review of the frontend shell and interview UI

| | |
|---|---|
| **Target** | T9 — Frontend shell and interview UI (`dev-diary/PLAN.md` §T9). Uncommitted working tree based on `main` at `66dc151`. |
| **Reviewer** | Fresh independent review agent (`gpt-5.6-terra`), 2026-09-06. Not the implementation agent. |
| **Owns audited** | `static/**`, `internal/web/**`, and T9's route lines in `newServer`. The narrow `internal/interview/**` byline repair was audited as the explicitly user-authorized T4 cross-track repair. |
| **Verdict** | **REMEDIATE — 0 × C, 3 × H, 4 × M, 0 × L** |

## Scope and verification

The diff adds the three human routes, server-rendered shells, vendored Preact/
htm/hooks, the browser flow, CSS, and the user-authorized `POST /interviews`
byline persistence repair. The latter is narrowly necessary for T9's question
zero wire contract: it trims the optional byline, does not put it in the M3
prompt, preserves a body-less POST, and its new tests pass. It is not a finding.

Read-only checks passed:

| Gate | Command | Result |
|---|---|---|
| Go suite and race detector | `go test ./... -race` | PASS |
| New Go formatting / diff whitespace | `gofmt -l cmd/thutapi/main.go internal/interview/http.go internal/interview/http_test.go internal/interview/interview.go internal/web`; `git diff --check` | PASS |
| Browser-module syntax | `node --check static/app.js` and each vendored module | PASS |
| Contrast probe | Node relative-luminance calculation for white on `#e66d3d` | **3.17:1** (fails the required normal-text AA pair) |

The passing Go/template tests only establish that three small shells render.
They do not execute the Preact flow, SSE lifecycle, audio lifecycle, adult
step, race hand-off, or a phone viewport.

## Findings

| # | Sev | Where | What | Pin (failing probe or test) | Mutation (minimal reverting edit) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:68-75` | The mandatory catch-up failure state is ignored. `GET /interviews/{id}` deliberately exposes `error` because the opening question can fail before a client can subscribe; this UI only reads `turns` and `status`. With `{status:"open", error:"internal", turns:[]}`, it sets `waiting` true, renders the thinking line, and disables Send forever. There is no warm recovery door, violating the error/no-dead-end contract. | Reproduce the documented opening-turn failure (`internal/interview/interview_test.go:1109-1158`), then load its `/interview/{id}` page. The transcript JSON has `error:"internal"`; the UI has no branch containing `state.error` and leaves `<button disabled>` in the DOM. A browser integration test must assert a warm retry path for this exact payload. | Delete the eventual `state.error` recovery branch (or change it to leave `waiting` true); the new integration test must fail. |
| H2 | H | `static/app.js:12-20, 110`; `static/app.js:65-66` | Screen 1 does not actually provide the required reusable audio unlock. Its click handler merely constructs `new Audio()` on the shelf document and then the link navigates to `/interview/new`, destroying that JavaScript realm and `sharedAudio`. In the freshly loaded interview page it is undefined, so a normal `question_audio` event calls `play(url, false)`, returns false, and needs an extra speaker tap. The primary tablet path therefore never speaks questions automatically. | Browser probe: click `[data-start]`, complete question zero, then deliver `question_audio` for turn 1. Before a speaker tap, `sharedAudio` in the new document is unset and `play()` returns false; the UI offers "Tap to listen" rather than playing. A phone test must assert one CTA gesture enables automatic playback of the next question. | Remove the shelf click listener at line 110; behavior is unchanged after navigation, proving the listener is not the session audio unlock. |
| H3 | H | `static/app.js:105` | The required grown-up screen is absent. The ended state shows an inert `data-voice-capture` div and a single button that immediately spends on generation; it presents neither an explicit skip nor record/upload choices, much less the three equally weighted choices required by screen 4 and decision 20. T13 may mount capture implementation in this seam later, but T9 owns the screen and its skip/generate transition. | Load an ended interview and inspect the only actionable controls: `Start drawing my book` is present; no control or text for record, upload, or skip exists. A UI test must assert all three choices before a POST to `/generate`, with skip as the only one that advances immediately. | Replace the required screen-4 component with the current direct generate button; the test must fail because record/upload/skip disappear. |
| M1 | M | `static/app.js:41-44`; `static/app.css:1` | T9 reimplements the completed T9a race in Preact/CSS instead of consuming `static/race/**`, despite the explicit boundary "Consume it; do not rewrite it." The shipped markup is five emoji lanes and eight generated `<i>` elements, independent of T9a's tested asset/widget; future fixes to `static/race/` cannot affect screen 5. This also breaks T9a's one-number widget hand-off rather than using it. | Change a distinguishing T9a animal/marker in `static/race/race.html` and render screen 5: its output is unchanged because `Race` is private in `app.js`. A component-level test should assert that T9 renders the T9a widget and changes only its `--done` value. | Restore the private `Race` implementation and its local `.race` CSS after replacing it with the T9a widget; the hand-off test must fail. |
| M2 | M | `static/app.css:1` | The CSS ignores the settled, verified design system: it defines none of `--bg`, `--surface`, `--ink`, `--muted`, `--mint`, `--accent`, `--rule`, `--warm`, or `--film`, and hardcodes a different cream/terracotta palette throughout. In particular the labelled primary button is white on `#e66d3d` (3.17:1); at its declared 18px it is below the required normal-text AA contrast and violates the specified white-on-accent 5.93:1 pair. | `rg -n -- '--(bg|surface|ink|muted|mint|accent|rule|warm|film)' static/app.css` returns no token definitions; the recorded luminance probe returns `3.17`. A CSS contract test must require the token set and test button foreground/background against the documented pair. | Replace the tokenized palette and button colours with the current literal declarations; the CSS contract/contrast test must fail. |
| M3 | M | `static/app.js:68-75` | Catch-up does not meet the interview stream contract beyond the failure case. It reconstructs a persisted interviewer turn with `chips: []`, does not set `activeTurn.current`, and has no turn de-duplication. On a reload after a question event, the child loses all tappable choices; if that turn's delayed `question_audio` arrives after the new subscription, its real `turn` is rejected against `0`, so it is silently discarded. The server explicitly requires subscribe first, then read, and deduplicate by turn. | Serve a transcript ending in interviewer turn 1, subscribe, then publish `question_audio {"turn":1,"audio_url":"/media/a"}`. Current code executes `if (a.turn === activeTurn.current)` with `0` and creates no speaker. The same replayed transcript has no chip buttons. A browser SSE/catch-up test must assert chips and current-turn audio survive this sequence. | Reset `activeTurn.current` to zero and replace recovered chips with `[]` after the corrected catch-up implementation; the replay test must fail. |
| M4 | M | `internal/web/web_test.go:10-47`; `static/app.js`; `static/app.css` | T9 has no executable coverage of its load-bearing browser behavior and no recorded real-phone acceptance result, so its `Done when` claim is not reviewable. The sole new frontend test searches strings in templates; it cannot catch H1–H3, the T9a substitution, SSE reconnect/catch-up behavior, keyboard/safe-area behavior, or audio unlock. `node --check` only parses the modules. | Introduce each of the H1–H3 minimal mutations above: all current tests, including `go test ./... -race`, remain green. Add browser tests for the specified API/SSE sequences and a recorded real-phone smoke that verifies tap completion, keyboard visibility, and audio. | Revert the added browser/phone acceptance checks; the three behavioral regressions become green again under the present test suite. |

## Boundary, security, and contract audit

* The three singular page routes and the `html/template` interpolation of path
  values are correct. Template/Preact escaping keeps the audited child text and
  ids out of raw HTML sinks. No CDN URL, `innerHTML`, or second `Audio` object
  was found.
* The static handler and vendored module imports are local and parse cleanly.
* Screen 5 does use the real generation `events_url`, `page_approved`,
  `narration_unavailable`, `book_ready`, and `failed` event names; its warm
  narration-outage copy and two failure doors are directionally correct. The
  race implementation finding above prevents this from satisfying the T9a
  integration contract.
* The user-authorized T4 repair is limited to the `byline` request shape,
  `UpdateBook`, and tests. It is correct and no broader change to the closed
  interview behavior was found.

## Required remediation exit criteria

1. Implement a real screen 4 with record/upload mount points and an explicit,
   equally weighted skip; only skip may start generation.
2. Keep the session on a single document or otherwise establish an audio
   element that a user gesture truly unlocks and that survives into interview
   playback; prove automatic first-question playback on a mobile-capable
   browser.
3. Render recorded interview errors warmly with a usable recovery action, and
   implement the subscribe-then-catch-up/turn de-duplication contract so
   recovered chips and late current-turn audio work.
4. Replace the private race clone with the completed T9a widget, changing only
   its documented `--done` interface.
5. Apply the settled token palette and contrast pair; add browser-level tests
   for the API/SSE/audio/phone-critical paths and record the real-phone smoke.

## Zero-residue claim

This is round 1, so there are no prior round findings to carry. Residue is
present: H1–H3 and M1–M4 above. The reviewed T9 implementation is **not**
ready to mark DONE.

**VERDICT: REMEDIATE (0/3/4/0).**
