# Thutapi

**A story is a thing a child already has. This one just listens.**

*Thutapi* (തുത്താപ്പി) is what Kunjupaathumma affectionately calls Aisha in
Vaikom Muhammad Basheer's *Ntuppuppakkoranendarnnu*. A term of endearment for a
small girl, on a product where a small girl tells the story.

An M3 agent interviews a child about the story they want to tell, then returns
it as an illustrated picture book, read aloud — optionally in the child's own
voice.

- **Event:** MiniMax Week × GMI Cloud, Multimodality track (see [minimax-week.md](../../hackathons/minimax-week.md))
- **Deadline:** 2026-09-06 — **two days from 2026-09-04**
- **Host:** container on the Hetzner box `foleyflow`, at **thutapi.nryn.dev**.
  GMI inference throughout.
- **Repo:** `/home/nryn/work/thutapi`. Build plan: [PLAN.md](PLAN.md).

---

## Scope, as decided

**In**
- Input is **typed**, not spoken. No ASR, no Whisper, no transcription anywhere.
- The child does **not** type a finished story. M3 **interviews** them — short
  questions, short typed answers — and authors the story from what it hears.
- Voice clone **optional**. The library voice is the default path.
- Music **optional**.

**Out**
- Speech input of any kind. This was cut deliberately: MiniMax has no ASR, and
  children's speech is the hardest possible input for one.
- H3 video. It is the one model in the lineup that is **not free**.

---

## Originality: the interview is the product

The campaign judges originality on *"Have we seen this before?"*, glossed on the
page as **"The obvious build is the one everyone submits"** — and headlined
*"We're looking for the ones nobody saw coming."* A blank textarea that turns a
typed prompt into an illustrated book is a build several entrants will submit.

**The interview is what makes this not that.** The child does not write a story;
they answer questions. *What is your hero called? What colour is the dragon? Why
was she scared?* M3 holds the plan across that conversation and authors the book
from the answers.

That matters three ways at once:

- **Originality.** The agent authors *with* the child, not *for* them.
- **Model usage** (criterion 1, judged on how far the model was pushed).
  Multi-turn elicitation that keeps a story coherent across a rambling
  conversation is M3's advertised headline capability — *"frontier reasoning
  that holds a plan across long agentic tasks"* — rather than M3 as a JSON
  formatter.
- **Usability** (criterion 2). A six-year-old cannot type four hundred words,
  but can type "a dragon" and "she was scared". Short answers are the
  accessibility story.

**The cast is an optional flourish on top.** Where a voice sample is supplied,
one short recording — eight seconds of a child saying anything at all — can
become every voice in the book:

| Character | How it is made from the same clip |
|---|---|
| Narrator | clone, `emotion` per page from M3 |
| Dragon | clone, `pitch: -8`, `sound_effects: spacious_echo` |
| Robot | clone, `sound_effects: robotic`, `timbre` shifted |
| Mouse | clone, `pitch: +6`, `intensity` lowered |

That is one MiniMax model family pushed hard, which serves criterion 1
(*"how far you pushed them, not how many calls you made"*). It is a bonus, not
the entry's reason to exist — the interview is.

---

## Pipeline

```
child ⇄ M3        interview: short questions, short typed answers          [free]
   └─► M3            page split + cast bible + per-page prompt & emotion   [free]
         ├─► t2i     one reference image per cast member                   [$0.01 ea]
         │     └─► i2i   page illustrations, referenced to the sheet       [$0.01 ea]
         ├─► Speech 2.8  narration + character lines from one clip         [free]
         └─► Music 3.0   optional bed                                      [free]
   └─► flipbook page, shareable URL
```

### 1. M3 interviews the child, then structures the result

`MiniMaxAI/MiniMax-M3` on `api.gmi-serving.com/v1/chat/completions`. Free, and
1M context, so the entire conversation stays in the window with room to spare —
no summarisation, no state to marshal.

**Phase A — elicit.** A multi-turn loop with a system prompt that enforces the
house style for talking to a child:

- **One question at a time.** Never a form, never two questions in one turn.
- **Concrete, not abstract.** *"What colour is the dragon?"* not *"describe the
  antagonist"*.
