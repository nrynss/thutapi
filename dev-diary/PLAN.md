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
T2  GMI clients                T10 book renderer + flipbook
T3  store and media            T11 hardening: gate, cap, prewarm
T4  interview loop (Phase A)   T12 music bed        (optional)
T5  structuring (Phase B)      T13 voice clone      (optional)
T6  illustration               T14 submission       (last, always)
T7  consistency verification
```

T1 comes second on purpose. **T14 is last and is never skipped** — an unsubmitted
project scores zero. T12 and T13 are the two optional models: scored, not
required, and the first things cut.

### Status

| ID | Status |
| --- | --- |
| **T0** | DONE. Closed at 166a992 after 3 review rounds (zero residue; see t0-round1.md, t0-round2.md, t0-round3.md, t0-remediation-round1.md, t0-remediation-round2.md). |
| **T1** | DONE. Closed at 65df379 — artifacts (Dockerfile, .dockerignore, deploy/docker-run.sh, deploy/README.md) committed, local smoke green, all Traefik labels byte-identical to project.md §Deployment. See dev-diary/adversarial-review/t1-round1.md. |
| **T1b** | Blocked. Live deployment verification — DNS A record + foleyflow deploy + curl. Cannot be done from this workstation; needs operator with DNS + SSH access. See T1b section below. |
| **T2** | Not started. Response shapes verified by hand 2026-09-04 — see T2. |
| **T3** | Not started. |
| **T4** | Not started. |
| **T5** | Not started. |
| **T6** | Not started. |
| **T7** | Not started. Capability **verified live** 2026-09-04 — see T7. |
| **T8** | Not started. |
| **T9** | Not started. |
| **T10** | Not started. |
| **T11** | Not started. |
| **T12** | Optional. Not started. |
| **T13** | Optional. Not started. |
| **T14** | Not started. Never cut. |

---

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
| → 16:00 | T8–T11 (+T12/T13 if reached): audio, interview UI, flipbook, hardening |
| **16:00** | **Hard stop on building.** Whatever is unfinished gets cut. |
| 16:00–20:00 | T14: video, repo public, form, X post |
| **20:00** | **Submit.** 90 minutes before the earliest plausible deadline. |

A finished project that is not submitted scores zero, and that failure mode is
common enough to write down.

---

## T0 — Repo and skeleton

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

## T1 — The deployment artifacts  *(Implementation complete, local smoke green — T1b holds the live verification)*

Ship the T0 skeleton to `thutapi.nryn.dev` and prove every environmental fact
while they are cheap to fix.

**Depends on:** T0. **Done when:** the Dockerfile, `.dockerignore`,
`deploy/docker-run.sh`, and `deploy/README.md` are committed; a local
`docker build -t thutapi:local . && docker run --rm -p 18080:8080 -e
PORT=8080 thutapi:local` succeeds; `curl http://127.0.0.1:18080/healthz`
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
2. **DNS.** An A record for `thutapi.nryn.dev` → `167.233.247.107`, **proxied**,
   matching `auteur` / `mosaic` / `eoc` / `serp`.
3. **Certificate issues.** Let's Encrypt HTTP-01 resolves through the proxy —
   the existing four subdomains prove the path, but confirm for this one.
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
* Generated media is immutable, so serve it with a long `max-age` and let the
  Cloudflare edge cache it. That is free performance for a judge.

---
## T1b — Live deployment verification (operator-dependent)

**Status:** Blocked. No agent on this workstation holds DNS credentials
for `nryn.dev` or SSH access to `foleyflow`. The round-1 review
(`dev-diary/adversarial-review/t1-round1.md`, C1) explicitly requires
both: the A record on the Cloudflare zone and the container run on
the Hetzner box. The artifacts (Dockerfile, deploy scripts, labels,
README) are sound; the live path needs an operator.

