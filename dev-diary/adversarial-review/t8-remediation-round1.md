# T8 round 1 — remediation

| | |
|---|---|
| **Target** | All findings of `t8-round1.md` (0 C, 0 H, 0 M, 2 L — L1, L2) + the sanctioned contract row C1 (media `SynthesizeSpeech` gains the per-page `emotion`), per the C1 ruling's mandated landing. |
| **Date** | 2026-09-05 |
| **Status** | COMPLETE — every row landed; all four mutation re-runs red as required and restored byte-identical (md5-verified); gates green. Residue note below. |
| **Scope** | `internal/audio/**` (L1/L2 fixtures, C1's own cutover docs, `wire_test.go`), `internal/gmi/media/**` (C1 landing + callers), this record. Nothing else touched: no `PLAN.md`, no `dev-diary/prototypes/`, no `cmd/`, `internal/illustrate/`, `store/`, `mediastore/`, `story/`. No live call, no money. Verdicts unchanged; nothing fixed that nobody found. |

## The emotion payload key — settled, with evidence

The wire field for the per-page emotion is **`emotion`** (payload key, value = the
verbatim page emotion), not ambiguous — every independent evidence line names it:

- The C1 contract row (`t8-round1.md`): "The payload key is `emotion`, the value is the verbatim page emotion"; the C1 ruling mandates the raw pins as `emotion: "happy"` present (narration) / absent (questions).
- The payload-echo fixture already in this repo's audio decode pins (`decode_test.go:34`) models the request payload with `"emotion":"happy"` beside `text`/`voice_id` — the key sits at payload top level, next to the flags.
- `internal/story/story.go` — `Page.Emotion` is `json:"emotion"`, and `story.Emotions` is documented as the MiniMax Speech 2.8 emotion vocabulary because "T8 passes the value straight into `minimax-tts-speech-2.8-hd`" (story.go:62-70, corroborated against Speech 2.8 provider docs 2026-09-05).
- `project.md` §4 — "Per-page `emotion` comes from M3's JSON", and M3's page JSON spells the field `"emotion": "happy"` (project.md:126).
- The audio package doc (audio.go §The emotion mechanism) already names the payload's `emotion` field; the round-1 audio `TTS` seam and its fakes thread `(text, emotion, voice, model)` with that spelling.

No guessing was required: the key is `emotion`, added **only when non-empty**, next
to the two audio flags.

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **C1** — media `SynthesizeSpeech` must carry the per-page `emotion` (sanctioned contract row; mandated landing) | Signature becomes `SynthesizeSpeech(ctx, text, emotion, voice, model string)` — the exact shape `audio.TTS` declares. Payload keeps `{text, voice_id, need_noise_reduction, need_volumn_normalization}` and gains `payload["emotion"] = emotion` **only when `emotion != ""`**, so the questions path stays byte-identical to the live-verified shape (`t2b-t5b-live-record.md`). Stale docs corrected per the ruling: the `client.go` SynthesizeSpeech docstring ("returns the raw audio bytes") and `attempt`'s "TTS returns audio bytes, which are not JSON" comment now describe the terminal request-queue body (`outcome.audio_url`); package-doc bullet extended. Every caller migrated (clean cutover, no shims): `client_test.go` 5 call sites + `live_test.go` pass `""` for questions-style calls; the audio package already spoke the emotion-carrying shape. `audio.go`'s two C1-pending doc passages updated to landed tense. | **DONE.** Pins: `client_test.go` `TestSynthesizeSpeech_EmptyEmotionKeepsPayloadByteIdenticalInRawJSON` (empty emotion → full body byte-identical to the live-verified question payload, no `emotion` substring) and `TestSynthesizeSpeech_EmotionPinnedInRawJSON` (`"emotion":"happy"` on the wire byte-for-byte beside the flags); `internal/audio/wire_test.go` `var _ TTS = (*media.Client)(nil)` (compile-time satisfaction, the illustrate precedent). |
| 2 | **L1** — `fetchAudio`'s non-200 guard has no load-bearing pin | New fixture: server answering `404` with `Content-Type: audio/mpeg` and an error body; `fetchAudio` must return an error matching `ErrNoAudio`. The status guard (`decode.go:138-140`) — not the content-type gate — is what fires: an audio-typed non-200 would otherwise sail through the type gate and be persisted as a clip. | **DONE.** Pin: `decode_test.go` `TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio`. Red under M-L1 (status block deleted), green restored byte-identical. |
| 3 | **L2** — `safeRedirectPolicy`'s hop cap is not uniquely pinned | New fixture: server answering `302` four times then `200`; `fetchAudio` must error because the 4th hop exceeds `maxRedirectHops` (3). This is the fixture that distinguishes cap-3 from Go's default 10 (which follows to the 200). | **DONE.** Pin: `decode_test.go` `TestFetchAudio_RedirectCapThreeDistinctFromGoDefault`. Red under M-L2 (hop-cap block deleted), green restored byte-identical. |

## Files changed

| File | Change |
|---|---|
| `internal/gmi/media/client.go` | C1: signature + conditional `emotion` payload key; corrected SynthesizeSpeech docstring and `attempt`'s stale TTS-as-bytes comment; package-doc bullet. |
| `internal/gmi/media/client_test.go` | All 5 `SynthesizeSpeech` call sites migrated (`""` emotion); two raw-wire pins added (empty-omission byte-identity + emotion-present). |
| `internal/gmi/media/live_test.go` | `TestLiveSynthesizeSpeech` migrated (`""` emotion); compiles under `-tags live` (`go vet -tags live` clean). |
| `internal/audio/decode_test.go` | L1 fixture + L2 fixture (pins above). |
| `internal/audio/audio.go` | Two C1-pending doc passages ("satisfies this seam only after contract row C1 lands") updated to landed tense — the contract row is now landed, so the old prose would be stale. |
| `internal/audio/wire_test.go` | New: `var _ TTS = (*media.Client)(nil)` — `*media.Client` satisfies `audio.TTS` at compile time, the wiring proof round 2 verifies. |

## Mutation re-runs — applied, observed red, restored byte-identical (md5-verified)

Method per the round-1 reviewer: each mutant applied by hand to the shipped
tree, the target pin run, the file restored from the `/tmp/t8rem` backup of the
shipped state, `md5sum -c` after every restore. Baseline manifest
(`/tmp/t8rem/baseline.md5`) recorded before the first mutation; **OK for both
files after every restore and once more at the end**. No mutant left the tree.

Shipped-file md5s: `internal/audio/decode.go` = `57d22e7886b066fd56cb8b357c405204`;
`internal/gmi/media/client.go` = `3797aaa2e343b092a2582553d799bac1`.

| # | Mutant | Result | Evidence (red reason) |
|---|---|---|---|
| M-L1 | `decode.go:138-140` non-200 status block deleted | **RED** | `TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio`: err = nil, want `errors.Is(.., ErrNoAudio)` — the audio-typed 404 was returned as a clip |
| M-L2 | `decode.go:191-193` hop-cap block deleted | **RED** | `TestFetchAudio_RedirectCapThreeDistinctFromGoDefault`: err = nil — Go's default 10-hop ceiling followed the 4th redirect to the 200 |
| M-C1a | `client.go` emotion insertion removed (always omit) | **RED** | `TestSynthesizeSpeech_EmotionPinnedInRawJSON`: raw body missing `"emotion":"happy"` |
| M-C1b | `client.go` emotion insertion unconditional (always add) | **RED** | `TestSynthesizeSpeech_EmptyEmotionKeepsPayloadByteIdenticalInRawJSON`: payload gained `"emotion":""` — no longer byte-identical to the live-verified shape |

## Gates

| Gate | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `gofmt -l .` | empty |
| `go test ./internal/audio/ -race -count=5` | ok (4.6 s) |
| `go test ./internal/audio/ -cover` | 97.6% of statements (floor 75%) |
| `go test ./internal/gmi/media/ -cover` | 92.0% of statements (floor 85%) |
| `go test ./... -race -count=1` | ok, all packages |
| `go vet -tags live ./internal/audio/ ./internal/gmi/media/` | clean (migrated live probe compiles) |

## Residue

* All three rows (C1, L1, L2) are DONE with pins that fail on reversion of the
  fix; the four mutation re-runs above prove the pins bite in both directions
  (L1/L2 red under their mutants; C1 red under always-omit and always-add).
* The pre-existing untracked `static/` (user work), sibling review files and
  `PLAN.md`/`dev-diary/prototypes/` were not touched; `git status --porcelain`
  shows only `internal/gmi/media/{client,client_test,live_test}.go` modified and
  `internal/audio/` + this record untracked (the round-1 baseline).
* **Zero residue is claimed only after round 2's verdict**: round 2 must verify
  `*media.Client` satisfies `audio.TTS` at the wiring point (`wire_test.go`'s
  compile-time check), that a narration request reaches the wire with the
  page's verbatim emotion and the questions path stays byte-identical, re-run
  M-L1/M-L2/M-C1a/M-C1b, and issue its own APPROVE with an explicit zero-residue
  claim against this round.
