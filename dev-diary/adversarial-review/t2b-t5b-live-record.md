# T2b and T5b — live verification record

**Date:** 2026-09-05. **Operator:** orchestrator session.
**Precedent:** T1b — an operator track closes on transcripts, not on an
adversarial review round, because what it verifies is the world rather than a
diff. The evidence is below and the probes are committed as `//go:build live`
tests, so they are re-runnable rather than pasted prose (`AGENTS.md`
§Definition of done item 4 — a cited check must pass from a clean tree).

**Unblocked by:** a working `GMI_API_KEY`, installed 2026-09-05. The key
recorded in the T2 row as rejected by both providers (`sk-or-v1-…`, an
OpenRouter shape) has been replaced with a 252-char JWT (`eyJhbGc…`) that
authenticates against both hosts.

**Run them:**

```
set -a; . ./.env; set +a
go test -tags live -run Live -v ./internal/gmi/text/ ./internal/gmi/media/ ./internal/story/
```

Cost: **zero.** M3 and Speech 2.8 are free during the campaign window; the
image path is deliberately probed only at model-resolution, which fails before
anything renders. No probe here generates a paid asset.

---

## T2b — live probes of both GMI endpoints

Closes the half of T2's original `Done when` that no agent could run while the
key was dead: *"an integration test hits both endpoints live and unmarshals
into typed structs."*

### Text — `api.gmi-serving.com` (`internal/gmi/text/live_test.go`)

| Probe | Result |
|---|---|
| `TestLiveChat_TypedUnmarshal` | **PASS** — `finish_reason="stop"` `role="assistant"` `text="pong"`, decoded through `ChatResponse` → `AssistantMessage.Text()` |
| `TestLiveChat_BareModelID404s` | **PASS** — `gmi: model not found: No matching target server found for model MiniMax-M3` |
| `TestLiveChat_ThinkingEnabled` | **PASS** — `thinking:{"type":"enabled"}` accepted; 17×23 answered `"391"` |

Two facts that were previously documentation are now verified against
production:

* **The `MiniMaxAI/` prefix is load-bearing.** The bare id fails with the exact
  upstream string the package doc quotes, and it classifies as
  `ErrModelNotFound` — so both the quirk and its error mapping are real.
* `GET /v1/models` returns **82 models**, including `MiniMaxAI/MiniMax-M3`.

### Media — `console.gmicloud.ai` (`internal/gmi/media/live_test.go`)

| Probe | Result |
|---|---|
| `TestLiveSynthesizeSpeech` | **PASS** — real TTS, 729-byte JSON envelope, `status:"success"`, ~24s |
| `TestLiveUnknownModel404s` | **PASS** — `gmi: model not found: {"error":"model … does not exist"}` |

**Finding — the package doc is wrong about the TTS return shape.**
`internal/gmi/media/client.go` package doc says the request-queue API returns
*"a binary blob for TTS"*. It does not. It returns the queue envelope:

```json
{"request_id":"2ba8cd0d-…","model":"minimax-tts-speech-2.8-hd","status":"success",
 "payload":{"need_noise_reduction":true,"need_volumn_normalization":true,
            "text":"Once upon a time, a small dragon lost her shoe.",
            "voice_id":"English_expressive_narrator"},
 "outcome":{"audio_url":"https://storage.googleapis.com/gmi-video-assests-prod/…/….mp3",
            "format":"mp3","status":"success"},
 "created_at":1788581287,"updated_at":1788581309,"queued_at":1788581287}
```

Three consequences, all of which land on T8:

1. **Audio arrives as a URL, not as bytes.** T8 must download `outcome.audio_url`
   and persist it. That is exactly the case `project.md` §T3 anticipates
   (*"Responses point at `storage.googleapis.com`; assume those URLs expire"*)
   — now confirmed as the literal shape rather than an assumption.
