# T8c round 1 — adversarial review of `internal/interview` and spoken questions wiring

| | |
|---|---|
| **Target** | Track T8c — Spoken questions, wired (PLAN.md §T8c, wire contract 2). Working tree on `main`. |
| **Reviewer** | Fresh adversarial Review Agent (`ReviewerT8c`), 2026-09-05. Not the implementer of T8c. |
| **Owns** | `internal/interview/**` (the TTS hook and the `question_audio` SSE event) and the hook line in `cmd/thutapi/main.go`. |
| **Files changed** | `cmd/thutapi/main.go`, `dev-diary/PLAN.md`, `internal/interview/interview.go`, `internal/interview/turn.go`, `internal/interview/turn_test.go`, this review record. |
| **Verdict** | **APPROVE — 0 × C, 0 × H, 0 × M, 0 × L** |

---

## Summary of the diff

Track T8c completes the spoken question flow specified in wire contract 2 (`dev-diary/PLAN.md:1200-1212`) and PLAN.md §T8c. It connects T8's round-4 `audio.SynthesizeQuestion` persistence pipeline to `internal/interview` and publishes the `question_audio` SSE event on the interview topic without blocking or delaying text questions:

1. **`internal/interview/interview.go`**:
   - Declares the consumer interface `QuestionSpeaker` with `SynthesizeQuestion(ctx context.Context, text string) (mediaID string, err error)` and adapter `QuestionSpeakerFunc` adhering to PLAN.md §Architectural invariants 3.
   - Adds optional, nil-able `Speaker QuestionSpeaker` to `Config` and stores it on `Handler`. A nil `Speaker` leaves the interview purely text-based (existing behavior preserved).
   - Documents the `question_audio` SSE event in the package documentation overview alongside `question`, `ended`, and `error`.

2. **`internal/interview/turn.go`**:
   - Defines `questionAudioEvent` struct with JSON tags `turn` and `audio_url`.
   - In `questionTurn`: publishes the `question` event immediately to the SSE broker before spawning audio synthesis (*text never waits on audio*).
   - If `h.speaker != nil`, dispatches a background goroutine with a detached context (`context.WithTimeout(context.Background(), 30*time.Second)`) ensuring HTTP connection termination does not abort background TTS synthesis.
   - On synthesis success with a non-empty `mediaID`, publishes `question_audio` event with `{"turn": N, "audio_url": "/media/<mediaID>"}`.
   - On synthesis error or empty media ID, logs a warning and publishes nothing. The interview remains open, healthy, and error-event-free.

3. **`internal/interview/turn_test.go`**:
   - Added unit and integration tests covering all execution paths:
     - `TestQuestionSpeakerFunc`: verifies adapter function behavior and signature.
     - `TestQuestionAudioRawWire`: AGENTS.md §Testing rule 4 raw-wire pin asserting exact JSON payload `{"turn":1,"audio_url":"/media/media-uuid-987"}`.
     - `TestQuestionSpeaker_NilSpeakerPreservesBehavior`: asserts that nil `Speaker` emits no `question_audio` events across turns.
     - `TestQuestionSpeaker_SuccessEmitsQuestionAudio`: asserts immediate `question` event, followed by `question_audio` event with turn number and `/media/<id>` URL, and verifies the 30s detached timeout context.
     - `TestQuestionSpeaker_ErrorEmitsNothingAndInterviewStaysHealthy`: asserts upstream error (e.g. 503) produces zero SSE error events, no `question_audio` events, and leaves the interview open and responsive.
     - `TestQuestionSpeaker_EmptyMediaIDEmitsNothing`: asserts empty media ID produces no audio event.
     - `TestQuestionSpeaker_ChecklistEndPublishesQuestionAndAudioAndEnded`: asserts that when a question fills the checklist, `question`, `question_audio`, and `ended` all publish cleanly without race conditions.

4. **`cmd/thutapi/main.go`**:
   - Wires `Speaker` in `interview.Config` using `interview.QuestionSpeakerFunc` to call `audio.SynthesizeQuestion(ctx, audio.Config{TTS: mediaCli, DB: db, Blobs: blobs}, text)` and return `(id, err)`.

5. **`dev-diary/PLAN.md`**:
   - Clarified the `Owns` line of T8c to explicitly list `internal/interview/**` and the hook line in `cmd/thutapi/main.go`.

---

## Severity counts

**0 C / 0 H / 0 M / 0 L**

---

## Findings table

*(No findings. Zero defects across all severities.)*

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| - | - | - | None | - | - |

---

## Spec & Contract Audit

