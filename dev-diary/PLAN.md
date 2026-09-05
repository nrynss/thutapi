# Thutapi — build scope

What Thutapi must build, as atomic tracks with their real dependencies.

**Authority:** [project.md](project.md). **Deadline:** 2026-09-06, submissions close
(MiniMax Week × GMI Cloud, Multimodality track). **Announced:** 2026-09-11.
**Working target: Sunday 2026-09-06, 20:00 IST** — see *The deadline's timezone*.

Where this doc and the spec disagree, the spec wins.

**Agents:** M3 implements, GLM 5.3 and Claude review. Every track closes through
`adversarial-review/<track>-round<N>.md` — implement → review → remediate →
re-review → APPROVE with zero residue. A track is not done because it runs; it
is done when a review round says so.

---

## The scoping mistake to avoid

There are two days. The instinct is to build the pipeline in pipeline order —
interview, then structure, then images, then audio, then UI, then deploy — and
discover on Sunday afternoon that the origin cannot be reached, or that a book
takes 140 seconds behind a proxy that gives up at 100.

**Deployment is T1, not T11.** The riskiest facts in this build are
environmental, not algorithmic: the Cloudflare timeout, the Traefik label, the
`source_audio` URL being publicly fetchable. All three are provable in an hour
against a stub that returns "hello", and all three are expensive to discover
late. Everything else is API calls we have already tested by hand.

The second trap is the reverse: over-building the interview. It is the
differentiator, but it is one system prompt and a loop. Do not spend Saturday
tuning question phrasing while there is no book renderer.

---

## Task graph

```
T0 ─→ T1 ─→ T2 ─→ T3 ─┬─→ T4 ─→ T5 ─→ T6 ─→ T7 ─→ T10
       (deploy early)  │                       ↘
                       └─→ T8 ─────────────────→ T9 ─→ T11 ─┬─→ T14
                                                            │
                                              T12, T13 ─────┘
                                              (optional, fold in if they land)

T0  repo skeleton              T8  audio: TTS + narration
T1  deploy path, end to end    T9  frontend shell + interview UI
T2  GMI clients                T10 book video (ffmpeg) + player
T3  store and media            T11 hardening: gate, cap, prewarm
T4  interview loop (Phase A)   T12 music bed        (optional)
T5  structuring (Phase B)      T13 voice clone      (optional)
T6  illustration               T14 submission       (last, always)
T7  consistency verification   T9a the waiting race (subtask)
```

T1 comes second on purpose. **T14 is last and is never skipped** — an unsubmitted
project scores zero. T12 and T13 are the two optional models: scored, not
required, and the first things cut.

### The generation path, end to end

The task graph above is dependency order. This is what actually happens to one
book at runtime, and **how much of each hop is already proven against
production** — because the tracks were built out of order, most of this chain
is verified well ahead of the code that will finally call it. Read this before
estimating anything: the unproven hops are the only ones that can still
surprise us.

| # | Hop | What crosses it | Proven? | Evidence |
| --- | --- | --- | --- | --- |
| 1 | child ⇄ **M3** | short questions, short typed answers → transcript | **live** (transport + model), **code** (loop) | T2b real `Chat` through the typed structs; T4 e2e start → turns → self-ended, transcript persisted and ordered |
| 2 | **M3** → story | transcript → 8 pages + cast bible + per-page prompt & emotion | **live** | T5b — two independent live calls with `thinking` ON over a 14-turn transcript, both schema-valid, both 8 pages, corrective system message obeyed |
| 3 | **t2i** → sheets | one reference image per cast member | **live** | T6b item 1 — `seedream-5.0-lite`, `outcome.media_urls[].url`, sizes pinned |
| 4 | **i2i** → pages | 8 pages locked to the sheets, multi-reference where two characters share a page | **live** | T6b item 2 — full eight-page book, 236 s, zero skips, no page byte-identical to its sheet, pages 3/4/7/8 hold both entities |
| 5 | **M3-as-judge** → verdict | sheet + page as inline base64 → `{match, drift}` | **live** | T6b item 2 — `match=true, drift=none` on all eight pages |
| 6 | verdict → **disk** | approved page → `mediastore` blob + `store` row | **live** | T6b item 3 (`6664be3`) — sheets and pages persisted by T7's `BookWriter` and fetched back through the real `GET /media/{id}` as 200 `image/jpeg`, byte-identical; `BookMedia` returns exactly 4 placed rows |
| 7 | **Speech 2.8** → narration | per-page text + emotion → one MP3 per page | **live** (transport + shape), track not built | T2b — real round-trip, result at `outcome.audio_url`, publicly fetchable, verified a real 128 kbps MP3. **T8 owes T10 one persisted clip per page, in page order** |
| 8 | pages + clips → **MP4** | ffmpeg segments + `concat -c copy` → one downloadable film | **verified, including inside the shipping image** | t10-video-record.md — the eight real T6b renders muxed to a playable 49.2 s file in 13.3 s; full chain re-run under the containerised ffmpeg 7.1 as uid 65532. Narration was **stand-in**, and serving/on-device playback are **not** covered — see that record's §What is NOT verified |
| 9 | MP4 → **the child** | book page, `<video controls playsinline>`, download, shareable URL | **not yet** | T10 |

Hops 1–6 are proven against production. Hop 8 is proven against real inputs
offline. **Hops 7 and 9 are the remaining risk**, and they are exactly T8 and
T10b.

### Sequencing, dependencies and complexity — what is left

Written 2026-09-05; **updated the same day** when T7 and T6b closed
(`fa3a516`, `6664be3`) and T10a opened.

**Complexity is calibrated against tracks already landed**, in this repo's own
currency (implement + at least one review round + remediation), using package
size as the yardstick:

| | Scale | Landed comparable |
| --- | --- | --- |
| **S** | one seam, offline-testable, a clean round is plausible | `internal/stream` 763 lines, `internal/job` 888 |
| **M** | two or three seams, or one live dependency | `internal/story` 1,587, `internal/mediastore` 1,291 |
| **L** | many seams, or the verification is human rather than mechanical | `internal/interview` 3,566, `internal/illustrate` 6,653 |

#### The remaining work

| Track | Owns | Blocked by | Cx | What makes it hard |
| --- | --- | --- | --- | --- |
| ~~**T7**~~ | — | — | **DONE** `fa3a516` | Closed at round-2 APPROVE 0/0/0/0 |
| ~~**T6b‑3**~~ | — | — | **DONE** `6664be3` | render → persist → serve, live PASS |
| ~~**T8**~~ | `internal/audio/**` | — | **DONE** `8b7066b` | Round-3 APPROVE 0/0/0/0. Wire-contract-2 amendment in round 4 |
| **T8b** | nothing in `internal/` (operator run) + `t8b-live-record.md` | T8, and the GMI TTS pool | **S** | Waiting on someone else's capacity, not on us |
| **T9** | `static/**`, `internal/web/**`, route lines | T4 — and see below on T8 | **L** | The only track whose verification is a **real phone**, not a test. Largest remaining surface |
| ~~**T9a**~~ | `static/race/**` | — | **DONE** | Closed at round-2 APPROVE 0/0/0/0 |
| **T10a** | `internal/bookvideo/**`, ffmpeg lines in `Dockerfile` | — | **S–M** | Nothing: recipe proven, fixtures on disk (t10-video-record.md). `exec.Command` hygiene, `t.Skip` when ffmpeg is absent |
| **T10b** | book template in `internal/web/**`, `static/book/**`, route lines | T9, T10a, T10c | **S** | One route, one `<video>`, `Content-Disposition`. Small *because* T10a took the work |
| **T10c** | `internal/bookgen/**` + route lines | T5, T6, T7, T8, T10a | **M** | Nothing joins the stages today. Exactly-once triggering at ~$0.35 a run, and the events screen 5 lives on |
| **T11** gate | `internal/gate/**` + route wrap | — | **S** | Middleware against no one else's code |
| **T11** sweep | retention sweep in `internal/mediastore/` | — (T3 closed) | **S** | Disk cap arithmetic |
| **T11** prewarm | fixtures | T10b | **M** | Needs real finished books: money, clock, free window |
| **T12** | `internal/audio/music.go` | T8, T10a | **S** | Nothing left: the mix is a **verified 1.1 s final pass** over the finished MP4 (below). One free live call to get the bed |
| **T13** | `internal/audio/clone.go`, **plus both capture modes in `static/**` and one upload route** — the recorded Owns line was incomplete | T8, T9 (adult corner), **T10a** (transcode), + the consent decision | **M** | Not the clone call — the **sample**. Recording is non-negotiable, and the recorder hands back opus/mp4, never mp3 |
| **T14** | `README.md`, submission assets | T11 (+T12/T13) | **M** | Fixed 4-hour box; the demo video is an edit, not a build |

#### Order: T8 before T9, and the rework question disappears

The recorded `T9 depends on T4, T8` is real, and **the cheapest way to honour
it is to build T8 first**, not to run the two in parallel and reconcile them.

Run them the other way round and T9 owes T8 a seam it cannot retrofit: the
**"Tap to start" entry gesture and the single unlocked `Audio` element it
creates** — an entry *screen*, and an object whose lifetime is the whole
session. Adding it after the flow exists means inserting a first screen and
re-plumbing every playback call, and the naive retrofit (`new Audio()` per
clip) is the exact silent iOS failure §T8 warns about. Sequencing removes the
seam question entirely: T9 builds against a wire that already carries the URL.

~~What T8 hands T9 is a field on `questionEvent`.~~ **Corrected 2026-09-05
after an outside review — that was wrong twice over**, and the correct
contract is in §The flow → *The wire contracts*. Putting `audio_url` on
`questionEvent` would make the event unpublishable until the TTS round-trip
finished, i.e. **text waiting on audio**, the one rule §T8 and project.md §4
both lead with; and it names a `/media/{id}` URL that does not exist, because
`audio.SynthesizeQuestion` returns bytes and deliberately touches no store
(`internal/audio/audio.go:380`, `:326`). Question audio is a **second SSE
event**, not a field.

#### Two dependencies this plan overstates

1. **T10's renderer needs nothing.** `internal/bookvideo` is a pure function —
   page images and audio files in, one MP4 out — already proven against
   `data/live/t6b-book/` with stand-in clips (t10-video-record.md). Only the
   *player* needs the shell and real data. Hence the T10a / T10b split above:
   T10a is **not on anyone's critical path and carries no merge risk**, so it
   fills any gap in the schedule — including a stall waiting on T7.
   What makes that safe is the contract already pinned in §T8: **one persisted
   clip per page, in page order.** T10a's `-shortest` timing model depends on
   nothing else about T8.
2. **T12 got cheaper when the book became a video.** Recorded as depending on
   T10; it now needs **T10a only**, and is an `amix` filter argument rather
   than a looping `<audio>` racing the page. Re-rate it S.

#### The critical path

**~~T7~~ → ~~T8~~ → T10c → T9 → T10b → T11 → T14**, with ~~T10a~~ and ~~T9a~~ done,
and **T8b** a retry that can fire any time the TTS pool recovers.

**T10c moved onto the critical path on 2026-09-05** and it sits *before* T9,
not after: screen 5 cannot be built against a stream that does not exist, and
T10c is what makes a book exist at all.

That is close to a straight line, and deliberately so. The one place a second
worker pays for itself is **T14's README and demo-video prep**, which is prose,
touches no code, and can be drafted against a book that already exists.

If two route-adding tracks ever do end up in flight together, the collision is
`newServer` (`cmd/thutapi/main.go:141`, also spelled at `main.go:272` and
`main_test.go:59`) — a positional parameter list plus a contiguous block of
adjacent `HandleFunc` lines. Taking a deps struct and one `Routes(mux, deps)`
per package fixes it in minutes. **Worth doing when T9 adds the second batch of
routes anyway; not worth doing speculatively before then.**


### Status

