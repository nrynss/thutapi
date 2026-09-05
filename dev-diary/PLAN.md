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
| **T1b** | DONE. Closed at 52b2cd2 — checks 1, 2, 3, 4 pass against https://thutapi.nryn.dev/healthz (cf-ray present, Let's Encrypt origin cert via DNS-01, all five Traefik labels byte-identical). Check 5 moved to T3 (re-scoped: T1 binary has no static-file route). Run also repaired Traefik's CLOUDFLARE_DNS_API_TOKEN — restored cert renewal for the whole box (thutapi, serp, auteur, eoc, mosaic). Full infrastructure record (incident, resolution, token-replacement procedure) is in `~/work/hetzner/docs/foleyflow-server.md` §5, deliberately kept out of this repo. |
| **T2** | DONE. Closed after a round-3 re-open: round 3 re-reviewed the whole `Done when` (rounds 1–2 had been scoped to round-1 residue plus a diff audit and were correct within that scope) and found **2 × H, 4 × M, 5 × L** — `EditImage` defaulting to the struck-through `Qwen-Image-2512` (would silently break T6's character lock), the `Retry:` contract unimplemented while `errors.go` claimed it, every-unclassified-4xx-`ErrTransient`, `GenerateImage` sharing the same bad default, the raw-bytes/no-live-test `Done when` gap, CI gofmt blind to `internal/`, and five L polish items. Remediation round 3 fixed all eleven; round 4 found **2 × M, 2 × L** (three coverage-floor statements disagreeing, an unrecorded AGENTS.md hunk, `&Reasoning{}` leaking `thinking:{"type":""}` on the wire, `ErrModelNotFound` docstring implying message matching); remediation round 4 fixed those. **Round 5: APPROVE 0/0/0/0, zero residue against rounds 1–4** (t2-round3/4/5.md, t2-remediation-round3/4.md). Defaults: `GenerateImage` → `Flux2-Klein` (§3 Start-on), `EditImage` rejects an empty model; retry contract implemented in both clients (one retry on `ErrTransient` only, request-queue `failed` included, never 4xx, default per-call deadline); catch-all 4xx → `ErrBadRequest`, 402 → `ErrPaymentRequired`; two-tier coverage floor (75% per package, 85% for `internal/gmi/*`). Live-endpoint half lives in **T2b**. |
| **T2b** | **DONE** 2026-09-05. Closes the live half of T2's original `Done when`. Working key installed (252-char JWT; the `sk-or-v1-…` OpenRouter-shaped key recorded in §T2 is replaced). Text: real `Chat` decoded through the typed structs (`finish_reason=stop`, `text="pong"`); bare `MiniMax-M3` 404s with the exact upstream string the package doc quotes and classifies as `ErrModelNotFound` — the prefix quirk is now verified against production, not documentation; `thinking:{"type":"enabled"}` accepted. `/v1/models` lists 82 models incl. `MiniMaxAI/MiniMax-M3`. Media: real TTS round-trip, unknown model → `ErrModelNotFound`. **Finding:** the media package doc is wrong — TTS returns the queue envelope, not a binary blob, with the audio at `outcome.audio_url` on `storage.googleapis.com`, publicly fetchable with no credential (verified 200 `audio/mpeg`, 63,348 bytes, real MP3). That lands on T8 (download and persist) and flags a T13 safety question (a cloned child voice would sit at an unauthenticated public URL). The `need_volumn_normalization` typo was echoed back by the upstream, confirming the pin against production. Probes committed as `//go:build live` tests. Zero cost. See t2b-t5b-live-record.md. |
| **T3** | DONE. Round 1 (t3-round1.md): REMEDIATE 0C/1H/1M/1L — `mediastore.ErrNotFound` documented but returned by no code path, media unbound to pages/cast with no listing queries (T6/T8/T10 unanswerable), five untested error branches. Remediation round 1 fixed all three (kind + page/cast anchor columns with composite FKs and partial unique indexes, `SetMediaPlace`/`BookMedia`/`PageMedia`/`CastMedia`, five branch pins, sentinel contract table). **Round 2: APPROVE 0/0/0/0, zero residue** (t3-round2.md). Restart-survival Done when pinned end to end incl. 206 Range after reopen; live smoke: cross-process WAL write, Range slice byte-exact. |
| **T3b** | DONE. Round 1 (t3b-round1.md): REMEDIATE 0C/0H/2M/2L — running jobs had no cancellation path (`WithoutCancel` + no `Cancel` → leaked slots, no handle for T11/operator on runaway spend), submit bodies with a request_id but unknown/absent status passed through as the result without polling, `Event.Name` newlines could inject SSE field lines, and a deadline landing mid-poll-GET surfaced `ErrTransient` instead of `ErrPollDeadline`. Remediation round 1 fixed all four (cancel registry + `Runner.Cancel` + `StatusCancelled` terminal, request_id-bearing submit bodies now poll, `oneLine` name sanitization, `pollCtx.Err()` guard before transient counting). **Round 2: APPROVE 0/0/0/0, zero residue** (t3b-round2.md) — Cancel-vs-completion proven deterministic (200 interleavings × 20 race runs, exactly-once terminal), all four mutations re-applied by hand and reverted byte-identical. Coverage: stream 94.4%, job 95.7%, media 92.5%. |
| **T4** | DONE. Round 1 (t4-round1.md): REMEDIATE 0C/1H/3M/3L — the stall rule ended interviews after two substantive one-word answers ("Mira" → "red"), end-marker-only replies left ended interviews reopenable, opening-turn failures were invisible to clients, error payloads carried freeform prose instead of machine classes, plus streak-rollback, enforced-end persistence, and doc-comment gaps. Remediation round 1 fixed all seven (end fires only on no-progress/repetition with a per-turn chip-signal directive to the model; closing turn persisted unconditionally on every end path; turn-failure marker surfaced through the catch-up route; sentinel-derived error tokens on the wire). **Round 2: APPROVE 0/0/0/0, zero residue** (t4-round2.md). E2e: start → turns → self-ended (checklist, stall, MaxTurns paths) against a scripted fake, SSE-observable, transcript persisted and ordered; thinking OFF pinned on raw wire. Coverage: interview 93.4%. |
| **T5** | DONE. Round 1 (t5-round1.md): REMEDIATE 0C/0H/3M/2L — transport-error pin had an empty assertion body (swallow mutant went green), the Done when's live half had no owner, the prompt taught "exactly 8 pages" while the validator enforced no count, a prose-embedded decoy JSON object could be taken as the book (full-story decoy silently in one call), and cast uniqueness was case-sensitive (Mira+mira split the T6 lock). Remediation round 1 fixed all five (real transport pins incl. six-sentinel probe; T5b operator row + amended Done-when; `PageCount = 8` taught by prompt and enforced by validator, cut-to-6 changes exactly that rule; extractStory scans all candidates for first decode-AND-validate with first-candidate fallback errors; EqualFold uniqueness with exact references). **Round 2: APPROVE 0/0/0/0, zero residue** (t5-round2.md). Coverage: story 100%. Live half owned by **T5b**. |
| **T5b** | **DONE** 2026-09-05. Closes the live half of T5's original `Done when`: two independent live M3 calls with `thinking` ON over a realistic 14-turn transcript, both schema-valid, both 8 pages, all emotions in the taught vocabulary, every page character present in the cast (14s and 37s). Corrective-system-message acceptance **confirmed** — MiniMax obeyed a `system`-role message placed after an assistant turn, which is the mechanism `story.Structure`'s corrective retry depends on. **Two findings handed to T6** (not T5 defects — the validator does what it was specified to do): live casts contain non-visual members (`Narrator`, `visual:"no visual"` / `"an unseen storyteller with no appearance"` — two phrasings, so no literal match works) which would each burn a ~$0.01 reference sheet on nothing; and one entity can occupy two cast slots (`Grumpy River` + `Happy River`, the latter's visual opening "the same wide blue river"), which the image lock would render as two unrelated rivers — the exact drift T6 exists to prevent. Also: M3's page prompts already carry their own style language, which competes with T6's constant style suffix. Probes committed as `//go:build live` tests. Zero cost. See t2b-t5b-live-record.md. |
| **T6** | **In progress** — implementation agent dispatched 2026-09-05 (agentic loop, `internal/illustrate/**`). Live inputs from T5b passed to it mid-build: non-visual cast members and duplicate-entity cast members both validate and both break a naive per-member reference sheet. Live model resolution confirmed: `Flux2-Klein` and `Z-Image` exist; `Qwen-Image-2512` also resolves and is callable, which is exactly why t2-round3 H1 was dangerous — nothing upstream would ever have errored. Awaiting implementation, then round 1 review. |
| **T7** | Not started. Capability **verified live** 2026-09-04 — see T7. |
| **T8** | Not started. |
| **T9** | Not started. |
| **T10** | Not started. |
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

## T6 — Illustration

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
| `Qwen-Image-2512` | **t2i only — it cannot take a reference image.** An i2i call against it succeeds and ignores the reference. | The image lock, i.e. the whole of §T6 |
| `H3` / any video model | Not free; explicitly out of scope (project.md §Scope) | Budget |

`Qwen-Image-2512` is not a hypothetical: it shipped as the default for both
`GenerateImage` and `EditImage` in T2 and survived two review rounds
(t2-round3.md, H1/M2). Grep for it before closing any track that renders.

**Provider behind a one-line switch.** Start on **Flux2-Klein** or **Z-Image**
($0.01). If characters drift, switch to `gemini-2.5-flash-image` ($0.0387) — the
spread across the whole catalog is about 23 cents a book, so choose on
consistency and never on price. **Do not use `Qwen-Image-2512`:** it is
text-to-image only and cannot take the reference.

Fan out with `errgroup`, bounded to ~4 concurrent.

---

## T7 — Consistency verification

**Owns:** `internal/illustrate/verify.go` (same package as T6 — it is T6's closing loop, not a separate seam).

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

**Owns:** `internal/audio/**`.

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

**Owns:** `static/book/**` and the book template under `internal/web/**`.

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

**Owns:** `internal/gate/**`, the retention sweep in `internal/mediastore/`, and the prewarm fixtures.

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

**Owns:** `internal/audio/music.go`.

**Depends on:** T10. Confirmed free. One `minimax-music-3.0` call plus one
looping `<audio>` at ~0.15 under the narration — perhaps half an hour. Puts a
third MiniMax model on the form and strengthens the "sound" half of a track that
is explicitly *picture and sound as a single output*.

**The cheapest remaining win.** First thing added back if Sunday goes well.

---

## T13 — Voice clone (optional)

**Owns:** `internal/audio/clone.go`.

**Depends on:** T8, and on the public-fetchability check (originally T1
check 5, **moved to T3** — the T1 stub has no file route to prove it with).

`minimax-audio-voice-clone-speech-2.8-turbo`, one synchronous call. Where a
sample is supplied, one short recording can voice the whole cast — narrator
plus dragon at `pitch: -8` with `spacious_echo`, robot with `robotic`, mouse at
`+6`.

`source_audio` is a URL GMI downloads — it does not accept an upload — so the
sample must be served over public HTTPS at a short-lived, unguessable path.

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
