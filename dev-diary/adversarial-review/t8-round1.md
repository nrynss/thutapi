# T8 round 1 — implementation stub (audio: narration + questions)

| | |
|---|---|
| **Target** | T8, `internal/audio/**` (all files new) on top of the working tree at `847e9b6`. `git status` at start: clean tree, no `internal/audio/`. This file is the implementer's pre-review record per the AGENTS.md loop: the proposed contract-change rows and the design decisions with their evidence. The verdict, findings and pins belong to the round-1 reviewer and are not written here. |
| **Recorded by** | Implementation agent (`T8Implementer`), 2026-09-05. |
| **Scope** | Only `internal/audio/**` and this stub were created. No file outside them was edited: no `cmd/`, no `PLAN.md`, no `internal/gmi/*`, `internal/store`, `internal/mediastore`, `internal/story`, `internal/illustrate`, `internal/interview`. |

---

## Contract-change rows proposed

### C1 — `internal/gmi/media` SynthesizeSpeech must carry the per-page `emotion`

- **Where:** `internal/gmi/media/client.go:296` (`func (c *Client) SynthesizeSpeech(ctx context.Context, text, voice, model string) ([]byte, error)`), payload map at `client.go:306-311` (`{text, voice_id, need_noise_reduction, need_volumn_normalization}` only).
- **Why:** the T8 spec narrates with per-page `emotion` from T5 — PLAN.md §T8 ("Narration: `minimax-tts-speech-2.8-hd`, per-page `emotion` from T5"), project.md §4 ("Per-page `emotion` comes from M3's JSON") and `internal/story/story.go:62-70` ("T8 passes the value straight into `minimax-tts-speech-2.8-hd`, where an out-of-set word is silently ignored"). The live-verified request payload (`t2b-t5b-live-record.md` §The finding, which echoes what the client sent) has no `emotion` key, so the closed client cannot express the spec today. PLAN.md invariant 1 forbids `internal/audio` from building its own call, and T8's `Owns` is `internal/audio/**` only — so the client change is out of this track's reach.
- **Change requested:** `SynthesizeSpeech` gains an `emotion string` parameter (an empty emotion omits the key, keeping today's payload byte-identical for the question path — that is the shape `audio.TTS` declares, see below). The payload key is `emotion`, the value is the verbatim page emotion.
- **Implement-around in this track:** `audio.TTS` (the consumer-declared seam, `internal/audio/audio.go`) is declared with the emotion-carrying shape `SynthesizeSpeech(ctx, text, emotion, voice, model)`. `*media.Client` satisfies it the moment C1 lands; nothing in this package routes around `internal/gmi`. Tests exercise the seam with a fake that records `(text, emotion, voice, model)` per call, which pins the emotion threading, the defaults and page order at the seam this package controls.
- **Why not a second seam or a silent drop:** a narration API whose emotion argument never reached the wire would be the silent omission the seam ruling forbids; two seams (one with, one without emotion) would saddle `Config` with a temporary second client field that vanishes when C1 lands.

No other contract change is proposed. In particular **`internal/mediastore` needs no change**: `audio/mpeg` and `audio/wav` (the Speech 2.8 mp3 and the documented TTS fallback) are already in its closed set (`internal/mediastore/mediastore.go:69-75`), and `store.MediaNarration` plus its `(book, page)` unique slot already exist (`internal/store/media.go:25-27`, `SetMediaPlace` at `media.go:133-181`, `PageMedia` at `media.go:218-233`). The `books`/`pages` anchors T8 needs were all built by T3.

---

## Design decisions, with evidence

### D1 — The emotion mechanism: payload `emotion`, verbatim page value, refused loudly when out of set

- **Evidence:** `internal/story/story.go:62-70` — `story.Emotions` *is* "the emotion vocabulary of MiniMax Speech 2.8, because T8 passes the value straight into `minimax-tts-speech-2.8-hd`, where an out-of-set word is silently ignored"; corroborated against independent Speech 2.8 provider docs 2026-09-05 (t5-round1.md). T5b verified every live page emotion inside that vocabulary. Lambo memory corroborates: "minimax-tts-speech-2.8-{hd,turbo} takes text + a library voice_id ... with emotion/speed/pitch/... controls".
- **Decision:** narration sends the page's `story.Page.Emotion` verbatim as the TTS payload's `emotion` field (C1); questions send none. `internal/audio` validates every page's emotion against `story.Emotions` (exact, lowercase) **before any call** — `ErrInvalidPage` naming the word and the vocabulary — because an out-of-set word would otherwise be silently ignored by the provider (the exact failure T5's "fail loudly" rule exists to prevent). Empty page text is `ErrNoText`, page numbers below 1 or duplicated in one run are `ErrInvalidPage` (two clips would race one slot).
- **Cast voice fields (`story.Voice.Pitch`, `SoundEffects`) do not reach the TTS call in T8.** project.md §4's mechanism for them — re-calling a voice clone with `pitch` / `timbre` / `sound_effects` — is the voice-clone track, and AGENTS.md's two-day cut list cuts voice clone (T13) and multi-character voices first, leaving the narrator-only default path. T8 speaks `DefaultVoice` (`English_expressive_narrator`, the PLAN.md §T8 default) and nothing else; `Config.Voice`/`Config.Model` remain the one-line switch for a later voice.

