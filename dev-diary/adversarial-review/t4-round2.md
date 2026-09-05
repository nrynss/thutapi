# T4 round 2 — adversarial review

- **Tree:** the same uncommitted work on `main`, base `49eacc1` ("T3b closes at round 2"), after the
  round-1 remediation (`t4-remediation-round1.md`). Diff surface unchanged: `internal/interview/**`
  (9 files), `cmd/thutapi/main.go` (+route lines, +broker/job wiring in `run`),
  `cmd/thutapi/main_test.go`.
- **Date:** 2026-09-05. Reviewer: T4Review2 (fresh agent; not the round-1 reviewer, not the
  remediator).
- **Evidence:** AGENTS.md; the review-loop README (per-finding schema, zero-residue requirement);
  `t4-round1.md` and `t4-remediation-round1.md` in full including its deviations/residue section;
  PLAN.md §T4; project.md §Phase A and §M3 phase settings; the full current source of
  `internal/interview` (interview.go, http.go, turn.go, prompt.go, reply.go) and the four test files.
- **Method:** probe, don't trust prose — same discipline as round 1. A reviewer probe file
  (`internal/interview/zz_probe_test.go`, six tests, `-race`, the package's scripted
  harness — no live network) exercised the new corners the remediation pins do not reach, was
  recorded, and was removed at close. Both named round-1 mutations (H1's end-condition widening,
  M4's `err.Error()` payload) were re-applied by hand, observed red for the recorded reasons, and
  reverted sha256-identical (`caefcf7e…` against a pre-mutation snapshot); the affected pins re-ran
  green after each revert. All five gates re-run by this reviewer (table below). No fixes applied;
  no formatters run; no git mutations. Three of the probe's initial expectations failed and were
  probe bugs, not product bugs — each correction is recorded in the Pins table because each
  confirmed product behaviour the hard way (noted per row).

## Gates (run for this review, current tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race -count=1` | ok — all 8 test packages (cmd 10.0s, stream 16.5s, interview 2.0s) |
| `gofmt -l .` | empty |
| `go test ./... -cover` | interview 93.4%, cmd 84.8%, gmi/media 92.5%, gmi/text 90.4%, job 95.7%, mediastore 95.6%, store 86.9%, stream 94.4% — all above the two-tier floor |
| `CGO_ENABLED=0 go build ./...` | ok |

## Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L

The remediation holds under adversarial re-reading. The H1 rework ends only on no-progress (the
spec's own words: "a stall or a repeated one-word answer"), signals the streak to the model every
turn so chips arrive before any end can fire, and the signal provably never touches the transcript.
Every end path closes restart-stable with a spoken goodbye. The failure marker is recorded, cleared
on re-start, replaced by a newer failure, and clean on an ended interview. Error payloads are total
machine classes with `internal` as the drift-proof fallthrough. No new findings.

## Findings

None. (0/0/0/0 — no per-finding rows.)

## Zero residue against round 1

**Zero, severity by severity.**

| Round-1 finding | Disposition on this tree |
| --- | --- |
| H1 (stall ends on substantive one-worders) | Fixed as reworked: end fires only on `stallStreak >= 2` (two consecutive genuine stalls), a repeated one-word answer, or MaxTurns (`http.go` :166-172); `lowEffort` unchanged as the chip signal, now surfaced to the model via `stallDirective(streak)` on every non-ending call (prompt.go :102-116). Pinned permanently by `TestStallEndRequiresNoProgress` (3 subtests) + `TestStallAnswer`. The round's own second-named mutation re-applied by hand this round: `case s.streak >= 2 || repeated:` → both substantive-one-worder subtests RED ("red"/"blue" → `ending`, want `open`; the genuine-stall subtest stays green, i.e. idk/idk still ends), reverted sha256-identical. |
| M2 (empty model "end" → bare stop, reopenable) | Fixed: `closeTurn` persists a closing turn unconditionally with `fallbackGoodbye` when the model gave no text (turn.go :226-239); all three end paths run through it. Pinned by `TestEmptyGoodbyeStillClosesTheInterview` (both subtests) + `TestEnforcedEndSurvivesRestart` + this round's probe `TestProbeR2WrapUpEmptyGoodbyeRestartStable` (the wrap-up-without-text corner the pins left without a restart check). Child-facing tone verified: warm, short, no error vocabulary (probe `TestProbeR2GoodbyeToneIsChildFacing`), and it is only ever a fallback — a model-authored goodbye still wins (this round's lifecycle probe observed the model's own text on an end reply). |
| M3 (failed opening turn unobservable) | Fixed: every turn-failure path records `errClass(err)` on `session.turnErr` (turn.go :252, :260); the transcript route carries it as `error` with status still `open` (http.go :60-68, :219-246); cleared the moment the next turn starts (http.go :204-207). Pinned by `TestOpeningTurnFailureIsObservable`. This round's probe `TestProbeR2TurnErrorMarkerLifecycle` walked the whole lifecycle on one interview: failure → marker `internal`; re-start → cleared immediately; second failure → recorded again (+ live `error` event); recovery; model end → status `ended`, no stale error. Clear-vs-record ordering is race-free by lock order, not luck: `answer` holds `s.mu` until it returns, and `runTurn`'s failure path can only acquire it after — a fresh failure can never be erased by the late clear. |
| M4 (error payloads carry prose) | Fixed: `errClass` is the single source of the wire token and, via `statusForClass`, the HTTP status (turn.go :54-67, http.go :312-332); prose goes to the log on both the SSE and request surfaces. Pinned by `TestErrorBodiesCarryMachineClass` + `TestHTTPStatusMapping` (retargeted to exact classes). The recorded mutation re-applied by hand this round (`writeError` payload → `err.Error()`): both tests RED with the round's quoted prose chains verbatim ("…not valid JSON: invalid character 'n' …", "answer text is empty", "not found", "a turn is already in progress"), reverted sha256-identical. Drift-proofing probed: a sentinel nobody mapped falls through to `internal`/500 on `errClass`, `statusForClass`, the SSE event and the body — never prose, never a wrong status (`TestProbeR2UnmappedSentinelFallsThroughToInternal`, incl. `statusForClass` totality on garbage input). |
| L5 (rolled-back answer keeps its streak point) | Fixed on both paths and all three counters (`streak`, `stallStreak`, `lastAnswer` snapshotted before bookkeeping, restored on the store-failure return http.go :179 and the job-refusal rollback :198). Pinned by `TestStreakRollsBackWithTheAnswer` (both subtests, re-run green). |
| L6 (enforced end reopens after restart) | Fixed: the enforcement branch goes through the same `closeTurn` (turn.go :214-216); closing turn + fallback goodbye persisted. Pinned by `TestEnforcedEndSurvivesRestart` (re-run green; restart status `ended`, answer rejected wrapping `ErrEnded`). |
| L7 (Slot constants lack per-name docs) | Fixed: per-name comments on `SlotHero`…`SlotEnding` (reply.go :15-29). Rule check re-run by this reviewer: `go doc ./internal/interview SlotHero` (and SlotEnding) shows every const with its own doc comment. Doc-only; no behaviour. |
| Round-1 already-green pins | `TestProbePass*` concerns now covered by the permanent suite: control tokens stored verbatim (`TestParseReply` 17 cases), exactly-one-winner busy race (`TestAnswerWhileTurnInFlightIsBusy`), topic isolation (`TestEventsRouteStreamsSSEOverHTTP`), thinking-off raw-wire pin (`TestInterviewTurnsOmitThinkingOnRawWire`) — all green under `-race -count=1`, untouched. `TestInterviewEndsEarlyOnStall` and `TestChipTapResetsStallStreak` green **unchanged** — the H1 fix ends genuine stalls exactly as before (round-1 exit criterion 1). |

## Pins (all re-run by this reviewer, `-race -count=1`, recorded output)

| Pin | Asserts | Status |
| --- | --- | --- |
| `TestStallEndRequiresNoProgress` (3 subtests) | distinct one-worders never end, streak signalled 1→2→3 on the raw last message; idk/idk ends reason `stall` | GREEN |
| `TestStallAnswer` | genuine stalls vs substantive one-worders split correctly | GREEN |
| `TestEmptyGoodbyeStillClosesTheInterview` (2 subtests) | end-only control line and text-less wrap-up both leave fallbackGoodbye + closing turn; restart rejects with `ErrEnded` | GREEN |
| `TestOpeningTurnFailureIsObservable` | catch-up read reports the failure (`error:"internal"`, status `open`, 0 turns); recovery answer clears it; question lands | GREEN |
| `TestErrorBodiesCarryMachineClass` | busy/ended/not_found/invalid bodies + SSE `internal` event carry exact classes; log carries the prose | GREEN |
| `TestHTTPStatusMapping` | four routes map to exact class+status pairs | GREEN |
| `TestStreakRollsBackWithTheAnswer` (2 subtests) | job-refusal and store-failure rollbacks take the whole stall-bookkeeping point back | GREEN |
| `TestEnforcedEndSurvivesRestart` | enforced end persists closing turn; restart status `ended`; answer wraps `ErrEnded` | GREEN |
| `TestInterviewEndsEarlyOnStall` | two stalls end early, goodbye call carries the ending directive, terminal 409 | GREEN (unchanged) |
| `TestChipTapResetsStallStreak` | tap after a stall continues (turn 5) | GREEN (unchanged) |
| `TestInterviewTurnsOmitThinkingOnRawWire` | no `thinking` field on any call's raw wire | GREEN |
| `TestInterviewRunsToEndOnChecklist` / `TestChecklistFullWithoutModelEndStillEnds` / `TestDefaultMaxTurnsEndsTheInterview` / `TestEventsRouteStreamsSSEOverHTTP` | e2e checklist run, enforcement, MaxTurns default, SSE over HTTP | GREEN |
| `TestAnswerWhileTurnInFlightIsBusy` / `TestRestartedHandlerSeesEndedInterview` / `TestChatFailurePublishesErrorAndStaysOpen` / `TestStoreFailuresSurfaceAsSentinels` / `TestJobStartFailureRollsBackTheAnswer` / `TestLowEffort` / `TestParseReply` / `TestMatchesChip` / `TestTranscriptRouteServesTheCatchUpShape` | the round-1 ProbePass concerns and neighbours, now permanent | GREEN |
| Probe `TestProbeR2DirectiveReachesWireNeverPersisted` | directive on the RAW wire: absent on the opening call, exactly `stallDirective(1..4)` as the last message of each question call, never inside history, absent after a chip tap, absent from the wrap-up call (which carries `endingDirective` instead, streak ≥ 1 notwithstanding); no `(Signal:` in any persisted turn or published question text; transcript ends on the closing turn | GREEN |
| Probe `TestProbeR2ChipTapExemptsAndResets` | two identical taps never fire the repetition rule; tap between two stalls resets the stall counter (second stall stays the first, directive restarts at 1) | GREEN |
| Probe `TestProbeR2TurnErrorMarkerLifecycle` | marker lifecycle: failure → recorded; re-start → cleared; second failure → re-recorded (+ live event); end → status `ended`, error empty | GREEN |
| Probe `TestProbeR2UnmappedSentinelFallsThroughToInternal` | unmapped sentinel → `internal`/500 everywhere; `statusForClass` total | GREEN |
| Probe `TestProbeR2WrapUpEmptyGoodbyeRestartStable` | the third end-path corner (text-less wrap-up) is restart-stable | GREEN |
| Probe `TestProbeR2GoodbyeToneIsChildFacing` | fallbackGoodbye short, no error/jargon vocabulary | GREEN |
| Mutation: `case s.streak >= 2 || repeated:` (H1 widening) | `TestStallEndRequiresNoProgress` subtests 1 and 3 RED ("red"/"blue" → `ending`, want `open`); genuine-stall subtest still GREEN | **RED** as recorded; reverted sha256-identical (`caefcf7e…`), pins GREEN |
| Mutation: `writeError` payload → `err.Error()` (M4) | `TestErrorBodiesCarryMachineClass` RED (racing answer body = the `busy` prose chain) and all four `TestHTTPStatusMapping` rows RED with the round's quoted prose verbatim | **RED** as recorded; reverted sha256-identical, pins GREEN |
| Probe forensics | three initial probe failures were probe bugs, each confirming product behaviour: the directive counts the low-effort streak (4 after three one-worders + a stall), a post-tap `idk` is only stall #1 (end needs the repeat), and a model-`end` reply publishes only `ended` (no question event) with the model's text — the fallback is strictly a fallback | probe-side; no product change |

Probe file `internal/interview/zz_probe_test.go` removed at close; `sha256sum -c` against a
pre-review snapshot verifies the tree byte-identical to the reviewed state (all touched files OK).

## Boundary

**Reviewed:** the seven remediated fixes in their current form (H1 stall rework + `stallDirective`
wire path + system-prompt house rule; M2 unconditional closing turn; M3 durable turn-failure marker
via the documented catch-up; M4 machine classes + `statusForClass` drift-proofing; L5 rollback of
all three counters on both failure paths; L6 enforcement via `closeTurn`; L7 doc comments), with
the adversarial questions from the round-2 brief each probed or reasoned to a conclusion: the new
end rule still fires on genuine stalls and never on distinct one-worders; the directive reaches the
raw wire and never the transcript; the chip-tap exemption resets both counters and the repetition
check; the marker lifecycle across success/new-failure/end; the unmapped-sentinel fallthrough; the
fallback goodbye's child-facing tone; the empty-goodbye restart matrix on all three end paths; F6
and F4 re-verified. Regression over the whole round-1 surface: the full interview suite green under
`-race -count=1`, plus the `cmd/thutapi` seam and `main_test.go` fallout via the package gates.
Cross-boundary check: the new wire vocabulary (class tokens, `transcriptResponse.error`) is
documented in the package doc and pinned; no new event, variant or value lacks a consuming branch.

**Not reviewed / out of scope:** live GMI behaviour (scripted fakes only, per method); T9 frontend
and T5 story package (not built); `internal/stream`/`internal/job`/`internal/store`/`internal/gmi`
internals beyond the seams T4 consumes (closed tracks); the pre-existing T4 state outside the
remediation delta beyond regression re-running; PLAN.md's T4 status line still reading "Not
started" is the orchestrator's step-5 flip at commit time, not review residue.

## Close

T4 can close. All seven round-1 findings are fixed, each red probe is now a permanent pin, two
round-1 mutations were re-applied by hand this round and observed red with byte-identical reverts,
the gates are green from this reviewer's own runs, and there is zero residue against round 1.
