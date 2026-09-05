# T8 round 2 — remediation

| | |
|---|---|
| **Target** | All findings of `t8-round2.md` — 0 C, **1 H**, 0 M, 0 L. H1: the top-level `emotion` payload key sent to minimax-tts-speech-2.8-hd is pinned as settled while its evidence chain is self-referential (Thutapi's own JSON tags/docs/decoys), no live record shows the key on the TTS wire, and neither the risk nor a settlement probe is documented. |
| **Date** | 2026-09-05 |
| **Status** | **COMPLETE** for the documentation + committed-obligation halves (doc notes, probe, record all landed; gates green). **H1 itself stays OPEN until the settlement probe's first passing run against a recovered upstream** — see Residue. |
| **Scope** | Doc notes in `internal/gmi/media/client.go` (SynthesizeSpeech docstring) + `internal/audio/audio.go` (§The emotion mechanism); the committed live settlement probe in `internal/gmi/media/live_test.go`; this record. Nothing else touched — no `PLAN.md` (out of scope), no verdict change, no fix of anything the review did not find. Verdicts unchanged: round 1's remediation file stands as written. |

## H1 — the `emotion` payload key: risk documented, settlement probe committed; live pass pending

The round-2 finding stands on two absences: (1) nowhere did code, docs or
records state that the `emotion` key, its top-level placement, and the in-set
vocabulary are **asserted-but-unverified live** — the prose asserted them as
settled fact; and (2) no committed live-probe obligation existed, so nothing
could ever go red on the upstream silently ignoring or rejecting the key. The
remediation is the three-part fix the finding mandated:

### 1. DOC NOTES — asserted-but-unverified, in both behaviour-defining doc sites

Both doc sites now say plainly that the key is pinned on the wire but has
never been confirmed against the upstream, name the silent-ignore class, cite
the committed probe as the thing that flips the note, and confirm the
empty-emotion path is unaffected:

| Location | Lines | What it now states |
|---|---|---|
| `internal/gmi/media/client.go` — `SynthesizeSpeech` docstring (the "emotion is…" paragraph, directly after the C1 sentence) | 300–313 | The top-level `emotion` payload key, its placement, and the in-set vocabulary (`story.Emotions`) are **asserted-but-unverified live as of 2026-09-05**: pinned on the wire (contract row C1) but never confirmed against the minimax-tts-speech-2.8-hd queue adapter — the TTS pool answered every settlement call with HTTP 503 "Upstream capacity temporarily exhausted" that evening, so no live echo of an emotion-carrying call exists yet. If upstream ignores or rejects the key, narration renders emotion-less with every check green (the silent-ignore class; t8-round2.md H1). The committed settlement probe is `TestLiveSynthesizeSpeech_EmotionKeyEchoed` (`live_test.go`, `//go:build live`); flip this note when it passes against a recovered upstream. The empty-emotion path is unaffected: byte-identical to the live-verified question shape (t2b-t5b-live-record.md). |
| `internal/audio/audio.go` — package doc, §The emotion mechanism (the paragraph following "refuses an out-of-set emotion loudly…") | 41–54 | Same assertion-but-unverified statement from the consumer side: the key's top-level placement on the minimax-tts-speech-2.8-hd payload and this in-set vocabulary are pinned on the wire but never confirmed against the upstream; if upstream ignores or rejects the key, narration renders emotion-less with every check green (the silent-ignore class; t8-round2.md H1); the committed settlement probe flips these notes when it passes; the empty-emotion path stays byte-identical to the live-verified question shape. |

No claim of verification appears in either note; each is date-bound and names
the probe whose passing run retires it.

### 2. COMMITTED SETTLEMENT PROBE — mechanically runnable from a clean tree

New `//go:build live` test in `internal/gmi/media/live_test.go` (the file the
b-track precedent and AGENTS.md §"Live verification splits into a `b` track"
own), named **`TestLiveSynthesizeSpeech_EmotionKeyEchoed`** (live_test.go:88).
It follows the file's conventions: `liveClient(t)` skips when `GMI_API_KEY`
is unset, a 120 s context deadline, `truncate`-bounded body logging. Two
subtests, both against the real host and the real client:

- **`emotion key echoed`** — the settlement assertion. POSTs a
  `SynthesizeSpeech` call with emotion `"happy"` (real client, current
  signature `(ctx, text, emotion, voice, model)`; the client drives the submit
  + poll to the terminal record itself), decodes the terminal record's
  echoed `payload` subtree (the queue echoes the submitted payload —
  t2b-t5b-live-record.md), and asserts the echo still carries
  `"emotion":"happy"` via a typed payload struct whose `Emotion` field decodes
  to `""` when the key was stripped. If upstream rejects the key the call
  errors; if it strips it the echo lacks it — either way the probe fails with
  the record body logged. **If the pool is still 503ing, the test fails with
  the upstream error — that is correct behaviour for a live probe (evidence,
  never a CI gate), not a flake.**
- **`model in catalog`** — the cheap second assertion. GETs the
  request-queue model catalog (`.../apikey/models`, same apikey prefix as
  `pathRequestQueue`) and asserts `minimax-tts-speech-2.8-hd` is present in
  the `model_ids` array. Run first so a 503'd TTS pool does not hide the
  catalog evidence.

Run it (needs the operator's live key; upstream must have recovered from the
2026-09-05 capacity exhaustion):

```
set -a; . ./.env; set +a
go test -tags live -run Live -v ./internal/gmi/media/
# or narrowed to the probe alone:
go test -tags live -run 'TestLiveSynthesizeSpeech_EmotionKeyEchoed' -v ./internal/gmi/media/
```

### 3. RECORD — the 2026-09-05 live evidence (echo experiment deferred, not refuted)

The settlement echo experiment ran on 2026-09-05 evening and was **deferred,
not refuted**: the upstream could not take the call.

| Probe | Result |
|---|---|
| POST `.../apikey/requests` TTS path (`minimax-tts-speech-2.8-hd`, emotion-carrying variant) | **HTTP 503 "Upstream capacity temporarily exhausted" on every POST** — 10+ attempts over ~10 minutes. The echo experiment therefore produced no record; it is re-runnable via the committed probe above. |
| GET `console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models` | **200** with `model_ids` — `minimax-tts-speech-2.8-hd` present in the full list, but **no parameter schemas** are published, which is why a schema lookup cannot settle the key and an echo probe can. |
| GET `api.gmi-serving.com/v1/models/minimax-tts-speech-2.8-hd` | **405** — TTS models are not on the text gateway; the text gateway cannot corroborate the media-side payload shape. |
| All live TTS payload echoes on record (t2b-t5b-live-record.md) | Carry only `{text, voice_id, need_noise_reduction, need_volumn_normalization}` — **no `emotion` key anywhere**, the exact gap H1 names. |

Net: no live observation of the `emotion` key on the TTS wire exists as of
2026-09-05; every alternative source of confirmation (schema catalog, text
gateway) is closed or silent. That is precisely the state the doc notes now
describe, and precisely what the committed probe exists to settle.

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **H1** — the top-level `emotion` payload key is asserted-and-pinned as settled while its evidence is self-referential (Thutapi's own JSON tags/docs/decoys), no live record shows the key on the TTS wire, and neither the risk nor a settlement probe is documented | Three-part fix per the finding: (1) asserted-but-unverified doc notes at both behaviour-defining sites (client.go SynthesizeSpeech docstring 300–313; audio.go §The emotion mechanism 41–54), each stating the key, its placement, and the in-set vocabulary are pinned but never live-confirmed, naming the silent-ignore consequence and the empty-emotion non-effect; (2) committed `//go:build live` settlement probe `TestLiveSynthesizeSpeech_EmotionKeyEchoed` (live_test.go:88, + the model-catalog subtest) mechanically runnable from a clean tree; (3) this record with the 2026-09-05 evidence (503 on every POST, catalog 200 without schemas, text gateway 405, no emotion on any recorded live echo) and the open-until-pass statement. | **DONE** (documentation + committed obligation; gates green). **The H stays open until the probe's first passing run against a recovered upstream** — see Residue. |

## Files changed

| File | Change |
|---|---|
| `internal/gmi/media/client.go` | Doc only: SynthesizeSpeech docstring gains the asserted-but-unverified note (lines 300–313). No code touched — behaviour byte-identical. |
| `internal/audio/audio.go` | Doc only: §The emotion mechanism gains the asserted-but-unverified note (lines 41–54). No code touched. |
| `internal/gmi/media/live_test.go` | New `//go:build live` probe `TestLiveSynthesizeSpeech_EmotionKeyEchoed` (two subtests: `emotion key echoed`, `model in catalog`) + `pathModelCatalog` const + typed echo record/payload structs. Follows the file's conventions (skip-if-no-key, timeouts, `truncate` logging). |
| `dev-diary/adversarial-review/t8-remediation-round2.md` | This record. |

## Gates

| Gate | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go vet -tags live ./internal/gmi/media/ ./internal/audio/` | clean — the new probe compiles under the `live` tag |
| `gofmt -l .` | empty |
| `go test ./internal/gmi/media/ ./internal/audio/ -count=1` | ok (media 4.97 s, audio 0.06 s) — unit only |
| `go test -tags live ... ./internal/gmi/media/` | **NOT RUN** — upstream is 503ing; a red live run teaches nothing. The probe is committed and will be run when the pool recovers (Residue). |

## Residue

* **H1 is open until the live pass.** This round lands the documentation and
  the committed obligation — the two halves a re-review can rule on — but the
  finding's own bar was never "documented risk": it was a live observation of
  the key on the wire. The probe `TestLiveSynthesizeSpeech_EmotionKeyEchoed`
  fails today (the pool 503s) and that failure is evidence, not a defect.
  **A re-review (round 3) rules on the DOCUMENTATION + committed obligation;
  the live pass is recorded here when upstream recovers.** The doc notes
  (client.go 300–313, audio.go 41–54) are date-bound and each names the probe
  that flips them — the recording of the pass must also flip those notes, or
  the "Docs about behaviour must match the behaviour" rule (AGENTS.md §Go
  style) turns the date-bound caveat itself stale.
* `PLAN.md` was not touched: no `T8b` row exists there (round 2 noted
  "`grep -r T8b dev-diary` returns nothing"), and adding one is outside this
  round's scope. The committed obligation therefore lives in the doc notes,
  the probe file, and this record — the round-3 reviewer rules on whether that
  satisfies the finding's "committed live-probe obligation" clause.
* Nothing else modified: `git status --porcelain` shows only the round-1
  baseline items (modified `internal/gmi/media/{client,client_test,live_test}.go`,
  untracked `internal/audio/`, untracked round-1/round-2 review + remediation
  files) plus the three edits above and this record. No verdict changed; round
  1's remediation file stands as written.