| ID | Status |
| --- | --- |
| **T0** | DONE. Closed at 166a992 after 3 review rounds (zero residue; see t0-round1.md, t0-round2.md, t0-round3.md, t0-remediation-round1.md, t0-remediation-round2.md). |
| **T1** | DONE. Closed at 65df379 — artifacts (Dockerfile, .dockerignore, deploy/docker-run.sh, deploy/README.md) committed, local smoke green, all Traefik labels byte-identical to project.md §Deployment. See dev-diary/adversarial-review/t1-round1.md. |
| **T1b** | DONE. Closed at 52b2cd2 — checks 1, 2, 3, 4 pass against https://thutapi.nryn.dev/healthz (cf-ray present, Let's Encrypt origin cert via DNS-01, all five Traefik labels byte-identical). Check 5 moved to T3 (re-scoped: T1 binary has no static-file route). Run also repaired Traefik's CLOUDFLARE_DNS_API_TOKEN — restored cert renewal for the whole box (thutapi, serp, auteur, eoc, mosaic). Full infrastructure record (incident, resolution, token-replacement procedure) is in `~/work/hetzner/docs/foleyflow-server.md` §5, deliberately kept out of this repo. |
| **T2** | DONE. Closed after a round-3 re-open: round 3 re-reviewed the whole `Done when` (rounds 1–2 had been scoped to round-1 residue plus a diff audit and were correct within that scope) and found **2 × H, 4 × M, 5 × L** — `EditImage` defaulting to the struck-through `Qwen-Image-2512` (would silently break T6's character lock), the `Retry:` contract unimplemented while `errors.go` claimed it, every-unclassified-4xx-`ErrTransient`, `GenerateImage` sharing the same bad default, the raw-bytes/no-live-test `Done when` gap, CI gofmt blind to `internal/`, and five L polish items. Remediation round 3 fixed all eleven; round 4 found **2 × M, 2 × L** (three coverage-floor statements disagreeing, an unrecorded AGENTS.md hunk, `&Reasoning{}` leaking `thinking:{"type":""}` on the wire, `ErrModelNotFound` docstring implying message matching); remediation round 4 fixed those. **Round 5: APPROVE 0/0/0/0, zero residue against rounds 1–4** (t2-round3/4/5.md, t2-remediation-round3/4.md). Defaults: `GenerateImage` → `Flux2-Klein` (§3 Start-on), `EditImage` rejects an empty model; retry contract implemented in both clients (one retry on `ErrTransient` only, request-queue `failed` included, never 4xx, default per-call deadline); catch-all 4xx → `ErrBadRequest`, 402 → `ErrPaymentRequired`; two-tier coverage floor (75% per package, 85% for `internal/gmi/*`). Live-endpoint half lives in **T2b**. |
| **T2b** | **DONE** 2026-09-05. Closes the live half of T2's original `Done when`. Working key installed (252-char JWT; the `sk-or-v1-…` OpenRouter-shaped key recorded in §T2 is replaced). Text: real `Chat` decoded through the typed structs (`finish_reason=stop`, `text="pong"`); bare `MiniMax-M3` 404s with the exact upstream string the package doc quotes and classifies as `ErrModelNotFound` — the prefix quirk is now verified against production, not documentation; `thinking:{"type":"enabled"}` accepted. `/v1/models` lists 82 models incl. `MiniMaxAI/MiniMax-M3`. Media: real TTS round-trip, unknown model → `ErrModelNotFound`. **Finding:** the media package doc is wrong — TTS returns the queue envelope, not a binary blob, with the audio at `outcome.audio_url` on `storage.googleapis.com`, publicly fetchable with no credential (verified 200 `audio/mpeg`, 63,348 bytes, real MP3). That lands on T8 (download and persist) and flags a T13 safety question (a cloned child voice would sit at an unauthenticated public URL). The `need_volumn_normalization` typo was echoed back by the upstream, confirming the pin against production. Probes committed as `//go:build live` tests. Zero cost. See t2b-t5b-live-record.md. |
| **T3** | DONE. Round 1 (t3-round1.md): REMEDIATE 0C/1H/1M/1L — `mediastore.ErrNotFound` documented but returned by no code path, media unbound to pages/cast with no listing queries (T6/T8/T10 unanswerable), five untested error branches. Remediation round 1 fixed all three (kind + page/cast anchor columns with composite FKs and partial unique indexes, `SetMediaPlace`/`BookMedia`/`PageMedia`/`CastMedia`, five branch pins, sentinel contract table). **Round 2: APPROVE 0/0/0/0, zero residue** (t3-round2.md). Restart-survival Done when pinned end to end incl. 206 Range after reopen; live smoke: cross-process WAL write, Range slice byte-exact. |
| **T3b** | DONE. Round 1 (t3b-round1.md): REMEDIATE 0C/0H/2M/2L — running jobs had no cancellation path (`WithoutCancel` + no `Cancel` → leaked slots, no handle for T11/operator on runaway spend), submit bodies with a request_id but unknown/absent status passed through as the result without polling, `Event.Name` newlines could inject SSE field lines, and a deadline landing mid-poll-GET surfaced `ErrTransient` instead of `ErrPollDeadline`. Remediation round 1 fixed all four (cancel registry + `Runner.Cancel` + `StatusCancelled` terminal, request_id-bearing submit bodies now poll, `oneLine` name sanitization, `pollCtx.Err()` guard before transient counting). **Round 2: APPROVE 0/0/0/0, zero residue** (t3b-round2.md) — Cancel-vs-completion proven deterministic (200 interleavings × 20 race runs, exactly-once terminal), all four mutations re-applied by hand and reverted byte-identical. Coverage: stream 94.4%, job 95.7%, media 92.5%. |
| **T4** | DONE. Round 1 (t4-round1.md): REMEDIATE 0C/1H/3M/3L — the stall rule ended interviews after two substantive one-word answers ("Mira" → "red"), end-marker-only replies left ended interviews reopenable, opening-turn failures were invisible to clients, error payloads carried freeform prose instead of machine classes, plus streak-rollback, enforced-end persistence, and doc-comment gaps. Remediation round 1 fixed all seven (end fires only on no-progress/repetition with a per-turn chip-signal directive to the model; closing turn persisted unconditionally on every end path; turn-failure marker surfaced through the catch-up route; sentinel-derived error tokens on the wire). **Round 2: APPROVE 0/0/0/0, zero residue** (t4-round2.md). E2e: start → turns → self-ended (checklist, stall, MaxTurns paths) against a scripted fake, SSE-observable, transcript persisted and ordered; thinking OFF pinned on raw wire. Coverage: interview 93.4%. |
| **T5** | DONE. Round 1 (t5-round1.md): REMEDIATE 0C/0H/3M/2L — transport-error pin had an empty assertion body (swallow mutant went green), the Done when's live half had no owner, the prompt taught "exactly 8 pages" while the validator enforced no count, a prose-embedded decoy JSON object could be taken as the book (full-story decoy silently in one call), and cast uniqueness was case-sensitive (Mira+mira split the T6 lock). Remediation round 1 fixed all five (real transport pins incl. six-sentinel probe; T5b operator row + amended Done-when; `PageCount = 8` taught by prompt and enforced by validator, cut-to-6 changes exactly that rule; extractStory scans all candidates for first decode-AND-validate with first-candidate fallback errors; EqualFold uniqueness with exact references). **Round 2: APPROVE 0/0/0/0, zero residue** (t5-round2.md). Coverage: story 100%. Live half owned by **T5b**. |
| **T5b** | **DONE** 2026-09-05. Closes the live half of T5's original `Done when`: two independent live M3 calls with `thinking` ON over a realistic 14-turn transcript, both schema-valid, both 8 pages, all emotions in the taught vocabulary, every page character present in the cast (14s and 37s). Corrective-system-message acceptance **confirmed** — MiniMax obeyed a `system`-role message placed after an assistant turn, which is the mechanism `story.Structure`'s corrective retry depends on. **Two findings handed to T6** (not T5 defects — the validator does what it was specified to do): live casts contain non-visual members (`Narrator`, `visual:"no visual"` / `"an unseen storyteller with no appearance"` — two phrasings, so no literal match works) which would each burn a ~$0.01 reference sheet on nothing; and one entity can occupy two cast slots (`Grumpy River` + `Happy River`, the latter's visual opening "the same wide blue river"), which the image lock would render as two unrelated rivers — the exact drift T6 exists to prevent. Also: M3's page prompts already carry their own style language, which competes with T6's constant style suffix. Probes committed as `//go:build live` tests. Zero cost. See t2b-t5b-live-record.md. |
| **T6** | **DONE** 2026-09-05. Round-1 review (0C/3H/3M/2L) remediated and re-reviewed: **round 2 APPROVE 0/0/0/0, zero residue** (t6-round1.md → t6-remediation-round1.md → t6-round2.md). T6b item 1 (live, ~$0.25) had already established the model change beneath the round: **`Flux2-Klein` and `Z-Image` never generate** (accept, sit at `queued` forever) — they moved onto the forbidden table; **`seedream-5.0-lite`** is `DefaultModel`, synchronous, references as an **array of URLs**, results at `outcome.media_urls[].url` (array of objects beside `thumbnail_image_url`). H1 (payload echo beats the result) was confirmed Critical and fixed by keying the decode on `outcome.media_urls` by name; the echoing fixture now lives in the suite and page bytes are asserted ≠ sheet bytes. Round-1 remediation also landed the sanctioned `media.EditImage`/`GenerateImage` contract change (`ImageOptions`, `refImages []string` URL array, clean cutover) and fixed H2 (variant fusion only same-entity), H3 (absence words drawable), M1 (normalised forbidden-id guard incl. dead models and video stems), M2 (zero-Book pinned on render paths, M-k red), M3 (deferred unsupported candidates), L1, L2 (redirect scheme allow-list per hop + cap). Full M-a..M-k table re-run 11/11 red by the round-2 reviewer, tree byte-identical. Pinned production settings: `size:"1792x2240"` (not the `2K` preset), `output_format:"jpeg"`, `max_images:1`, `watermark:false`. The live half of the Done when (eight pages, constant cast; render → persist → serve) is T6b items 2–3, which open now that T6 has closed. |
| **T6b** | **DONE** 2026-09-05 — all three items closed, ~$1.00 total. Item 1 (~$0.25): settled the model question (seedream-5.0-lite; Flux2-Klein/Z-Image never generate), the response shape (`outcome.media_urls[].url`, array of objects) and H1's severity. Item 2 (10 calls, ~$0.35): full eight-page constant-cast book live after T6 closed — PASS, 236 s, no page byte-identical to its sheet (H1 echo absent live), multi-reference pages hold both entities, M3-as-judge `match=true, drift=none` on all eight pages; renders under `data/live/t6b-book/` for human review. Item 3 (8 calls over two runs, ~$0.28): render → persist → serve end to end after T7 closed — T7's BookWriter placed 2 sheets + 2 pages (immediate sheets, post-approval pages, echo guard held), every id fetched back 200 `image/jpeg` byte-identical through the real `GET /media/{id}` route pattern; the first run's 404s were the probe's own serve-leg bug (bare-mount vs route pattern), not the product. Full record: t6b-live-record.md. The download/mux tail after serve is T10's (offline chain proven in t10-video-record.md). |
| **T7** | **DONE** 2026-09-05, closed at `fa3a516`. Round-1 review (t7-round1.md) REMEDIATE 0C/0H/2M/1L → remediation (t7-remediation-round1.md) pinned all three → **round-2 APPROVE 0/0/0/0, zero residue** (t7-round2.md; all seven mutants re-run red/hang, byte-identical restores). Implemented T6's closing loop in `internal/illustrate/verify.go` + `persist.go`: echo guard (a page byte-identical to a locked sheet is `ErrDecodeEcho` — a decode defect, judged never, regenerated never), M3-as-judge verdict with up to 2 regenerations of the same prompt+sheets (`ErrConsistency` after the cap), judge transport errors surfaced unretried, `ErrBadVerdict` on unusable replies; sheets persist immediately per render, pages only post-approval, `ErrConflict` re-run replaces the occupant. Config gains `Judge` + `Persist` (nil = off; zero value byte-for-byte T6, no T6 test edited). Six contract rows C1–C6 sanctioned. The judge mechanism was pre-verified live (T6b item 2: M3 verdicts over the eight-page book). Live half of T7's loop (render → persist → serve end to end) is T6b item 3, which opens now. |
| **T8** | **DONE** 2026-09-05, closed at `8b7066b` — round 1 (0C/0H/0M/2L) → round 2 (0C/1H/0M/0L, the emotion-key evidence chain) → **round 3 APPROVE 0/0/0/0**. `NarrateBook` (one clip per page, in page order, blob-first then `MediaNarration` place, `ErrConflict` replaces the occupant) and `SynthesizeQuestion`; sanctioned contract row C1 gave `media.SynthesizeSpeech` an emotion parameter. **Two things ride on top.** (a) **Round 4, in review now**: the wire-contract-2 amendment — question audio persists as an **unplaced** row and returns its id, because a `question_audio` event needs a URL that exists; this supersedes round-1's D3. (b) **T8b**: the emotion key is asserted-but-unverified live. |
| **T8b** | **Not started. Recorded 2026-09-05** — T8's commit called it "a recorded T8b obligation" and no such row existed until now. Settles the one thing T8 could not: the emotion key's live behaviour. See §T8b. |
| **T9** | Not started. |
| **T9a** | **DONE** 2026-09-05. Closed after round-1 remediation and **round-2 APPROVE 0/0/0/0, zero residue** (t9a-round1.md, t9a-remediation-round1.md, t9a-round2.md). Delivered in `static/race/**`: standalone `race.html` and `index.html` with single-property `--done: 0..8` interface, 8 markers, composite-only `transform: scaleX(...)` progress bar, 5 distinct custom animal sprites (Hare, Tortoise, Fox, Duck, Mouse) with unique silhouettes at 64×44, `.runner svg{overflow:visible}` preventing ear clipping during `.bob`, non-overshooting deceleration curve `cubic-bezier(.25, 1, .5, 1)` preventing finish line clipping, full palette tokenization via CSS `var()`, and `prefers-reduced-motion` support. Suite passes in `race_test.go`. |
| **T10c** | **Not started. Assigned 2026-09-05** on an outside review of the T9 spec — the Phase B orchestration seam. Every stage is built and APPROVE'd and **nothing joins them**: `closeTurn` publishes `ended` and returns, and no caller of `story.Structure`, `illustrate.Illustrate` or `internal/bookvideo` exists outside their own tests. Now on the critical path, **ahead of T9**. See §T10c. |
| **T10a** | **DONE** 2026-09-05. Closed after round-1 remediation and **round-2 APPROVE 0/0/0/0, zero residue** (t10a-round1.md, t10a-remediation-round1.md, t10a-round2.md). Implemented video pipeline in `internal/bookvideo` (`types.go`, `command.go`, `video.go`, `bookvideo_test.go`): title card (blurred page 1 with title + byline via `textfile=`), page segments (`-shortest`, scale/pad/setsar 1080x1350 4:5 portrait, libx264/aac), end card (flat `0x1b1614` with domain attribution), and concat demuxer (`-c copy +faststart` with MP4 metadata tags). Bounded concurrency via `errgroup.SetLimit`. Added static ffmpeg 7.1 pinned by immutable sha256 digest to `Dockerfile`. Coverage 89.1%. |
| **T10b** | Not started. Book template under `internal/web/**`, `static/book/**`, and route lines in `newServer`. |
| **T11** | Not started. |
| **T12** | Optional. Not started. |
| **T13** | Optional. Not started. |
| **T14** | Not started. Never cut. |

---

---

## Architectural invariants

Rules that hold across every track. Breaking one is a **plan change**, not an
implementation detail — propose it in the track's review file first
(`AGENTS.md` §Read order, pre-flight assertion).

1. **`internal/gmi` is the only thing that talks to GMI.** No track builds its
   own HTTP call to `api.gmi-serving.com` or `console.gmicloud.ai`. If the
   client cannot do what a track needs, the client changes — the track does not
   route around it.
2. **Config flows down from `main`; packages do not read the environment.**
   `cmd/thutapi/main.go` already does this properly with `config` +
   `parseFlags`. **`internal/gmi` currently violates it** — `New()` reads
   `GMI_*_BASE_URL` at construction and both clients read `GMI_API_KEY` on
   every call. The cost is concrete and already paid: any test that needs a key
   must use `t.Setenv`, and `t.Setenv` makes `t.Parallel` panic, so the gmi
   suites can never run in parallel. Recorded as a known deviation, not a
   licence — **new packages take a config struct.**
3. **Consumers declare interfaces; producers return concrete types.** T4 should
   depend on a one-method interface *it* declares (`type chatter interface {
   Chat(context.Context, text.ChatRequest) (*text.ChatResponse, error) }`), not
   on `*text.Client`. Otherwise every downstream package needs `httptest` to
   test a code path that has nothing to do with HTTP.
4. **The request queue is a queue.** `console.gmicloud.ai/.../requests` returns
   `{request_id, status}` for image work; T2 ships one POST and hands back raw
   bytes, so **the polling layer does not exist yet**. Whoever needs it first
   builds it *inside* `internal/gmi/media` — not privately in `internal/illustrate`
   and again in `internal/audio`.
5. **`newServer` in `cmd/thutapi/main.go` is a shared seam, and the only one.**
   A track that adds a route appends its `s.mux.HandleFunc` line there and
   nothing else; the handler itself lives in that track's package. `main.go`
   stays free of business logic (§T0 conventions). Expect to touch this file
   from a track that does not own it — that is the one sanctioned exception to
   the `Owns` rule, and it is one line.
6. **Nothing blocks longer than 100 seconds.** Cloudflare kills a proxied
   request at 100s with a 524 (§T1). Long work is started by a short POST that
   returns a job id and observed over SSE. This is an architectural constraint,
   not a tuning knob.
7. **Persist GMI output on receipt.** Response URLs point at
   `storage.googleapis.com` and are assumed to expire (§T3).
8. **Errors cross a package boundary as a sentinel**, matched with
   `errors.Is` — an `internal/gmi` sentinel, or one the track declares itself.
   Never a matched substring of a provider message.
9. **A `b`-suffixed track owns the `live_test.go` in each package its parent
   track owns**, and owns nothing else. Live verification is split off into a
   `b` track whenever it needs credentials, an operator, or the world (the
   T1/T1b split is the precedent; T2b and T5b follow it). Probes go in
   `//go:build live` files so they never run in CI and never gate a build —
   they are evidence, and `AGENTS.md` §Testing forbids a live call from being a
   CI gate. A parent that owns no Go package leaves its `b` track owning no
   repo paths at all, which is why T1b's `Owns` line reads as it does.

---

## Unowned seams

Things the task graph assumes exist, that no track's `Owns` line covers.
**Assign each one before the track that depends on it starts.**

| Seam | Assumed by | Status |
| --- | --- | --- |
| **SSE broker** (`internal/stream`) | §T4 *"Turns stream over the T1 SSE channel"*, T9, T10, and invariant 6 — i.e. the entire non-blocking architecture | **Assigned: T3b** (was "proposed, needs sign-off"; the operator's 2026-09-05 overnight go-ahead on the task graph is the recorded sign-off). project.md §Lift from Mosaic names `internal/stream/broker.go` (223 lines) as directly reusable. |
| **Job orchestration** (start → job id → progress events → result) | T4, T5, T6, T8, and every SSE consumer | **Assigned: T3b** as `internal/job`, alongside the broker. |
| **Request-queue polling** | T6, T8 | **Assigned: T3b** — built inside `internal/gmi/media` per invariant 4, so it lands once. |
| **`internal/web`** (shell template, book template) | T9, T10 | Now named in T9's and T10's `Owns`. Previously implied by AGENTS.md's file layout and by nothing else. |
| **Phase B orchestration** (interview ends → structure → illustrate + verify + persist → narrate → render → serve) | §The flow screens 5–6, T5, T6, T7, T8, T10a — *everything downstream of the interview* | **Assigned: T10c** (2026-09-05, on an outside review of the T9 spec). Found by grep: **nothing outside its own package and tests calls `story.Structure`, `illustrate.Illustrate` or `internal/bookvideo`.** `closeTurn` (`internal/interview/turn.go:238`) publishes `ended` and triggers nothing. Every generation stage exists and is APPROVE'd; no code makes a book. |
| **Illustration persistence** (render → `mediastore` blob → `store.SetMediaPlace`) | T6 renders and deliberately writes nothing; T10 serves what must already be on disk | **Resolved: T7** (2026-09-05 — assigned on round-1 ruling 2, implemented in `persist.go`, closed at `fa3a516`). A page is final when the judge approves it; sheets persist immediately, pages post-approval, `ErrConflict` re-run replaces the occupant. T10 now reads what T7 wrote; T6b item 3 proves the render → persist → serve path live. |


## The deadline's timezone

**The campaign page never states one.** It says "Submit your project by
September 6, 2026" and nothing more. The spread is 15 hours — a serious
fraction of what is left:

| If "Sep 6" means end of day in… | In IST | Hours left from Fri 15:13 |
| --- | --- | --- |
| **China** (MiniMax, UTC+8) | **Sun 6 Sep, 21:29** | 54.3 |
| India | Sun 6 Sep, 23:59 | 56.8 |
| UTC | Mon 7 Sep, 05:29 | 62.3 |
| US Pacific (GMI, PDT) | Mon 7 Sep, 12:29 | 69.3 |

Best evidence points to **Pacific**: the only timezone stated anywhere on the
page is PDT, for both live sessions, and GMI Cloud is US-based. But that is
inference from a scheduling widget, not a rule — and the page already
contradicts itself once, with the submission form saying "closes on a date to
be confirmed" directly beneath a rulebook naming September 6.

**Build to the earliest and treat the rest as buffer.** A wrong guess then
costs slack instead of the entry.

**Resolve it cheaply:** GMI runs a Discord. One line, and 15 hours stop being a
guess.

The same ambiguity hits the **free-model window**, which also only says
"– 2026.09.06". If it expires on China time while generation is still running
on Sunday evening, those calls bill at standard rates.

---

## Two-day shape

**Saturday 2026-09-05** — T0 through T7. A book generates end to end from a
hardcoded story, on the live URL. Ugly is fine; the pipeline must be real.

**Sunday 2026-09-06** —

| Time (IST) | |
| --- | --- |
| → 16:00 | T8–T11 (+T12/T13 if reached): audio, interview UI, book video, hardening |
| **16:00** | **Hard stop on building.** Whatever is unfinished gets cut. |
| 16:00–20:00 | T14: video, repo public, form, X post |
| **20:00** | **Submit.** 90 minutes before the earliest plausible deadline. |

A finished project that is not submitted scores zero, and that failure mode is
common enough to write down.

---

## T0 — Repo and skeleton

**Owns:** `cmd/thutapi/**`, `go.mod`, `go.sum`, `AGENTS.md`, `.gitignore`.

`go mod init`, module layout, `AGENTS.md`, a `Dockerfile`, and a `/healthz` that
returns 200. One binary. No cleverness — a skeleton written before there is
anything to link against gets rewritten.

**Depends on:** nothing. **Done when:** `go build ./...` is green and
`./thutapi` serves `/healthz`.

Conventions set here that every later track follows:

* **Go stdlib first.** `net/http`, `encoding/json`, `html/template`, `sync`,
  `log/slog`. Third-party dependencies are `modernc.org/sqlite` and
  `golang.org/x/sync/errgroup`. Nothing else without a reason written down.
* **No Node in the build.** The frontend is vendored (T9). If a track proposes
  a build step, it is the wrong track.
* **`AGENTS.md` carries the pins**, because three different models write here:
  * htm uses backticks and `${}`, never real JSX — ``html`<div class=${c}>${k}</div>` ``
  * every GMI response shape is a named struct in `internal/gmi`, never
    `map[string]any` at a call site
  * user-facing strings are child-facing: no jargon, no error codes on screen
* **File discipline:** soft target ~400 lines, split at seams.

---

## T1 — The deployment artifacts  *(DONE — closed at 65df379; T1b live verification closed at 52b2cd2)*

**Owns:** `Dockerfile`, `.dockerignore`, `deploy/**`, `.github/workflows/**`, `.env.example`.

Ship the T0 skeleton to `thutapi.nryn.dev` and prove every environmental fact
while they are cheap to fix.

**Depends on:** T0. **Done when:** the Dockerfile, `.dockerignore`,
`deploy/docker-run.sh`, and `deploy/README.md` are committed; a local
`docker build -t thutapi:local . && docker run --rm -p 18080:8080 -e
PORT=8080 thutapi:local` succeeds (a local smoke test — the deploy path
itself is the GHCR image, see project.md §Packaging); `curl http://127.0.0.1:18080/healthz`
returns 200 JSON with the version field; `docker ps` shows no dangling
container after smoke; `go vet ./...`, `go test ./... -race`, and
`gofmt -l cmd/thutapi/` are clean; and the five Traefik labels in
`deploy/docker-run.sh` are byte-identical to project.md §Deployment.
T1 covers the artifacts; **T1b owns the live deploy probe** (the five
live URL checks against `thutapi.nryn.dev`) because that work needs
operator access to foleyflow and the nryn.dev DNS zone that no agent
on this workstation holds.

1. **Traefik picks it up.** Container started with the five labels:
   ```
   traefik.enable=true
   traefik.http.routers.thutapi.entrypoints=websecure
   traefik.http.routers.thutapi.rule=Host(`thutapi.nryn.dev`)
   traefik.http.routers.thutapi.tls.certresolver=letsencrypt
   traefik.http.services.thutapi.loadbalancer.server.port=8080
   ```
2. **DNS.** An A record for `thutapi.nryn.dev` → `${ORIGIN_IP}`, **proxied**,
   matching `auteur` / `mosaic` / `eoc` / `serp`. The literal address is in
   the gitignored `.env`; it is never committed, because publishing the
   origin defeats the proxy that hides it.
3. **Certificate issues.** Traefik issues by **DNS-01** via a Cloudflare
   token (`/opt/traefik/static.yml`), not HTTP-01 — so the proxy is
   irrelevant to validation and the origin IP need not be publicly
   resolvable. The existing four subdomains prove the path, but confirm
   for this one. Note the Let's Encrypt cert is the **origin** cert; the
   public handshake returns Cloudflare's edge cert instead.
4. **`curl https://thutapi.nryn.dev/healthz` returns 200** with `cf-ray` present.
5. **A public file is fetchable by a third party.** Put a dummy MP3 at a signed
   path and confirm an outside fetch works — this is the exact mechanism
   Speech 2.8's `source_audio` needs (T14), and the only way to find out it is
   broken is to try it.

### Cloudflare — the constraint that shapes the architecture

`nryn.dev` runs on Cloudflare nameservers and the existing subdomains are
**proxied** (`server: cloudflare`, `cf-ray`, 104.21/172.67 addresses).

**A proxied request dies at 100 seconds with HTTP 524.** A book is ~10 image
generations plus audio. **Generation therefore cannot be a blocking POST.**

* Generation is **started** by a short POST that returns immediately with a job
  id, and **observed** over SSE. First byte is instant, so the 524 never arms.
* SSE responses set `Content-Type: text/event-stream`, `Cache-Control: no-cache`
  and `X-Accel-Buffering: no`, and flush after every event.
* A heartbeat comment (`: ping`) every 15s keeps intermediaries honest.
* **SSL mode must be Full (strict).** Flexible plus Traefik's HTTPS redirect is
  an infinite redirect loop. It is zone-wide and the existing sites work, so
  this should already be correct — verify, do not assume.

> **Measured 2026-09-04: the zone is on `full`, NOT `strict`.** The
> assertion above that it "should already be correct" was wrong. Full
> encrypts to the origin but does not validate the origin certificate,
> which was the only reason `thutapi` and `serp` served 200 while Traefik
> handed out a self-signed `CN=TRAEFIK DEFAULT CERT`. That is now fixed —
> the Traefik DNS-01 token was repaired and all five origins hold real
> Let's Encrypt certificates, so the zone **can** safely move to strict.
> It has been left on `full`, which fails nothing. If you do switch it,
> confirm every origin cert first: a hostname on the default cert returns
> 526 the instant strict is on. See `~/work/hetzner` §5.1.
* Generated media is immutable, so serve it with a long `max-age` and let the
  Cloudflare edge cache it. That is free performance for a judge.

---
## T1b — Live deployment verification

**Owns:** **no repo paths.** Operator track: it changes DNS, the box, and `~/work/hetzner/docs/`, and writes back only its status row here.

**Status:** DONE. Closed at 52b2cd2. Checks 1, 2, 3, 4 all pass;
check 5 moved to T3 (re-scoped — the T1 binary has no static-file
route, so it cannot pass here regardless of DNS state). The run also
repaired Traefik's broken `CLOUDFLARE_DNS_API_TOKEN` and restored
certificate renewal for every site on the box (thutapi, serp,
auteur, eoc, mosaic). See `~/work/hetzner/docs/foleyflow-server.md` §5
for the full transcript and the two
artifact defects (D1 `--network proxy`, D2 chown uid 65532) the run
surfaced and fixed.

**Depends on:** T1 (artifacts). **Unblocks:** T2 (GMI clients),
T13 (voice clone, check 5), T14 (submission requirement: "Live app URL
on the Hetzner container").

**Done when:** all five checks from the original T1 done-when pass
against `https://thutapi.nryn.dev/healthz`:

1. Traefik on `foleyflow` picks up the container with the five labels.
2. DNS A record `thutapi.nryn.dev` → `${ORIGIN_IP}`, proxied
   (matching `auteur`/`mosaic`/`eoc`/`serp`).
3. Let's Encrypt **DNS-01** (Cloudflare token) issues the origin cert.
4. `curl https://thutapi.nryn.dev/healthz` returns 200 JSON with
   `cf-ray` present.
5. ~~A dummy MP3 at a signed path is fetchable by a third party~~
   **Moved to T3.** The T1 binary serves only `GET /healthz` — there is
   no static-file route for a probe file to be reachable through, so this
   can never pass at T1b. It is T3's `http.ServeContent` handler that
   makes it answerable. The reason for the check is unchanged and still
   blocks T13; it just moves one track later.

**Operator runbook:**

```bash
# Publish an image from CI (manual — image.yml is workflow_dispatch only):
gh workflow run image.yml -f latest=true
# The box pulls it anonymously; docker-run.sh does the pull itself.
#
# Fallback if GHCR is unreachable — a workstation build, shipped directly.
# Not reproducible: the box cannot rebuild this image.
#   docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t thutapi:local .
#   docker save thutapi:local | ssh foleyflow 'docker load'
#   then: IMAGE=thutapi:local /srv/thutapi/deploy/docker-run.sh

# On foleyflow (via SSH), prepare the data dir and run the deploy:
ssh foleyflow
sudo mkdir -p /srv/thutapi/data
sudo chown 65532:65532 /srv/thutapi/data    # distroless nonroot uid, NOT 1000

# Secrets: a root-owned 0600 file, NOT an interactive export (an export
# persists in ~/.zsh_history in plaintext). One-time:
sudo install -d -m 0700 /etc/thutapi
sudo install -m 0600 /dev/null /etc/thutapi/env
sudo $EDITOR /etc/thutapi/env               # GMI_API_KEY=<from operator vault>

/srv/thutapi/deploy/docker-run.sh           # uses Traefik labels from project.md

# Verify:
curl -fsS -i https://thutapi.nryn.dev/healthz
# Expect: HTTP/2 200, cf-ray header, body {"status":"ok","version":"dev",...}
```

**Failure modes the operator should expect and the work needed:**

* DNS not propagated yet (Cloudflare adds the record on the operator's
  side; nryn.dev is on Cloudflare nameservers, so 1.1.1.1 is the right
  resolver to check).
* Let's Encrypt rate limit if a sibling cert was issued in the last
  five minutes — retry.
* Cloudflare SSL mode for the zone is **`full`, not `strict`** (measured
  2026-09-04). Flexible plus Traefik's HTTPS redirect is an infinite
  redirect loop, so `full` is safe; `strict` is not, until every origin
  has a real cert. Do not "fix" this setting — see `~/work/hetzner` §5.1.
* If `curl /healthz` returns 524, Cloudflare's 100s proxy timeout
  fired — but `/healthz` is sub-second, so the cause is upstream
  (Traefik not picking the container; check labels and Traefik logs).

**When done:** paste the five transcripts (or a single one with all
five signal-bearing lines) into `~/work/hetzner/docs/foleyflow-server.md`,
mark T1b status DONE in the table above, and the chain to T2/T13/T14
opens. Until then T1b stays `Blocked`.


---

## T2 — GMI clients  *(DONE — closed at round 5, see t2-round5.md; live half in T2b)*

**Owns:** `internal/gmi/**` (`errors.go`, `text/`, `media/`).

Two clients, because GMI has two APIs with different shapes.

**Depends on:** T1. **Done when:** both endpoints are exercised end to end by
the httptest suite at the raw-wire level — the `{model, payload}` envelope,
bearer auth, the `MiniMaxAI/` prefix pin, the `need_volumn_normalization`
typo pin, and the retry contract below — and responses are handed to callers
as **raw bytes**. Typed response shapes were dropped **deliberately**
(amended 2026-09-04 at round 3, t2-round3.md M3): the request-queue API
returns a different result schema per model and GMI has not published them
all, so inside the two-day window a typed struct would be a fabrication; the
decode belongs to T6 and T8, where the model — and therefore the schema — is
known. The live half of the original criterion is **T2b**, an operator track
on the T1/T1b precedent: it owns the live probes of both endpoints and runs
once a working key exists, keeping this criterion mechanically checkable
from a clean tree.

| | Text | Audio / image / video |
| --- | --- | --- |
| Host | `api.gmi-serving.com` | `console.gmicloud.ai` |
| Path | `/v1/chat/completions` | `/api/v1/ie/requestqueue/apikey/requests` |
| Shape | OpenAI-compatible | `{model, payload}` envelope |
| Auth | `Authorization: Bearer $GMI_API_KEY` | same |

Facts already established by hand on 2026-09-04 — do not rediscover them:

* Model id **must** carry the `MiniMaxAI/` prefix. Bare `MiniMax-M3` 404s with
  "No matching target server found".
* `reasoning_effort` is **ignored**. Reasoning is `thinking:{"type":"enabled"}`
  in the body.
* Voice clone is **synchronous**, 5–15s, minimum body `text` + `source_audio`.
* The noise/volume flags are `need_noise_reduction` and
  **`need_volumn_normalization`** — the typo is theirs and must be matched.
* Image input is inline `data:` base64; **no hosting needed**. `source_audio`
  is the opposite and **must** be a public URL.

**Retry:** one retry on 5xx and on a request-queue `failed` status. Never retry
a 4xx. Every call carries a context deadline.

---

---

## T2b — Live GMI endpoint verification  *(DONE — 2026-09-05, see t2b-t5b-live-record.md)*

**Owns:** `internal/gmi/text/live_test.go`, `internal/gmi/media/live_test.go`
— the `//go:build live` files in each package T2 owns. Nothing else.

**Depends on:** T2 (clients), plus a working `GMI_API_KEY`. **Unblocks:** T6,
T7, T8 — every track that makes a real call.

**Done when:** both endpoints are reached live and their responses decode into
the packages' typed structs — the half of T2's original `Done when` that no
agent could run while the operator key was rejected by both providers.

Closed 2026-09-05. Both endpoints reached, both error classifications confirmed
against real upstream responses. The probes are committed rather than pasted,
so they satisfy `AGENTS.md` §Definition of done item 4 (a cited check passes
from a clean tree):

```bash
set -a; . ./.env; set +a
go test -tags live -run Live -v ./internal/gmi/text/ ./internal/gmi/media/
```

**What it changed downstream.** The media package doc claimed TTS returns a
binary blob. It returns the request-queue envelope, with the audio at
`outcome.audio_url` on `storage.googleapis.com` — so **T8 downloads and
persists rather than receiving bytes**, which is exactly the expiring-URL case
§T3 anticipated. That URL is publicly fetchable with no credential (verified:
`200 audio/mpeg`, 63,348 bytes, real MP3), which hands **T13 a safety
question**: a cloned child voice would sit at an unauthenticated public URL on
GMI's bucket. `AGENTS.md` §Safety governs what *we* host, not what GMI does.

## T3 — Store and media  *(DONE — closed at round 2, see t3-round2.md)*

**Owns:** `internal/store/**`, `internal/mediastore/**`, plus the `/media/` route line in `newServer`.

**Depends on:** T0. **Done when:** a book survives a container restart and its
media still serves.

* **SQLite via `modernc.org/sqlite`** — pure Go, so `CGO_ENABLED=0` and
  distroless still hold. Proven in Mosaic.
* Tables: `books`, `pages`, `cast`, `interviews`, `media`.
* **Media on a Docker volume**, not in the database. Served with
  `http.ServeContent` so Range requests work — `<audio>` scrubbing depends on
  it, and getting this wrong means narration that cannot be sought.
* **Persist GMI output immediately.** Responses point at
  `storage.googleapis.com`; assume those URLs expire. Download on receipt.
* Media paths are unguessable (random id), so a book link is shareable without
  being enumerable.

---

## T3b — SSE broker, job runner, request-queue polling

**Owns:** `internal/stream/**`, `internal/job/**`, and the polling addition
inside `internal/gmi/media` (invariant 4's mandated home — one polling layer,
not one per consumer).

**Depends on:** T3. **Unblocks:** T4, T5, T6, T8, T9, T10 — the entire
non-blocking architecture (invariant 6: nothing blocks longer than 100
seconds).

Three seams the task graph assumed and nothing built (see §Unowned seams):

* **SSE broker** (`internal/stream`). Per project.md §Lift from Mosaic:
  subscribe/publish by topic, `: ping` heartbeat every 15s, correct SSE
  headers (`text/event-stream`, `Cache-Control: no-cache`,
  `X-Accel-Buffering: no`), flush per event, and detect client disconnect so
  topics don't leak writers.
* **Job runner** (`internal/job`). Start returns a job id immediately;
  progress events flow to the broker topic; the final result lands in the
  store (T3) so a page reload can catch up. The runner owns the goroutine and
  its exit path (AGENTS.md §Concurrency).
* **Request-queue polling** (in `internal/gmi/media`). The POST returns
  `{request_id, status}`; poll `queued`/`processing` to `completed` and hand
  back raw bytes; a `failed` terminal status surfaces through the existing
  `ErrTransient` classification (one internal retry already implemented by
  T2) and the whole poll respects the per-call context deadline.

**Done when:** an httptest SSE client
subscribes, a fake long job started through `internal/job` streams progress
events and a terminal event, the heartbeat is observed between events, a
second subscriber joining mid-job catches up from the store, and the polling
layer drives a fake request queue through `queued` → `processing` →
`completed` (plus `failed` → `ErrTransient` and a context deadline cutting the
poll short).

---


## T4 — The interview (Phase A)

**Owns:** `internal/interview/**`, plus its route lines in `newServer`.

The differentiator. One system prompt and a loop; resist making it more.

**Depends on:** T2, T3. **Done when:** a full interview completes, ends on its
own, and produces a transcript the structurer can read.

House style, enforced in the system prompt:

* **One question at a time.** Never a form.
* **Concrete, not abstract** — "what colour is the dragon?", not "describe the
  antagonist".
* **Offer a binary when the child stalls** — "is she brave, or is she sneaky?"
  Never an open re-ask. These render as tappable chips (T9), so the interview is
  completable almost entirely by tapping.
* **Accept everything.** No correcting spelling, logic or plausibility. The
  charm is the child's logic; catching it is the job, tidying it is not.
* **Stop.** Hold a checklist — hero, companion, want, obstacle, turn, ending —
  and close when it is filled or the child tires. ~6–10 exchanges. A stall or a
  repeated one-word answer ends it early.

**`thinking` is OFF here.** Short conversational questions; a child watching a
spinner is a usability failure, and usability is a third of the score.

Turns stream over the SSE channel — **which does not exist yet.** T1 shipped
deployment artifacts and `/healthz`, not a broker; see §Unowned seams. Do not
start T4 until that seam has an owner, or T4 will grow a private streaming
implementation that T9 and T10 then have to be rewritten around.

---

## T5 — Structuring (Phase B)

**Owns:** `internal/story/**` (the Phase-B schema and its validator).

One call over the whole transcript. M3's 1M context means no summarisation and
no state to marshal.

**Depends on:** T4. **Done when:** a transcript yields valid JSON that
validates against the schema, twice running against the scripted suite —
the same transcript through the extract-and-validate pipeline yields the
same valid story twice, with the corrective retry bounded
(`TestStructureDeterministicTwiceRunning` is that pin). The live half of
the original criterion — M3 with `thinking` ON returning schema-valid
JSON (8 pages, taught emotion vocabulary, consistent cast names) on two
real calls, and MiniMax accepting a `system`-role corrective message
mid-conversation — is **T5b**, an operator track on the T2b precedent: it
owns those live probes and runs once a working key exists, keeping this
criterion mechanically checkable from a clean tree.

```json
{ "title": "...",
  "cast":  [ { "name": "Mira",
               "visual": "a small girl, red raincoat, black bob haircut, round glasses",
               "voice": { "pitch": 0, "sound_effects": "" } } ],
  "pages": [ { "n": 1, "text": "...", "prompt": "...",
               "characters": ["Mira"], "emotion": "happy",
               "lines": [ { "character": "Mira", "text": "..." } ] } ] }
```

**`thinking` is ON here.** One call, quality matters, and a progress bar is
already showing.

Validate before use. A malformed structure must fail loudly into a retry, not
half-render a book.

---

---

## T5b — Live Phase-B verification  *(DONE — 2026-09-05, see t2b-t5b-live-record.md)*

**Owns:** `internal/story/live_test.go` — the `//go:build live` file in the
package T5 owns. Nothing else.

**Depends on:** T5 (structuring), plus a working `GMI_API_KEY`.
**Unblocks:** T6 (it consumes the `story.Story` this proves M3 actually
produces).

**Done when:** a real transcript yields schema-valid JSON twice running,
against the live model with `thinking` ON, and MiniMax is shown to accept a
`system`-role corrective message mid-conversation — the mechanism
`story.Structure`'s retry depends on.

```bash
set -a; . ./.env; set +a
go test -tags live -run Live -v ./internal/story/
```

Closed 2026-09-05. Two independent calls, both `Validate`-clean, both 8 pages,
emotions inside the taught vocabulary, every page character present in the
cast. The corrective-system-message acceptance holds.

**Two live cast shapes handed to T6.** Both validate, and both defeat a naive
"one reference image per cast member" loop:

* **Non-visual members.** Both runs emitted a `Narrator` — `visual: "no visual"`
  in one, `"an unseen storyteller with no appearance"` in the other. Two
  phrasings, so no literal-string match works; `Narrator` is not reservable
  either, since a child may legitimately name a character that. A per-member
  sheet spends ~$0.01 a book rendering nothing.
* **One entity in two cast slots.** `Grumpy River` and `Happy River`, the
  second's visual opening *"the same wide blue river"*. Unique names, so
  `Validate` accepts them; under the image lock they become two unrelated
  rivers — the exact drift T6 exists to prevent.

Neither is a T5 defect: the schema and validator do what they were specified to
do. Both are T6's to absorb.

## T6 — Illustration  *(DONE — closed 2026-09-05 after round-2 APPROVE 0/0/0/0, zero residue; t6-round1.md → t6-remediation-round1.md → t6-round2.md; live verification in T6b items 2–3)*

**Owns:** `internal/illustrate/**`.

**Depends on:** T5. **Done when:** eight pages render with a recognisably
constant cast.

The hard problem is **character consistency**, not image quality — 2D cartoon is
the easy style and every model does it. The book falls apart if Mira's hair
changes on page 4. Three locks:

1. **Verbatim text lock.** The cast bible's `visual` string is pasted into every
   page prompt unchanged. Never paraphrased, never regenerated.
2. **Image lock.** One reference image per cast member first, then every page as
   **image-to-image** against it.
3. **Style lock.** One constant suffix on every prompt — *"flat 2D children's
   picture book illustration, thick outlines, gouache texture, soft palette"*.

**Forbidden model ids.** These are wrong answers, not merely suboptimal ones,
and an agent that reaches for one gets a silently-degraded book rather than an
error:

| Model id | Why it is forbidden | Where it bites |
| --- | --- | --- |
| `Flux2-Klein`, `Z-Image` | **They accept and then never generate.** `status:"queued"` forever — measured 2026-09-05 over ~72s, reproduced in the GMI console. Worse than a 404, which would fail on the first call: this hangs every page to its deadline and reads as a network fault. | Everything. These were §T6's *chosen* models. |
| `Qwen-Image-2512` | **t2i only — it cannot take a reference image.** An i2i call against it succeeds and ignores the reference. | The image lock, i.e. the whole of §T6 |
| `H3` / any video model | Not free; explicitly out of scope (project.md §Scope) | Budget |

**Use `seedream-5.0-lite`.** Measured working 2026-09-05; full record in
`adversarial-review/t6b-live-record.md`. The production call shape:

```json
{"model":"seedream-5.0-lite","payload":{
  "prompt":"…visual verbatim… + style suffix…",
  "image":["https://…reference sheet URL…"],
  "size":"1792x2240","output_format":"jpeg",
  "max_images":1,"watermark":false}}
```

* **Synchronous.** `status:"success"` comes back on the POST, ~14s. No polling.
* **Read the result at `outcome.media_urls[].url`** — an array of **objects**
  `{"id","url"}`, with `outcome.thumbnail_image_url` sitting beside it. Read it
  **by name**. Do not walk the body looking for something media-shaped: that is
  round-1 **H1**, and the thumbnail alone is enough to break a walker.
* **`payload.image` is an array of URLs, not inline base64.** Chain GMI's own
  public output URL from the reference sheet — no hosting needed mid-generation.
* **`size` is pinned to `1792x2240`, not the `2K` preset.** Both give the same
  pixels, but the preset infers its shape from prose in the prompt, so a prompt
  edit silently changes a page's aspect ratio — and eight pages must be
  identical or the two-page spread has mismatched heights. There is a hard
  pixel floor of 3,686,400, so a phone-sized image cannot be requested at all.
* **`output_format: "jpeg"`** — 342 KB against 4.4 MB for the same-size PNG.
  Decisive because T7 inlines the reference sheet as base64 to M3.

**Contract change (landed in round-1 remediation, APPROVE'd round 2).**
`media.EditImage` now takes `refImages []string` — an array of reference
URLs — plus named `ImageOptions`; seedream needs URLs, not the inline base64
the old signature carried. T2 was closed, so the change ran as a sanctioned
contract row of T6's own remediation round rather than a fix in passing.

`Qwen-Image-2512` is not a hypothetical: it shipped as the default for both
`GenerateImage` and `EditImage` in T2 and survived two review rounds
(t2-round3.md, H1/M2). Grep for it before closing any track that renders.

**Provider behind a one-line switch.** `DefaultModel` is `seedream-5.0-lite`,
the one image model measured generating on the live queue (T6b item 1;
t6b-live-record.md). If characters drift, switch to `gemini-2.5-flash-image` —
the spread across the whole catalog is about 23 cents a book, so choose on
consistency and never on price. `Flux2-Klein` and `Z-Image` sit on the
forbidden table above, and `Qwen-Image-2512` with them: t2i only, it cannot
take the reference.

Fan out with `errgroup`, bounded to ~4 concurrent.

---

---

## T6b — Live illustration verification

**Owns:** `internal/illustrate/live_test.go` — the `//go:build live` file in
the package T6 owns (§Architectural invariants 9). Nothing else.

**Depends on:** T6 (implementation). **Unblocks:** T6's remediation round —
see the ordering note below. **Runs paid calls deliberately.**

**Done when:**

1. **The terminal request-queue record for an image call is captured and its
   shape written down.** One `EditImage` call, ~$0.01. This settles T6
   round-1 **H1**: does the image queue echo `payload` back inside its result
   the way the audio queue demonstrably does? If it does, H1 is Critical and
   the decoder must key on `outcome`; if it does not, H1 stays High and the
   fix is narrower. Either way the answer costs one cent and removes all
   guesswork from remediation.
2. **Eight pages render with a recognisably constant cast** — T6's original
   `Done when`, which is not mechanically checkable from a clean tree and is
   why this track exists (the T1b / T2b / T5b precedent).
3. **The render → persist → serve path works end to end**: an illustration is
   rendered, persisted by T7's writer, and fetched back through the
   `/media/{id}` route with a correct content type.

### Ordering — this track inverts the usual b-track rule

T2b and T5b ran *after* their parents closed, verifying finished work. **T6b's
item 1 runs first, before T6 remediation**, because it is the evidence
remediation needs: fixing a decoder against a guessed response shape is how H1
happened in the first place. Items 2 and 3 wait until T6 closes — an eight-page
run while H1 is open would render eight copies of a character sheet and teach
nothing.

**Cost.** Item 1 is one image. Item 2 is ~10. At ~$0.01 each that is about a
tenth of one book — see project.md §3, where the column is per *book*, not per
image. Operator policy (2026-09-05) is to spend it: proving the shape now is
cheaper than building two tracks on a wrong assumption.

## T7 — Consistency verification  *(DONE — closed 2026-09-05 at fa3a516; t7-round1.md → t7-remediation-round1.md → t7-round2.md, round-2 APPROVE 0/0/0/0 zero residue; live tail is T6b item 3)*

**Owns:** `internal/illustrate/verify.go` and `internal/illustrate/persist.go`
(same package as T6 — it is T6's closing loop, not a separate seam), plus the
`internal/mediastore` / `internal/store` writes those make.

**Depends on:** T6. **Done when:** a deliberately drifted page is caught and
regenerated.

M3 has native image input and **it works on GMI — verified live 2026-09-04.** A
single image was described correctly on all four synthetic features; two images
in one message were compared and returned clean JSON
(`{"match":false,"inner_shape_1":"circle","inner_shape_2":"square",...}`) at 471
prompt tokens.

So: after each page renders, send M3 the reference sheet **and** the new page in
one message, ask for a match verdict, regenerate on `false`. **Cap at 2 retries**
so a stubborn page cannot spin.

This is worth real points — it uses multimodality *as input* in the
Multimodality track, and answers criterion 1's "how far you pushed the model"
with something other than call volume.

### The blind spot this check does NOT cover

**A page that is byte-identical to its reference sheet passes this test
perfectly.** T6 round-1 H1 is precisely that failure: the decoder picks the
request-queue's *echoed* `payload.image` — which is the reference sheet
`EditImage` inlined — instead of the real result, so every page comes back as
the character sheet. Ask M3 "does this page match the reference?" and the
answer is an emphatic yes, on all eight pages, and the book of eight identical
portraits ships green.

So T7 must assert the negative as well as the positive:

* **The page must not be near-identical to the reference it was locked to.**
  Cheapest form is a byte/hash comparison against the reference bytes, which
  catches the exact-echo case for free and needs no model call. A perceptual
  or size-delta check catches the near-echo case.
* A match verdict of `true` on a page that *is* the reference is a **decode
  defect, not a consistency success** — surface it as its own error rather than
  folding it into the regenerate path, because regenerating will produce the
  same echo again and burn the 2-retry cap on every page.

Consistency verification is not a safety net for decode bugs unless it is
written to be one.

### Persistence lands here, not in T6

T6 deliberately returns bytes and writes nothing (its round-1 notes argue this,
and the round-1 review ruled the reasoning sound but the write **unassigned**).
It belongs to T7 for a reason that dissolves the original difficulty:

**A page becomes final when T7 approves it, not when T6 renders it.** Persist
after the verdict and it is one write. Persist before, and every regeneration
is a delete/insert against `SetMediaPlace`'s one-illustration-per-page unique
slot — the churn T6 was right to avoid. Same for the reference sheets: they are
final as soon as they render, so they persist immediately.

T6 already left the seam cheap: content types are inside `mediastore`'s image
set, stage names equal `store.MediaKind` values, and `Illustration.Reference` /
`Book.Skipped` carry what `MediaPlace` needs.

**Download on receipt.** If a render arrives as a URL rather than as bytes
(T2b proved the queue returns `outcome.audio_url` for audio; T6b settles
whether images do the same), fetch it before persisting — those URLs point at
`storage.googleapis.com` and are assumed to expire (§T3).

---

## T8 — Audio

**Owns:** `internal/audio/**`.

**Depends on:** T2, T5. **Done when:** questions speak, and a finished book
reads itself.

* **Questions:** `minimax-tts-speech-2.8-turbo`. Latency beats fidelity.
  Text streams over SSE immediately and the TTS call fires in parallel — **text
  never waits on audio.**
* **Narration:** `minimax-tts-speech-2.8-hd`, per-page `emotion` from T5.
* Default `voice_id`: `English_expressive_narrator`.

**`SynthesizeSpeech` does NOT return audio bytes — read this before writing a
line of T8.** Verified live 2026-09-05 (T2b). The request queue returns its own
envelope, and the audio is a URL inside it:

```json
{"request_id":"…","model":"minimax-tts-speech-2.8-hd","status":"success",
 "payload":{ … the request, echoed back … },
 "outcome":{"audio_url":"https://storage.googleapis.com/…/….mp3",
            "format":"mp3","status":"success"}}
```

* **Download `outcome.audio_url` and persist on receipt.** Those URLs point at
  `storage.googleapis.com` and are assumed to expire (§T3). Confirmed
  publicly fetchable with no credential: `200 audio/mpeg`, 63,348 bytes, a real
  128 kbps MP3.
* **The envelope echoes the request back in `payload`.** That echo is what
  produced T6's round-1 H1 — a decoder scanning the whole body for content
  found the echoed input before the real output. Key on `outcome`, not on "the
  first thing in the body that looks like media".
* One call took ~24s, so it goes through T3b's polling. Budget for that in the
  questions path, where latency is the whole point.

The same shape almost certainly applies to **T12's music call** — it is the
same request queue. Do not assume; check the terminal record once, cheaply.
* **Mobile autoplay is blocked.** iOS Safari refuses audio not triggered by a
  gesture, so spoken questions die silently on an iPad. One **"Tap to start"**
  gesture on entry unlocks an audio element; reuse that element for every later
  clip. Without this the headline feature does not work on the primary device.
  **This now applies to the interview only.** Since T10's reshape (2026-09-05)
  the narration does not play in the browser at all — it is muxed into the
  book MP4, where the native controls are themselves the gesture. What T8 owes
  T10 is one persisted clip per page, in page order, nothing more.

---

## The flow — screen by screen

*Written 2026-09-05, on the operator's ruling. Until now the plan had
principles (§T9) and components (§T9, §T10, §T11, §T13) but no sequence
connecting them, and no document stated how long the child waits.*

**The number that drives this section:** post-interview generation is **about
six minutes**, best case, before any regeneration — 236 s for images measured
live (T6b item 2), plus T7's judge pass, plus eight narration calls at ~24 s
fanned out, plus structuring and the video render. Every screen below exists
to make that six minutes survivable.

| # | Screen | What it is | Owns |
| --- | --- | --- | --- |
| 1 | **Shelf** | Two or three prewarmed books, and one big *Make your own book*. Cost control (§T11 item 2) and the judge's default landing | T9 + T11 |
| 2 | *(no screen)* | **The CTA on screen 1 *is* the "Tap to start" gesture.** It unlocks the `Audio` element §T8 needs and costs no extra tap and no extra screen | T9 |
| 3 | **Interview** | Chips, text box as the escape hatch. Questions stream over SSE and speak; text never waits on audio | T9 + T4/T8 |
| 4 | **The grown-up step** | Interview ends → *"a grown-up can add a voice"* → record / upload / skip. **One screen, skippable, exactly once** | T13 |
| 5 | **The wait** | The race (below) plus pages landing as they are approved. ~6 minutes | T9 |
| 6 | **The book** | Video plays, download, shareable URL, cold-open works without JS | T10b |

### Screen 3 — question zero: whose book is this?

**The interview opens by asking the child's name** (operator, 2026-09-05), and
it is the byline on §T10's title card: *"a book by Mira"*.

**It is not a checklist slot and it is not in the prompt.** The checklist is
six **story** slots — hero, companion, want, obstacle, turn, ending
(`internal/interview/reply.go:13`) — taught in
`internal/interview/prompt.go` in three places, and T4 closed at
APPROVE 0/0/0/0. A seventh slot would reopen a closed track to collect
something that is not a story element, and it would put a count that is
currently one constant into several rules — the §T5 `PageCount` lesson.

So **question zero is asked by the UI, answered by the child, and stored on the
book. M3 never sees it**, `filled` stays six slots, and `prompt.go` is not
touched. It is also the warmest possible opening — *"whose book are we
making?"* — and the one moment where an adult is most likely still holding the
device, which is what makes it the right place for the product's only
unavoidable piece of typing. Blank is a normal case: the title card drops the
line, not the card.

**One consequence to handle before T9 starts, not during.** `books` is
`(id, title, created_at)` (`internal/store/store.go:153`) — there is nowhere to
put a byline. It needs `byline TEXT NOT NULL DEFAULT ''`, which is a **declared
contract change against closed T3**, in the shape T7's C1–C6 rows already set:
name the file, the line and the reason in the track's review file rather than
reaching in quietly. Add it to the `CREATE TABLE books` statement rather than
appending an `ALTER TABLE` — `migrate` applies `schema` in order and leans on
`IF NOT EXISTS` for idempotency, which `ADD COLUMN` does not have. That means
**deleting any existing dev database**, which costs nothing: there is no
production data, and the box's generated media is disposable until judging
opens.

### Screen 4 — voice capture sits at the interview's end (decided)

Not before the interview: an adult-facing detour in front of every child's
first run. Not after the book: that means re-synthesising narration and
re-rendering the video for a book that was already "done". At the end of the
interview, narration has not started yet, so the clone is ready exactly when
§T8 needs it and **nothing regenerates**. The cost is that it lands at the
moment the child most wants the book to begin — so it is one screen, one
sentence, and *skip* is as large as the other two buttons.

### Screen 5 — the wait is a race, not a spinner

§T9's rule is *no naked spinners — every wait is a character doing something*.
The wait is the longest screen in the product, so it gets the most character:
**a field of SVG animals running a race**, one lane each.

**The race is the progress bar.** The track is divided into **eight markers,
one per page**; the pack advances one marker each time a page is approved, and
the per-animal jitter on top is cosmetic. This keeps §T10's *"the book filling
up is the progress bar — no percentage"* rule while giving a four-year-old
something to watch for six minutes. A decorative loop would not have earned
its place; a loop that encodes real state does.

* **Prototyped 2026-09-05** — mechanism confirmed at ~60 lines of CSS and JS,
  desktop and 390 px. Body bob plus two leg pairs rotating out of phase; **no
  sprite sheet, no animation library, no build step.** Per-animal stride
  duration is jittered so nobody runs in lockstep.
* **The cost is the art, not the motion.** The prototype's five animals are
  ellipses and rounded rects; they read as animals but the silhouettes are too
  alike and the ears barely register at 64 px. Budget the time there, not on
  the mechanism, which is done.
* Animate `transform` only, and honour `prefers-reduced-motion` — under it the
  animals hold position and the markers still light.
* **It is genuinely separable.** No backend, no SSE, no data: it takes one
  number (pages approved) and renders. It can be built and reviewed in a
  browser on its own, ahead of or behind anything else in T9.

### The wire contracts these screens need

*Added 2026-09-05 after an outside review of the T9 spec. Every item below was
checked against the code, and each one is a place where T9's implementer would
otherwise have had to guess across a closed track.*

**1. Question zero reaches the book through `POST /interviews`.**
`h.start` takes no request body and creates the book with `WorkingTitle`
(`internal/interview/http.go:74-75`, `interview.go:157`), and no route writes
`books.byline`. So the column added at `847e9b6` is currently unreachable.
**Contract:** `POST /interviews` accepts an optional JSON body
`{"byline": "Mira"}`; the name lands on the book row **before** the opening
turn fires, and absent or empty is a normal case. Not a separate endpoint —
the UI already has to make this call, and a second round-trip buys nothing.

**2. Question audio is a second SSE event, never a field on `questionEvent`.**
`audio.SynthesizeQuestion` returns **bytes** and touches no store by design —
*"the question path needs neither"* (`internal/audio/audio.go:380`, `:326`).
Bundling an `audio_url` into `questionEvent` would therefore both name a URL
that does not exist **and** hold the text until the TTS round-trip finished,
which is exactly *text waiting on audio*.

**Contract:** two events on the existing `/interviews/{id}/events` stream.

```
event: question         → {"turn":1,"text":"…","chips":[…]}   fires immediately
event: question_audio   → {"turn":1,"audio_url":"/media/<id>"} fires when it lands
```

The second may **never arrive** — TTS failed, or was slow past the turn — and
the UI must treat that as normal and stay silent, not spin. This makes question
audio persist to `mediastore` like narration does, so the URL is real and
serves over the proven `GET /media/{id}` with Range. **That is a contract
change against T8, which is mid-remediation: it belongs in T8's next round
file, not in a quiet edit.**

**3. Something has to start the six minutes — see T10c.** `closeTurn`
publishes `ended` and returns (`internal/interview/turn.go:226-239`). Nothing
in the repo calls `story.Structure`, `illustrate.Illustrate` or
`internal/bookvideo` outside their own tests. Screen 5 has no stream to
subscribe to and no job to watch until **T10c** exists.

**4. Human pages are singular; the API stays plural.** `GET /interviews/{id}`
already serves JSON (`cmd/thutapi/main.go:154`), and Go's `ServeMux` matches
patterns literally — two handlers cannot share that pattern.

| Purpose | Path |
| --- | --- |
| Shelf (screen 1) | `GET /` |
| Interview page (screens 3–5) | `GET /interview/{id}` |
| Book page (screen 6) | `GET /book/{id}` |
| API, unchanged | `POST/GET /interviews…`, `GET /media/{id}` |

**Do not content-negotiate on `Accept`.** It cannot be expressed in the mux, it
makes a cold link's behaviour depend on a header the sharer never sees, and it
is the kind of thing that works in `curl` and fails in a messaging app's link
preview.

**5. The audio unlock can be missed, and must fail soft.** Screen 1's CTA is
the gesture — but a refresh mid-interview, or a shared `/interview/{id}` link
opened cold, never passes through it, and iOS Safari then throws
`NotAllowedError` on the first `play()`. **Contract:** when the element is not
unlocked, render a small *"tap to listen"* speaker chip beside the question;
the first tap unlocks the element and plays. Never a silent failure, never a
modal.

**6. Screen 4 degrades on the device, not on the cut list.** T12 and T13 both
ship — **nothing is cut** (operator, 2026-09-05). What screen 4 must survive is
a *device* that cannot record: no `MediaRecorder`, no secure context, or a
denied microphone permission. In each case the record button is absent or
disabled with a warm line, **upload and skip remain**, and the flow continues.
`getUserMedia` needs a secure context, so this is not hypothetical — it is what
a phone hitting `http://192.168.x.x:8080` during §T9 testing will do.

### Screen 5's other state — the failure path (decided)

T6 and T7 return a **zero Book on any error**, so a failure is total, not
partial: there is no five-page book to show. §T9's *no failure text, never a
dead end* is a tone rule, and this is the behaviour behind it.

**Decided 2026-09-05: the animals stop and sit down, one warm line, and two
equally-sized ways forward — *try again* and *look at other books*.**

* **No automatic retry.** A regeneration is another ~$0.35 and another six
  minutes, and after 2026-09-06 it bills at standard rates. An auto-retry on a
  public URL is an open wallet, which is the exact thing §T11 item 1 exists to
  prevent. A retry is a tap, and it spends a §T11 gate token like any other
  generation.
* **Two doors is what "never a dead end" means here** — one forward, one
  sideways to the shelf. A single "try again" on a failing pipeline is a dead
  end with extra steps.
* **No error codes, no classes, no prose about what broke** (AGENTS.md pin).
  The operator gets the detail in `log/slog`; the child gets a sentence.

### The rest of screen 5, decided

* **Layout: the race is a band pinned at the bottom, pages stack above it.**
  Portrait phone is the primary device and vertical space is the scarce
  resource; the pages are the payload and get the room, the race is the engine
  and gets a strip.
* **The wait is silent.** No music bed, no narration-as-it-lands. The bed is
  muxed into the film (§T12) and the book is where the sound lives — six
  minutes of loop under a wait is worse than quiet, and it buys a second
  autoplay problem for nothing.
* **The interview's own micro-waits reuse the race.** M3 takes seconds per
  turn, and §T9's no-spinner rule applies there too: **one animal from the
  race, trotting in place**, is the thinking indicator. One component, two
  screens, one visual language.

---

## T8b — The emotion key, settled live

**Owns:** nothing under `internal/` — an operator run, plus its record
`dev-diary/adversarial-review/t8b-live-record.md`.

**Depends on:** T8 (done, `8b7066b`) and on the GMI TTS pool having capacity.
**Done when:** `TestLiveSynthesizeSpeech_EmotionKeyEchoed` passes against
production and the two asserted-but-unverified doc notes flip
(`internal/audio/audio.go:46-56`, `internal/gmi/media/client.go:300-310`).

**Recorded 2026-09-05.** T8's own commit calls this "a recorded T8b
obligation" — and no T8b row existed anywhere in this plan until now. The
obligation was real; the place it pointed at was not.

### Why it cannot be skipped: the failure is silent

T5 gives every page an `emotion`, and T8 sends it as a **top-level key** on the
`minimax-tts-speech-2.8-hd` payload. That placement and the in-set vocabulary
are pinned on the wire by contract row C1 — but **never confirmed against the
upstream**. The TTS pool answered every settlement call on the evening of
2026-09-05 with `HTTP 503 "Upstream capacity temporarily exhausted"`, so no
live echo of an emotion-carrying call exists.

If the upstream ignores or rejects the key, **narration renders emotion-less
with every check green** (t8-round2.md H1, the silent-ignore class). Nothing
fails, no test reddens, the book still reads itself — just flatly, in one
register, for all eight pages. That is precisely the kind of defect this
project has been bitten by before: T6's H1 was an unchecked assumption about
a payload shape, and it took a live call to find.

It also touches the entry's pitch. Per-page emotion is part of the claim that
the models were *pushed*, not merely called (§T14, track Multimodality). A
demo whose narration is uniformly flat quietly gives that up.

### The run

```console
$ set -a; . ./.env; set +a
$ go test -tags live -run TestLiveSynthesizeSpeech_EmotionKeyEchoed -v -count=1 ./internal/gmi/media/
```

The probe is committed and mechanically runnable — this track is a **retry on
someone else's capacity**, not new work. Retry it whenever the pool recovers;
it is one short TTS call and costs approximately nothing (T2b's TTS probes
billed zero).

**Three outcomes, and only one needs a track after it:**

| Result | What it means | Next |
| --- | --- | --- |
| Key echoed in `payload` | Placement and vocabulary confirmed | Flip both doc notes; T8b closes |
| Key absent from the echo | Upstream silently drops it — the H1 failure, live | A media contract change: find the right placement, as T2b did for `outcome.audio_url` |
| Call rejects the key | Loud, and therefore the good case | Same, but the upstream tells us why |

**If the pool never recovers before the deadline**, the honest close is to
record the 503s and ship with the notes standing as written — narration works
either way, and it is the expressiveness, not the audio, that is at risk. Say
so in the record rather than letting an unverified pin read as a verified one.

---

## T9 — Frontend shell and the interview UI

**Owns:** `static/**` (including `static/vendor/`), `internal/web/**` templates, plus its route lines in `newServer`.

**Depends on:** T4, T8. **Done when:** an interview is completable by tapping,
on a real phone.

**Preact + `htm` + hooks, vendored into `static/vendor/`, no build step.** Not
Svelte (its 5-rune idiom is thin in training data and fails silently), not
vanilla (hand-rolled DOM fails the same silent way). Hooks, not signals.
Pinned copies committed — **no CDN**, so a judge's `docker run` has no network
surprise.

Go renders the shell with `html/template`, so a cold link works without JS.

Child-facing rules:

* **Tappable chips**, text box as the escape hatch. Typing is the friction point.
* **~60px targets**, generously spaced. Fine motor control is poor.
* **No naked spinners** — every wait is a character doing something. The six-minute generation wait is **the race** (§The flow, screen 5): SVG animals, one lane each, the pack advancing one marker per approved page. Prototyped; the mechanism is ~60 lines and the remaining cost is the sprites.
* **Chunky rounded type** (Fredoka, Baloo 2), warm palette.
* **No failure text.** Errors are warm and never a dead end.
* **Adult corner.** Voice-sample capture and settings sit out of the child's
  flow; a young child is likely operating this with a parent.

Responsive, **tablet-first**:

* `100dvh` not `100vh`; `env(safe-area-inset-bottom)` on the pinned input;
  scroll input into view on focus. **Chrome devtools does not reproduce the
  mobile keyboard — test on a real phone.**
* `touch-action: manipulation` on buttons.
* Plain CSS and media queries. **No Tailwind** — it reintroduces the build step.

---

## T9a — The race

**Owns:** `static/race/**`.

**Depends on:** nothing. **Done when:** the race renders on a phone and a
laptop, advances one marker per approved page, holds still under
`prefers-reduced-motion`, and its five animals are distinguishable at a glance.

A subtask of T9, given its own number because it has its own boundary and can
be built and reviewed before the shell exists. It is screen 5's waiting state
and, trotting in place, the interview's per-turn thinking indicator (§The
flow).

**The interface is one number: `--done` on `.race`.** No backend, no SSE, no
store, no model call — and, decisively, **no DOM construction**. Markup and
CSS only: positions, per-lane jitter and the lit markers all derive from that
one custom property.

That constraint is what actually makes the track independent, and it is not
cosmetic:

* **Preact is not available yet.** `static/vendor/` is T9's `Owns` and T9 has
  not started, so a component written against Preact cannot be built now.
* **Hand-rolled DOM is forbidden** — AGENTS.md:149 rules out vanilla JS by
  name, *"same silent DOM-sync class of bug"*. So "just write it in plain JS
  for now" is not the escape hatch it looks like.
* CSS-from-one-number dodges both. When T9 exists the Preact component is one
  line — ``style=${`--done:${n}`}`` — and nothing inside the race changes.
* It also arrives **server-renderable with JS switched off**, which is
  AGENTS.md's *"a cold link works without JS"* for free.

Every colour is a `var()` so T9's palette drops in without touching this
track.

**Start from `dev-diary/prototypes/race.html`**, committed 2026-09-05. It is a
standalone page that already runs at 900 px and 390 px: eight markers, five
animals, per-lane jitter, the pack positioned from `--done` alone. The
mechanism is settled — static markup plus one stylesheet, no sprite sheet, no
animation library, no build step (§T0's *no Node in the build* holds). The one
`<script>` in it is a demo button and is labelled as scaffolding that ships
nowhere.

**The work that is actually left is the sprites.** In the prototype the animals
are ellipses and rounded rects; they read as animals, but the silhouettes are
too alike and the ears vanish at 64 px. Five clearly-different animals at a
glance, in the warm palette, is the deliverable — the motion is done.

* **Animate `transform` only.** Nothing that triggers layout, on a screen that
  holds for six minutes on a phone.
* **`prefers-reduced-motion`**: animals hold position, markers still light. The
  progress information must survive the motion being switched off.
* **The markers are the progress bar** (§T10: no percentage). Eight of them,
  one per page. If §Decisions row 8 ever cuts the book to six pages, this is
  one constant.
* **Five lanes is a guess, not a pin.** Enough for a race, few enough to read
  on a 390 px screen. Change it if it looks thin.
* Keep it in one file with its SVGs inline. It has no dependencies and should
  not acquire any — that is the whole point of the track.

---

## T10 — The book: one MP4, played and downloadable

**Owns:** `internal/bookvideo/**`, the book template under `internal/web/**`,
`static/book/**`, and the ffmpeg lines in the `Dockerfile`.

**Depends on:** T6, T8, T9 — **and on T7, which owns the illustration write.**
T6 renders bytes and persists nothing by design; a book cannot be muxed until
T7's writer has put the images on disk and in `store`. **Done when:** a
finished book plays and downloads on a phone and on a laptop, and its URL opens
cold.

### The decision — MP4, not flipbook (operator, 2026-09-05)

**The finished book is a video file.** `ffmpeg` stitches each persisted page
image to its own narration clip; the browser gets one `<video controls
playsinline>` and a download link. This **supersedes project.md §"The book is a
layout fork"** — the two-page spread, swipe-to-turn, arrow keys and click zones
are cut by decision, not by the clock.

Why the trade is one-sided on a Sunday:

* **It deletes the most expensive UI on the board.** Flipbook = swipe gestures,
  a portrait/landscape layout fork, page-turn state, prefetch, and per-page
  `<audio>` sequencing kept in sync with the page the reader is actually on —
  all of it needing a **real phone** to test (§T9), on the compressed day.
  A `<video>` element is none of that, and its controls, scrubbing, fullscreen
  and background-audio behaviour are the platform's problem, not ours.
* **It collapses the mobile-autoplay hazard** (§T8). Native controls *are* the
  gesture; there is no unlock-an-element-on-first-tap trick to get wrong, and
  no way for narration to desync from the page, because there are no longer two
  things to sync.
* **The artifact is the deliverable.** T14 must produce a ≤3-minute demo video
  and **an X post** — a shareable MP4 of the book is exactly what those want,
  already rendered, instead of a screen-capture of someone hand-turning pages
  while audio plays.
* **It makes "picture and sound as a single output" literal**, which is the
  Multimodality track's own framing.
* **T12's music bed gets cheaper, not harder** — one `amix` at ~0.15 under the
  narration inside the render, instead of a looping `<audio>` racing the page.

**What is kept.** *The book filling up is still the progress bar* (§T10's
original first bullet). Pages arrive over SSE as they are approved and land as
a growing strip of `<img>`s — appending an image per event is a handful of
lines, none of the flipbook's cost — and the `<video>` replaces the strip when
the render finishes. The server-rendered cold-open URL is also kept: the book
page renders from `store` without JS, with the MP4 and the page images in the
markup.

### The recipe, verified 2026-09-05 on the real T6b renders

**Full record: `adversarial-review/t10-video-record.md`** — commands,
transcripts, a six-row *what is verified* table and a six-row *what is NOT*
table. Cost $0.00; every input was already on disk.

Proven end to end against `data/live/t6b-book/` (8 pages, 1792×2240 JPEG) with
stand-in clips of varying length: **49.2 s output, 2.6 MB, 13.3 s wall** to
encode and concat on the workstation, and **the same chain re-run inside the
shipping image** under ffmpeg 7.1 as uid 65532.

**What that record does not cover, and this track therefore still owns:** real
Speech 2.8 narration (the clips were generated tones), serving the file
(`Content-Disposition`, Range for a scrubbing `<video>`), playback on an actual
phone, the Fredoka face in place of the stand-in font, pinning the ffmpeg base
image by digest, and wall time on foleyflow rather than the workstation.

Per page — one segment, **audio-driven, with no duration arithmetic anywhere**:

```
ffmpeg -loop 1 -i page-NN.jpg -i narration-NN.mp3 \
  -vf "scale=1080:1350:force_original_aspect_ratio=decrease,\
pad=1080:1350:(ow-iw)/2:(oh-ih)/2:color=0x1b1614,setsar=1,format=yuv420p" \
  -r 25 -c:v libx264 -preset veryfast -crf 20 \
  -c:a aac -b:a 128k -ar 44100 -ac 2 -shortest -movflags +faststart seg-NN.mp4
```

then `-f concat -safe 0 -i list.txt -c copy -movflags +faststart book.mp4`.

* **`-shortest` is the whole timing model.** The page holds exactly as long as
  its own narration; nothing calls `ffprobe`, nothing sums durations, and a
  re-narrated page cannot drift.
* **The scale/pad/setsar/format chain is not optional.** libx264 needs even
  dimensions and the concat demuxer needs every segment to agree on geometry,
  SAR, pixel format, frame rate and audio layout — `-c copy` concat is what
  keeps the join free, and it only stays free if the segments match.
* **`+faststart`** so the browser can start playing before the file is down.
* 1080×1350 (4:5) matches the pages' own aspect and is the right portrait shape
  for a phone and for X. Do not letterbox to 16:9.
* A ≤0.5 s `xfade` between pages is the one nice-to-have worth the time.
  **Ken Burns (`zoompan`) is not** — a picture book wants the page held.

### The file leaves the site — build it to stand alone

Downloading is not the end of the artifact's life: a parent uploads it to
YouTube, sends it to a grandparent, posts it. Everything below is cheap and all
of it was **built and verified 2026-09-05** (`title card + end card + tags`:
55.7 s, 2.7 MB).

* **A title card and an end card, not a nice-to-have.** A bare sequence of
  pages arriving on someone else's timeline has no title, no author and no
  attribution. Title card = page 1 through `boxblur=18:2,eq=brightness=-0.22`
  with the story title and the child's byline over it (no design assets
  needed); end card = flat `0x1b1614` with "made with Thutapi" and the domain.
  Both are ordinary segments built to the same geometry, so they concat with
  `-c copy` like any page. The static build has freetype/fontconfig — point
  `fontfile` at the Fredoka TTF T9 vendors. **The byline is the child's answer
  to question zero** (§The flow screen 3, decision 14); left blank it drops the
  line, not the card.
* **Text goes in via `textfile=`, never `text=`.** The title is model output
  and a child's own words: apostrophes, colons, commas and backslashes are all
  live ammunition in a filtergraph. Write the string to a file under `/data`
  and point `drawtext` at it — that is the same rule as "never interpolate
  model output into an argument", applied where it actually bites.
* **Set the MP4 metadata** — `-metadata title=` / `artist=Thutapi` /
  `comment=` with the URL. It is what YouTube and every player read.
* **Serve it under the story's name**, via `Content-Disposition: attachment;
  filename=`, slugified — `Mira-and-Brambles-Long-Day.mp4`, not `book.mp4`.
* **Aspect: stay at 4:5.** Vertical and ~90 s means YouTube will most likely
  ingest it as a Short, which is the right shape for how this gets shared
  (worth one check on the day, not a claim). If a landscape master is ever
  wanted it is a second render off the same segments, not a redesign.

### ffmpeg in the image — verified, and the one real cost

The runtime is distroless (no shell, no apt, `nonroot`). A **static** ffmpeg
copied in runs there as uid 65532 — built and executed 2026-09-05:

```dockerfile
FROM mwader/static-ffmpeg:7.1 AS ff
...
COPY --from=ff /ffmpeg /usr/local/bin/ffmpeg
```

`ffmpeg version 7.1` reports in, with libx264/aac/freetype/fontconfig present
— and the **whole chain** (title card with `drawtext`+`textfile`, a page
segment, `concat -c copy` with `-metadata`) was then executed inside that image
as uid 65532, closing the version-skew question against the workstation's
n9.0.1.

* **Cost: image grows 33.8 MB → 222 MB.** Deploy pull time on the box; nothing
  else. 23 G free (§T11 item 4).
* `CGO_ENABLED=0` is untouched — ffmpeg is a subprocess, not a link-time
  dependency, so the pure-Go/distroless posture in §T0 survives intact.
* **Invoke it by absolute path with an argument slice, never through a shell**
  (there is no shell), and never interpolate a story title, cast name or any
  other model output into an argument. Write scratch under the `/data` volume,
  not `/tmp`.
* **The "no Node in the build" invariant (§T0) is untouched.** That rule bars a
  build step that generates assets; this is one `COPY --from` of a prebuilt
  binary, and nothing is compiled or regenerated at image-build time.
* A judge's `docker run` gains no network dependency — the binary is baked in.
* Tests that shell out must `t.Skip` when the binary is absent, in the shape
  `live_test.go` already uses.

---

## T10c — The generation pipeline

**Owns:** `internal/bookgen/**` and its route lines in `newServer`.

**Depends on:** T5, T6, T7, T8, T10a — every stage it drives. **Done when:** a
closed interview becomes a finished, playable book with no human step in the
middle, and screen 5 can watch it happen.

**Assigned 2026-09-05, on an outside review of the T9 spec.** Until then this
was an unowned seam of the most dangerous kind: *every stage was built and
APPROVE'd, and nothing joined them.* `closeTurn` publishes `ended` and returns
(`internal/interview/turn.go:226-239`), and grep finds **no caller of
`story.Structure`, `illustrate.Illustrate` or `internal/bookvideo` outside
their own packages and tests**. The task graph's arrows were real as
dependencies and imaginary as code.

### It is triggered, not automatic — and that follows from screen 4

```
POST /interviews/{id}/generate   →  job id, then events on the book's topic
```

Generation must **not** fire on `ended`, because §The flow puts the grown-up
voice step *between* the interview ending and generation starting, and a clone
has to exist before narration runs. An automatic trigger would either race
screen 4 or force it earlier. So the UI calls this once screen 4 is done or
skipped, and the route is also the natural place for §T11's gate to sit — it is
the one route in the product that spends money.

**A second POST for a book already generating must not start a second run.**
T3b's runner has a cancel registry and exactly-once terminal states; use them.
At ~$0.35 and six minutes a run, a double-fire is the cheapest possible
expensive bug.

### The stages, in order

1. `story.Structure` — transcript → 8 pages, cast, per-page prompt and emotion.
2. `illustrate.Illustrate` with **both** `Config.Judge` and `Config.Persist`
   set — T7's closing loop. Sheets persist on render, pages post-approval.
3. `audio.NarrateBook` — one persisted clip per page, in page order (§T8).
4. `bookvideo` — segments and `concat -c copy` into the film (§T10a), then
   §T12's bed if it landed.
5. Mark the book ready, so `GET /book/{id}` serves cold.

### The events screen 5 lives on

Runs under `internal/job` (T3b) and publishes to the broker on the **book's**
topic — not the interview's, which carries turns and ends at `ended`.

```
event: page_approved  → {"n":3,"image_url":"/media/<id>"}
event: book_ready     → {"video_url":"/media/<id>"}
event: failed         → {}                       (no code, no prose — §T9)
```

`page_approved` is what drives the race: **screen 5 counts them into
`--done`**, which is the whole reason T9a's interface is one number. A late
subscriber must be able to catch up — a reload at minute four cannot restart
the race at zero — so the book's current state has to be readable, the way
`GET /interviews/{id}` already lets a late subscriber catch up on turns.

### Cost, and the shape of failure

A run is ~$0.35 and ~6 minutes (236 s images measured in T6b item 2, plus the
judge pass, narration and the render). T6 and T7 return a **zero Book** on any
error, so a failure is total: there is no partial book to serve, and `failed`
means the whole run. §The flow's failure path — animals sit, two doors, **no
automatic retry** — is the UI half of this, and the reason a retry is a tap
that spends a gate token rather than something the pipeline does on its own.

---

## T11 — Hardening

**Owns:** `internal/gate/**`, the retention sweep in `internal/mediastore/`, and the prewarm fixtures.

**Depends on:** T10. **Done when:** the four items below are true.

1. **Gate generation.** A public generate button is an open wallet at $0.01 an
   image, and the campaign has an explicit anti-abuse clause. Passcode or
   per-IP cap; a hackathon does not need more.
2. **Prewarm two or three finished books** and make them the default landing
   experience. **The free window closes 2026-09-06 and judging runs to
   2026-09-11** — every generation a judge triggers after the 6th bills at
   standard pricing, out of pocket.
3. **`GMI_API_KEY` never in the repo.** The repo is public for the whole
   judging period. **Largely done at `e2d696c`** — the key lives in
   `/etc/thutapi/env` (root, 0600) on the box, is sourced by
   `deploy/docker-run.sh`, and reaches the container through docker's
   **name-only** `--env GMI_API_KEY` form so it never enters the argument list.
   Shell-history and `ps` exposure are closed; presence in `docker inspect` and
   `config.v2.json` is **accepted deliberately** (it is what lets
   `--restart unless-stopped` recover the service after a reboot with no
   operator present) — see `deploy/README.md` §Safety for the full table.
   History swept and verified clean 2026-09-05: the key appears in no object
   across any ref, and `.env` has never been tracked. **What remains for T11:**
   re-run the sweep immediately before the repo goes public, and **rotate the
   key once judging closes** — it is an HS256 JWT with **no `exp` claim**, so
   it cannot expire on its own and the only clock on it is one we set.
4. **Cap disk, and sweep the orphans.** 23G free on the box; generated media
   accumulates. Two classes, and only one of them is reachable by any existing
   delete path:

   * **Placed blobs** — references, illustrations, narration — are anchored to
     a book and `DeleteBook` cascades them
     (`media.book_id … ON DELETE CASCADE`, `internal/store/store.go:196`).
     Deleting a book is enough; the sweep only decides *when* a book goes.
   * **Unplaced blobs have no book, and nothing can ever cascade to them.**
     `media.book_id` is nullable and the table's `CHECK` explicitly permits
     `book_id IS NULL AND kind = '' AND page_n IS NULL AND cast_name IS NULL`
     (`store.go:196`, `:205-207`). With no `book_id` there is no parent row to
     cascade *from*, so `DeleteBook` never reaches one, and `BookMedia` never
     lists one either — *"Unplaced blobs have no book and are never listed"*
     (`internal/store/media.go:184`). They are invisible to every query the
     product uses and immortal under every delete it performs.

   **This is not hypothetical and it is not small.** Since T8's round-4
   amendment (wire contract 2), **every spoken question persists as an
   unplaced row** — question audio is interview-scoped and exists before Phase
   B creates any book to anchor to. At roughly 6–10 questions an interview
   (`internal/interview/prompt.go:34`) plus retries, that is **~8–14 orphan
   blobs per interview, forever**, on a box with 23G and a public URL.
   The same class also collects the crash-window orphans of narration's and
   illustration's replace paths (blob written, row not yet placed).

   **The sweep therefore has to target unplaced rows by age**, not by book:
   delete `media` rows with `book_id IS NULL` older than a threshold, blob and
   row, using `mediastore.Delete`'s row-first ordering. A few hours is
   generous — a question clip is worthless the moment its turn is answered.
   Nothing else deletes these, so if the sweep does not, nothing does.

---

## T12 — Music bed (optional)

**Owns:** `internal/audio/music.go`.

**Depends on:** T10. Confirmed free. One `minimax-music-3.0` call plus one
looping `<audio>` at ~0.15 under the narration — perhaps half an hour.

**Same request queue, so assume the same envelope as T8**: the result is a URL
inside `outcome`, not bytes, and `payload` is echoed back beside it. Free, so
capture the terminal record once before writing the decode rather than
assuming — that single unchecked assumption is what T6 round-1 H1 cost. Puts a
third MiniMax model on the form and strengthens the "sound" half of a track that
is explicitly *picture and sound as a single output*.

**The mix, verified 2026-09-05** on the finished proof book (55.7 s) with a
deliberately-too-short 30 s bed. It is a **separate final pass over the whole
MP4**, not a change to the per-page segments — so §T10's free `concat -c copy`
is untouched, and adding music re-encodes audio only:

```
ffmpeg -i book.mp4 -stream_loop -1 -i music.mp3 \
  -filter_complex "[1:a]volume=0.15,afade=t=out:st=<dur-3>:d=3[bed];\
[0:a][bed]amix=inputs=2:duration=first:normalize=0[a]" \
  -map 0:v -map "[a]" -c:v copy -c:a aac -b:a 128k -movflags +faststart out.mp4
```

**1.1 s wall. Video stream copied. Duration byte-for-byte unchanged.** Three
pins:

* **`normalize=0` is not optional.** `amix` normalises by default, which halves
  the narration the moment a bed appears — the bug would sound like "the music
  is fine but the voice went quiet", and it is one flag.
* **`-stream_loop -1` + `duration=first`** means Music 3.0's clip length simply
  does not matter: a short bed loops, a long one is cut at the book's end. One
  less thing to check on the live call.
* **`afade=t=out` over the last 3 s**, or the book ends on a cut bed.

**The cheapest remaining win**, and cheaper still since the reshape: it needs
T10a's renderer, not the browser, and it is now one post-pass rather than a
looping `<audio>` racing the page.

---

## T13 — Voice clone (optional)

**Owns:** `internal/audio/clone.go`, **both capture modes in `static/**`
(adult corner) and the one upload route that receives them.**

*Owns corrected 2026-09-05.* The recorded line was `internal/audio/clone.go`
alone, which is a PLAN.md defect of the kind AGENTS.md names: a clone needs a
sample, a sample has to be captured and stored, and none of that fits in
`clone.go`. Whoever picks this up would have had to guess their boundary.

**Depends on:** T8, T9 (the adult corner), **T10a** (see the transcode below),
and the public-fetchability check (originally T1 check 5, **moved to T3** — the
T1 stub has no file route to prove it with).

### Two front doors, one pipe

**Both modes ship. In-browser recording is non-negotiable** (operator,
2026-09-05) — a voice clone the parent cannot make on the spot is a feature
nobody at the judging table will see. File upload sits beside it for the
prepared sample and for any device where the recorder misbehaves.

**It sits at the interview's end** — screen 4 of §The flow, one skippable
step, decided 2026-09-05. Narration has not started at that point, so the
clone is ready exactly when §T8 needs it and nothing regenerates.

```
record (MediaRecorder)  ─┐
                         ├─→ POST /voice-sample → /data tmp → ffmpeg → mp3
upload (<input type=file>)┘        → mediastore.Persist(audio/mpeg)
                                   → GET /media/{id}  → GMI source_audio
```

The two differ only in how the blob is obtained. Everything after the POST is
one path.

**Serving the sample needs no new route — settled 2026-09-05.** `source_audio`
must be a public HTTPS URL at a short-lived, unguessable path, and T3 already
built exactly that: `GET /media/{id}`, ids **128 bits of `crypto/rand`**, with
`Persist` documenting the id as unguessable
(`internal/mediastore/mediastore.go:132`, `:311`). T11's retention sweep
supplies the "short-lived" half.

**The recorder does not produce mp3, and that is why T13 now depends on T10a.**
Chrome and Firefox hand back `audio/webm;codecs=opus`; Safari and iOS hand back
`audio/mp4`. Neither is in `mediastore`'s closed content-type set, and neither
is a safe bet for what GMI will accept. **Transcode on receipt, before
`Persist`** — verified 2026-09-05, both formats, inside the shipping image as
uid 65532:

```
ffmpeg -i sample.<webm|m4a> -vn -c:a libmp3lame -b:a 128k -ar 44100 -ac 1 sample.mp3
```

8 s in → 8 s out, mono 44.1 kHz, ~129 KB, from both inputs. Because the
transcode happens before `Persist`, **`mediastore` only ever sees
`audio/mpeg`** — the closed type set is untouched and T13 never edits T3's
package. Without §T10's ffmpeg already in the image this would need an opus
decoder in Go, which is not a Sunday task; the video reshape paid for it.

**Three browser facts that will otherwise cost an afternoon:**

* **`getUserMedia` needs a secure context.** Production is HTTPS (T1b), and
  `localhost` counts — but **a LAN IP over plain HTTP does not**, and testing
  on a real phone against `http://192.168.x.x:8080` is exactly how §T9 says to
  verify. The recorder will fail there and look like a code bug.
* **Do not hardcode a `mimeType`.** Ask `MediaRecorder.isTypeSupported`, send
  whatever came out, and let the server transcode. That is the whole reason
  the pipe converges on the server.
* **It needs a gesture and a mic-permission prompt**, which is precisely why it
  belongs in the adult corner rather than the child's flow.

Cap the recording (a countdown to ~8–15 s) in the UI *and* by size on the
route. The model wants about eight seconds; an unbounded upload is an open
disk.

`minimax-audio-voice-clone-speech-2.8-turbo`, one synchronous call. Where a
sample is supplied, one short recording can voice the whole cast — narrator
plus dragon at `pitch: -8` with `spacious_echo`, robot with `robotic`, mouse at
`+6`.

**This does not disturb T10a.** §T8's contract is *one persisted clip per page,
in page order* — it says nothing about how many calls built that clip. With
T13 landed, a page's clip is **assembled** from several synths (narrator line,
then the dragon's line at `pitch: -8`, then narrator again) and handed over as
one file. The renderer's `-shortest` timing model neither knows nor cares. It
is the one place the sequencing pays off for free.

`source_audio` is a URL GMI downloads — it does not accept an upload — so the
sample must be served over public HTTPS at a short-lived, unguessable path.

### The consent decision, surfaced before anyone builds this

`AGENTS.md` §Safety requires a child's voice sample to live at a short-lived,
unguessable URL. That rule governs **what we host**. It does not reach **what
GMI hosts**, and T2b established what GMI hosts:

> Speech 2.8 returns its result at a `storage.googleapis.com` URL that is
> **publicly fetchable with no credential** — verified 2026-09-05, `200
> audio/mpeg`, a real MP3, no bearer token required.

So a voice clone puts the child's *synthesised voice* at a permanent,
unauthenticated public URL on infrastructure we do not control and cannot
delete from. The input sample is ours to make short-lived; the output is not.

That is a **consent decision, not a technical one**, and it is the reason T13
is optional rather than merely deferred. Decide it before the first real clone
call, not after. The library voice remains the default path and carries none of
this.

Optional throughout. The library voice is the default path.

---

## T14 — Submission

**Owns:** `README.md` and the submission assets. Touches no `internal/` package.

**Depends on:** T11, plus T12/T13 if they landed. **Starts 16:00 IST Sunday
regardless of state; submits by 20:00 IST.** Never cut, never deferred — an
unsubmitted project scores zero.

* **Public repository**, public for the whole judging period. **No license
  is required** — confirmed 2026-09-04; an earlier draft of this plan
  asserted one. Thutapi ships proprietary, all rights reserved.
* **Demo video, 3 minutes max.** Lead with the interview — a child answering
  short questions, and the book assembling itself from the answers.
* Track **Multimodality**. Tick **M3 + Speech 2.8** (+ Music 3.0 if T12 landed).
  The models ticked must match the track.
* GMI account email must be **the account the generations actually ran on**.
* **Share the demo on X tagging MiniMax and GMI Cloud** — required, not optional.

---

## Cut list

**Cut first:** T12 music, T13 clone, multi-character voices, page count to 6,
controlnet.

**Cut by decision, not by the clock:** the flipbook. §T10 is now an MP4 render
plus a `<video>`; there is no page-turn UI left to run out of time on.

**Do not cut:** the interview (T4 — it is the entry's reason to exist) and
character consistency (T6 — without it there is no book).

---

## Decisions needed, in the order they bite

| # | Decision | Blocks | Cost of deciding late |
| --- | --- | --- | --- |
| ~~1~~ | ~~Name~~ | — | **Decided: Thutapi**, `thutapi.nryn.dev`. |
| ~~2~~ | ~~Speech input or typed~~ | — | **Decided: typed.** MiniMax has no ASR and children's speech is the hardest input for one. |
| ~~3~~ | ~~Backend language~~ | — | **Decided: Go.** M3 writes it; the compiler is the first reviewer. |
| ~~4~~ | ~~Frontend~~ | — | **Decided: Preact + htm + hooks, vendored, no build.** Densest idiom in training data. |
| ~~5~~ | ~~Music 3.0 free~~ | — | **Confirmed free.** |
| ~~6~~ | ~~Voice clone scope~~ | — | **Decided: optional throughout.** Library voice is the default. |
| 7 | Image provider: `$0.01` tier or `gemini-2.5-flash-image` | T6 | Cheap. One-line switch by design; decide from the first reference sheet. |
| 8 | Page count: 6 or 8 | T5, T6 | Cheap early, annoying once the book render is laid out. |
| 9 | Gate mechanism: passcode or per-IP cap | T11 | Cheap, but it must exist before the URL is public. |
| 11 | T1 artifacts vs T1b live verification — split T1 into artifact-only close + operator-dependent T1b (DNS + foleyflow SSH). | T13, T14 | Done 2026-09-04 — T1 closed at 65df379, T1b unblocks operator run; without the split T2-T13 all blocked on operator work. |
| 13 | Book delivery: flipbook or MP4. | T10, T14 | **Decided 2026-09-05: MP4.** Free to decide now, expensive once a page-turn UI is laid out. Deletes the swipe/spread/audio-sync work, collapses the T8 autoplay hazard, and hands T14 a shareable artifact it needs anyway. |
| 14 | Byline on the title card, and where the name comes from. | T9, T10 | **Decided 2026-09-05: the child's name, and the interview asks for it — as question zero, outside the model loop.** The byline is not a privacy question: the parent uploads the file and can change anything before it goes anywhere. It is also **not a checklist slot and not in the prompt** (operator, explicit): the checklist is six *story* slots taught in `internal/interview/prompt.go` in three places, and T4 is closed. Question zero is asked by the UI, answered by the child, stored on the book — M3 never sees it. Blank ⇒ the card carries the title alone. |
| 18 | How question zero reaches `books.byline`. | T9, T4 | **Decided 2026-09-05: `POST /interviews` takes an optional `{"byline":"…"}`.** The UI already makes that call; a second endpoint buys a round-trip and nothing else. Absent or empty is normal. |
| 19 | How question audio reaches the client. | T8, T9 | **Decided 2026-09-05: a second SSE event `question_audio`, never a field on `questionEvent`.** A field would hold the text until TTS returned — *text waiting on audio* — and would name a `/media` URL that does not exist, since `SynthesizeQuestion` touches no store. It may never arrive; the UI stays silent, not spinning. |
| 20 | What starts the six minutes. | T9, T10c | **Decided 2026-09-05: an explicit `POST /interviews/{id}/generate`, not an automatic trigger on `ended`.** Screen 4 sits between the interview ending and generation starting, and the clone must exist before narration; an automatic trigger would race it. It is also where §T11's gate belongs — the one route that spends money. |
| 21 | UI routes vs the JSON API. | T9, T10b | **Decided 2026-09-05: human pages are singular (`/`, `/interview/{id}`, `/book/{id}`), the API stays plural.** `GET /interviews/{id}` already serves JSON and Go's `ServeMux` matches literally. **No `Accept` negotiation** — it cannot be expressed in the mux and makes a shared link's behaviour depend on a header the sharer never sees. |
| 15 | The ~6-minute wait: spinner, or something to watch. | T9, T9a | **Decided 2026-09-05: the race** (§The flow screen 5). Eight markers, one per page — the animation encodes real progress, so §T10's "no percentage" rule survives. Prototyped the same day; the mechanism is settled and only the sprites are left. |
| 16 | Where voice capture sits in the flow. | T9, T13 | **Decided 2026-09-05: at the interview's end**, one skippable screen. Narration has not started, so the clone is ready exactly when T8 needs it and nothing regenerates. |
| 17 | What the child sees when generation fails. | T9 | **Decided 2026-09-05: animals sit down, one warm line, two equal doors — *try again* and *look at other books*. No automatic retry** — a regeneration is ~$0.35 and six minutes, and after 2026-09-06 it bills; an auto-retry on a public URL is the open wallet §T11 item 1 exists to prevent. |
| 12 | Operator runbook for T1b (DNS record, `docker run` on foleyflow, five live checks, transcript placeholders). | T1b | Free if operator can find the runbook; 15+ hours of clock if not. |
Decision 7 is deliberately deferred to evidence rather than argued now: the
spread across the whole image catalog is about 23 cents a book, so the only
input that matters is whether the cast holds, which is not knowable until a
reference sheet exists.
