# Thutapi Agent Protocol

This file is binding for every human or coding agent working in this repository.
It governs the workflow. The product spec is
[`dev-diary/project.md`](dev-diary/project.md); the build plan with per-track
status is [`dev-diary/PLAN.md`](dev-diary/PLAN.md); the review loop is defined
in [`dev-diary/adversarial-review/README.md`](dev-diary/adversarial-review/README.md).

## Process — implement → review → remediate → re-review → APPROVE

Every track runs through this loop until **APPROVE** with **zero findings
(0/0/0)** across all severities. There are no exceptions.

1. **Implement.** Edit only paths listed in the track's `Owns` section of
   `dev-diary/PLAN.md`. Stay inside the seam.
2. **Review.** A reviewer reads the diff against the spec and writes
   `dev-diary/adversarial-review/t<N>-round<K>.md` with verdict
   (`REMEDIATE` / `APPROVE`), findings counted by severity (C / H / M / L),
   and per-row **Where / What / Pin (failing probe or test) / Mutation (what
   breaks if reverted)**. Reviewers do not remediate their own findings.
3. **Remediate.** For each finding, write the fix into
   `dev-diary/adversarial-review/t<N>-remediation-round<K>.md` with one row
   per finding. **No severity is exempt.** Only truly trivial L-severity fixes (a one-line doc typo or type annotation that needs no review) may be committed in-line with the track and recorded as a note in the round's review file.
4. **Re-review.** Re-run the same review against the new commit. Repeat 2–4
   until verdict is **APPROVE** with an explicit "zero residue" claim against
   all prior rounds.
5. **Commit.** Land the APPROVE'd track on `main` as one focused commit
   (or a small sequence if the track spanned several seams).

**Severities.** C = breaks the demo. H = real defect the demo survives.
M = real defect with workaround. L = polish. Per
`dev-diary/adversarial-review/README.md`.

**Lambo memory MCP is mandatory.** `lambo_recall` before starting a track,
`lambo_derive` / `lambo_record_action` after each meaningful change. Use one
stable `agent_id` for the session so locks and attribution are coherent.

## Read order

Before changing a file, read these in order:

1. This file in full.
2. The track's section in `dev-diary/PLAN.md`.
3. Any decisions and open questions in `dev-diary/project.md` that touch the
   track.
4. The relevant `dev-diary/adversarial-review/t<N>-round*.md` history.
5. The current code paths you will edit and their tests.

Pre-flight assertion: *I own every path I will edit, the cross-package shapes
I need already exist, and I can validate this track independently.* If any
part is false, stop and propose a contract change in the review file.

## Stack and ground rules

These are frozen and reviewed on sight:

- **Backend: Go 1.27.1, stdlib-first.** `net/http`, `encoding/json`,
  `html/template`, `sync`, `golang.org/x/sync/errgroup`. Build with
  `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`. Ship on
  `gcr.io/distroless/base-debian12:nonroot`, `EXPOSE 8080`, listen on
  `0.0.0.0:${PORT}`.
- **Frontend: Preact + `htm` + hooks, vendored into `static/vendor/`, no build
  step.** Not Svelte 5 (runes thin in training data, fails silently). Not
  vanilla JS (same silent DOM-sync class of bug).
- **htm pinning.** Tagged-template JSX with backticks and `${}`:
  ``html`<div class=${cls}>${kids}</div>` `` — never `<>` / `{}`. Drift is a
  common model failure mode.
- **Hooks, not signals.** `useState` / `useEffect`; reach for
  `@preact/signals` only if hook prop-drilling actually hurts.
- **Server-rendered shell.** Go's `html/template` renders the page so a cold
  link works without JS.
- **No Tailwind.** Reintroduces a build step we don't want.

## GMI endpoints — both clients must live

- **Text.** `https://api.gmi-serving.com/v1/chat/completions`,
  OpenAI-compatible. Model id is the full **`MiniMaxAI/MiniMax-M3`**. The bare
  `MiniMax-M3` 404s. Reasoning is enabled only by body field
  `thinking:{"type":"enabled"}`; `reasoning_effort` is ignored by MiniMax
  models.
- **Image and audio.** `https://console.gmicloud.ai/api/v1/ie/requestqueue/apikey/requests`,
  `{model, payload}` envelope.
- **TTS typo.** `need_volumn_normalization: true` (spelled `volumn`) and
  `need_noise_reduction: true` — match the typo or it is silently ignored.
- **`source_audio`** for voice clone must be a public URL GMI can fetch;
  images take inline base64 in the text client.

## Safety and data rules

- `GMI_API_KEY` is **never in the repo.** Pass by `docker run -e`. The repo
  is public for the whole judging period; sweep history before going public.
- The demo uses synthetic / child-generated content. No real PII, no real
  voices uploaded to the repo, no real voice clones stored alongside source.
- Voice samples are short-lived and unguessable URLs, not stable paths — it is
  a child's voice.
- Generated media accumulates on disk; cap retention.
- A public generate button is an open wallet at ~$0.01 an image. Gate behind
  a passcode or per-IP cap.

## Two-day cut list

**Cut first:** music (T12), voice clone (T13), multi-character voices, page
count down to 6, controlnet.

**Do not cut:** the interview (T4 — it is the entry's reason to exist) and
character consistency (T6 — without it there is no book).

**Never cut:** submission (T14). An unsubmitted project scores zero.

## Definition of done (per track)

A track is done when **all** of the following are true:

1. `dev-diary/PLAN.md` shows the track's status as `DONE`.
2. `dev-diary/adversarial-review/t<N>-round<K>.md` ends in **APPROVE** with
   an explicit zero-residue claim against every prior round.
3. The track's `Done when` line in `dev-diary/PLAN.md` is met end-to-end.
4. The acceptance test or smoke test cited in the review's `Pin` column
   passes from a clean tree on a fresh checkout.
5. The track is committed to `main`.

## File layout

```
/
├── AGENTS.md                            ← this file
├── Dockerfile                           ← distroless static, multi-stage
├── go.mod / go.sum
├── cmd/thutapi/                         ← process entry
├── internal/                            ← packages (stream, gmi, store, …)
├── static/                              ← Preact shell + vendored vendor/
│   ├── vendor/preact.{js,mjs,hooks.js,htm.js,…}
│   └── …
├── data/                                ← gitignored: SQLite, generated media
└── dev-diary/
    ├── project.md                       ← product spec
    ├── PLAN.md                          ← tracks, status, decisions
    └── adversarial-review/
        ├── README.md                    ← loop definition
        ├── t<N>-round<K>.md              ← review
        ├── t<N>-remediation-round<K>.md ← fixes
        └── …
```