- **Offer choices when they stall.** A child who answers "i dunno" gets
  *"Is she brave, or is she sneaky?"* — never an open re-ask.
- **Accept everything.** No correction of spelling, logic or plausibility. The
  charm is the child's logic; the agent's job is to catch it, not tidy it.
- **Know when to stop.** Hold a checklist — hero, companion, want, obstacle,
  turn, ending — and close the interview once it is filled or the child tires.
  Roughly 6–10 exchanges. A stall or a repeated one-word answer ends it early.

**Phase B — structure.** One final call over the whole transcript, returning a
single JSON object:

```json
{
  "title": "...",
  "cast": [
    { "name": "Mira",
      "visual": "a small girl, red raincoat, black bob haircut, round glasses",
      "voice": { "pitch": 0, "sound_effects": "" } }
  ],
  "pages": [
    { "n": 1,
      "text": "...",
      "prompt": "...",
      "characters": ["Mira"],
      "emotion": "happy",
      "lines": [ { "character": "Mira", "text": "..." } ] }
  ]
}
```

Enable reasoning with `thinking:{"type":"enabled"}` in the body —
`reasoning_effort` is **ignored** by MiniMax models. Model id must carry the
full `MiniMaxAI/` prefix; the bare `MiniMax-M3` in GMI's own docs 404s.

### 2. Character consistency is the real technical problem

Not image quality — 2D cartoon is the easy style, every model does it. The book
falls apart if Mira's hair changes on page 4. Lock it twice:

- **Verbatim text lock.** The `visual` string from the cast bible is pasted
  into every page prompt unchanged. Never paraphrased, never regenerated.
- **Image lock.** Generate one reference image per cast member first, then
  produce every page as **image-to-image** against that reference.
- **Style lock.** One constant suffix on every prompt, e.g. *"flat 2D
  children's picture book illustration, thick outlines, gouache texture, soft
  palette"*.

### 2b. M3 checks its own consistency — VERIFIED WORKING

M3 has native image input, and it works on GMI's OpenAI-compatible endpoint.
**Tested 2026-09-04 against `MiniMaxAI/MiniMax-M3` at
`api.gmi-serving.com/v1/chat/completions`:**

- **Single image** — a synthetic test image was described correctly on all four
  features (magenta triangle, green circle inside, two blue squares). Not
  guessable.
- **Two images in one message** — accepted, compared, and returned clean JSON
  on request:
  `{"match":false,"inner_shape_1":"circle","inner_shape_2":"square","reason":"..."}`
- **Cost** — 471 prompt tokens for two 320x320 images plus the prompt. Trivial
  against a 1M window, and free until Sep 6.

So the drift problem gets a closing loop. After each page renders, send M3 the
character reference sheet and the new page and ask for a match verdict;
regenerate on a `false`. Cap retries (2 is plenty) so a stubborn page cannot
spin.

Content-block shape:

```json
{"role":"user","content":[
  {"type":"text","text":"IMAGE 1 is the reference. IMAGE 2 is a new render. Reply with JSON only..."},
  {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}},
  {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]}
```

**Useful asymmetry:** for **M3's text endpoint** (this consistency check),
images go in as **inline `data:` base64 URIs** — no hosting needed.

**The image *generation* endpoint is different, and this was measured on
2026-09-05.** `seedream-5.0-lite` takes `payload.image` as an **array of
reference image URLs**, not inline base64. The "no hosting needed" conclusion
survives anyway, by a different route: GMI's own output URLs are public and
unauthenticated, so a reference sheet renders, GMI returns a public URL, and
that URL feeds straight into the next call's `image` array. Nothing needs
hosting on our side *during* a generation — but those URLs are assumed to
expire, so T7 still downloads and persists for the finished book.

Speech 2.8's `source_audio` is the third case: a public URL we must host
ourselves, because the sample originates with us.

This is also worth real points: it uses multimodality *as input* in the
Multimodality track rather than only emitting picture-and-sound, and it is a
concrete answer to criterion 1's "how far did you push the model".

### 3. Image model — choose on consistency, not price

At 8 pages plus ~2 reference sheets, the spread across the entire catalog is
about **23 cents a book**. Price is noise. Put the provider behind a one-line
switch and pick on how well the character holds.

