# T8 round 2 — re-review (audio: narration + questions)

| | |
|---|---|
| **Target** | T8, uncommitted working tree on `main` at **`b658464`** (`T9a: the waiting race sprites…`, which also carries T10a `08a7f34`; **main moved during the T8 cycle** — the user committed T9a/T10a while T8 sat uncommitted). `git status` at review start and end: modified `internal/gmi/media/{client,client_test,live_test}.go` (the C1 cutover), untracked `internal/audio/**`, untracked `t8-round1.md` + `t8-remediation-round1.md` + this file. No `dev-diary/PLAN.md` / `dev-diary/prototypes/**` / `static/**` touched. Review base for the patch: working tree vs `b658464`. |
| **Evidence** | `t8-round1.md` in full (findings L1/L2, C1 ruling, D1–D5, requirement table) and `t8-remediation-round1.md` (the record this round audits); `internal/audio/**` in full (`audio.go`, `decode.go`, all seven test files incl. the L1/L2 fixtures and `wire_test.go`); the C1 cutover in `internal/gmi/media` (`client.go` SynthesizeSpeech + payload + attempt comment, `client_test.go` migrated sites + two byte pins, `live_test.go`); repo-wide `SynthesizeSpeech` grep; the emotion-key evidence chain: `internal/story/story.go` (Page.Emotion `json:"emotion"`, Emotions doc), `dev-diary/project.md` §4, `dev-diary/PLAN.md` §T8 + task-graph row 7 + status table, `t2b-t5b-live-record.md` (the live TTS payload echo), `t5-round1.md` (the vocabulary corroboration), all `*live-record*.md`; lambo recall; and — for the upstream question — the MiniMax native `speech-t2a-http` OpenAPI (primary source) and GMI request-queue docs. |
| **Method** | Fresh reviewer (`T8Round2Reviewer`) with no prior T8 round. Re-verified every closure by hand rather than trusting the records: shipped-file md5s confirmed byte-identical to the remediation record's own manifest before any mutation; all four mutants (M-L1, M-L2, M-C1a, M-C1b) re-applied one at a time, target pins observed **RED**, each restore verified `md5sum -c` OK over the full 13-file manifest; four pins re-run **GREEN** on the shipped tree; gates re-run in full. No live GMI call; no money; no file edited except this verdict file (mutations were transient `cp`-restored, byte-identical). |
| **Date** | 2026-09-05. |

*Mid-review note: a PLAN.md-only spec commit (`74e083b`, T9 seam gaps) landed
during this round; it touched no reviewed file, and the audited working tree
was byte-identical (md5, 13/13) before and after every mutation run.*


## Verdict

**REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L.**

The L1 and L2 closures are genuine (both fixtures bite the exact mutants round 1 named, proven by my own re-runs), and the C1 cutover is mechanically complete and clean (signature matches the seam exactly, all callers migrated, both raw-wire pins are true byte-equality assertions, both C1 mutants red). **One new H finding stands against the C1 landing itself** — the emotion-key question round 2 was directed to rule on: the payload key `emotion` on GMI's `minimax-tts-speech-2.8-hd` is asserted-and-pinned as settled fact while its evidence chain is self-referential, no live observation of the key exists anywhere, the only authoritative upstream schema contradicts the placement and vocabulary, and neither the risk nor a committed live-probe obligation is documented anywhere in the repo. The full ruling is below. This round cannot APPROVE; a remediation round must close H1 (doc note + committed probe obligation) and round 3 re-review.

## Closures verified (re-run by hand, not trusted from the record)

### L1 — closed. Fixture is load-bearing and bites the exact swallow mutant.

`TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio` (decode_test.go:210-221) serves **404 with `Content-Type: audio/mpeg`** and an error body — the shape that sails through the content-type gate, so only the status block (decode.go:138-140) stands between it and a persisted clip. **My re-run:** status block deleted → `err = <nil>, want errors.Is(.., ErrNoAudio)` → RED; restored byte-identical (md5 OK). Green on the shipped tree (0.00s).

### L2 — closed. Fixture genuinely distinguishes cap-3 from Go's default 10.

