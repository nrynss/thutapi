# T13 remediation round 3

This round fixes C1, H3, and H4. H1 and H2 remain external/provider-contract
blockers: no clone response field or clone-to-HD narration handoff is claimed
without the required deployed HTTPS live probe. The application now fails
closed for both conditions.

| # | Status | Remediation | Pin / evidence | Mutation |
|---|---|---|---|---|
| C1 | fixed | `cmd/thutapi/run` now joins the `ListenAndServe` goroutine through `serveDone` on both the signal and server-error paths, so its listener log cannot race the caller after `run` returns. | `go test -count=1 -race -run '^TestShutdownLogRecordsSignalName$' ./cmd/thutapi` passes. | Removing the `serveDone` join permits the test to read the shared log buffer while the listener goroutine writes it; the race detector reports the original `bytes.Buffer` conflict. |
| H1 | safety fixed; capability unresolved | `DecodeVoiceCloneResponse` rejects every response until T13b records the real provider response contract. The synthetic root/`outcome.voice_id` shape is no longer accepted, and the upload route returns 502 without exposing a voice id when the cloner cannot verify one. No provider result shape is invented. | `TestDecodeVoiceCloneResponse_FailsClosed` rejects the fabricated `outcome.voice_id` JSON; `TestVoiceSampleHandler_CloneRequestAndUnverifiedResponse` pins the 502/no-id behavior. The required consented deployed HTTPS sample and terminal raw response are still missing, so H1 is not marked resolved. | Restoring either guessed decoder field makes fabricated JSON produce a usable clone id again. |
| H2 | unresolved external blocker | No generation or narration handoff was added because the verified clone id and provider result shape do not exist. The UI continues to use the library narrator and the server never treats the unverified id as a narration selection. A T13b live probe must first establish the clone result and then pin the exact HD narration request carrying that id. | Repository search still finds no production consumer for the browser's selected voice id; no live probe or real sample was run. This remains an explicit blocker for the optional clone capability. | Adding a session-storage-only value or accepting a synthetic id would still leave generation on the library voice and would falsely claim completion. |
| H3 | fixed | `VoiceSampleHandler` now runs a bounded expiry sweeper with a defined shutdown path. Construction performs an immediate sweep of expired durable entries; the live handler deletes samples when their lifetime elapses without a later `/media` request. | `TestVoiceSampleHandler_ExpiresAndNeverUsesImmutableCache` advances its injected clock and waits for the sweeper's delete signal without issuing a media GET; the sample bytes are gone. | Removing the sweeper leaves an expired sample present forever unless an unrelated later GET happens to trigger lazy deletion. |
| H4 | fixed | The consumer media seam now supports `PersistWithID` and idempotent `DeleteIfPresent`. T13 generates the id, writes the expiry sidecar atomically before persistence, then persists the blob under that id. A crash before row insertion leaves a tracked id that cleanup can safely discard; a crash after insertion cannot create an untracked sample. This is the sanctioned mediastore contract change required because the previous `Persist` API generated the id after the sidecar point. | `TestVoiceSampleHandler_ExpiryRecordedBeforePersist` observes the durable expiry file before `PersistWithID`; `mediastore.Store.PersistWithID` validates the id and cleans failed inserts, while `DeleteIfPresent` handles a pre-registered id with no row. | Reverting to post-persist sidecar tracking recreates the crash window where an ordinary immutable media route can serve a child sample indefinitely. |

No real voice sample or clone call was used. H1/H2 require the operator-owned
T13b probe against a deployed HTTPS origin before their capability status can
change.

Verification for this remediation: `go vet ./...`, `go test -count=1 -race
./internal/audio ./internal/mediastore ./cmd/thutapi`, the repeated C1 race pin
(`-count=20`), the repeated T13 H1/H3/H4 pins (`-count=10`), `gofmt -l .`,
`git diff --check`, the release build, `node --check`, and `bash -n` passed.
One full `go test -count=1 -race ./...` attempt was red only at the existing
flaky `internal/interview.TestRestartedHandlerSeesEndedInterview` timeout;
the T13 and mediastore packages passed in that run.
