# T4 round 1 — remediation

| | |
|---|---|
| **Target** | All 7 findings (1 × H, 3 × M, 3 × L) in `dev-diary/adversarial-review/t4-round1.md`. Verdict was REMEDIATE (0C/1H/3M/3L). |
| **Date** | 2026-09-05 |
| **Commit** | Uncommitted at remediation time; the working tree stays uncommitted — the orchestrator commits on APPROVE (AGENTS.md §Process step 5). |
| **Round file** | `dev-diary/adversarial-review/t4-round1.md` is unchanged. PLAN.md is untouched. |
| **Verification methodology** | H1's rework landed first and the full interview suite re-run green before the remaining fixes (M2/M3/L5/L6 share the turn flow). Every reviewer probe converted into a permanent pin and re-run green under `-race -count=1`; each of the seven reviewer-prescribed mutations applied by hand, observed RED for the recorded reason, tree restored byte-identical (sha256 against a pre-mutation snapshot), pin green again. Gate outputs in the aggregate section below; full suite green twice back-to-back as the flake check. Everything ran locally against `httptest`/scripted fakes; no live GMI or network calls. |
| **Scope note** | `internal/interview/**` and this file. `cmd/thutapi/main.go` and `main_test.go` untouched — no `newServer` signature change (the seam is route lines only, per the round file's own boundary). `internal/stream`, `internal/store`, `internal/job`, `internal/gmi/*` untouched. |

## Rows

### H1 — Stall rule ends the interview on two substantive one-word answers

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `internal/interview/http.go` — `answer`'s bookkeeping and end decision (streak/`stallStreak`/`lastAnswer` update, `switch` at now :171-175); `internal/interview/prompt.go` — new `stallDirective` (:64-73) and `buildMessages(history, ending, streak)` (now :102-121); `internal/interview/turn.go` — `runTurn` reads the streak under `s.mu` and feeds it to the Chat call (now :129-141); `internal/interview/interview.go` — session fields (now :315-317) and the package doc's stall paragraph. |
| **What was done** | The spec's words are now literal: "a stall or a **repeated** one-word answer ends it early". `lowEffort` is unchanged — it stays the streak signal, exactly as the round's mutation anticipated ("keep the streak") — but the streak **alone never ends anything**. The end fires only on no-progress: (a) `stallStreak >= 2` — two consecutive genuine stall answers (`stallAnswer`, a new predicate over the existing `stallWords`), or (b) `repeated` — a low-effort answer whose normalised form equals the previous answer's (chip taps exempt both, as before), or (c) MaxTurns. "Mira" then "red" (distinct one-worders) raises the streak but never ends; "idk" then "idk" ends; "i dunno" then "idk" ends (two stalls, distinct words — the existing `TestInterviewEndsEarlyOnStall` semantics preserved). **The fairness half of the fix:** the streak is surfaced to the model every turn — `stallDirective(streak)` travels as a system message appended after the transcript on every non-ending call with streak ≥ 1, and the system prompt gains the matching house rule ("short answers are answers … never end the interview because of short answers alone") — so the model can offer chips before any end can fire, closing the round's "the streak is server-side only" gap. Package doc's stall paragraph rewritten to match. **Recorded choice:** the applied mutation is the round's second option ("re-widen the answer streak condition to any one-word answer"), because the fix site is the end condition in `answer`, not `lowEffort`'s fallthrough. |
| **Test added** | `TestStallEndRequiresNoProgress` — the reviewer's F1 probe suite-ised, three subtests: "Mira"→"red" stays `open` with `stallDirective(1)` then `stallDirective(2)` on the wire as the last message of the answering calls (and the opening call carries **no** directive); "idk"→"idk" ends with reason `stall`; "red"/"blue"/"green" all stay `open` with the streak signalled through call 4. `TestStallAnswer` — unit table splitting genuine stalls ("i dunno", "IDK!", "hmm...", "no") from substantive one-worders ("Mira", "red"), which are low-effort but never stalls. |
| **Pin re-run status** | Green under `-race -count=1`, alongside the untouched `TestLowEffort` (its `{"blue": true}` row is correct: blue raises the chip signal) and the 17-case `TestParseReply` table. Mutation-checked (see table below). |

### M2 — A model "end" with empty reply text publishes `ended` with no goodbye and no closing turn

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/interview/turn.go` — `closeTurn` (now :226-239), which every end path now goes through; `internal/interview/interview.go` — new `fallbackGoodbye` constant (now :136-142) and the `endedEvent` doc (turn.go now :27-30). |
| **What was done** | `closeTurn` no longer skips persistence when `rep.Text` is empty: the text falls back to `fallbackGoodbye` ("What a lovely story! Let's make your book." — child-facing per the T0 conventions: warm, short, no jargon), the closing turn is persisted **unconditionally**, and the `ended` event always carries a spoken goodbye. All three end paths now close restart-stable: the model's own "end" (with or without text), the stall/limit wrap-up call (with or without text), and the checklist enforcement (L6, below). The package doc's restart paragraph now states the invariant; the "one known gap" sentence is gone with the gap itself (leaving it would have misdescribed the behaviour — AGENTS.md §Go style). |
| **Test added** | `TestEmptyGoodbyeStillClosesTheInterview` — the F2 probe suite-ised, two subtests: an end-only control line (`[[filled: hero; end]]`, no text) and a stall wrap-up returning only a control line. Each asserts the `ended` text equals `fallbackGoodbye`, the last transcript turn is a `closing` turn carrying it, and a restarted Handler (fresh `Handler`, same store) rejects a new answer wrapping `ErrEnded`. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below). |

### M3 — A failed opening turn is unobservable to the client

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/interview/turn.go` — `turnFailed`/`failLocked` record the failure (now :247-262); `internal/interview/http.go` — `transcriptResponse.Error` (now :53-71), `transcript` (now :223-251), and the marker clear on a successful turn re-start in `answer` (now :204-207); `internal/interview/interview.go` — session field `turnErr` (now :320) and the package doc's new "# Turn failures" section (now :85-101). |
| **What was done** | The failure is now durable state, per the contract's mechanism ("persist the error into the catch-up state like turns are, then clear it on successful re-start of the turn"): every turn-failure path stores the **machine class** (`errClass(err)`) on the session; the transcript route surfaces it as `transcriptResponse.Error` (`"error,omitempty"` in JSON) with status deliberately still `open` — the interview is recoverable; the child may answer again; the marker clears the moment the next turn successfully starts. This is the replay-to-late-subscribers half of the fix: a client that connects after the failure follows the package's documented catch-up pattern (subscribe first, then GET the transcript) and reads the failure from the authoritative state — exactly how a missed question event is replayed. **Recorded choice:** the SSE event itself cannot be replayed to a late subscriber — `internal/stream` deliberately has no replay (`Subscribe`'s and `ServeTopic`'s doc comments say so: "there is no replay … replay would duplicate that mechanism") and is outside this track's ownership — and the round-1 pin's own assertion reads "reaches a post-start subscriber **/** the catch-up state", of which the catch-up half is what the platform supports; the recovery path the finding called undiscoverable is now stated in the package doc ("# Turn failures") and testable end to end. The already-subscribed-client path is unchanged: the live `error` event still fires, now carrying a machine class (M4). |
| **Test added** | `TestOpeningTurnFailureIsObservable` — the F3 probe suite-ised: `errAt = 1` fails the opening call; start still returns 201 (the turn runs off the request); the catch-up read reports `error: "internal"` with `status: "open"` and 0 turns (polled deterministically via the new `waitTranscriptError` helper); a recovery answer returns 202, the marker is cleared the moment the turn re-starts, and the question lands over SSE. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below). |

