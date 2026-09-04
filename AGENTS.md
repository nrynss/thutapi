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

## Agentic development — the orchestrator and its agents

The loop above is executed by **four distinct roles**. They are separate on
purpose: an agent that reviews its own work is not adversarial, and an agent
that both finds and fixes a defect will quietly narrow the finding until the
fix it already wrote is sufficient.

| Role | Does | Never does |
| --- | --- | --- |
| **Orchestrator** | Picks the track. Dispatches the other three. Gates the loop and refuses to advance on a non-clean verdict. Lands the commit. Fixes trivially-exempt defects itself (below). | Implement a track. Write a review verdict. Decide that a finding "isn't worth it". |
| **Implementation agent** | Implements the track inside its `Owns` paths (PLAN.md). Stays in the seam. Raises a contract change in the review file rather than reaching outside it. | Review its own work. Mark the track done. |
| **Review agent** | Reads the diff against the spec. Writes `t<N>-round<K>.md` with a verdict, findings counted by severity, and Where / What / Pin / Mutation per finding. | Fix anything it found. Soften a finding because the fix looks expensive. |
| **Remediation agent** | Fixes every finding. Writes `t<N>-remediation-round<K>.md`, one row per finding. | Change the verdict. Fix things nobody found (that is scope creep, and it arrives unreviewed). |

**A fresh agent per role per round.** The round-2 reviewer should not be the
round-1 reviewer, and neither should have been the implementer. Re-using an
agent across roles is how a review round becomes a formality — and T2 is the
worked example: rounds 1 and 2 were sound within the scope they took, but
nobody re-opened the full `Done when`, and two H-severity defects sat behind an
APPROVE for a day (`adversarial-review/t2-round3.md`).

**The cycle does not end early.** implement → review → remediate → review →
… → **APPROVE with 0/0/0 and an explicit zero-residue claim against every
prior round**. A verdict of REMEDIATE always costs another full round; there is
no "fixed it, close it out" path.

**Every severity gets fixed.** C, H, M and L all land a row in the remediation
file. "It's only an L" is not a disposition. A reviewer may record a finding as
a **false positive** — round 1's L1 was, correctly — but that is a judgement
about whether the defect is real, not about whether a real defect is worth
fixing.

*(If you are working in P0–P3 vocabulary: P0 = C, P1 = H, P2 = M, P3 = L.
This repo writes C/H/M/L; use it in review files so the counts stay
comparable across rounds.)*

### The one exemption — the orchestrator's fast path

An **L (P3)** finding that is a doc typo, a comment fix, or a similar triviality
**that cannot change behaviour** may be fixed by the orchestrator directly, in
the same commit, without a remediation agent and without another review round.
The orchestrator then closes the track and moves on.

Conditions, all four required:

1. The finding is **L**. Never M or above, whatever the fix looks like.
2. The fix **cannot alter behaviour** — prose, a comment, a name in a doc. If
   the change touches a value, a branch, or a signature, it is not exempt.
3. It is **recorded by name and file** in that round's review file, so the
   audit trail shows who fixed what without a remediation round.
4. **If it is arguable, it is not trivial.** The exemption is for cases with no
   judgement in them at all.

**A comment that misdescribes behaviour is not a doc defect.** It carries the
severity of the behaviour it misdescribes, because the next agent will code
against it — see §Go style, "Docs about behaviour must match the behaviour". A
docstring claiming a retry that does not exist is an H, not an exempt typo,
and it goes through the full cycle like anything else.

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

## Go style — enforced, not suggested

Three different models write Go here. These rules exist so a reviewer can
reject drift mechanically instead of arguing taste. `gofmt` and `go vet` catch
none of them, which is exactly why they are written down.

**Types and pointers**

* **No pointer to a pointer.** `**T` never appears. If you think you need one,
  you want to return a value, or the caller wants a different type.
* **No pointer to a slice, map, channel, or func.** `*[]T` and `*map[K]V` are
  already-reference types behind a second indirection; they are a bug or a
  sign the function should return the value.
* **No pointer to an interface.** `*io.Reader` is almost always a mistake —
  interfaces hold pointers already.
* **`any`, not `interface{}`.** Go 1.18 renamed it; keep one spelling.
* **`any` is not a data model.** Every GMI response shape is a named struct in
  `internal/gmi` — never `map[string]any` at a call site. If a union genuinely
  needs `any` on the wire, give the type an `UnmarshalJSON` that resolves it
  into typed fields, and never document a field as holding a named struct that
  `encoding/json` cannot actually produce. (Cost of getting this wrong:
  `t2-round3.md` L1.)
* **Small structs go by value.** Below ~64 bytes a pointer parameter buys
  nothing and costs nil-checks.
* **Zero value usable, or a constructor that makes it so.** Not both halves
  optional.

**Functions and APIs**

* **`ctx context.Context` is the first parameter of anything that does I/O.**
  Never stored in a struct field. Never `context.TODO()` in committed code.
* **Accept interfaces, return concrete types** — and the interface is declared
  by the *consumer*, one or two methods wide. A package does not export a
  `Client` interface for its own struct. (See PLAN.md §Architectural
  invariants 3.)
* **Never return a non-nil value alongside a non-nil error.** A caller must be
  able to trust that `err != nil` means the rest is meaningless.
  (`t2-round3.md` L3.)
* **No naked returns.** Named results only when a deferred function assigns to
  them.
* **Every exported identifier has a doc comment starting with its own name.**
* **No `panic` and no `log.Fatal` outside `func main`.** A library returns
  errors.