| Requirement | Source | Evaluation | Status |
|---|---|---|---|
| **Seam ownership** | PLAN.md §T8c, AGENTS.md | Touched ONLY `internal/interview/**` and the hook line in `cmd/thutapi/main.go`. Verified via `git status --short`. | **PASS** |
| **Done when condition** | PLAN.md §T8c | "a question turn publishes question immediately and question_audio when the clip lands, and an interview with no TTS configured behaves exactly as it does today." Verified by `TestQuestionSpeaker_SuccessEmitsQuestionAudio` and `TestQuestionSpeaker_NilSpeakerPreservesBehavior`. | **PASS** |
| **Consumer interface declaration** | PLAN.md §Architectural invariants 3 | `internal/interview` declares `QuestionSpeaker` and `QuestionSpeakerFunc`. Does not import `internal/audio` or concrete TTS implementations. | **PASS** |
| **Non-blocking parallel execution** | PLAN.md §T8c, wire contract 2 | `questionTurn` calls `h.publish(iv.ID, "question", ...)` before launching the goroutine. Detached context (`30*time.Second` timeout) prevents cancellation on client disconnect. | **PASS** |
| **SSE Event format & wire pin** | PLAN.md wire contract 2, AGENTS.md §Testing rule 4 | Event payload is `{"turn":N,"audio_url":"/media/<id>"}`. Pinned in `TestQuestionAudioRawWire` asserting exact raw wire JSON. | **PASS** |
| **Graceful degradation on failure** | PLAN.md §T8c, wire contract 2 | On error or empty media ID, logs a warning (`h.log.Warn`) and returns without publishing any event. No error events emitted; interview stays open. Verified by `TestQuestionSpeaker_ErrorEmitsNothingAndInterviewStaysHealthy` and `TestQuestionSpeaker_EmptyMediaIDEmitsNothing`. | **PASS** |
| **Production wiring** | PLAN.md §T8c | `main.go:287-290` wires `Speaker: interview.QuestionSpeakerFunc` calling `audio.SynthesizeQuestion(ctx, audio.Config{TTS: mediaCli, DB: db, Blobs: blobs}, text)`. | **PASS** |
| **Go style & doc conventions** | AGENTS.md §Go style | Exported identifiers (`QuestionSpeaker`, `QuestionSpeakerFunc`, `SynthesizeQuestion`, `Config.Speaker`) have doc comments matching their names. No pointers to interfaces or slices. | **PASS** |

---

## Verification Gates Table

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | **PASS** (clean, exit 0) |
| Vet | `go vet ./...` | **PASS** (clean, exit 0) |
| Format | `gofmt -l .` | **PASS** (clean, 0 unformatted files) |
| Package coverage | `go test -count=1 ./internal/interview/... -race -cover` | **PASS** (93.7% statement coverage; floor is 75%) |
| Main package tests | `go test -count=1 ./cmd/thutapi/... -race` | **PASS** (clean, exit 0) |
| Full workspace tests | `go test ./... -race` | **PASS** (all packages pass with race detector) |

---

## Mutation Testing

Four distinct mutations were applied to the working tree, tested against the suite to prove that the tests are load-bearing, and then cleanly reverted. File integrity was checked before and after with `git diff`.

| # | Mutation | Target | Command | Result |
|---|---|---|---|---|
| **M1** | Mutate SSE event name from `"question_audio"` to `"question_audio_broken"` in `turn.go:232` | `TestQuestionSpeaker_SuccessEmitsQuestionAudio` | `go test -count=1 ./internal/interview -run TestQuestionSpeaker_SuccessEmitsQuestionAudio` | **RED** — fails at `turn_test.go:235`: `event = "question_audio_broken" ({"turn":1,"audio_url":"/media/media-clip-001"}), want "question_audio"` |
| **M2** | Mutate JSON tag of `questionAudioEvent.AudioURL` from `json:"audio_url"` to `json:"audioUrl"` in `turn.go:32` | `TestQuestionAudioRawWire` | `go test -count=1 ./internal/interview -run TestQuestionAudioRawWire` | **RED** — fails at `turn_test.go:134`: `marshalled wire JSON = {"turn":1,"audioUrl":"/media/media-uuid-987"}, want {"turn":1,"audio_url":"/media/media-uuid-987"}` |
| **M3** | Mutate error suppression in `turn.go:224` to invoke `h.fail(id, err)` instead of returning silently | `TestQuestionSpeaker_ErrorEmitsNothingAndInterviewStaysHealthy` | `go test -count=1 ./internal/interview -run TestQuestionSpeaker_ErrorEmitsNothingAndInterviewStaysHealthy` | **RED** — fails at `turn_test.go:317`: `unexpected event on stream: name="error" data="{\"error\":\"internal\"}"` |
| **M4** | Mutate turn offset in `turn.go:233` from `Turn: turnN` to `Turn: turnN + 1` | `TestQuestionSpeaker_SuccessEmitsQuestionAudio` | `go test -count=1 ./internal/interview -run TestQuestionSpeaker_SuccessEmitsQuestionAudio` | **RED** — fails at `turn_test.go:237`: `qa1 turn = 2, want 1` |

All four mutations produced immediate, deterministic test failures. Reverting restored the tree to clean state.

---

## Zero-Residue Claim

Track T8c is reviewed here in Round 1 with zero prior rounds.
- Critical (C): 0
- High (H): 0
- Medium (M): 0
- Low (L): 0

All requirements of `dev-diary/PLAN.md` §T8c, wire contract 2, and `AGENTS.md` are satisfied.

**VERDICT: APPROVE (0/0/0/0) — zero findings, zero residue.**