### D2 — Narration persistence mirrors T7's BookWriter: caller owns rows, blob first, then place, conflict replaces

- **Who owns row creation:** the caller, exactly as for T7 (`internal/illustrate/persist.go:21-25`): `store.CreateBook` / `store.CreatePage` must have run before narration places a clip, or the place fails with `store.ErrInvalid` (the page FK fires on the update — pinned by `TestNarrate_MissingPageRowFailsLoudly`). Narration never creates rows; it writes blobs and anchors `MediaNarration` at `(book, page N)`, one clip per page.
- **Ordering:** download `outcome.audio_url` on receipt (PLAN.md invariant 7 — the URLs are assumed to expire), then `mediastore.Persist` the bytes, then `SetMediaPlace` — the same blob-first-then-place order as T7, so a re-run keeps serving the previous clip until the replacement is on disk. A slot already occupied (`store.ErrConflict`) is replaced T7-style: read the occupant (`PageMedia`), `mediastore.Delete` it, retry the place. Pinned by `TestNarrate_DownloadCompletesBeforeTheRowExists` (the row must not exist while its download is gated) and `TestNarrate_ReRunReplacesTheOccupant` (the old row and blob survive until the replacement download finishes; after the run only the new rows remain).
- **Fan-out:** per-page synthesis/download/persist runs through `golang.org/x/sync/errgroup` bounded to `Config.Limit` (default 4, mirroring illustrate's `DefaultLimit`), never a serial loop — the plan's "8 narration calls at ~24 s each need fan-out" milestone. The first failure cancels the rest and returns a nil clip result; page order is deterministic (calls and returned clips follow the input order; pinned with `Limit: 1`, and the fan-out itself pinned by a latch test).

### D3 — Question audio shape: a plain function returning downloaded bytes, no store write

- **Evidence:** project.md §4 ("Stream the question text over SSE immediately, fire the TTS call in parallel, play the audio when it lands. Text never waits on audio") and PLAN.md §T8's same sentence. Nothing in project.md §4 / PLAN.md §T4–T9 persists interview audio: the transcript the store keeps is text turns (`internal/store/interviews.go`), the `media` table anchors only books/pages/cast, and the flow plays the clip once over SSE. The voice-clone sample capture (T13) is the only interview-adjacent audio, and it is cut.
- **Decision:** `SynthesizeQuestion(ctx, cfg, text) ([]byte, error)` — turbo model (`DefaultQuestionModel`, latency beats fidelity), `DefaultVoice`, no emotion (payload stays byte-identical to the live-verified request), download on receipt, return the playable bytes, touch no store row. A plain synchronous bytes-returning function is the parallel-fire shape: the wiring track fires it in a job/goroutine while the text streams. No `internal/job` dependency — that seam is T3b's and stays the caller's.

### D4 — The envelope decode keys on `outcome` alone

- **Evidence:** the live terminal TTS record (`t2b-t5b-live-record.md`): `{request_id, model, status, payload: <request echoed verbatim>, outcome: {audio_url, format, status}}`, the audio a public `storage.googleapis.com` URL answered `200 audio/mpeg` (~63 KB). `media.SynthesizeSpeech` returns that raw body — not audio bytes (its package doc is stale on this; the live record is the authority the plan §T8 pins).
- **Decision:** decode reads only `outcome.audio_url` (absolute http(s)); the `payload` echo is never consulted — the exact echo hazard that produced T6's round-1 H1. A body without `outcome.audio_url` is `ErrNoAudio` (raw MP3 bytes included — a shape change must be loud, never passed through as audio). Download content type gates persistence: the response's `Content-Type` header must be `audio/mpeg` or `audio/wav` (the mediastore closed set), wav spellings normalised; a body sniff is only the header-less fallback because Go detects an MP3 solely by its ID3 prefix and an ID3-less real clip sniffs as `application/octet-stream` — the header is the statement to trust (live-verified). Raw wire pins: the verbatim live envelope decodes to its URL; an audio-looking URL inside the payload echo can never win.

### D5 — Coverage note for the reviewer

Reachable error branches are all asserted with `errors.Is` (97.6% statement coverage on `internal/audio`). Four statements are not fault-injectable through the concrete `*store.DB` / `*mediastore.Store` pointers: `narrationWriter.storeClip`'s read-back wrap and `place`'s three replace-path wraps (occupant read, `Delete`, retried `SetMediaPlace` — each requires the store to break between two adjacent successful calls). This mirrors `internal/illustrate/persist.go`'s `BookWriter.place`, which ships with the same concrete-pointer profile; if the reviewer wants them pinned, the fault seam belongs in `store`/`mediastore` (another contract row), not in this package.

---

## Files created

- `internal/audio/audio.go` — package doc; models/voice/limit defaults; sentinels; `TTS`, `Config`, `Clip`; `NarrateBook`, `SynthesizeQuestion`; validation; narration writer (blob-then-place, conflict replace).
- `internal/audio/decode.go` — envelope decode (keys on `outcome.audio_url`), download with size cap and redirect policy, content-type gate.
- `internal/audio/audio_test.go` (harness), `narrate_test.go`, `question_test.go`, `decode_test.go`, `persist_test.go`.
- This stub.

## Export surface of `internal/audio`

- Constants: `DefaultNarrationModel`, `DefaultQuestionModel`, `DefaultVoice`, `DefaultLimit`, `MaxAudioBytes`.
- Errors: `ErrNoTTS`, `ErrNoStore`, `ErrNoPages`, `ErrInvalidPage`, `ErrNoText`, `ErrNoAudio`, `ErrUnsupportedAudio`.
- `type TTS interface { SynthesizeSpeech(ctx context.Context, text, emotion, voice, model string) ([]byte, error) }`
- `type Config struct { TTS; DB *store.DB; Blobs *mediastore.Store; HTTPClient *http.Client; Limit int; Voice, Model string }`
- `type Clip struct { N int; Media store.Media }`
- `func NarrateBook(ctx context.Context, cfg Config, bookID string, pages []story.Page) ([]Clip, error)`
- `func SynthesizeQuestion(ctx context.Context, cfg Config, text string) ([]byte, error)`

---

# T8 round 1 — adversarial review (audio: narration + questions)

The implementer's record above (contract row C1, design decisions D1–D5) is
preserved verbatim as the pre-review evidence this round rules on.

| | |
|---|---|
| **Target** | T8, uncommitted working tree on `main` at `1bbd21d`. `git status` at review start: untracked `internal/audio/**` (audio.go, decode.go, audio_test.go, narrate_test.go, question_test.go, decode_test.go, persist_test.go), untracked `dev-diary/adversarial-review/t8-round1.md` (the stub above), plus pre-existing untracked user work under `static/` and — appearing mid-review from a concurrent sibling round — `dev-diary/adversarial-review/t9a-round1.md`. Neither of those two was touched. |
| **Evidence** | `AGENTS.md` in full; `PLAN.md` §T8, §T5, §T5b, §T7, §T6, §T3, §Architectural invariants 1–9, task-graph rows 7–8, §Status; `project.md` §4 (Speech 2.8: hd/turbo, `English_expressive_narrator`, per-page emotion, the `volumn` typo), §Pipeline, §cast table; `adversarial-review/t2b-t5b-live-record.md` (the terminal TTS envelope, `outcome.audio_url`, `200 audio/mpeg` 63,348 bytes); `t6-round1.md` + `t6-remediation-round1.md` + `t6-round2.md` (the sanctioned media contract-change precedent on the T2-closed package); `t6-round1-notes.md`; `internal/audio/**` in full; the consumed surfaces: `internal/gmi/media/client.go` (SynthesizeSpeech + payload + retry/poll), `internal/store/{store,media,books,pages}.go` (schema, anchors, `SetMediaPlace`/`ErrConflict`), `internal/mediastore/mediastore.go` (closed set, Persist/Delete/ServeHTTP), `internal/illustrate/persist.go` (the T7 BookWriter pattern), `internal/story/story.go` + `validate.go` (Page.Emotion, Emotions), `cmd/thutapi/main.go`. |
| **Method** | Probe, don't trust prose. Eight source mutations plus five reviewer probes applied by hand one at a time to a pristine tree, each reverted byte-identical from a `/tmp` copy with the md5 baseline (`/tmp/t8rev/baseline.md5`, all seven package files) verified after every restore and once at the end. Gates re-run: `go vet ./...`, `go build ./...`, `go test ./... -race -count=1`, `go test ./internal/audio/ -race -count=5`, `gofmt -l .`, statement coverage on `internal/audio`. A full-suite flake episode on two T8-untouched packages was chased to attribution with paired with/without-audio full-suite runs and a clean-checkout baseline at `1bbd21d`. No live GMI call; no money spent. |
| **Date** | 2026-09-05. **Reviewer: a fresh agent (`T8Round1Reviewer`) with no prior T8 round and no memory of the implementer's reasoning — the independence of this round is the point.** |

## Verdict

**REMEDIATE — 0 × C, 0 × H, 0 × M, 2 × L.**

The package is sound: narration and questions speak the right models and
voices through the right seam, emotion is refused loudly before any paid call,
clips persist blob-first-then-place in page order with a T7-mirroring conflict
replace, the decode keys on `outcome` alone, and download-on-receipt is real
and pinned. The two L findings are pin-load-bearing gaps on defensive download
guards (both swallow-mutant proven below). Contract row C1 is ruled sanctioned
with a mandated landing (below) — not a finding. Nothing in this patch reaches
C, H or M.

## Findings

### L1 — `fetchAudio`'s non-200 guard has no load-bearing pin (swallow mutant survives)

| Column | |
|---|---|
| **Severity** | L |
| **Where** | `internal/audio/decode.go:138-140` (the `resp.StatusCode != http.StatusOK` block in `fetchAudio`) |
| **What** | The guard that stops a non-200 response from ever being persisted as a clip is asserted (the suite's 404 rows match `ErrNoAudio`) but not *uniquely* pinned: those fixtures answer `text/plain`, so the content-type gate at `decode.go:148-154` maps them to the same sentinel after the status check is deleted. Deleting the block changes real behaviour — a non-200 body served as `audio/mpeg` (a 403/404 with an audio content type) would be persisted as the page's narration — yet the entire audio suite stays green. The error branches are all asserted with `errors.Is` (the stub's D5 claim is literally true); this one is merely double-covered, which is exactly the swallow-mutant shape AGENTS.md §Testing rule 3 and the per-finding Mutation column exist to prevent (T5 round-1's empty transport pin is the house precedent). |
| **Pin** | New fixture in `decode_test.go`: a server answering `404` with `Content-Type: audio/mpeg` and an error body; `fetchAudio` must return an error matching `ErrNoAudio`. Red on the mutation, green on the tree as shipped. |
| **Mutation** | Delete the `if resp.StatusCode != http.StatusOK { … }` block (decode.go:138-140). Observed: `go test ./internal/audio/ -count=1` → `ok` — the whole suite survives the deletion. |

### L2 — `safeRedirectPolicy`'s hop cap is not uniquely pinned (documented bound can drift to Go's default 10)

| Column | |
|---|---|
| **Severity** | L |
| **Where** | `internal/audio/decode.go:190-198` (the `len(via) > maxRedirectHops` block inside `safeRedirectPolicy`) |
| **What** | The doc at decode.go:110-117 promises the download chain is "capped at maxRedirectHops" (3); the cap test only asserts that *some* error eventually arrives. Go's own client ceiling is 10 hops, so deleting the cap leaves `TestFetchAudio_RedirectPolicy` green (observed, ~30 s run): the documented 3-hop bound silently relaxes to 10 with no test able to see it. The scheme half of the same policy is behaviour-neutral if deleted — the stdlib client refuses non-http(s) redirect schemes itself, probe P1b — so this finding is scoped to the hop cap only. |
| **Pin** | New fixture in `decode_test.go`: a server answering `302` four times then `200`; `fetchAudio` must error (the 4th hop exceeds the documented cap of 3). Red on the mutation, green on the tree as shipped. |
| **Mutation** | Delete the `if len(via) > maxRedirectHops { … }` block (decode.go:191-193). Observed: `go test ./internal/audio/ -run TestFetchAudio_RedirectPolicy -count=1` → `ok` (30.0 s) — the suite survives the deletion. |

## C1 ruling — emotion must reach the wire, and the seam is acceptable this round with a mandated landing

**Ruling: C1 is sanctioned. Per-page emotion is a wire requirement, the
consumer-declared emotion-carrying seam is the correct implement-around, and
the fact that `*media.Client` does not yet satisfy `audio.TTS` is an acceptable
seam for this round — not a finding — provided C1 lands as a recorded item of
this track's remediation and is verified at round 2.**

The evidence chain that per-page emotion must reach the wire:

* `PLAN.md` §T8 — "Narration: `minimax-tts-speech-2.8-hd`, per-page `emotion` from T5."
* `PLAN.md` §Task graph row 7 — "Speech 2.8 → narration: per-page text + emotion → one MP3 per page."
* `project.md` §4 — "Per-page `emotion` comes from M3's JSON."
* `internal/story/story.go:62-70` — `Emotions` is "the emotion vocabulary of MiniMax Speech 2.8, because **T8 passes the value straight into `minimax-tts-speech-2.8-hd`**, where an out-of-set word is silently ignored"; `story.Validate` enforces membership from the same slice, and T5b verified every live page emotion inside it.
* The review brief (the amendment this round checks): "emotion on the wire (via the TTS interface shape)."

The live-verified request payload (`t2b-t5b-live-record.md` §The finding) has
no `emotion` key, so the closed client cannot express the spec today — the
change is out of T8's `Owns` (`internal/audio/**` only), and the implementer
raised it in this review file rather than reaching outside the seam. That is
exactly what `AGENTS.md` §Read order's pre-flight assertion and the T6
precedent require: T6's round carried a sanctioned contract change on the
T2-closed `internal/gmi/media` package through its remediation
(`t6-round1.md` → `t6-remediation-round1.md` "the sanctioned media.EditImage
contract change" → `t6-round2.md` §"The sanctioned contract change — clean
cutover, verified by grep"), and the review-2 of that round verified the cutover
as part of the track. C1 follows the same path.

Why the not-yet-satisfied interface is not a silent-drop hazard: `audio.TTS`
declares `SynthesizeSpeech(ctx, text, emotion, voice, model)`; `*media.Client`
declares four parameters today. The first wiring attempt — passing the real
client into `Config{TTS: …}` — fails at **compile time** with a
does-not-satisfy error. A silent emotion drop is impossible by construction;
every test in this package pins the emotion threading, the defaults and the
order at the seam the package controls, through a fake that records
`(text, emotion, voice, model)` per call.

**Mandated landing (binds the remediation round, orchestrator-sanctioned per
the T6 precedent — the edit sits in the T2-closed `internal/gmi/media`):**

1. `SynthesizeSpeech` gains `emotion string` (between `text` and `voice`, the
   order `audio.TTS` declares) and adds the payload key only when emotion is
   non-empty — the question path keeps today's live-verified payload
   byte-identical. The empty-value omission must be pinned on the marshalled
   bytes in `internal/gmi/media` (the `TestSynthesizeSpeech_TypoPinnedInRawJSON`
   model), both directions: `emotion: "happy"` present for narration, absent
   for questions.
2. The stale `client.go:285` docstring ("returns the raw audio bytes") and the
   stale TTS-as-bytes comment at `client.go:387-388` are corrected to the
   terminal request-queue body — the audio package's doc (audio.go:50-65) and
   the live record are the authority; two contradictory docstrings about the
   same function are an automatic finding per AGENTS.md §Docs.
3. Round 2 verifies `*media.Client` now satisfies `audio.TTS` at a wiring
   point (compile check) and that a narration request reaches the wire with
   the page's verbatim emotion.

Until C1 lands, `internal/audio` cannot be exercised against the real client
— that is the recorded, compile-time-enforced state of the seam, acceptable
for this round only because the landing is a mandated remediation item and the
round-2 verdict is the gate that closes the track. If C1 were *not* landed
and the track closed anyway, `story.go`'s committed doc claim ("T8 passes the
value straight into `minimax-tts-speech-2.8-hd`") would become a live lie at H
severity — which is precisely why the landing is part of this verdict, not
left to a later track's discretion.

## Design-decision rulings

### D1 — emotion mechanism (verbatim payload `emotion`, refused loudly before any call; empty emotion for questions) — **SOUND**
The vocabulary check against the single `story.Emotions` slice (audio.go:419-421)
matches `story.Validate`'s own exact-lowercase enforcement, so the two cannot
drift; refusing before any call converts the provider's silent ignore into a
loud typed error naming the word and the vocabulary — the T5 "fail loudly"
rule. Mutation M4 (guard removed) goes red on the validation rows. The cast
voice fields staying off the wire is correct: their mechanism is the voice-clone
track (T13), which the two-day cut list cuts first.

### D2 — narration persistence (caller owns rows; blob-first-then-place; conflict replaces occupant, mirroring T7) — **SOUND**
`storeClip`/`place` (audio.go:454-501) reproduce T7's `BookWriter.place`
(illustrate/persist.go:84-103) ordering and replace semantics exactly: new blob
persisted first, occupant's row+file deleted, place retried; crash windows are
T11's sweep's. Caller-owned rows match the T7 precedent (persist.go:22-25), and
the anchor contract is real: the `(book_id, page_n) → pages(book_id, n)`
foreign key fires on `SetMediaPlace`'s UPDATE, so a missing page row is
`store.ErrInvalid` (pinned by `TestNarrate_MissingPageRowFailsLoudly`; the media
schema's partial unique index `media_page_narration` is what makes a second
occupant `ErrConflict`). The read-back wrap (`storeClip:463-466`) and the three
replace-path wraps are the four statements D5 discloses — consistent with T7's
own concrete-pointer profile; the fault seam belongs in store/mediastore, not
this package.

### D3 — question shape (plain bytes-returning function, turbo, no store write) — **SOUND**
Nothing in project.md §4 / PLAN.md §T4–§T9 persists interview audio: the store's
interview rows are text turns (interviews.go), the media table anchors only
books/pages/cast, and the flow plays the clip once over SSE. The parallel-fire
shape is correctly the caller's (a job/goroutine per invariant 3's consumer
seam); `SynthesizeQuestion(ctx, cfg, text) ([]byte, error)` with the turbo
default and no DB/Blobs requirement is the right pure seam, and
`TestSynthesizeQuestion_NoStoreNeeded` pins that no row appears.

### D4 — envelope decode keys on `outcome` alone — **SOUND**
`queueRecord` parses only the `outcome` subtree (decode.go:40-51); the payload
echo is never consulted, and the decoy fixture proves an audio-looking URL in
the echo can never win — the H1 lesson applied. `ErrNoAudio` covers every
unrecognised shape loudly (raw MP3 bytes included), the content-type gate
matches mediastore's closed set with the header as the trusted statement, and
`MaxAudioBytes` is enforced on the download. Two of the download guardrails
carry the L1/L2 pin gaps above.

### D5 — coverage note — **CONFIRMED**
`go test ./internal/audio/ -cover`: 97.6% (floor 75%). `place` 76.9%,
`storeClip` 90.0%, every other function 100%. The four uncovered statements are
exactly the read-back and replace-path wraps D5 names, none fault-injectable
through the concrete `*store.DB`/`*mediastore.Store` pointers — mirrors T7's
accepted profile.

## Requirement check per PLAN §T8 + amendment

| Requirement | Status | Pin (red on revert where marked) |
|---|---|---|
| Narration: one clip per page, `minimax-tts-speech-2.8-hd` default | PASS | `TestNarrateBook_DefaultsReachTheCalls` (default model through the default path); `TestNarrate_PersistsOneClipPerPage` |
| Voice default `English_expressive_narrator` | PASS | `TestNarrateBook_DefaultsReachTheCalls`, `TestSynthesizeQuestion_DefaultsAndBytes` |
| Per-page emotion on the wire (via the TTS interface shape) | PASS (seam) / PENDING (wire, C1) | `TestNarrateBook_DefaultsReachTheCalls` (emotion verbatim at the seam); wire pinned by the mandated C1 raw-wire test |
| Out-of-set emotion refused loudly before any call | PASS | M4 red: `TestNarrateBook_ValidationFailsBeforeAnyCall`/`out-of-set_emotion` + `empty_emotion` + the naming assertion |
| `audio_url` download-on-receipt, bytes taken now | PASS | M1 red: `TestNarrate_DownloadedBytesPersist`, `TestNarrate_DownloadCompletesBeforeTheRowExists`, `TestNarrate_PersistsOneClipPerPage` |
| Persist blob-first, then `SetMediaPlace` `MediaNarration` at (book, page) | PASS | M1 red (row exists only after download); `TestNarrate_DownloadCompletesBeforeTheRowExists` |
| Re-run: `ErrConflict` replaces occupant (new blob first, old row+file deleted, retry) | PASS | M3 red: `TestNarrate_ReRunReplacesTheOccupant` |
| Caller owns `CreateBook`/`CreatePage`; missing page row → `store.ErrInvalid` | PASS | `TestNarrate_MissingPageRowFailsLoudly` |
| Deterministic page order (clips follow input order) | PASS | M2 red: `TestNarrateBook_SerialRunSpeaksPagesInTheOrderGiven`, `TestNarrateBook_DefaultsReachTheCalls` |
| Fan-out bounded by `Config.Limit` (default 4), never a serial loop | PASS | `TestNarrateBook_FanOutRunsThePagesConcurrently` (8 in flight before release) |
| `SynthesizeQuestion`: downloaded bytes returned, no store write, turbo default, pure parallel-fire shape | PASS | `TestSynthesizeQuestion_DefaultsAndBytes`, `TestSynthesizeQuestion_NoStoreNeeded` |
| No second retry layer | PASS | no retry construct in the package; the media client's own single retry is the only one |
| Sentinels matched with `errors.Is`, wrapped with context | PASS | M6/M7/P2/P3 red and `TestSynthesizeQuestion_UpstreamErrorPassesThrough` |
| Defaults through default paths; ctx-first | PASS | `TestNarrateBook_DefaultsReachTheCalls`, `TestFetchAudio_ContextCancellation` |
| Zero value unusable → loud typed errors (no silent half-config) | PASS | M6 red (ErrNoTTS); `ErrNoStore`/`ErrNoTTS` before any call |
| Decode keys on `outcome.audio_url` by name; never scans the body | PASS | M5 red: `TestDecodeAudioURL_KeysOnOutcome` (live record + payload-echo decoy) |
| `MaxAudioBytes` cap enforced on download | PASS | M8 red: `TestFetchAudio_ErrorBranches`/`oversized_body_is_ErrUnsupportedAudio` |

## Mutation table — applied by hand, observed, reverted byte-identical (md5-verified after every restore)

Baseline manifest: md5 of all seven `internal/audio/*.go` files taken before the
first mutation (`/tmp/t8rev/baseline.md5`); `md5sum -c` reported **OK for all
seven files after every restore and once more at the end**. No mutation left
the tree: final `git status --porcelain` shows only the pre-existing untracked
items (`internal/audio/`, this review file, user `static/`, sibling
`t9a-round1.md`).

| # | Mutation | Result | Evidence (red reason) |
|---|---|---|---|
| M1 | Download not taken on receipt — `narratePage` persists `[]byte(audioURL)` as the clip | **RED** | `TestNarrate_DownloadedBytesPersist`: page 3 blob holds the URL string; `TestNarrate_DownloadCompletesBeforeTheRowExists`: clip server handled 0 requests |
| M2 | Page order scrambled — fan-out launches pages in reverse | **RED** | `TestNarrateBook_SerialRunSpeaksPagesInTheOrderGiven` + `TestNarrateBook_DefaultsReachTheCalls`: calls and clips arrive reversed |
| M3 | Conflict-replace disabled — `place` returns `ErrConflict` | **RED** | `TestNarrate_ReRunReplacesTheOccupant`: "second run: … conflict" |
| M4 | Out-of-set emotion refusal removed from `validatePages` | **RED** | `TestNarrateBook_ValidationFailsBeforeAnyCall`/`out-of-set_emotion` + `/empty_emotion` (err nil, want `ErrInvalidPage`); naming assertion red |
| M5 | Decode keys on the `payload` echo instead of `outcome` | **RED** | `TestDecodeAudioURL_KeysOnOutcome`: live record decodes to no URL |
| M6 | `ErrNoTTS` guard removed from `resolve` | **RED** | nil-TTS narration rows panic in the errgroup goroutine (test binary FAIL) |
| M7 | Question empty-text guard removed | **RED** | `TestSynthesizeQuestion_EmptyTextIsErrNoText`: err is the fake's, want `ErrNoText` |
| M8 | `MaxAudioBytes` oversize guard removed | **RED** | `TestFetchAudio_ErrorBranches`/`oversized_body_is_ErrUnsupportedAudio`: err nil |
| P2 (probe) | `ErrNoStore` guard removed | **RED** | nil-DB/nil-Blobs narration rows panic (test binary FAIL) |
| P3 (probe) | `ErrNoPages` guard removed | **RED** | `TestNarrateBook_ValidationFailsBeforeAnyCall`/`no_pages`: err nil, want `ErrNoPages` |
| P1 (probe → L1) | Non-200 status guard removed from `fetchAudio` | **suite GREEN** | `go test ./internal/audio/ -count=1` → ok — swallow mutant; filed as L1 |
| P1b (probe) | Redirect scheme guard removed from `safeRedirectPolicy` | **suite GREEN** | behaviour-neutral: the stdlib client refuses non-http(s) redirect schemes itself — not a finding |
| P1c (probe → L2) | Redirect hop cap removed from `safeRedirectPolicy` | **suite GREEN** (30.0 s) | Go's default 10-hop ceiling still ends the chain with an error — documented 3-hop bound unpinned; filed as L2 |

## Gates

| Gate | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `gofmt -l .` | empty |
| `go test ./internal/audio/ -race -count=5` | ok (4.6 s) |
| `go test ./internal/audio/ -cover` | 97.6% (floor 75%) |
| `go test ./... -race -count=1` | ok in quiet-window runs (see note) |

**Flake note (not T8 residue).** Two full-suite `-race` runs failed during
heavy concurrent sibling-agent load: `internal/interview`'s
`TestRestartedHandlerSeesEndedInterview` (5.07 s wait for a streamed closing
turn) and once `cmd/thutapi`'s `TestShutdownLogRecordsSignalName`. Both
packages are untouched by T8 (no import or code coupling to `internal/audio`),
both pass alone and in their own package, and attribution runs settled it:
clean-checkout baseline at `1bbd21d` green 6/6; paired with/without-audio
full-suite runs in one quiet window green 4/4 (audio present); the flake
reproduced only under sibling load, with and without the T8 package on the
same tree. Recorded as environmental, not patch-introduced.

## Zero-residue claim and disposition

This reviewer created or modified nothing outside `internal/audio/**` (untouched,
byte-identical to review start — baseline md5 OK) and this verdict file. The
pre-existing untracked `static/` (user work) and `t9a-round1.md` (concurrent
sibling round) were not touched. Review probes were transient mutations,
reverted byte-identical; no probe files remain. The temporary `git worktree`
used for the baseline comparison was removed.

**Round-1 findings: 0 C, 0 H, 0 M, 2 L.** The remediation round must (1) fix
L1 and L2 with the fixture pins above, and (2) land contract row C1 in
`internal/gmi/media` with the orchestrator's sanction per the T6 precedent,
including the raw-wire emotion-omission pin and the stale-docstring correction,
before round 2 re-reviews. This is a REMEDIATE verdict: another full round is
required.

**REMEDIATE — 0 × C, 0 × H, 0 × M, 2 × L.**