### M4 — Error payloads carry internal prose where the doc promises "a machine class"

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/interview/turn.go` — the class tokens and `errClass` (now :36-66), `errorEvent`'s doc (now :68-76), `fail` (now :91-97); `internal/interview/http.go` — `writeError` (now a method, :306-316) and `statusForClass` (:318-332); `internal/interview/interview.go` — the package doc's SSE schema block (now :61-84). |
| **What was done** | Every wire error is now a stable token derived from the sentinel: `invalid` (ErrEmptyAnswer, ErrBadBody → 400), `ended` (ErrEnded → 409), `busy` (ErrBusy → 409), `not_found` (store.ErrNotFound → 404), `internal` (everything else, including `gmi.ErrTransient` and `job.ErrLimit` → 500). `errClass` is the single source for both the token and — via `statusForClass` — the HTTP status, so the status mapping and the wire vocabulary cannot drift. `fail` and `writeError` publish/log exactly this split: the full prose chain goes to `h.log`, the token goes on the wire. `writeError` became a method (`h.writeError`) for one reason: the round says "keep the prose in the log (it already goes there via `h.log`)" — true for the SSE path, false for the request surface, which previously logged nothing; now both surfaces log the prose. Unexported signature change; no caller outside the package. The `errorEvent` doc and the package doc's SSE block name the exact token set — the schema T9 codes against. |
| **Test added** | `TestErrorBodiesCarryMachineClass` — the F5 probe suite-ised, covering four of the five classes on the wire plus the log half: a gated opening turn makes a racing answer 409 `error=busy`; a failing Chat call makes the SSE `error` event carry `error=internal` while the log buffer contains the `errChat` prose; the model's end makes a further answer 409 `error=ended`; an unknown id GET is 404 `not_found`; a broken-JSON POST is 400 `invalid` with the `ErrBadBody` prose in the log buffer. `TestHTTPStatusMapping` — the existing test, retargeted from prose substrings ("not valid JSON", "empty", "not found") to exact machine classes (`invalid`, `invalid`, `not_found`, `not_found`): the changed contract breaks the old assertions by the finding's own argument, and the new ones assert the same four routes' status+class pairs. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below): the mutated tree's bodies carry the round's quoted prose chains verbatim. |

### L5 — A rolled-back answer leaves its stall-streak point behind

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/interview/http.go` — `answer` snapshots the counters before the bookkeeping (now :153) and restores them on **both** rollback paths: the store-failure return (now :179) and the job-refusal rollback (now :198). |
| **What was done** | The reviewer's asymmetry closed over all three counters the bookkeeping touches — `streak`, the new `stallStreak`, and `lastAnswer` — not just the round-1 `streak`: a rolled-back answer takes its whole stall-bookkeeping point back, on both the store-failure path (which round 1 noted had "the same asymmetry") and the job-refusal path. The existing `exchanges` restore is untouched; `TestJobStartFailureRollsBackTheAnswer` and `TestStoreFailuresSurfaceAsSentinels` run green unchanged. |
| **Test added** | `TestStreakRollsBackWithTheAnswer` — the F4 probe suite-ised, two subtests, both discriminating on the synchronous `answer` response status (deterministic, no end-event race): after a refused/failed "hmm", the first processed "i dunno" reports `statusOpen` (under the mutation: `"ending"`), with a `flakyRunner` (refuses the nth `Start`, then delegates) for the job path and the harness's `testStore.failUpd` for the store path. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked per path — each restore line deleted separately turns its own subtest red (see table below). |

