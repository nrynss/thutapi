# T4 round 1 — adversarial review

- **Tree:** uncommitted work on `main`, base `49eacc1` ("T3b closes at round 2"). Diff:
  `internal/interview/**` (9 files, new), `cmd/thutapi/main.go` (+route lines, +broker/job/interview
  wiring in `run`), `cmd/thutapi/main_test.go` (+`echoChatter`, +`TestInterviewRoutesServeThroughMux`).
- **Date:** 2026-09-05. Reviewer: T4Review1 (fresh agent; not the implementer).
- **Evidence:** AGENTS.md; PLAN.md §T4, §T5 (consumption), §T9 (chips/UI), §Architectural invariants,
  §Unowned seams, T0 conventions; project.md §Pipeline Phase A, §M3 phase settings; the review loop
  README (per-finding schema); the code and tests listed above; the consumed seams
  (`internal/stream`, `internal/job`, `internal/store/interviews.go`, `internal/gmi/text`) read at the
  surface T4 touches.
- **Method:** probe, don't trust prose. Every finding below was demonstrated with a throwaway probe
  (`internal/interview/zz_probe_test.go`, `-race`, deterministic scripted fakes — no live network),
  recorded in the Pins table, and removed at close; the thinking pin was mutation-tested
  (enable `thinking` → observe red → revert byte-identical). All five gates re-run by this reviewer.
  No fixes were applied; no formatters run; no git mutations.

## Gates (run for this review, current tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race -count=1` | ok, all packages |
| `gofmt -l .` | empty |
| `go test ./... -cover` | interview 93.1%, cmd 84.8%, all packages ≥ floor |
| `CGO_ENABLED=0 go build ./...` | clean |

## Verdict: REMEDIATE — 0 C / 1 H / 3 M / 3 L

The loop architecture is sound (turn-per-job under invariant 6, persist-before-Chat, sentinel-mapped
statuses, lenient control line, consumer-declared interfaces per invariant 3, topic isolation, the
busy race and MaxTurns default all probed correct). But the stall rule fires on the demo's happy
path, two ending paths leave the transcript in a state the package doc claims they can't, the first
client-visible failure of the product's opening turn is invisible, and the error payloads T9 must
build against are prose where the doc promises a machine class.

## Findings

### H1 — Stall rule ends the interview on two substantive one-word answers