2. **The URL is publicly fetchable with no credential.** Verified: `200`,
   `content-type: audio/mpeg`, 63,348 bytes, `MPEG ADTS layer III v1 128 kbps
   32 kHz Stereo`. Useful for T13 (`source_audio` needs a public URL) — and a
   **safety flag**: if a cloned child voice is returned the same way, it sits
   at an unauthenticated public URL on GMI's bucket. `AGENTS.md` §Safety
   requires child voice samples to be short-lived and unguessable; that rule
   governs what we host, and this shows it does not govern what GMI hosts.
   T13 must weigh that before any real clone is made.
3. **The `volumn` typo is accepted.** The upstream echoed
   `need_volumn_normalization: true` back in `payload`, so the typo pin is
   confirmed against production and not merely against our own marshalling.

Elapsed ~24s for one TTS call means T3b's request-queue polling is doing real
work on the live path, not just against fakes.

**Verdict: T2b closes.** Both endpoints reached live, both decoded into typed
structs, both error classifications confirmed against real upstream responses.

---

## T5b — live half of T5's `Done when`

*"a transcript yields valid JSON that validates against the schema, twice
running"*, with `thinking` ON.

`internal/story/live_test.go`, over a realistic 14-turn Phase-A transcript
(hero, companion, want, obstacle, turn, ending).

| Probe | Result |
|---|---|
| `TestLiveStructure_TwiceRunning` | **PASS** — two independent live calls, both `Validate`-clean |
| `TestLiveCorrectiveSystemMessage` | **PASS** — reply `"Cerulean."` |

| | Run 1 (14s) | Run 2 (37s) |
|---|---|---|
| Title | "Mira, Pip, and the Lost Shoe" | "Mira and the Steam Bridge" |
| Pages | 8 ✓ | 8 ✓ |
| Emotions | all in taught vocabulary ✓ | all in taught vocabulary ✓ |
| Page characters ⊆ cast | ✓ | ✓ |
| Cast | Narrator, Mira, Pip, Grumpy River, Happy River | Narrator, Mira, Pip, River, Shoe |

**The corrective-system-message acceptance holds.** MiniMax accepted a
`system`-role message placed *after* an assistant turn, mid-conversation, and
obeyed it. That is the mechanism `story.Structure`'s corrective retry depends
on, and it was previously an assumption.

### Two findings handed to T6

Both are live-observed cast shapes that `Validate` accepts and that break a
naive "one reference image per cast member" loop. Sent to the T6 implementation
agent while it was still building, so they land as design input rather than as
a round-1 finding.

1. **Non-visual cast members validate.** Both runs emitted a `Narrator` whose
   `visual` is `"no visual"` (run 1) / `"an unseen storyteller with no
   appearance"` (run 2). A per-member reference sheet spends ~$0.01 a book
   rendering nothing. The two phrasings differ, so no literal-string match
   works; `Narrator` is not reservable either, since a child may legitimately
   name a character that.
2. **One entity can occupy two cast slots.** Run 1 produced both
   `Grumpy River` and `Happy River` — the latter's visual literally opens *"the
   same wide blue river"*. `Validate` accepts them because the names are
   unique. Under the image lock they become two unrelated rivers, which is the
   exact drift T6 exists to prevent.

Minor, also passed on: M3's page prompts already carry their own style language
("Soft storybook illustration style"), which will compete with T6's mandated
constant style suffix. A deliberate decision, not an accident.

Neither is a T5 defect — the schema and validator do what they were specified
to do, and `internal/story` is closed. Both are T6's to absorb.

**Verdict: T5b closes.**

---

## Process note

Neither T2b nor T5b has an `Owns` line in `PLAN.md` — they were added as status
rows when T2 and T5 split off their live halves. The probes above landed in
`internal/gmi/text/`, `internal/gmi/media/` and `internal/story/`, all behind
`//go:build live`, which is a seam nobody owns. Worth assigning before the next
operator track: the natural rule is that a `b`-suffixed track owns the
`live_test.go` file in each package its parent track owns.