**Depends on:** T1 (artifacts). **Unblocks:** T2 (GMI clients),
T13 (voice clone, check 5), T14 (submission requirement: "Live app URL
on the Hetzner container").

**Done when:** all five checks from the original T1 done-when pass
against `https://thutapi.nryn.dev/healthz`:

1. Traefik on `foleyflow` picks up the container with the five labels.
2. DNS A record `thutapi.nryn.dev` → `167.233.247.107`, proxied
   (matching `auteur`/`mosaic`/`eoc`/`serp`).
3. Let's Encrypt HTTP-01 resolves through the proxy.
4. `curl https://thutapi.nryn.dev/healthz` returns 200 JSON with
   `cf-ray` present.
5. A dummy MP3 at a signed path is fetchable by a third party
   (proves the `source_audio` mechanism Speech 2.8 needs at T14).

**Operator runbook:**

```bash
# On workstation, push the image the artifacts ship:
docker build -t thutapi:local .
docker save thutapi:local | ssh foleyflow 'docker load'

# On foleyflow (via SSH), prepare the data dir and run the deploy:
ssh foleyflow
sudo mkdir -p /srv/thutapi/data
sudo chown 1000:1000 /srv/thutapi/data      # match container nonroot UID
export GMI_API_KEY='<from operator vault>'
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
* Cloudflare SSL mode for the zone is `Full (strict)`; Flexible plus
  Traefik's HTTPS redirect is an infinite redirect loop. Verify, do not
  assume.
* If `curl /healthz` returns 524, Cloudflare's 100s proxy timeout
  fired — but `/healthz` is sub-second, so the cause is upstream
  (Traefik not picking the container; check labels and Traefik logs).

**When done:** paste the five transcripts (or a single one with all
five signal-bearing lines) into `dev-diary/adversarial-review/t1b-round1.md`,
mark T1b status DONE in the table above, and the chain to T2/T13/T14
opens. Until then T1b stays `Blocked`.


Two clients, because GMI has two APIs with different shapes.

**Depends on:** T1. **Done when:** an integration test hits both endpoints live
and unmarshals into typed structs.

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

## T3 — Store and media

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

## T4 — The interview (Phase A)

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

Turns stream over the T1 SSE channel.

---

## T5 — Structuring (Phase B)

One call over the whole transcript. M3's 1M context means no summarisation and
no state to marshal.

**Depends on:** T4. **Done when:** a transcript yields valid JSON that
validates against the schema, twice running.

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

## T6 — Illustration

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

**Provider behind a one-line switch.** Start on **Flux2-Klein** or **Z-Image**
($0.01). If characters drift, switch to `gemini-2.5-flash-image` ($0.0387) — the
spread across the whole catalog is about 23 cents a book, so choose on
consistency and never on price. **Do not use `Qwen-Image-2512`:** it is
text-to-image only and cannot take the reference.

Fan out with `errgroup`, bounded to ~4 concurrent.

---

## T7 — Consistency verification

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

---

## T8 — Audio

**Depends on:** T2, T5. **Done when:** questions speak, and a finished book
reads itself.

* **Questions:** `minimax-tts-speech-2.8-turbo`. Latency beats fidelity.
  Text streams over SSE immediately and the TTS call fires in parallel — **text
  never waits on audio.**
* **Narration:** `minimax-tts-speech-2.8-hd`, per-page `emotion` from T5.
* Default `voice_id`: `English_expressive_narrator`.
* **Mobile autoplay is blocked.** iOS Safari refuses audio not triggered by a
  gesture, so spoken questions die silently on an iPad. One **"Tap to start"**
  gesture on entry unlocks an audio element; reuse that element for every later
  clip. Without this the headline feature does not work on the primary device.

---

## T9 — Frontend shell and the interview UI

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
* **No naked spinners** — every wait is a character doing something.
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

## T10 — The book

**Depends on:** T6, T8, T9. **Done when:** a book reads and turns on a phone and
on a laptop, and its URL opens cold.

* **The book filling up is the progress bar.** Pages arrive one at a time over
  SSE; no percentage.
* **Layout fork, not fluid resizing:** portrait phone → one page; tablet
  landscape and desktop → **two-page spread**, which reads like a real book and
  is considerably cuter.
* Swipe on touch; arrow keys and click zones on desktop.
* Server-rendered book page so a shared link works without JS.

---

## T11 — Hardening

**Depends on:** T10. **Done when:** the four items below are true.

1. **Gate generation.** A public generate button is an open wallet at $0.01 an
   image, and the campaign has an explicit anti-abuse clause. Passcode or
   per-IP cap; a hackathon does not need more.
2. **Prewarm two or three finished books** and make them the default landing
   experience. **The free window closes 2026-09-06 and judging runs to
   2026-09-11** — every generation a judge triggers after the 6th bills at
   standard pricing, out of pocket.
3. **`GMI_API_KEY` never in the repo.** It is public for the whole judging
   period. Pass by `docker run -e`; sweep git history before going public.
4. **Cap disk.** 23G free on the box; generated media accumulates.

---

## T12 — Music bed (optional)

**Depends on:** T10. Confirmed free. One `minimax-music-3.0` call plus one
looping `<audio>` at ~0.15 under the narration — perhaps half an hour. Puts a
third MiniMax model on the form and strengthens the "sound" half of a track that
is explicitly *picture and sound as a single output*.

**The cheapest remaining win.** First thing added back if Sunday goes well.

---

## T13 — Voice clone (optional)

**Depends on:** T8, and on T1 check 5 passing.

`minimax-audio-voice-clone-speech-2.8-turbo`, one synchronous call. Where a
sample is supplied, one short recording can voice the whole cast — narrator
plus dragon at `pitch: -8` with `spacious_echo`, robot with `robotic`, mouse at
`+6`.

`source_audio` is a URL GMI downloads — it does not accept an upload — so the
sample must be served over public HTTPS at a short-lived, unguessable path.

Optional throughout. The library voice is the default path.

---

## T14 — Submission

**Depends on:** T11, plus T12/T13 if they landed. **Starts 16:00 IST Sunday
regardless of state; submits by 20:00 IST.** Never cut, never deferred — an
unsubmitted project scores zero.

* **Public repository**, public for the whole judging period, with a license.
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
| 8 | Page count: 6 or 8 | T5, T6 | Cheap early, annoying after the flipbook is laid out. |
| 9 | Gate mechanism: passcode or per-IP cap | T11 | Cheap, but it must exist before the URL is public. |
| 11 | T1 artifacts vs T1b live verification — split T1 into artifact-only close + operator-dependent T1b (DNS + foleyflow SSH). | T13, T14 | Done 2026-09-04 — T1 closed at 65df379, T1b unblocks operator run; without the split T2-T13 all blocked on operator work. |
| 12 | Operator runbook for T1b (DNS record, `docker run` on foleyflow, five live checks, transcript placeholders). | T1b | Free if operator can find the runbook; 15+ hours of clock if not. |
Decision 7 is deliberately deferred to evidence rather than argued now: the
spread across the whole image catalog is about 23 cents a book, so the only
input that matters is whether the cast holds, which is not knowable until a
reference sheet exists.