`TestFetchAudio_RedirectCapThreeDistinctFromGoDefault` (decode_test.go:279-295) answers **302 four times then 200** — under Go's own 10-hop ceiling the chain is followed to the clip (success); only the package's 3-hop cap (decode.go:191-193) errors on the 4th hop. The older self-redirect fixture cannot see this difference (its chain never reaches a 200, so the stdlib ceiling errors it too). **My re-run:** hop-cap block deleted → `err = nil, want the 3-hop chain refused` → RED (Go followed to the 200); restored byte-identical (md5 OK). Green on the shipped tree (0.00s).

### C1 — mechanically complete. Signature, payload, callers, and both byte pins verified.

- **Signature:** `media.SynthesizeSpeech(ctx, text, emotion, voice, model string)` (client.go:309) matches `audio.TTS`'s declaration (audio.go:201) parameter-for-parameter; the compile-time wiring check `var _ TTS = (*media.Client)(nil)` (wire_test.go:15) builds.
- **Payload:** `payload["emotion"] = emotion` only when `emotion != ""` (client.go:325-327).
- **Callers migrated:** repo-wide grep + `go build ./...` + `go vet -tags live ./...` — no stale 4-arg call anywhere. Five `client_test.go` sites and the `live_test.go` probe pass `""`; `narratePage` passes `p.Emotion` and `SynthesizeQuestion` passes `""` through the 5-arg seam.
- **The two raw-wire pins are byte-equality assertions, not contains-checks:** both compare the full marshalled body `string(raw) != want` (client_test.go:306-312 empty-direction, 342-345 present-direction), with the empty-direction additionally asserting the `emotion` substring is absent. **My re-runs:** M-C1a (insertion removed) → RED "raw body missing the verbatim emotion key"; M-C1b (unconditional insertion) → RED, body gained `"emotion":""` and no longer matches the live-verified shape. Both restored byte-identical.
- The two stale-docstring corrections the C1 ruling mandated are present (client.go:288-291, 400-405 region) and match the live record.
- Shipped md5s match the remediation record's manifest exactly (`decode.go` `57d22e7886b066fd56cb8b357c405204`, `client.go` `3797aaa2e343b092a2582553d799bac1`), so the tree audited is the tree the remediator shipped — decode.go itself was untouched by the remediation, as claimed.

## H1 — the `emotion` payload key is asserted-and-pinned but never verified against the upstream (self-referential evidence chain; no committed probe)

| Column | |
|---|---|
| **Severity** | H |
| **Where** | `internal/gmi/media/client.go:325-327` (the `payload["emotion"] = emotion` insertion), with the asserting byte pin at `client_test.go:342` and the settling claims at client.go:293-299, `internal/audio/audio.go` §The emotion mechanism, and `t8-remediation-round1.md` §The emotion payload key — settled. |
| **What** | C1's entire purpose is to carry the per-page emotion *into the model*. The landing sends a payload-top-level key named `emotion` and pins that exact byte string both directions — but no committed evidence shows that `emotion` is a real request key on **GMI's `minimax-tts-speech-2.8-hd` queue adapter**, where it sits, or which values it accepts. The remediation record claims the key is "settled, with evidence"; the chain it cites is fully self-referential: the C1 contract row (asserted, no upstream citation), a `decode_test.go` payload-echo fixture (authored by the same implementer), `story.Page.Emotion`'s `json:"emotion"` tag (the **M3 book-JSON** field name), project.md §4 ("Per-page `emotion` comes from M3's JSON" — names the M3 field, never a TTS request key), and audio/media docs describing Thutapi's own code. Against that: (a) **no live record anywhere shows an emotion key on any TTS wire** — the only `"emotion":"happy"` occurrences in all of dev-diary are the M3 book-JSON schema examples (PLAN.md:713, project.md:126); the one live TTS payload echo (t2b-t5b-live-record.md §The finding) carries `{text, voice_id, need_noise_reduction, need_volumn_normalization}` only; (b) the only authoritative upstream schema for the underlying model — MiniMax's own `speech-t2a-http` OpenAPI (read 2026-09-05) — places `emotion` **inside `voice_setting`** (`voice_setting: {voice_id, speed, vol, pitch, emotion}`), not at request top level, and enumerates `happy, sad, angry, fearful, disgusted, surprised, calm, fluent, whisper` — no top-level `emotion`, and no `neutral` (the value story teaches and audio validates as in-set, then refuses out-of-set words against Thutapi's own list, so an upstream-set mismatch is silently passed). GMI's live-verified dialect *is* flattened (voice_id at payload top level, queue-specific flag names), so a top-level `emotion` is plausible by analogy — and unverified by any observation. If the key or value is wrong, the queue's silently-tolerant behavior (established by t2b: a misspelled flag was accepted and echoed) delivers emotion-less narration with **every committed check green** — the exact "silently ignored" failure class this mechanism exists to prevent, on the exact seam (the T2-closed media client) T10 and T13 will build on. |
| **Pin** | No committed test can fail on this: the byte pins assert what *we* marshal, not what upstream accepts, and an emotion-carrying live call is the repo's own established bar for a wire shape (t2b pinned the `volumn` typo only after the live echo; AGENTS.md §Testing rule 5 / operator policy 2026-09-05: "prove it against the real API rather than against a fake whose shape you guessed… discovering a wire shape now is far cheaper than discovering it after two tracks are built on the wrong assumption"). Failing probe: `go test -tags live -run TestLiveSynthesizeSpeech ./internal/gmi/media/` with an emotion-carrying variant does not exist anywhere, and `grep -r T8b dev-diary` returns nothing — the settlement obligation is not committed. |
| **Mutation** | No source mutation can go red on this (that is the finding); the reviewer probe is documentary: replace the remediation's evidence chain with what it actually cites — grep of every live record (no wire emotion), of project.md §4 (M3 field only), of the native upstream schema (voice_setting placement, no `neutral`) — and the "settled" claim dissolves. |