> **Read the unit before quoting a number from this table.** The column is
> **per book (~10 images)**, not per image. Per *image* the same models are
> ~$0.01; the pipeline diagram above uses the per-image unit and this table
> uses the per-book one. A T2 docstring already conflated the two while
> justifying a forbidden model — see `adversarial-review/t2-round3.md` L5.
> Also note four models tie at $0.10, so **price cannot select between them.**
> That is the point of the heading.

| Model | Per book (10 imgs) | i2i |
|---|---|---|
| ~~Z-Image / Flux2-Klein~~ **— DO NOT USE, they never generate** | $0.10 | accepts, then hangs |
| GLM-Image / Flux2-Dev / Z-Image-Turbo-…-Controlnet | $0.10 | untested — assume nothing |
| ~~Qwen-Image-2512~~ **— FORBIDDEN, do not use anywhere** | $0.10 | **no — t2i only, cannot take the reference. An i2i call succeeds and silently ignores the reference.** |
| gemini-2.5-flash-image | $0.39 | yes |
| seedream-5.0-lite / gemini-3.1-flash-lite-image | $0.35 | yes |

**Chosen: `seedream-5.0-lite` ($0.035 an image, ~$0.39 a book).** Measured
live 2026-09-05 — see `adversarial-review/t6b-live-record.md`.

**`Flux2-Klein` and `Z-Image` do not work.** They accept a request, return
`status:"queued"`, and never generate — verified over ~72s of polling, and
reproduced by the operator in the GMI console. That is a worse failure than a
404: a 404 classifies as `ErrModelNotFound` and fails on the first call, while
this hangs every page until its deadline and looks like a network fault. The
price advantage of the $0.10 tier was never real, because the tier never
delivered an image.

Seedream is also **synchronous** (`status:"success"` on the POST itself, ~14s),
takes up to 14 reference images, and offers `sequential_image_generation` for
consistency. If characters still drift, `gemini-2.5-flash-image` remains the
fallback — choose on consistency, never on price.

### 4. Audio — Speech 2.8

Both flavours are `text` → audio; neither transcribes.

- **With a voice sample:** `minimax-audio-voice-clone-speech-2.8-turbo`, one
  synchronous call, 5–15s. Minimum body is `text` + `source_audio`.
- **Without:** `minimax-tts-speech-2.8-hd`, `voice_id: English_expressive_narrator`.

Per-page `emotion` comes from M3's JSON. Character lines re-call the same clone
with that character's `pitch` / `timbre` / `sound_effects`.

Set `need_noise_reduction: true` and `need_volumn_normalization: true` — the
API genuinely spells it **`volumn`**; match the typo or it is silently ignored.

---

## Architecture consequences

- **Two base URLs.** Text goes to `api.gmi-serving.com/v1/chat/completions`
  (OpenAI-compatible). Audio and images go to
  `console.gmicloud.ai/api/v1/ie/requestqueue/apikey/requests` (request queue,
  `{model, payload}` envelope). Two client shapes in the codebase.
- **Voice samples must be reachable by URL.** `source_audio` is a URL GMI
  downloads — it does not accept an upload. Serving it publicly is free on the
  existing edge (below); what still needs deciding is that the URLs are
  **short-lived and unguessable**, not stable paths. It is a child's voice.
- **Cost per book ≈ $0.10**, all of it images. Everything else is free during
  the campaign window — but see the billing trap below.

---

## Stack

**Go backend, `html/template` + SSE + a little vanilla JS. No Node in the
build, no reactive framework.**

The stack was chosen on one criterion: **M3 is the agent writing this**, with
GLM 5.3 and Claude reviewing. So the question is not which stack is most
elegant, it is which one a coding agent writes reliably and which one produces
errors a reviewer can catch mechanically.

### Why Go

- Small language surface, one idiomatic way to do most things.
- The stdlib covers this entire app: `net/http`, `encoding/json`,
  `html/template`, `sync`. Almost nothing to hallucinate.
- No framework churn. Go 1.27.1 code looks like Go 1.16 code, so training data
  does not rot.
- **`go build` and `go vet` are the feedback loop.** Errors are immediate,
  precise and machine-readable — an agent iterates against a compiler.