### L6 — The server-enforced checklist end reopens after a restart

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/interview/turn.go` — the enforcement branch of `questionTurn` (now :206-217) now calls `closeTurn(ctx, s, iv, reply{}, ReasonChecklist)`. |
| **What was done** | The enforcement close goes through the same `closeTurn` as every other end path, with an empty reply: the fallback goodbye is persisted as the closing turn and the `ended` event carries it — one close path, one event shape, no special-casing. The question still lands first (transcript content and often a confirmation), exactly as before; only the end behind it is now durable. After this and M2, the package doc's restart semantics state the closed invariant and the "one known gap" paragraph is deleted. The contract's note — "this also closes L6" — is the M2 row's mechanism applied to the enforcement branch; both findings share `TestEmptyGoodbyeStillClosesTheInterview`'s machinery and both are pinned independently below. |
| **Test added** | `TestEnforcedEndSurvivesRestart` — the F6 probe suite-ised: the model reports all six slots without "end"; the question lands (turn 3), `ended` follows with reason `checklist` and the fallback text, the last transcript turn is the `closing` turn, and a restarted Handler reports `statusEnded` and rejects a new answer wrapping `ErrEnded`. |
| **Pin re-run status** | Green under `-race -count=1`. Mutation-checked (see table below). |

### L7 — Exported `Slot` constants lack the per-name doc comments the package's own const blocks carry

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/interview/reply.go` — the `Slot` const block (now :15-29). |
| **What was done** | Per-name doc comments added to `SlotHero`…`SlotEnding`, each starting with its own name (AGENTS.md §Go style), matching the sibling `Role*` and `Reason*` blocks; the intra-package drift is closed. The group comment stays. Doc-only; no behaviour, value, branch or signature touched. |
| **Test added** | None — doc-only, per the round-1 file's own pin column: "reviewer inspection (mechanical rule; no behavioural probe applies)". `go vet` and `gofmt` cannot catch this rule, which is precisely why AGENTS.md writes it down. |
| **Pin re-run status** | Rule check passes by inspection after revert. Mutation-checked by the same method: with the comments deleted, `go vet`/`gofmt` stay green (no mechanical gate exists) and `go doc ./internal/interview SlotHero` shows the bare group comment with no per-name docs — the rule failure the reviewer recorded; restored byte-identical. |

