# T13 round 4 — adversarial review

| | |
|---|---|
| **Target** | T13 after remediation round 3: consented adult capture/upload, ffmpeg transcode, public `source_audio`, GMI clone boundary, expiry lifecycle, route wiring, and adult-corner UI. |
| **Evidence read** | `AGENTS.md`; `PLAN.md` §T13 and its T13b/live-verification rule; the touching `project.md` decisions; all T13 rounds and remediations 1–3; current T13 diff; `internal/audio/clone.go` and its tests; `internal/gmi/media`; `cmd/thutapi`; mediastore; deployment files; browser contract; and the generation/narration call chain. |
| **Verification** | `go vet ./...`, the release build, `git diff --check`, selected-path `gofmt -l`, `node --check static/app.js static/browser-test.js`, and `bash -n deploy/docker-run.sh` passed. `go test -count=20 -race -run '^(TestShutdownLogRecordsSignalName|TestVoiceSampleHandler_ExpiresAndNeverUsesImmutableCache|TestVoiceSampleHandler_ExpiryRecordedBeforePersist|TestDecodeVoiceCloneResponse_FailsClosed|TestVoiceSampleHandler_CloneRequestAndUnverifiedResponse)$' ./cmd/thutapi ./internal/audio` passed. The focused package race suite passed. Full `go test -count=1 -race ./...` was red only at the pre-existing `internal/interview.TestRestartedHandlerSeesEndedInterview` timeout; all T13-touching packages passed in that run. No browser DOM runner, deployed HTTPS consented sample, or live GMI clone request was available. |

**Verdict: REMEDIATE — 0 C / 2 H / 0 M / 0 L.**

C1 has zero residue: `run` joins the listener before the log buffer can be
read, and the repeated race pin is clean. H3 has zero residue: the expiry
sweeper deletes tracked samples without a later media request, startup sweeps
durable expired entries, and tracked media is served with `Cache-Control:
no-store`. H4 has zero residue: a cryptographically generated ID is durably
tracked before `PersistWithID`, and cleanup is idempotent if persistence never
reaches its row. H1's fail-closed safety behavior is also correct: every raw
response, including fabricated JSON containing `voice_id`, returns
`ErrVoiceCloneUnverified` and produces no browser-visible identity. That
safety fix does not establish the provider contract or deliver the required
clone feature, so H1 and H2 remain open.

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `cmd/thutapi/main.go:81-91`; `internal/audio/clone.go:283-324,481-489`; `internal/gmi/media/client.go:359-381` | The application correctly fails closed, but therefore cannot complete a voice clone: `gmiVoiceCloner` submits the request and `DecodeVoiceCloneResponse` rejects every terminal body. There is still no observed clone response from the real provider, so no response field may be promoted to a `VoiceCloneResult`. The safe 502 is preferable to inventing a shape, but the non-optional T13 capability remains unavailable. | Add T13b's `internal/audio/live_test.go` behind `//go:build live`, gated by explicit operator consent and a `T13_SOURCE_AUDIO_URL` that is an operator-created, short-lived, unguessable HTTPS URL for the consented sample. The probe must call the real clone model with the confirmed payload, preserve the terminal raw response in the T13b record (redacting only secrets), and identify a result field only from that observation. Until that evidence exists, `TestDecodeVoiceCloneResponse_FailsClosed` is the required unit pin and all raw responses must return `ErrVoiceCloneUnverified`. | Restoring a root or `outcome.voice_id` decoder before the live record lets fabricated JSON create a usable clone identity and reopens the prior false-success path. |
| H2 | H | `static/app.js:549-553`; `internal/bookgen/bookgen.go:431-444`; `internal/audio/audio.go:258-263`; repository search for `thutapi:voice-id` | There is no verified clone-to-HD narration handoff. The UI can only place an ID in `sessionStorage`; `POST /interviews/{id}/generate` carries no selected voice, the server never reads that key, and `bookgen` constructs narration with the library default. This is intentionally inert while H1 is unverified, but it means a successful clone cannot affect the generated book. | After H1's live response observation, the same T13b probe must decode only the observed clone field, call the real `minimax-tts-speech-2.8-hd` request with that exact ID, and require a successful terminal HD record whose echoed `payload.voice_id` equals it (then fetch and validate the returned audio URL). Commit the live probe and record the consented run's sanitized terminal clone and HD envelopes. Only then may a remediation add a durable server-side selected-voice contract from the end-of-interview action through `bookgen` into `audio.Config.Voice`, with an integration pin proving the exact ID reaches the HD wire. | Accepting a browser-only ID or adding a server handoff before the observed clone contract either leaves narration on `English_expressive_narrator` or treats a guessed provider field as a real voice. |

No other new finding is recorded. The T13 safety path now rejects unknown
provider results, retains the library narrator as the working fallback, and
removes source samples on schedule; the remaining work is the provider-backed
capability proof and the contract it authorizes.