**Ruling on the emotion-key question (explicit, as round 2 was directed):** the evidence is **insufficient**, and the current state is **a defect, not an acceptable documented-risk state** — both conditions that would make it acceptable are absent: (1) nowhere in code, docs, or records is the key stated as *asserted-but-unverified-live*; the prose asserts it as settled fact (story.go:62-70 "T8 passes the value straight into `minimax-tts-speech-2.8-hd`" reads as established behaviour — the same claim the C1 ruling itself graded H if the landing never happened, and its truth now depends on an unverified upstream fact); (2) **no T8b row or live-probe obligation is committed anywhere** (PLAN.md status table: `T8 | Not started`; task-graph row 7: "live (transport + shape), track not built"; zero `T8b` matches in dev-diary). The remediation record's own residue note hands "a narration request reaches the wire with the page's verbatim emotion" to round 2 — which cannot run live probes — so nothing after this round is committed to settle the key before T10/T13 consume the narration. The mandated fix is small and does not disturb the mechanically-correct landing: an explicit doc note (client.go SynthesizeSpeech docstring and audio.go §The emotion mechanism) that the `emotion` key, its top-level placement, and the in-set vocabulary (incl. `neutral`) are **asserted from Thutapi's own M3 field by analogy and not yet live-verified**, that the empty-emotion path is byte-identical to the live-verified question shape (unaffected), and that a T8b emotion-carrying probe (the queue echoes the payload — a real emotion-carrying call shows whether upstream kept the key, and GMI's model-details endpoint `GET /models/minimax-tts-speech-2.8-hd` settles the parameter schema for free) is the committed settlement point. The empty-emotion path is byte-identical to the live-verified shape either way, so questions are unaffected.

## Design rulings D1–D5 after the cutover (spot-check)

| Ruling | Status |
|---|---|
| **D1** emotion mechanism | **HOLDS.** Validation against `story.Emotions` before any call (validatePages/`isEmotion`, audio.go:408-438) intact; out-of-set refused loudly as `ErrInvalidPage`; questions pass `""`. The upstream-validity gap in the mechanism's own premise is H1 above. |
| **D2** narration persistence | **HOLDS.** `storeClip`/`place` (audio.go:456-503) untouched by the remediation (decode.go's md5 is identical to the remediation record's shipped manifest); blob-first-then-place, `ErrConflict` replace, caller-owned rows unchanged. |
| **D3** question shape | **HOLDS.** `SynthesizeQuestion` pure bytes-returning, turbo + `DefaultVoice`, no store write, `""` emotion (audio.go:380-401). |
| **D4** decode keys on `outcome` | **HOLDS.** decode.go unchanged; `queueRecord` reads only the `outcome` subtree; the payload echo is never consulted. |
| **D5** coverage note | **CONFIRMED.** 97.6% of statements on `internal/audio` (floor 75%); the four disclosed non-fault-injectable wraps are the only gaps. |
| **TTS interface ↔ concrete signature** | **EXACT.** `audio.TTS` (audio.go:201) and `media.SynthesizeSpeech` (client.go:309) declare identical parameter lists; `wire_test.go`'s `var _ TTS = (*media.Client)(nil)` compiles. |