- Review is cheap: the compiler does half of it before a reviewer reads a line,
  and `gofmt` removes style drift between three different agents.
- `errgroup` for the ~10 parallel image calls is the one place Go was already
  ahead of the alternative.

### Why not Svelte, despite it being the house default

Mosaic's UI is `svelte@^5.0.0`, and Svelte 5 is currently a poor agent target.
Training corpora are dominated by Svelte 3/4 — `export let`, `$:`, stores —
while runes (`$state`, `$derived`, `$props`, `$effect`) are recent and
comparatively scarce. Agents routinely emit Svelte 4 idioms into a Svelte 5
file, and **the failure mode is silent**: it compiles, and reactivity simply
does not fire. That is the worst possible signal for an autonomous agent, and
it is expensive to review because the wrong code looks right.

This is not hypothetical. Dry Run already hit this class of trap: a reassigned
`$state` binding cannot be exported from a module.

SvelteKit would add further surface to get wrong — `+page.server.js` vs
`+page.js`, load functions, form actions, `$app/*` imports.

*Caveat, checked 2026-09-04. M3 (released 2026-06-01) is a strong coding model
— 59.0% SWE-Bench Pro, narrowly ahead of GPT-5.5, though several published
results were run on MiniMax's own infrastructure with their agent scaffolding.
**But SWE-bench is Python**, so it says nothing about Go or Svelte 5. No source
found addresses per-language performance. Capability is not the worry; training
density on a recently-changed frontend idiom is, and the benchmark is silent on
it. Note also that Svelte is the house constant while Go is not: Mosaic is Go +
Svelte, Auteur/FoleyFlow is **Python** + Svelte, Dry Run was Svelte 5 + Netlify
functions. Mosaic is the only Go precedent.*

### If Svelte is used anyway

Take the Mosaic shape — plain Svelte SPA on Vite, no SvelteKit, Go binary
serving the built assets via a `UI_DIR` env var — and pin the idiom in
`AGENTS.md`: **runes only, never `export let`, never `$:`**. Brief the
reviewers to look specifically for Svelte 4 leakage.

### Lift from Mosaic

- **`internal/stream/broker.go`** (223 lines) — a working SSE broker, directly
  reusable for streaming interview turns.
- **Skip `internal/openaimodel`** — it targets the OpenAI *Responses* API
  (`/v1/responses`). GMI is *Chat Completions* (`/v1/chat/completions`),
  a different request and response shape. Useful only as a retry/config
  skeleton.

### Packaging

Follow the Mosaic Dockerfile pattern minus the Node stage: build with
`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`, ship on
`gcr.io/distroless/base-debian12:nonroot`, `EXPOSE 8080`, listen on
`0.0.0.0:${PORT}`.

**Where images come from.** GitHub Actions publishes to
`ghcr.io/nrynss/thutapi` as a public package, so the box pulls anonymously
and holds no registry credential. Publishing is **manual**
(`gh workflow run image.yml`) — an image is a deployment artifact, not a
byproduct of committing. Two tags: the commit SHA, which names one build
forever and is what you deploy, and `latest`, which moves and is a
convenience for humans. `VERSION` is injected as a build arg and surfaces
at `/healthz`, so a running instance is traceable to its source commit.

The first deployment was a workstation build shipped with
`docker save | ssh docker load`. That still works as a fallback when the
registry is unreachable, but nothing about it is reproducible — the box
cannot rebuild its own image that way.

### M3 phase settings

| Phase | `thinking` | Why |
|---|---|---|
| A — interview turns | **off** | Short conversational questions. A child watching a spinner is a usability failure, and usability is a third of the score. |
| B — structuring | **on** | One call, quality matters, and a progress bar is already showing while images generate. |

---

## UI — child-friendly, and why that does not change the stack

"Cute" is CSS, SVG, typography and motion — not a framework. But the UI does
need to be **reactive**, and hand-rolled DOM updates fail the same way Svelte 5
runes do: you change state, forget to sync the DOM, and nothing errors. That is
the silent-failure property this project rejected Svelte over, so vanilla JS is
not the agent-safe choice either.

**Preact + `htm` + hooks, vendored, no build step.**

- **React/Preact idioms are the densest frontend material in any model's
  training data** — far denser than Svelte 5 runes. `useState`, `useEffect` and
  JSX-shaped markup are written near-flawlessly by models. That is the same
  criterion that chose Go.
