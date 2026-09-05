# T9 round 2 — adversarial re-review of the frontend shell and interview UI

| | |
|---|---|
| **Target** | T9 — Frontend shell and interview UI (`dev-diary/PLAN.md` §T9), after `t9-remediation-round1.md`. |
| **Reviewer** | Fresh independent review agent (`gpt-5.6-terra`), 2026-09-06. Not the implementation or remediation agent. |
| **Owns audited** | `static/**`, `internal/web/**`, T9's page/static route lines, the explicitly user-authorized T4 byline repair, and the narrow T9a bridge/theme edits. |
| **Verdict** | **REMEDIATE — 0 × C, 2 × H, 4 × M, 0 × L** |

## Scope and verification

Round 1's opening-error handling, same-document shelf gesture, single
`Audio` allocation, token palette, T9a iframe consumption, and deterministic
browser contract page are present. The T4 change is limited to decoding and
trimming the optional `POST /interviews` byline before the opening turn; its
focused tests establish both a populated and body-less start, and no M3 prompt
change was found. The T9a changes retain byte-identical `race.html` and
`index.html`, but their new runtime bridge remains independently defective or
unproven as noted below.

Read-only checks passed:

| Gate | Command | Result |
|---|---|---|
| Go suite, including race detector | `go test ./... -race` | PASS |
| Vet, formatting, whitespace | `go vet ./...`; `test -z "$(gofmt -l .)"`; `git diff --check` | PASS |
| Browser module syntax | `node --check static/app.js static/browser-test.js` | PASS |
| Race static contract | `go test ./static/race -race` | PASS |

