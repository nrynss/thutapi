# T8b — the emotion key, settled

**Date:** 2026-09-05. **Operator:** orchestrator session.
**Precedent:** T1b / T2b / T5b / T6b — a capability closes on transcripts.
**Cost:** $0.00. One authenticated GET. No synthesis, no queue submission.
**Outcome:** **H1 is settled.** The placement Thutapi ships is **correct**; the
vocabulary it teaches is **not** — one value is outside the provider's enum.

---

## It was never blocked on the TTS pool

Two T8b attempts were spent waiting on `HTTP 503 "Upstream capacity
temporarily exhausted"`. That 503 is on **POST synthesis**. The parameter
schema is behind a **GET**, and answered 200 the whole time:

```console
$ curl -H "Authorization: Bearer $GMI_API_KEY" \
    https://console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models/minimax-tts-speech-2.8-hd
200
```

`t8-round2.md` H1 named this exact route — *"GMI's model-details endpoint
`GET /models/minimax-tts-speech-2.8-hd` settles the parameter schema for
free"*. The round-2 remediation implemented a **catalog-membership** check
instead (`model_ids` contains the model, `live_test.go:97-133`), which proves
only that the model exists, and then recorded settlement as blocked on
capacity. The weaker check displaced the one that would have answered it.

**Two host facts, because they cost a detour:**

* The request queue is on **`console.gmicloud.ai`** (`client.go:60`), **not**
  `api.gmi-serving.com`. Every request-queue path on the api host answers
  `405 Method Not Allowed` — including the catalog, so a 405 there means
  *wrong host*, not *missing endpoint*.
* `…/apikey/models` is the **catalog** (324 ids); `…/apikey/models/{id}` is the
  **detail**. Only the second carries the parameter schema.

## What the provider says

`parameters[]` for `minimax-tts-speech-2.8-hd`, verbatim:

| Parameter | Type | Default | Constraint |
| --- | --- | --- | --- |
| `emotion` | enum | `"auto"` | `auto, calm, happy, sad, angry, fearful, disgusted, surprised` |
| `pitch` | integer | `0` | −12 … 12 |
| `timbre` | integer | `0` | −100 … 100 |
| `intensity` | integer | `0` | −100 … 100 |
| `vm_pitch` | integer | `0` | −100 … 100 |
| `sound_effects` | enum | `""` | `"", spacious_echo, auditorium_echo, lofi_telephone, robotic` |
| `speed` | float | `1` | 0.5 … 2 |
| `vol` | float | `1` | 0 … 10 |
| `format` | enum | `"mp3"` | `mp3, flac` |

The endpoint's own usage guide shows `emotion` **at payload top level**, beside
`voice_id` and `pitch`, and adds: *"By default, the model automatically selects
the most natural emotion based on text. Manual specification is only
recommended when explicitly needed."*

## Finding 1 — the placement is CORRECT. C1 stands.

`emotion` is a **top-level payload key** in GMI's flattened dialect, exactly as
`client.go:325-327` sends it. Round 2's objection — MiniMax's native
`speech-t2a-http` schema nests `emotion` inside `voice_setting` — was **right
about MiniMax and wrong about GMI**, which is the flattening the reviewer
themselves flagged as "plausible by analogy". The analogy held. No code change.

## Finding 2 — `neutral` is NOT in the enum. This is the H1 failure, live.

```
GMI:   auto, calm, happy, sad, angry, fearful, disgusted, surprised
Ours:  happy, sad, angry, fearful, disgusted, surprised, neutral
                                                         ^^^^^^^
```

`story.Emotions` (`internal/story/story.go:70`) teaches **`neutral`**, which the
provider does not accept. Six of seven values are valid; the seventh is the one
a gentle children's book is most likely to reach for.

This is precisely the class H1 described. `internal/audio` validates the page
emotion against **Thutapi's own list**, so `neutral` passes every check we
have, reaches the wire, and is not recognised — **narration renders
emotion-less with every gate green.** Nothing reddens. The book still reads
itself, flatly.

**Where it came from:** `t5-round1.md:182` recorded the vocabulary as matching
"independent Speech 2.8 provider docs verbatim". That was true of *MiniMax's*
documentation and is false of *GMI's* dialect, which adds `auto` and `calm` and
drops `neutral`. A review round confirmed the list against the wrong authority.

**The fix is `neutral` → `calm`** — the closest documented meaning, and the
value GMI offers in that register.

**`auto` is deliberately NOT added.** It is the provider default and would be
the safe choice for a page with no strong emotion — but a vocabulary offering
`auto` invites M3 to choose it everywhere, and per-page emotion is the whole
point of narrating a picture book rather than reading it. Six explicit
emotions, no escape hatch.

## Finding 3 — T13's cast voices are confirmed, not assumed

The knobs the voice-clone pitch rests on are real, top-level, and bounded:
`pitch` ±12, `timbre` ±100, `intensity` ±100, `sound_effects` including
**`robotic`** and **`spacious_echo`** verbatim. Dragon at `pitch:-8` with
`spacious_echo`, robot with `robotic`, mouse at `+6` — every one of those is a
documented value, not an extrapolation. §T13 can be written against the schema.

## Finding 4 — the committed probe cannot settle what it was built for

`TestLiveSynthesizeSpeech_EmotionKeyEchoed` asserts the terminal record's
echoed payload still carries the emotion key **we submitted**, on the premise
that *"an upstream that strips or rejects the key shows it in the echo"*.

**That premise is unverified.** The only live echo on record
(t2b-t5b-live-record.md) contains four keys — `text`, `voice_id`,
`need_noise_reduction`, `need_volumn_normalization` — and all four are real
MiniMax fields. **No unknown key has ever been submitted**, so nothing
establishes whether the queue normalises its echo or copies the payload
verbatim. If verbatim, the probe goes green whether or not upstream honoured
the key, and its first pass would be read as settling H1 while proving only
that we marshalled correctly.

Two cheap repairs, for whoever runs the live call:

1. **A canary key.** Add `"thutapi_canary":"xyzzy"` to the same submission. If
   it comes back in the echo, the echo is verbatim and echo-based assertions
   are worthless; if it is dropped, the echo normalises and the existing
   assertion means something. One call, settles it for good.
2. **Make the real assertion differential.** Same text at `happy` and at `sad`;
   assert the two `outcome.audio_url` payloads **differ**. That tests the
   model instead of our own marshalling, and is immune to echo semantics
   either way.

## Status

**T8b closes on this record for the schema question** — placement confirmed,
vocabulary corrected, and the correction handed to **T5c** (§PLAN.md), which
owns the constant.

What remains is *behavioural* and optional: whether the audio audibly differs
per emotion. That still wants the pool, and it is the differential assertion
above rather than the echo one. Narration works either way; it is the
expressiveness at stake, which is exactly what finding 2 was already costing us
silently.

## Reproducing

```console
$ set -a; . ./.env; set +a
$ curl -s -H "Authorization: Bearer $GMI_API_KEY" \
    https://console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models/minimax-tts-speech-2.8-hd \
  | python3 -c "import json,sys; [print(p['name'], p.get('options') or (p.get('min_value'), p.get('max_value'))) for p in json.load(sys.stdin)['parameters']]"
```

Zero cost, no synthesis, unaffected by the pool's capacity.
