# T13 round 5 — adversarial review

| | |
|---|---|
| **Target** | T13 after the user-approved R4 disposition: adult consented capture/upload, fixed ffmpeg transcode, public `source_audio`, fail-closed GMI clone boundary, expiry lifecycle, route wiring, and adult-corner UI. |
| **Evidence read** | `AGENTS.md`; `PLAN.md` §T13; the relevant `project.md` consent and audio decisions; all T13 rounds and remediations 1–4; the current T13 diff; `internal/audio/clone.go` and tests; `cmd/thutapi`; mediastore; deployment configuration; browser contract; and the generation/narration call chain. |
| **Verification** | `go test -count=10 -race ./internal/audio ./cmd/thutapi` was started but the command's aggregate result was not available before this review closed; the focused T13 race pins passed: `go test -count=1 -race -run '^(TestVoiceSampleHandler_ExpiresAndNeverUsesImmutableCache|TestVoiceSampleHandler_ExpiryRecordedBeforePersist|TestDecodeVoiceCloneResponse_FailsClosed|TestVoiceSampleHandler_CloneRequestAndUnverifiedResponse|TestVoiceSampleHandler_FilesizeFlagMutationCannotSilentlyClip|TestVoiceSampleHandler_LongPreparedFileRejectedBeforeTranscode)$' ./internal/audio` and `go test -count=1 -race -run '^(TestVoiceSampleRouteServesThroughMux|TestShutdownLogRecordsSignalName|TestRunRejectsMissingVoiceSampleConfiguration)$' ./cmd/thutapi`. Full `go test -count=1 -race ./...`, `go vet ./...`, `gofmt -l .`, `git diff --check`, `node --check static/app.js static/browser-test.js`, `bash -n deploy/docker-run.sh`, and `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/thutapi` passed. No browser DOM runner, deployed HTTPS consented sample, or live GMI clone request was available. |

**Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L.**

There is zero local-code residue from every finding in rounds 1–4. In
particular, the fixed recorder negotiation, input/output and duration bounds,
affirmative disclosure and bearer admission, absolute public-origin input,
expiry sweep/no-store treatment, pre-persist durable tracking, and joined
shutdown path remain covered. The clone response decoder still fails closed
for every unobserved response, so no synthetic provider identity can reach the
browser or narration.

R4 H1/H2 are deliberately outside this local-code approval by the user's
explicit, recorded decision in `t13-remediation-round4.md`: an operator must
use a deployed public HTTPS origin and an operator-owned, consented,
short-lived sample; retain redacted terminal clone-response evidence; then
prove that the observed identity is accepted by `minimax-tts-speech-2.8-hd`,
that its terminal payload echoes the exact identity, and that the returned
audio URL is valid. Until that T13b-style operator verification authorizes a
future server-side handoff, the application accurately remains fail-closed and
uses the library narrator. This approved deferral is analogous to the accepted
physical-phone verification and is not reissued as local code residue.

**Zero-residue claim:** R1's H1–H3/M1–M2, R2's C1/H1–H4/M1, R3's C1/H1–H4,
and R4's C1/H3/H4 are closed in local code and tests; R4 H1/H2 retain only the
explicit user-approved external operator-verification scope stated above. No
additional finding is present.
