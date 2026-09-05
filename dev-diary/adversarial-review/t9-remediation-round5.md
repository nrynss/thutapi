# T9 round 5 — remediation

The single H1 finding from `t9-round5.md` is remediated within the T9-owned
frontend seam. No verdict or plan status is changed, and no unrelated dirty
work is included.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `static/app.js` (`silentUnlockURL`); `static/browser-test.js` (`isSilentPCM16WAV`, `FakeAudio.play`) | The gesture unlock source declared one byte of PCM data for a 16-bit mono format with two-byte block alignment, so it was an incomplete sample frame and could be rejected by a mobile decoder. | Replaced the source with a 46-byte RIFF/WAVE PCM16 mono asset whose RIFF and data sizes agree and whose data contains one complete zero sample. The browser fake now decodes data URLs and requires RIFF/WAVE/fmt/data markers, PCM16 mono fields, matching sizes, complete block alignment, and silent data before resolving `play()`. | `testAudioUnlockContract` asserts the shelf gesture plays the embedded WAV and later question audio plays after the gesture. The deterministic decoder pin rejects the former one-byte/misaligned base64 mutation with `invalid silent wav`; the repaired asset passes those same checks. | Restoring the one-byte payload or removing the fake's WAV validation makes the frame-alignment mutation pass, so the contract no longer detects an unplayable unlock source. |

## Checks

* `node --check static/app.js static/browser-test.js` — PASS
* `go test ./... -race` — PASS
* `go vet ./...` — PASS
* `test -z "$(gofmt -l .)"` — PASS
* `git diff --check` — PASS
* WAV invariant probe (repaired source and one-byte mutation) — PASS; repaired source has `bytes=46`, `riffSize=38`, `dataSize=2`, `blockAlign=2`, while the mutation is rejected

**REMEDIATED — H1 has a behavioral fix and deterministic mutation pin.**