## Mutation table — re-applied by this reviewer, observed red, restored byte-identical (md5-verified)

Baseline manifest of all 13 files (`/tmp/t8r2/baseline.md5`) recorded before the first mutation; shipped-file md5s match the remediation record (`decode.go` `57d22e…`, `client.go` `3797aaa…`). `md5sum -c` reported **OK for all 13 files after every restore and once more at the end**; `git status --porcelain` at the end shows only the pre-existing items above.

| # | Mutant | Result | Evidence (red reason) |
|---|---|---|---|
| M-L1 | `decode.go:138-140` non-200 status block deleted | **RED** | `TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio`: `err = <nil>, want errors.Is(.., ErrNoAudio)` — the audio-typed 404 sailed through the type gate and returned as a clip |
| M-L2 | `decode.go:191-193` hop-cap block deleted | **RED** | `TestFetchAudio_RedirectCapThreeDistinctFromGoDefault`: `err = nil` — Go's default 10-hop ceiling followed the 4th redirect to the 200 |
| M-C1a | `client.go:325-327` emotion insertion removed (always omit) | **RED** | `TestSynthesizeSpeech_EmotionPinnedInRawJSON`: raw body missing `"emotion":"happy"` |
| M-C1b | emotion insertion unconditional (always add) | **RED** | `TestSynthesizeSpeech_EmptyEmotionKeepsPayloadByteIdenticalInRawJSON`: payload gained `"emotion":""` — no longer byte-identical to the live-verified shape |

Plus the four pins verified **GREEN on the shipped tree** before mutating (both L1/L2 fixtures 0.00s; both byte pins 0.00s) and the compile-time wiring check builds with the package.

## Requirement re-check (round-1 requirement table, post-remediation)

| Requirement | Status (round 2) |
|---|---|
| L1 pin load-bearing (non-200 guard uniquely pinned) | **PASS** — fixture RED under M-L1, GREEN shipped |
| L2 pin load-bearing (3-hop cap distinct from Go's 10) | **PASS** — fixture RED under M-L2, GREEN shipped |
| C1 signature + conditional emotion payload key | **PASS** — matches seam; empty omits; M-C1a/M-C1b red |
| C1 all callers migrated, incl. live-tag files | **PASS** — grep + `go build ./...` + `go vet -tags live ./...` clean |
| C1 raw-wire pins are byte-equality, both directions | **PASS** — full-body `!= want` comparisons; emotion-absent check |
| `*media.Client` satisfies `audio.TTS` at compile time | **PASS** — `wire_test.go` var-check builds |
| Emotion key is the real upstream key / live-probe obligation | **FAIL — H1** (see finding) |

## Gates

| Gate | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go vet -tags live ./...` | clean (superset of the remediation's two-package claim; migrated live probe compiles) |
| `go test ./... -race -count=1` | ok, all 14 packages |
| `go test ./internal/audio/ -race -count=5` | ok (4.6 s) |
| `go test ./internal/audio/ -cover` | 97.6% of statements (floor 75%) |
| `go test ./internal/gmi/media/ -cover` | 92.0% of statements (floor 85%) |
| `gofmt -l .` | empty |
| Full-suite flake | none this run — no re-attribution needed; the round-1 environmental-flake note was not reproduced |

## Disposition

This reviewer created or modified nothing outside this verdict file. Mutations were transient, reverted byte-identical, md5-verified after every restore and at the end; no probe files remain. L1, L2 and the C1 cutover are closed with pins that bite their exact mutants in both directions. **H1 stands** — the emotion wire key is asserted-and-pinned but unverified against the upstream, with neither the risk documented nor a settlement probe committed. The remediation round must close H1 with the doc note and the recorded T8b probe obligation (per the finding's mandated fix), and round 3 re-review before this track can APPROVE with a zero-residue claim against rounds 1 and 2.

**REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L.**