These checks do not exercise an out-of-order real `page_approved` sequence, a
live-SSE-before-catch-up sequence, the iframe's receiving `postMessage`, a
terminal catch-up HTTP error, or a real phone. The available environment has no
browser surface, so no physical-device claim is made here.

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (minimal reverting edit) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:236` | Screen 5 treats a page number as a number of pages approved: `Math.max(current, event.n)`. T6/T7 fan pages out concurrently, so page 8 can be the first real approval. One approval then tells the T9a widget `--done:8`, lights the whole race, and claims all pages are complete. The contract requires one marker **per approval**, not the greatest page ordinal received. | In the browser contract, dispatch `page_approved {n:8}` followed by `{n:1}` and assert race titles `1 of 8` then `2 of 8`. Current code produces `8 of 8` for both. `internal/illustrate/illustrate.go:64-67,451-454` establishes that completion order is intentionally concurrent. | After correcting the state to count distinct approved `n` values, restore `setDone(current => Math.max(current, event.n))`; the out-of-order probe fails. |
| H2 | H | `static/app.js:189-207, 247` | A failed catch-up read remains a dead end. A cold `/interview/missing` has an SSE error and `GET /interviews/missing` returns the typed `not_found` JSON, but the catch swallows that class, renders a generic warm sentence, and leaves only Send. Every Send then posts to the same missing interview and fails again; there is no new-story or shelf door. This violates the requirement that an opening/catch-up failure be warm **and usable**. | Mock both the stream and `GET /interviews/missing` as 404 `{"error":"not_found"}`, then click Send. Assert an actionable new-story/shelf door exists and that a missing-id answer cannot be the sole recovery path. Current DOM has no link or button other than disabled/enabled Send, and the subsequent POST repeats the failure. | Restore the current generic catch that only calls `setNotice`/`setWaiting(false)` after adding the recovery door; the 404 catch-up probe fails. |
| M1 | M | `static/app.js:144-153, 186-203` | The subscribe-then-catch-up race is still not safely deduplicated. If the live `question` lands before the transcript fetch resolves and `sessionStorage` is unavailable/empty, `applyQuestion` has the live chips, then `applyRecoveredQuestion` accepts the equal turn and replaces it with `recoverQuestion`'s empty chips. The child is forced to type despite a valid tapped choice. Equal turns need to preserve the live authoritative question; only a catch-up turn that is not already live should populate state. | Make the transcript promise deferred; dispatch `question {turn:1,chips:["A fox"]}` before resolving it with the matching interviewer turn while `sessionStorage` throws or is empty. Assert the chip remains. Current code renders zero chips after the fetch resolves. | After fixing the equal-turn guard/merge, restore `q.turn < activeTurn.current` and unconditional recovered replacement; the ordering probe fails. |
| M2 | M | `static/app.js:107-110, 245-247` | The grown-up screen exposes record and upload controls when T13 has mounted nothing, but neither records nor uploads: Record only changes local text and Upload only selects a file, while the only working path is Skip. This is a broken adult-facing promise and contradicts the declared seam: T9 owns the step and mount point, T13 owns both capture modes, and **with nothing mounted screen 4 does not render at all**. The shell must either omit screen 4 until T13 registers a capture mount, or present only mounted working controls; it cannot ship inert capture affordances. | Run the ended-interview path without a T13 mount, click Record, or select a file. Assert no fake completion/capture control is shown and that the flow either bypasses screen 4 or contains a mounted T13 implementation. Current UI says capture is available “when the voice tool is ready” and stalls except for Skip. | Restore the current unconditional `AdultStep` and mode-only handlers after a mount-presence/bypass test is added; the test fails. |
| M3 | M | `static/app.js:69-77`; `static/race/race.html:375-387`; `static/browser-test.js:96-99`; `static/race/race_test.go` | The closed T9a widget was changed to add a same-origin `postMessage` bridge, but no executable test proves that the parent’s real iframe message changes the child `.race` `--done`. The browser contract only observes the parent iframe `title`; the Go test only searches source and compares the duplicate files. The change may be warranted to preserve the one-number seam across an iframe, but this untested cross-track behavior is precisely the hand-off T9 must prove. | Load the real race iframe, dispatch a parent `page_approved`, and assert `iframe.contentDocument.querySelector('.race').style.getPropertyValue('--done')` changes from `0` to the count. No existing test can fail if the receiver script is removed while the title update remains. | Remove the receiver's `message` listener (leaving the parent title logic) after adding the iframe probe; all current tests stay green and the new probe fails. |
| M4 | M | T9 `Done when`; `static/browser-test.html`; `t9-remediation-round1.md` | The track still has no recorded real-phone acceptance result. Its own Done-when criterion is “an interview is completable by tapping, on a real phone”; the plan specifically says desktop emulation does not prove keyboard or iOS audio behavior. The remediation accurately disclaims that evidence, but deterministic mocks cannot close this acceptance criterion. | Record a fresh physical-phone smoke from a clean checkout: shelf CTA unlocks audio, question audio plays or the cold-link speaker tap works, chips complete an interview, text input remains above the keyboard/safe area, and screen 5 moves only on events. No such transcript or executable device probe is in the track. | Remove the device-smoke record or its reproducible probe once added; the acceptance evidence is absent again. |

## Boundary, security, and remediation-residue audit

* Round-1 H1's valid-session opening failure now clears waiting and exposes an
  answer form. H2's one-allocation same-document unlock is implemented, and
  the cold-link speaker uses the same element with a gesture. H3 now has the
  intended mount-point shape, but M2 remains because T13 has not supplied a
  mount and the screen advertises non-working actions.
* The shell uses local vendored modules, Preact escaping, `html/template`
  path-value escaping, and no child-content HTML sink or CDN URL. No XSS issue
  was found in the audited T9 paths.
* `narration_unavailable` remains a warm successful state; `failed` retains
  two doors and a new generation POST. The interview and book EventSources are
  closed on their terminal events. H1 means the race nevertheless does not
  represent real progress.
* The T4 exception is warranted and tightly confined to optional byline
  persistence plus tests. The T9a edits are bounded to theme aliases and the
  bridge in identical copies, but M3 prevents treating their runtime hand-off
  as verified.

## Zero-residue claim

Round-1 H1–H3 and M2 are materially addressed, but the full original Done
when was re-opened and residue remains: H1–H2 and M1–M4 above. In particular,
the race no longer meets its real-event progress contract, catch-up has a
terminal dead end and a live/catch-up ordering regression, and real-phone
acceptance is still not evidenced.

**VERDICT: REMEDIATE (0/2/4/0).**
