# T8 round 3 — re-review (audio: narration + questions)

| | |
|---|---|
| **Target** | T8, uncommitted working tree on `main` at **`74e083b`** (`Spec: close five T9 seam gaps…` — a PLAN.md-only commit, 171 insertions, that landed during round 2; it touched no reviewed file). The audited patch is the working tree vs `74e083b`: modified `internal/gmi/media/{client,client_test,live_test}.go`, untracked `internal/audio/**`, untracked `t8-round1.md` + `t8-remediation-round1.md` + `t8-round2.md` + `t8-remediation-round2.md` + this file. No `dev-diary/PLAN.md` / `dev-diary/prototypes/**` / `static/**` touched. Review base for the round-2 remediation: the round-2 review's H1 finding and its mandated three-part fix, as landed by `t8-remediation-round2.md`. |
| **Evidence** | `t8-round2.md` in full (H1 finding, ruling, mandated fix, disposition), `t8-remediation-round2.md` in full (the record this round audits), `t8-round1.md` (L1/L2 + C1 ruling + manifest md5s `decode.go` `57d22e7886b066fd56cb8b357c405204`, `client.go` `3797aaa2e343b092a2582553d799bac1`) and `t8-remediation-round1.md`; `internal/gmi/media/client.go` (SynthesizeSpeech docstring note 300–313, signature 323, payload 333–342), `internal/audio/audio.go` (package doc §The emotion mechanism note 41–54, §The response shape, TTS seam 211–217), `internal/gmi/media/live_test.go` in full (the settlement probe, conventions, typed echo structs), `internal/gmi/media/client_test.go` (migrated call sites, both raw-wire byte pins), `internal/audio/decode.go` + `decode_test.go` (L1/L2 guards + fixtures), `wire_test.go`, `internal/story/story.go` (Emotions doc); `git show --stat 74e083b`; batch brief (re-observation 2026-09-05 evening: probe emotion subtest fails with the upstream transient error, catalog subtest passes). |
| **Method** | Fresh reviewer (`T8Round3Reviewer`) with no prior T8 round. Every closure re-verified by hand rather than trusted from the records: doc-note text read and matched against the code it describes and the record's evidence; probe logic and its settle-claims checked against the decode path and the file's conventions; shipped-file md5s confirmed against the round-1 remediation manifest (decode.go byte-identical — it did not move) and the client.go delta shown to be a pure 14-line doc insertion (signature/payload line arithmetic +14, byte pins green, code read); all four mutants (M-L1, M-L2, M-C1a, M-C1b) re-applied one at a time, target pins observed **RED**, each restore `md5sum -c` OK over the full 11-file manifest; the four pins re-run **GREEN** on the shipped tree; gates re-run in full, including a full-suite flake episode chased to attribution with a clean `74e083b` worktree baseline (no T8 patch). No live GMI call; no money; no file edited except this verdict file (mutations were transient `cp`-restored, byte-identical). |

**Date**: 2026-09-05.

## Verdict

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**

H1's remediation is complete to the standard the finding itself set: both
behaviour-defining doc sites now state the emotion key as asserted-but-unverified
live with the silent-ignore consequence and the named probe as the flip trigger;
the settlement probe is committed, mechanically runnable, convention-conforming,
and its assertions genuinely settle the wire question; the remediation record
carries the 503 evidence and an explicit open-until-pass statement. Round 1's L1,
L2 and the C1 cutover are undisturbed — decode.go is byte-identical to the round-1
manifest, client.go's only delta is the doc note insertion, and all four mutant
re-runs bite as they did in rounds 1–2. The residual live pass is recorded,
committed b-track evidence — not an open defect — and is stated as a boundary
below. Full details: H1 ruling, closures, mutation table, gates, zero-residue claim.

## Ruling on the H1-closure question (explicit, as round 3 was directed)

**The H1 remediation is COMPLETE for this round. Verdict APPROVE, with the live
pass carried as a stated boundary obligation into the T8b record.**

Round 2's finding graded the pre-remediation state H because *both* conditions
that would make it an acceptable documented-risk state were absent. Its own
ruling text is the standard to judge against:

> "the current state is **a defect, not an acceptable documented-risk state** —
> both conditions that would make it acceptable are absent: (1) nowhere in code,
> docs, or records is the key stated as *asserted-but-unverified-live*; the prose
> asserts it as settled fact …; (2) **no T8b row or live-probe obligation is
> committed anywhere**."

Both conditions are now present, and I verified each by hand:

**(a) The doc notes state asserted-but-unverified live, claim nothing the code
does not implement, and match the record's evidence.** `client.go:300–313` (the
SynthesizeSpeech docstring) and `audio.go:41–54` (package doc, §The emotion
mechanism) each say: the emotion key's top-level placement and the in-set
vocabulary (`story.Emotions`) are pinned on the wire (contract row C1) but have
never been confirmed against the minimax-tts-speech-2.8-hd queue adapter; the TTS
pool answered the settlement call(s) with HTTP 503 "Upstream capacity temporarily
exhausted" on 2026-09-05, so no live echo of an emotion-carrying call exists; if
upstream ignores or rejects the key, narration renders emotion-less with every
check green (the silent-ignore class; t8-round2.md H1); the empty-emotion path is
unaffected and stays byte-identical to the live-verified question shape. Every
clause cross-checks: the code sends `payload["emotion"] = emotion` only when
non-empty (client.go:339–341) — empty omission is real and byte-pinned; the 503
claim matches the record's evidence table (§3: 10+ POST attempts, all 503); no
live echo exists anywhere on record (all live TTS payload echoes carry only
`{text, voice_id, need_noise_reduction, need_volumn_normalization}`). Neither
note asserts verification; each is date-bound to 2026-09-05. No remaining
behaviour-defining site asserts the key as settled fact: the package-doc bullet
(client.go:17–23) and `story.go:62–70` describe the pass-through mechanism and
provider-doc vocabulary provenance (t5-round1.md), not a live confirmation — the
refined, honest statement lives at the two mandated sites.

**(b) The flip mechanism is real and findable.** `client.go:310–311`: "flip this
note when it passes against a recovered upstream (t8-remediation-round2.md)";
`audio.go:51–52`: "these notes flip when it passes against a recovered upstream
(t8-remediation-round2.md)". The only trigger named in either note is the probe
`TestLiveSynthesizeSpeech_EmotionKeyEchoed`; the record's residue binds the pass
to the flip ("the recording of the pass must also flip those notes, or the 'Docs
about behaviour must match the behaviour' rule … turns the date-bound caveat
itself stale"). The mechanism is manual (no test can mechanically force a doc
flip), but it is explicit, cross-referenced, and enforceable at the next touch of
these files — that is as real as a doc-flip mechanism can be, and it is the one
the finding mandated.

**(c) The settlement probe is committed, mechanically runnable, convention-
conforming, and genuinely settles the question.** `TestLiveSynthesizeSpeech_
EmotionKeyEchoed` (live_test.go:88–156) lives behind `//go:build live` in the
file the b-track precedent owns, uses `liveClient(t)` (skip when `GMI_API_KEY`
unset), a 120 s deadline, and `truncate`-bounded logging — the file's own
conventions. Two subtests:
- **`emotion key echoed`** — the settlement assertion. Calls the real
  `SynthesizeSpeech(ctx, text, "happy", voice, model)` through the real client
  (submit + poll to the terminal record), decodes the record's echoed `payload`
  subtree into a typed struct, and fails unless `Payload.Emotion == "happy"`.
  The settle-mechanics hold by inspection of the decode path: the echo payload
  is unmarshalled into `emotionEchoPayload` with `Emotion string \`json:"emotion"\``
  (live_test.go:181), so a stripped key leaves `Emotion` at the zero value `""`
  → `"" != "happy"` → the subtest fails with the record body logged; a rejected
  or 503'd call fails at the `t.Fatalf` on the error — which is what the
  re-observed 2026-09-05 evening run did (upstream transient error), per the
  batch brief and consistent with the record's §3. There is no false-pass path:
  passing requires the terminal record to literally echo `"emotion":"happy"`.
- **`model in catalog`** — the cheap second assertion, registered first. GETs
  `pathModelCatalog` with the same apikey prefix and asserts
  `minimax-tts-speech-2.8-hd` is in `model_ids`. It makes no TTS POST, so it
  passes or fails independent of the TTS pool (live-observed present on
  2026-09-05 even while the pool 503'd every POST — record §3; re-observed
  passing the same evening).