- **`htm` removes the build step**: tagged-template JSX, no Babel, no Vite, no
  Node stage in the Dockerfile. Go still serves one static binary from
  distroless. ~16KB for preact + htm + hooks.
- **Hooks, not signals.** `useState`/`useEffect` are the maximally-dense idiom;
  `@preact/signals` is newer and thinner in training data. Reach for signals
  only if hook prop-drilling actually hurts.
- **Vendor, do not CDN.** Pinned copies committed to `static/vendor/`. No
  runtime CDN dependency, works offline, no network surprises in a judge's
  `docker run`.
- **Pin in `AGENTS.md`:** htm uses backticks and `${}` where JSX uses `<>` and
  `{}` — ``html`<div class=${cls}>${kids}</div>` ``. It is the one place a model
  will drift toward real JSX.

Go still renders the shell and the shareable book page with `html/template`, so
a cold link works without JavaScript.

The UI is nonetheless load-bearing: **usability is a third of the score and the
users are children.**

### Questions are spoken aloud

Speech 2.8 is free, so **every interview question is read aloud**. A child who
can barely read can listen and answer in two words. This is a genuine
accessibility story for criterion 2 and more depth of model use for criterion 1
at no extra cost.

- Stream the question **text over SSE immediately**, fire the TTS call in
  parallel, play the audio when it lands. Text never waits on audio.
- Use **`minimax-tts-speech-2.8-turbo`** for questions — latency beats fidelity
  here. Save **`-hd`** for the finished book.

### The rest of the rules

- **Tappable chips, not typing.** Typing is the friction point for a six-year-
  old. When M3 offers a binary ("is she brave, or is she sneaky?"), render the
  options as buttons. The interview should be completable almost entirely by
  tapping, with the text box as an escape hatch.
- **Big targets**, ~60px minimum, generously spaced. Fine motor control is poor.
- **No naked spinners.** Every waiting state is a character doing something.
- **The book filling up is the progress bar.** Pages appearing one at a time
  replaces any percentage.
- **Chunky rounded type** (Fredoka, Baloo 2), warm palette, rounded everything.
  Nothing geometric or cold.
- **No failure text.** Errors are warm and never a dead end.

### Responsive — tablet-first, all four form factors

Must work on phone, tablet, laptop and desktop. **Build the touch layout as the
real one** and adapt upward; children use tablets and phones, so a squeezed
desktop layout is the wrong default.

**Two traps that silently kill the core feature on the primary device:**

1. **Autoplay is blocked on mobile.** Questions are spoken automatically, and
   iOS Safari refuses audio not triggered by a user gesture — so spoken
   questions die silently on an iPad. Fix: one **"Tap to start"** gesture on
   entry that unlocks an `AudioContext`/`<audio>` element, then reuse that
   unlocked element for every later question.
2. **The mobile keyboard covers the input.** Use `100dvh`, not `100vh`; honour
   `env(safe-area-inset-bottom)` on the pinned input; scroll it into view on
   focus. **Chrome devtools does not reproduce this — test on a real phone.**
   The chips-first design mitigates it: if most answers are taps, the keyboard
   rarely appears.

**The book is a layout fork, not fluid resizing:**

| Viewport | Book layout |
|---|---|
| Portrait phone | one page at a time |
| Tablet landscape / desktop | **two-page spread** — reads like a real book, and is considerably cuter |

**Touch:** swipe to turn pages, arrow keys and click zones on desktop,
`touch-action: manipulation` on buttons to kill the 300ms double-tap-zoom delay.

Plain CSS and a couple of media queries. **No Tailwind** — it would reintroduce
the build step Preact + htm just removed.

### Assume an adult is present

A young child is likely operating this *with* a parent. Voice-sample capture
and any settings belong in a small adult-facing corner, out of the child's
flow — which is also where the consent decision for the voice clone lives.

---

## Deployment — the box already does this

Hetzner box `foleyflow`, Ubuntu 26.04. **Traefik v3** holds 80/443 with a
`letsencrypt` certresolver; existing apps are `auteur`, `mosaic`, `eoc` and
`serp` on `*.nryn.dev`. Containers are started with plain `docker run` and
labels — there is no compose project on the box.