**Errors**

* One sentinel per distinct condition; wrap with `%w`; compare with
  `errors.Is` / `errors.As`. **Never match a substring of a provider's error
  message.**
* Error strings are lowercase, no trailing period, no error code the user sees
  (child-facing strings are governed separately, below).
* `_ = f()` requires a comment on the same line saying why the error cannot
  matter.
* **A retry classification is part of the error's contract.** If a sentinel
  says "retryable" in its doc comment, every status mapped to it must actually
  be safe to retry — mapping unknown 4xx to a retryable sentinel is a defect,
  not a default. (`t2-round3.md` M1.)

**Concurrency**

* Every goroutine has a defined exit path. Fan-out uses
  `golang.org/x/sync/errgroup` with `SetLimit`, never an unbounded `go` loop.
* `go test -race` is mandatory and non-negotiable; a race is a C-severity
  finding.

**Docs about behaviour must match the behaviour.** A comment that describes a
retry, a fallback, or a default that the code does not implement is a defect at
the severity of the behaviour it misdescribes — not a typo. Two docstrings in
the same package contradicting each other is an automatic finding.
(`t2-round3.md` H2.)

## Testing — what "enough" means

Statement coverage is a floor, not the target. T2 sat at 85.1% on
`internal/gmi/media` while shipping the wrong model constant, because the two
statements that carried it were the `if model == "" { ... }` defaults and every
test passed an explicit model. Coverage counted the lines and missed the
decision.

So, per track:

1. **Every exported function has at least one test.**
2. **Every default value is exercised through its default path.** If a
   function substitutes a value when an argument is empty, a test calls it with
   that argument empty and asserts what reached the wire. This is the single
   rule that would have caught `t2-round3.md` H1 two rounds earlier.
3. **Every error branch has a test asserting the sentinel** with `errors.Is` —
   including the branches you expect never to fire, which are where the wrong
   classification hides.
4. **Every documented upstream quirk gets a raw-wire pin test, named for the
   quirk.** `TestSynthesizeSpeech_TypoPinnedInRawJSON` is the model: it asserts
   against the marshalled JSON bytes, not against a Go struct, so a rename
   cannot silently unfix it. The `MiniMaxAI/` prefix, the `thinking` body
   field, the `need_volumn_normalization` typo and the five Traefik labels all
   need one.
5. **Table-driven past two cases.** `t.Setenv`, never `os.Setenv`. `httptest`,
   never a live call — live probes are transcripts pasted into a review file,
   and they are evidence, never a CI gate.
6. **A `Done when` line must be mechanically checkable from a clean tree.**
   If it says "an integration test does X", such a test exists and runs. If the
   check can only be performed by an operator, it goes in a `b`-suffixed track
   (the T1/T1b split is the precedent), not in prose.
7. **Coverage floor: 75% of statements per package**, enforced in
   `verify.yml`. It rises to 85% once T2's round-3 remediation lands. The floor
   exists to catch an untested *package*; rules 1–4 are what catch an untested
   *decision*.

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

## CI and images

* **`verify.yml` runs on code pushes and PRs** — `go vet ./...`,
  `go test ./... -race -cover`, a **75%-per-package coverage floor**,
  **`gofmt -l .` across the whole tree**, and `bash -n` on the deploy script.
  It ignores doc-only changes; the dev-diary moves far more often than the code
  and a green tick on prose is noise.
  * The gofmt step was scoped to `cmd/` until 2026-09-04, which meant every
    line under `internal/` — i.e. every remaining track — went unchecked by
    the mechanism §Stack relies on to remove drift between three agents. If
    you narrow it again, you switch that off (`t2-round3.md` M4).
  * The coverage floor catches an untested *package*. It does not catch an
    untested *decision* — T2 shipped the wrong model constant at 85.1%. See
    §Testing for the rules that do.
* **`image.yml` is `workflow_dispatch` only.** Publishing an image is a
  deliberate act, never a side effect of committing — otherwise the
  registry fills with builds nobody asked for and `latest` drifts under the
  running box. `gh workflow run image.yml -f latest=true`.
* **Deploy a SHA tag, not `latest`**, for anything you need to reproduce.
* **Secrets never enter the repo or an image layer.** `GMI_API_KEY` flows
  shell env → `docker run -e` → container. The origin IP of the box is in
  the gitignored `.env` as `ORIGIN_IP`; the repo is public.

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
5. `go vet ./...`, `go test ./... -race`, `gofmt -l .` and the coverage floor
   are all green — i.e. `verify.yml` would pass on the track's own paths.
6. The track's new code satisfies §Go style and §Testing: default paths
   exercised, every error branch asserted with `errors.Is`, and a raw-wire pin
   test for every upstream quirk the track encodes.
7. Every path the track touched is inside its `Owns` list in `PLAN.md`, with
   the single sanctioned exception of one route line in `newServer`
   (PLAN.md §Architectural invariants 5).
8. The track is committed to `main`.

## File layout

```
/
├── AGENTS.md                            ← this file
├── README.md                            ← public-facing; what this is
├── Dockerfile                           ← distroless static, multi-stage
├── .env.example                         ← copy to .env (gitignored)
├── .github/workflows/
│   ├── verify.yml                       ← vet/test/gofmt on code changes
│   └── image.yml                        ← GHCR publish, MANUAL only
├── deploy/
│   ├── docker-run.sh                    ← the five Traefik labels + proxy net
│   └── README.md                        ← operator notes for the box
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