The probe is the committed obligation the finding's Pin said was missing, in the
exact form the Pin demanded ("an emotion-carrying live call … against
production"), and it is vet-clean under `-tags live` (gate below).

**(d) The record carries the 503 evidence and the open-until-pass statement.**
`t8-remediation-round2.md` §3 tables the 2026-09-05 live evidence — POST TTS
emotion-carrying variant: HTTP 503 on every attempt (10+ over ~10 minutes);
model catalog GET: 200 with the model present, no parameter schemas published
(why an echo probe, not a schema lookup, settles the key); text-gateway GET 405;
no live TTS payload echo anywhere carries an emotion key — and its Residue
states plainly: "**H1 is open until the live pass** … the live pass is recorded
here when upstream recovers." That is the open-until-pass record, in the file
the doc notes cite as the flip's home.

**Why the live pass is not an open defect this round.** The remaining step —
the probe's first green run against a recovered upstream, recorded in the T8b
record, flipping both notes — is live evidence, not code or documentation.
AGENTS.md is explicit that live verification splits into a `b` track: "A live
call is evidence, never a CI gate"; "Commit the probe, not a transcript"; the
T1/T1b, T2b, T5b and T6/T6b precedents all land b-track live evidence after
their parent track's APPROVE (T6 closed at round-2 APPROVE 0/0/0/0 while T6b
items 1–3 continued to land separately). A REMEDIATE verdict here could not
conjure an upstream recovery — the round's only remaining condition is an
external service's availability — which is precisely the case the b-track split
exists to keep out of the code-review loop. The obligation is committed,
findable in three places (two doc notes + the probe file + the record's
residue), and its settlement is scheduled by the record, not lost. A formal
`T8b` row in PLAN.md's status table is the orchestrator's to add when the track
lands (PLAN.md is concurrent user work this round was forbidden to touch; its
status table does not yet list T8's landing at all). The finding's Pin used the
disjunction "a T8b row **or** live-probe obligation" — the obligation is
committed; that satisfies the clause.

## Closures verified (re-run by hand, not trusted from the record)

### H1 — remediation complete to the finding's own standard (see ruling above)

Doc notes verified textually at both sites and cross-checked against code and
record; probe verified for existence, conventions, compile-cleanliness under
`-tags live`, and settle-mechanics (stripped key decodes to `""` and fails;
503/reject fails; catalog subtest is pool-independent); flip mechanism confirmed
findable in both notes; record confirmed to carry the 503 evidence and the
open-until-pass statement. No live run performed by this reviewer (no live GMI
calls permitted this round); the 2026-09-05 evening re-observation (emotion
subtest fails with the upstream transient error, catalog subtest passes) is the
batch brief's and the record's evidence, and both are consistent with the
probe's design.

### L1 / L2 — closed and undisturbed (decode.go never moved)

`internal/audio/decode.go` md5 = `57d22e7886b066fd56cb8b357c405204` — byte-
identical to the round-1 remediation manifest, which rounds 1 and 2 both audited.
The round-2/3 remediation touched no code in the audio package. The L1 fixture
(`TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio`, decode_test.go:210 —
404 served as `audio/mpeg`, which sails through the content-type gate, so only
the status block at decode.go:138–140 stands) and the L2 fixture
(`TestFetchAudio_RedirectCapThreeDistinctFromGoDefault`, decode_test.go:279 —
302×4 then 200, which only the 3-hop cap at decode.go:191–193 refuses) are
present. **My re-runs:** both fixtures GREEN on the shipped tree; M-L1 (status
block deleted) → RED `err = <nil>, want errors.Is(.., ErrNoAudio)`; M-L2 (hop
cap deleted) → RED `err = nil` — Go's default 10-hop ceiling followed the 4th
redirect to the 200. Both restores md5-verified.

### C1 cutover — undisturbed (client.go's only delta is the doc note)

The round-2 remediation changed client.go by doc insertion only: the
asserted-but-unverified note occupies 14 lines (300–313) immediately before the
signature, and every code landmark round 2 recorded has shifted by exactly +14
(signature was 309 → now 323; payload insertion was 325–327 → now 339–341;
stale-docstring corrections still present at 288–291 and the attempt comment at
415–425). Signature `(ctx, text, emotion, voice, model string)` still matches
`audio.TTS` exactly; `payload["emotion"] = emotion` still conditional on
non-empty; `wire_test.go`'s `var _ TTS = (*media.Client)(nil)` still builds.
Both raw-wire byte pins are full-body equality assertions (client_test.go:284–313
empty-direction with an added emotion-substring-absent check, 320–346
present-direction). **My re-runs:** both pins GREEN on the shipped tree;
M-C1a (insertion removed) → RED `raw body missing the verbatim emotion key`;
M-C1b (unconditional insertion) → RED, payload gained `"emotion":""` and no
longer matches the live-verified shape. Both restores md5-verified. All five
`client_test.go` call sites and the migrated `live_test.go` probe pass the 5-arg
signature with `""`; repo-wide grep finds no stale 4-arg call; `go vet -tags
live` and `go build ./...` are clean.

## Mutation table — re-applied by this reviewer, observed red, restored byte-identical (md5-verified)

Baseline manifest of all 11 reviewed files (`/tmp/t8r3/baseline.md5`) recorded
before the first mutation; shipped md5s match the record where a manifest exists
(`decode.go` `57d22e…`) and reflect the round-2 doc insertion where expected
(`client.go` `6b5b421b…` — code regions verified identical by read + line-shift
arithmetic + the pins below). `md5sum -c` reported **OK for all 11 files after
every restore and once more at the end**; `git status --porcelain` at the end is
identical to the start snapshot (the round-1 baseline items plus the round-2
edits and the review files — nothing else).

| # | Mutant | Result | Evidence (red reason) |
|---|---|---|---|
| M-L1 | `decode.go:138-140` non-200 status block deleted | **RED** | `TestFetchAudio_Non200WithAudioContentTypeIsErrNoAudio`: `err = <nil>, want errors.Is(.., ErrNoAudio)` — the audio-typed 404 returned as a clip |
| M-L2 | `decode.go:191-193` hop-cap block deleted | **RED** | `TestFetchAudio_RedirectCapThreeDistinctFromGoDefault`: `err = nil` — Go's default 10-hop ceiling followed the 4th redirect to the 200 |
| M-C1a | `client.go:339-341` emotion insertion removed (always omit) | **RED** | `TestSynthesizeSpeech_EmotionPinnedInRawJSON`: raw body missing the verbatim emotion key |
| M-C1b | emotion insertion unconditional (always add) | **RED** | `TestSynthesizeSpeech_EmptyEmotionKeepsPayloadByteIdenticalInRawJSON`: payload gained `"emotion":""` — no longer byte-identical to the live-verified shape |

Plus the four pins verified **GREEN on the shipped tree** both before and after
the mutation cycle, and the compile-time wiring check builds with the package.

## Gates

| Gate | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go vet -tags live ./internal/gmi/media/ ./internal/audio/` | clean — the settlement probe compiles under the `live` tag |
| `gofmt -l .` | empty |
| `go test ./internal/audio/ ./internal/gmi/media/ -race -count=1` | ok (audio 1.7 s, media 6.0 s) |
| `go test ./internal/audio/ -race -count=10` | ok (8.1 s) — flake shake, no intermittent deadlock in isolation |
| `go test ./internal/audio/ -cover` | 97.6% of statements (floor 75%) |
| `go test ./internal/gmi/media/ -cover` | 92.0% of statements (floor 85%) |
| `go test ./... -race -count=1` | **ok — final run, all 14 packages** (EXIT=0). Flake episode: three earlier full-suite runs failed in three *different* packages (cmd/thutapi race 1×; internal/audio 600 s stall 1×; internal/interview poll timeout 1×), none reproducible in isolation (cmd 5× green; audio 10× green; interview green in the two-package and clean-base runs). Attribution: three full-suite runs on a clean `74e083b` worktree *without the T8 patch* flaked the same way (internal/job poll timeout 1×) — the class is a pre-existing environmental full-suite `-race` timing flake on this box (round 1 recorded the same class), not patch-attributable. The audio stall consumed 0 CPU for 8:46 (a blocked wait, not computation); every audio test wait is bounded by a guard, cleanup or timeout channel (verified by reading audio_test.go/narrate_test.go/decode_test.go/persist_test.go), and it never reproduced. |
| Tree byte-identity | `md5sum -c` 11/11 OK after every restore and at the end; `git status --porcelain` identical to the start snapshot; single worktree (`74e083b [main]`). This reviewer created nothing in the tree except this verdict file. |

## Zero-residue claim

**Against round 1** (L1, L2, C1 cutover) — zero residue: the L1 and L2 fixtures
are load-bearing (RED under their exact mutants, GREEN shipped, re-run by this
reviewer), decode.go is byte-identical to the round-1 manifest, the C1 cutover
is mechanically complete and pinned in both byte directions with all callers
migrated and the compile-time wiring check in place, and the round-2/3
remediation touched none of that code.

**Against round 2** (H1) — zero residue within the track's reach: the
asserted-but-unverified doc notes, the committed settlement probe, and the
503/open-until-pass record are all present, correct to the finding's own
standard, and verified above. The one remaining obligation is the **live pass**:
`TestLiveSynthesizeSpeech_EmotionKeyEchoed`'s first green run against a
recovered upstream — recorded in `t8-remediation-round2.md` (the T8b record)
and flipping both date-bound doc notes per their own "flip when it passes"
lines (`client.go:310-311`, `audio.go:51-52`). That obligation is committed,
findable, and b-track evidence by AGENTS.md design; it is stated here as the
boundary of this APPROVE, not as residue of this track.

**No new findings** in the round-2/3 remediation.

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**