| | |
| --- | --- |
| **Severity** | H (fires on the demo's happy path; approaches C in live judging) |
| **Where** | `internal/interview/reply.go:236` (`return len(fields) == 1`), consumed at `internal/interview/http.go:132-147` |
| **What** | `lowEffort` classifies *any* single-word answer as low-effort, and two consecutive ones end the interview with reason `stall`. The house style's concrete questions ("What is your hero called?", "What colour is the dragon?") are answered in exactly one word by the target user — "Mira", "red". Probed: `Mira` → `red` yields `status: "ending"` and an `ended{reason:"stall"}` event at exchange 2. PLAN.md §T4 / project.md §Phase A say "a stall or a **repeated** one-word answer ends it early" and separately require the model to offer binaries *when the child stalls* — but the streak is server-side only and never reaches the model, so after streak=1 the model has no signal to offer chips; a chip exemption cannot rescue exchange 2. Chip taps ("Brave!") survive only when the model happened to offer chips. A child answering concretely in single words gets a goodbye after two questions and a two-fact book. |
| **Pin** | `TestProbeF1SubstantiveOneWordAnswersEndTheInterview` — RED: `answer 2 ("red") status = ending, want "open"` |
| **Mutation** | In the fix, restore `return len(fields) == 1` as `lowEffort`'s fallthrough (or re-widen the `answer` streak condition to any one-word answer) — the probe goes red again. |

### M2 — A model "end" with empty reply text publishes `ended` with no goodbye and no closing turn, contradicting the package doc

| | |
| --- | --- |
| **Severity** | M |
| **Where** | `internal/interview/turn.go:128-133` (empty-text guard only on the question path) and `turn.go:176-183` (`closeTurn` persists only when `rep.Text != ""`); contradiction at `internal/interview/interview.go:83-84` |
| **What** | `runTurn` treats a control-line-only reply as a failed turn — but only when `endNow` is false. An end-marker-only reply (`[[end]]`) falls through to `closeTurn`, which skips persistence for empty text: the child gets `ended` with `text: ""` (a bare stop, no goodbye), and the transcript's last turn stays the child's answer. The package doc pins the opposite: "the primary path — the model's own 'end' — persists a closing turn and stays ended." Probed: after such an end, a restarted Handler accepts a new answer (the ended interview reopens). The same hole opens on the stall/limit wrap-up call if the model returns only the control line. |
| **Pin** | `TestProbeF2EndOnlyGoodbyeLeavesReopenableInterview` — RED: `ended` carries no text; last turn role `child`; post-restart answer returns `nil`, want `ErrEnded` |
| **Mutation** | Delete the empty-goodbye guard on the `endNow` path (or re-shorten `closeTurn` to skip persistence when `rep.Text == ""`) — the probe goes red again. |

### M3 — A failed opening turn is unobservable to the client

| | |
| --- | --- |
| **Severity** | M |
| **Where** | `internal/interview/turn.go:88-105` (failure paths publish to a topic that has no subscriber yet) and `internal/interview/http.go:193-196` (catch-up status stays `open`) |
| **Why it matters** | The start response returns before the opening question exists, and the client can only subscribe *after* it (the id arrives in that response). If the opening Chat call fails (both internal retries exhausted), the `error` event publishes to zero subscribers and nothing durable records the failure: the transcript reads `status:"open"` with 0 turns — indistinguishable from a slow model. The documented recovery ("the interview stays open … the child may answer again") is unusable because no UI shows an input before question 1. From the child's side the interview is wedged; the only recovery (start a new interview) is undiscoverable. The asymmetry is the design's own: questions are persisted and therefore catch-up-able, failures are not. |
| **Pin** | `TestProbeF3OpeningTurnFailureIsObservable` — RED: no `error` event reaches a post-start subscriber within 500 ms; catch-up says `status="open"` with 0 turns |
| **Mutation** | Revert the durable failure signal round 2 adds (or re-order to publish-only) — the probe goes red again. |

### M4 — Error payloads carry internal prose where the doc promises "a machine class"

| | |
| --- | --- |
| **Severity** | M |
| **Where** | `internal/interview/turn.go:35-41` (`errorEvent` doc: "the payload stays a machine class"), `turn.go:60` and `internal/interview/http.go:284` (`errorEvent{Error: err.Error()}`) |
| **What** | Every 4xx/5xx body and every SSE `error` event carries `err.Error()` — full internal chains like `interview: answer: interview: request body is not valid JSON: invalid character 'n' …`. This is the schema T9 builds against. PLAN.md §T9 forbids failure text on screen ("Errors are warm and never a dead end"), so the only legal consumption of this field is branching — which freeform prose does not support (substring matching is forbidden house style). AGENTS.md §Go style: a comment that misdescribes behaviour carries the severity of the behaviour — T9 coding against the claimed machine class integrates against a contract that does not exist. Keep the prose in the log (it already goes there via `h.log`); put a stable token (sentinel-derived) on the wire. |
| **Pin** | `TestProbeF5ErrorBodiesCarryMachineClass` — RED: 400 body `error` = `"interview: answer: interview: request body is not valid JSON: …"` |
| **Mutation** | Replace the stable token with `err.Error()` in `writeError`/`fail` — the probe goes red again. |

### L5 — A rolled-back answer leaves its stall-streak point behind

| | |
| --- | --- |
| **Severity** | L |
| **Where** | `internal/interview/http.go:132-136` (streak incremented before persistence) vs `http.go:161-171` (rollback restores `inFlight`, `ending`, `exchanges` — not `streak`) |
| **What** | When the job Runner refuses the turn, the answer is rolled out of the transcript and `exchanges` is decremented, but the `streak++` from the same answer survives. Probed: `hmm` (refused, rolled back) then `idk` — the *second* actually-processed low-effort answer — ends the interview with reason `stall` (`status: "ending"`), although only one low-effort answer was ever processed. The store-failure path (`appendChildTurn` error) has the same asymmetry. |
| **Pin** | `TestProbeF4StreakRollsBackWithTheAnswer` — RED: answer 2 status `ending`, want `open` + question |
| **Mutation** | Delete the `s.streak--` (or equivalent) from the answer-failure rollback — the probe goes red again. |

### L6 — The server-enforced checklist end reopens after a restart (documented, but real for the demo)

| | |
| --- | --- |
| **Severity** | L |
| **Where** | `internal/interview/turn.go:163-171` (enforcement branch sets `s.ended` in memory only); acknowledged at `internal/interview/interview.go:80-84` |
| **What** | When the model reports all six slots without "end", the question lands and `ended` publishes — but no closing turn is persisted, so the last transcript turn is an `interviewer` question. Probed: a restarted Handler accepts a new answer on the "ended" interview. The doc calls this "the one known gap"; recording it as a finding because the corner is real (a deploy/restart between enforcement and T5 reopens an interview the child was told had ended — the recovery costs one wasted exchange before the model re-reports the full set), the fix is one branch (persist a closing turn — `rep.Text` is already at hand — or a durable ended marker), and no severity is exempt. |
| **Pin** | `TestProbeF6EnforcedEndSurvivesRestart` — RED: last role `interviewer`; post-restart answer returns `nil`, want `ErrEnded` |
| **Mutation** | Remove the closing-turn persistence (or durable ended marker) from the enforcement branch — the probe goes red again. |

### L7 — Exported `Slot` constants lack the per-name doc comments the package's own const blocks carry

| | |
| --- | --- |
| **Severity** | L |
| **Where** | `internal/interview/reply.go:15-22` |
| **What** | AGENTS.md §Go style: "Every exported identifier has a doc comment starting with its own name." `SlotHero`…`SlotEnding` sit under a group comment only, while the sibling `Role*` (interview.go:105-116) and `Reason*` (interview.go:136-144) blocks each carry per-name comments — intra-package drift in the exact mechanic gofmt/vet cannot catch. Doc-only; cannot change behaviour. |
| **Pin** | Reviewer inspection (mechanical rule; no behavioural probe applies) |
| **Mutation** | Delete the per-name comments once added — the rule check fails again. |

## Pins

| Pin | Asserts | Status on this tree |
| --- | --- | --- |
| `TestProbeF1SubstantiveOneWordAnswersEndTheInterview` | two substantive one-word answers keep the interview open | **RED** (defect shown) |
| `TestProbeF2EndOnlyGoodbyeLeavesReopenableInterview` | an end-only reply still leaves text + a closing turn + a closed restart | **RED** (defect shown) |
| `TestProbeF3OpeningTurnFailureIsObservable` | a failed opening turn reaches a post-start subscriber / the catch-up state | **RED** (defect shown) |
| `TestProbeF4StreakRollsBackWithTheAnswer` | a refused answer's streak point rolls back with it | **RED** (defect shown) |
| `TestProbeF5ErrorBodiesCarryMachineClass` | 4xx bodies carry a stable token, not internal prose | **RED** (defect shown) |
| `TestProbeF6EnforcedEndSurvivesRestart` | the enforced end survives a restart | **RED** (defect shown) |
| `TestProbePassChildControlTokenStoredVerbatim` | child text containing a control token is stored verbatim; checklist untouched | GREEN |
| `TestProbePassConcurrentAnswersExactlyOneAccepted` | 10 racing answers: exactly one 202, nine 409, one persisted child turn | GREEN |
| `TestProbePassTopicIsolation` | interview B's traffic never reaches interview A's subscription | GREEN |
| Mutation: `Thinking: &text.Reasoning{Type:"enabled"}` added to the `runTurn` request | `TestInterviewTurnsOmitThinkingOnRawWire` must go red | **RED on all 3 calls** under mutation; reverted (sha256 `7a2d8801…` identical), pin GREEN — the thinking-off pin is load-bearing |
| Existing suite | `go test ./... -race -count=1` | GREEN |

Probes ran under `-race`, deterministic (scripted chatter, gated calls, real store/broker), no live
network. `zz_probe_test.go` removed at close; tree byte-identical to the reviewed state except this file.

## Boundary

**Reviewed:** the whole of §T4's Done when and house style — control-line reply protocol
(false positives, leniency exactly as claimed, slot/chip/end parsing), stall and repeated-one-word
rules, MaxTurns default reached end to end, checklist enforcement and the wrap-up Chat call on
stall/limit endings (including its failure path: error event, `ending` reopens, a low-effort
re-answer re-triggers the goodbye, a substantive re-answer continues the interview — benign),
chip matching (case/punctuation/whitespace normalised; multi-word never matches; empty chip list
matches nothing), busy 409 under a real race, SSE event schema and subscription semantics
(from-now-on + transcript catch-up; `ended` dedupes on status), transcript ordering under
concurrency, store-failure and job-refusal rollback claims, restart semantics incl. the documented
corner, the `main.go` seam (four route lines + `newServer` signature + `run` wiring — sanctioned
per invariant 5; wiring stays free of business logic), `main_test.go` fallout, invariant 3
(consumer-declared interfaces; concrete types appear only in doc comments and test helpers;
`map[string]any` only in test decoders), and the child-facing string audit (finding M4).

**Not reviewed / out of scope:** live GMI behaviour (no network — scripted fakes only, per method);
T9 frontend and T5 story package (not built; role vocabulary consumed only by tests);
`internal/stream`/`internal/job`/`internal/gmi/text`/`internal/store` internals beyond the seams
T4 consumes (T3/T3b/T2 closed); input-size caps and spend gating (T11 owns capping); deploy
artifacts (T1).

## Exit criteria for round 2

1. All seven findings fixed; no severity exempt. Each red probe above re-run GREEN (the remediator
   converts each into a regression test or the fix makes the behaviour directly observable), and
   `TestInterviewEndsEarlyOnStall` / `TestChipTapResetsStallStreak` stay GREEN — the H1 fix must end
   genuine stalls ("i dunno" ×2) exactly as before.
2. Zero residue claimed against this round, severity by severity, in `t4-round2.md`.
3. Gates re-run green: `go vet ./...`, `go test ./... -race -count=1`, `gofmt -l .`,
   `go test ./... -cover`, `CGO_ENABLED=0 go build ./...`.

## Candidates examined and dismissed

- **Control-token injection via child answers** — the server never parses child text as a control
  line (stored verbatim, checklist untouched — GREEN probe). The residual vector is model mimicry
  of a child-supplied line, and the model already holds `filled`/`end` authority, so injection
  grants no capability the model lacks. Optional prompt hardening; not a defect.
- **Bracketed last-line false positives** — a model reply whose final line is bracketed prose is
  stripped with no state change; documented lenient degradation, not a bug.
- **MaxTurns off-by-one** — none: 12 questions, the 13th answer gets the goodbye
  (`TestDefaultMaxTurnsEndsTheInterview`, chat calls = 14).
- **Restart while a turn is in flight** — the in-process job dies; the answer is already persisted;
  the interview reopens open and recoverable. Documented in the package doc's restart semantics.
- **Chat failure mid-interview** — error event, interview open, re-answer recovers
  (`TestChatFailurePublishesErrorAndStaysOpen`); double child turns in the transcript are the
  documented persist-first trade.
- **Wedged in-flight turn on a hung upstream** — impossible: `text.Client` carries a 60 s
  per-request timeout (`client.go:47,70`).
- **Store failure mid-turn** — rejected with nothing persisted
  (`TestStoreFailuresSurfaceAsSentinels`); job-refusal rollback verified except the L5 asymmetry.
- **Chip matching injection** — normalised exact equality only; model-authored chips cannot widen
  the exemption beyond their own text.