## Mutation check (applied by hand, observed red, reverted byte-identical, pin green again)

| Mutation | Pin that goes red |
|---|---|
| H1: end condition re-widened to `case s.streak >= 2 \|\| repeated:` | `TestStallEndRequiresNoProgress/substantive_one-word_answers_stay_open_with_the_chip_signal` (answer "red" → `status: ending`, want "open") and `/three_distinct_one-word_answers_stay_open_and_signalled` ("blue" → ending) — the demo's happy-path answers end the interview again |
| M2: `closeTurn`'s empty-text path reverts to publish-without-persist (`s.ended = true` + bare `ended` publish, no closing turn) | `TestEmptyGoodbyeStillClosesTheInterview` both subtests (`ended text = <nil>`, want the fallback goodbye) and `TestEnforcedEndSurvivesRestart` (same symptom via the shared close path) |
| M3: `s.turnErr = errClass(err)` deleted from `turnFailed` | `TestOpeningTurnFailureIsObservable` (the catch-up read never reports a failure — timed out waiting for the recorded turn failure) |
| M4: `writeError` payload back to `err.Error()` | `TestErrorBodiesCarryMachineClass` (racing answer body = `interview: answer …: interview: a turn is already in progress`, want `busy`) and all four `TestHTTPStatusMapping` rows (the round's quoted prose chains: "request body is not valid JSON: invalid character 'n' …", "answer text is empty", "not found") |
| L5: first counter restore (store-failure path) deleted | `TestStreakRollsBackWithTheAnswer/store_failure` (processed stall reports `"ending"`, want `"open"`) |
| L5: second counter restore (job-refusal rollback) deleted | `TestStreakRollsBackWithTheAnswer/job_refusal` (same symptom — each restore line proven load-bearing for its own path) |
| L6: enforcement branch reverts to `s.ended = true` + bare publish | `TestEnforcedEndSurvivesRestart` (`ended text = <nil>` — no goodbye, and no closing turn behind the question) |
| L7: per-name `Slot` comments deleted | No mechanical gate exists (`go vet`/`gofmt` green — the rule is AGENTS-only, exactly as the round-1 file recorded); `go doc ./internal/interview SlotHero` shows the consts with no per-name docs — rule check fails by inspection |

Every mutation was reverted from a pre-mutation snapshot and the file's sha256 verified identical to the pre-mutation value; the affected pin re-ran green after the revert.

## Residue against round 1

**Zero.** Each of the seven findings has a row above following its mutation's letter, with two recorded choices stated in place: (1) H1's fix site is the end condition in `answer` plus the new chip-signal directive, so the applied mutation is the round's second-named option; `lowEffort` itself is deliberately unchanged — it is the streak signal, and the existing `TestLowEffort` table (including `{"blue": true}`) is correct as written. (2) M3's replay rides the documented catch-up GET (subscribe first, then read the transcript), the platform's stated no-replay design and the pin's own "subscriber / the catch-up state" alternative — the event-based alternative would have required editing `internal/stream`, which is outside this track's ownership. The round-1 pins that already passed (`TestProbePass*` concerns: child control tokens stored verbatim, exactly-one-winner busy race, topic isolation) are covered by the unchanged suite — `TestAnswerWhileTurnInFlightIsBusy`, `TestRestartedHandlerSeesEndedInterview`, the checklist run — all green. The thinking pin `TestInterviewTurnsOmitThinkingOnRawWire` is green, untouched. `TestInterviewEndsEarlyOnStall` and `TestChipTapResetsStallStreak` are green **unchanged** — the H1 fix ends genuine stalls ("i dunno" ×2) exactly as before, per the round's exit criterion 1.

## Aggregate verification

```
$ go vet ./...                       clean
$ gofmt -l .                         empty
$ CGO_ENABLED=0 go build ./...       ok
$ go test ./... -race -count=1       ok — all packages, twice back-to-back (flake check)
$ go test ./... -cover
interview     93.4%  (floor 75%; was 93.1%)
cmd/thutapi   84.8%  (floor 75%)
gmi/media     92.5%  (floor 85%)
gmi/text      90.4%  (floor 85%)
job           95.7%  (floor 75%)
mediastore    95.6%  (floor 75%)
store         86.9%  (floor 75%)
stream        94.4%  (floor 75%)
```

Exit-criterion e2e tests re-run by name, green under `-race`: `TestInterviewRunsToEndOnChecklist` (checklist-filled, ordered transcript, book link), `TestInterviewEndsEarlyOnStall` (stall-early + goodbye directive), `TestDefaultMaxTurnsEndsTheInterview` (default MaxTurns), `TestEventsRouteStreamsSSEOverHTTP` (SSE over HTTP), `TestChipTapResetsStallStreak`.

## Files touched

* `internal/interview/reply.go` — `stallAnswer`; `lowEffort` doc; per-name `Slot` comments (L7)
* `internal/interview/http.go` — stall bookkeeping + end condition (H1); counter rollback (L5); `transcriptResponse.Error` + `transcript` (M3); `h.writeError` + `statusForClass` (M4)
* `internal/interview/turn.go` — class tokens + `errClass` + docs (M4); `closeTurn` fallback + enforcement via `closeTurn` (M2/L6); `turnFailed`/`failLocked` recording (M3); streak feed into `runTurn` (H1)
* `internal/interview/prompt.go` — `stallDirective`, `buildMessages` streak parameter, system-prompt house rule (H1)
* `internal/interview/interview.go` — package doc (stall rule, SSE schema, "# Turn failures", restart semantics); `fallbackGoodbye`; `ReasonStall` doc; session fields (all rows)
* `internal/interview/interview_test.go` — `flakyRunner`, `waitTranscriptError`, five pin tests
* `internal/interview/http_test.go` — `TestHTTPStatusMapping` retargeted to machine classes (M4's contract change)
* `internal/interview/reply_test.go` — `TestStallAnswer`
* `dev-diary/adversarial-review/t4-remediation-round1.md` — this file.