Adding this app is a DNS A record for `thutapi.nryn.dev`, **attachment to
the `proxy` Docker network**, plus five labels:

```
traefik.enable=true
traefik.http.routers.thutapi.entrypoints=websecure
traefik.http.routers.thutapi.rule=Host(`thutapi.nryn.dev`)
traefik.http.routers.thutapi.tls.certresolver=letsencrypt
traefik.http.services.thutapi.loadbalancer.server.port=8080
```

**The `proxy` network is not optional.** Traefik's docker provider runs
`exposedByDefault: false` with `network: proxy`; a container carrying the
labels but sitting outside that network is discovered and then unroutable,
which surfaces as a 404 or 502 rather than an obvious error. Every sibling
is on `proxy` and nothing else.

Routing and the public-HTTPS requirement for `source_audio` are satisfied
by the edge that is already running. **TLS and renewal are nearly free but
not zero:** issuance is DNS-01 against a Cloudflare token held by Traefik,
and on 2026-09-04 that token was found to have lapsed — `serp` had silently
fallen back to a self-signed origin certificate and nothing on the box
could renew. The zone's SSL mode is `full`, which does not validate the
origin certificate, so it stayed invisible. Treat certificate health as
something to check, not assume. The full record is in the private
infrastructure notes (`~/work/hetzner`), not this repo.

Headroom: 23G disk free, 2.9Gi RAM available. Ample for an API orchestrator —
nothing runs locally now that speech input is cut — but generated images and
audio accumulate, so cap retention.

### Three things the box does not solve

1. **The free window closes before judging ends.** The promo runs to
   **Sep 6**; judging runs Sep 6–11, winners announced Sep 11. Every generation
   a judge triggers on the live site bills at **standard pricing**. Pre-generate
   two or three finished books and serve those as the default path; keep live
   generation behind a passcode or a hard cap.
2. **A public generate button is an open wallet**, and the campaign carries an
   explicit anti-abuse clause covering automated farming. Rate-limit or gate.
3. **`GMI_API_KEY` must never reach the repo**, which stays public for the whole
   judging period. Pass it via `docker run -e`, and sweep history before going
   public.

---

## Two-day cut list

**Day 1** — M3 interview loop (system prompt, checklist, stop condition) and the
Phase-B JSON contract; cast bible; reference sheets; page images with the i2i
lock. A book that renders, silent.

**Day 2** — voice clone path and the character cast; flipbook UI; deploy to
Hetzner; then the video and deck.

**Cut first if time runs out:** music, multi-character voices (keep narrator
only), page count down to 6, controlnet.

**Cheapest remaining win, if Day 2 goes well:** the music bed. Confirmed free,
and it is *one* call plus one looping `<audio>` at low volume (~0.15) under the
narration — perhaps half an hour of work. It also puts a third MiniMax model on
the submission form, and the Multimodality track is explicitly "picture and
sound as a single output", so it strengthens the half of the pairing that is
otherwise just narration.

**Do not cut:** the interview (it is the entry's reason to exist) and character
consistency (without it there is no book).

---

## Submission requirements this drives

- **Public repo** for the whole judging period
- **Demo video, 3 minutes max** — lead with the interview: a child answering
  short questions, and the book assembling itself from the answers
- **Live app URL** on the Hetzner container
- Track **Multimodality**; tick M3 + Speech 2.8 (+ Music 3.0 if used) — the
  models ticked must match the track
- GMI account email must be **the account the generations actually ran on**
- **Share the demo on X tagging MiniMax and GMI Cloud** — required, not optional

---

## Open questions

1. ~~Name~~ — **Thutapi**, at `thutapi.nryn.dev`. Decided 2026-09-04.
2. ~~Poocha overlap~~ — **void.** Poocha is a custom fine-tuned model (the
   Gemma 4 E4B/E2B pair), not a kids app. No product overlap, nothing to check.
3. ~~Music 3.0 free status~~ — **confirmed free** (Narayan, 2026-09-04).

*Spec written 2026-09-04 against verified GMI docs. Scope per Narayan: typed
input, M3 interviews the child rather than taking a finished story, optional
clone, optional music.*
