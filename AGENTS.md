# Thutapi Agent Protocol

Binding for every human or coding agent working in this repository. It governs
how work runs. What the product is lives in
[`dev-diary/project.md`](dev-diary/project.md).

The old structure of phases, numbered tracks, `Owns` lists and a frozen stack is
over. `dev-diary/PLAN.md` and `dev-diary/adversarial-review/t*-round*.md` are
history: read them for the reasons behind the code, but they no longer say what
you may touch or what counts as done. Nothing is frozen. A change to the stack,
a package boundary or this file is an ordinary change, made on an issue.

## How work runs

One issue, one branch, one pull request.

1. **Issue.** Every change starts from an issue on `nrynss/thutapi`. It says
   what is wrong or missing, why now, the shape of the fix, and what is out of
   scope. A failure seen in production is an issue before it is a patch.
2. **Branch.** Work on `issue-<N>-<slug>` off `main`. Push as you go.
3. **Gate.** Every commit passes the same checks CI runs (see CI and images
   below) in a clean checkout of the branch. A commit that fails its own gate
   does not exist.
4. **Review.** A pull request is reviewed by an agent or human who did not write
   it. The reviewer reads the diff against `main` and the issue, runs the gate
   and every pin it cites, and writes the findings in the pull request (or a
   file under `dev-diary/reviews/` for anything worth keeping). The review opens
   with the commit hash it reviewed.
5. **Verdict.** APPROVE or REMEDIATE, with a count of findings per severity and
   one row per finding. On REMEDIATE the author fixes every finding as new
   commits and a reviewer who did not write the fix looks again. Repeat until a
   round returns APPROVE with zero findings.
6. **Land.** Merge with `Closes #<N>`. One change, one landing. Do not land two
   approved changes in one commit.

A reviewer does not fix what they find, and an author does not review their own
change: an agent that does both narrows the finding until its own fix is enough.

**Severities.**

| Level | Meaning |
|---|---|
| **C** | Breaks the product, loses data, leaks spend, or exposes a child's media. |
| **H** | A real defect the product survives. |
| **M** | A real defect with a workaround. |
| **L** | Polish. |

Every severity gets fixed. "It is only an L" is not a disposition. A reviewer may
record a finding as a false positive; that is a judgement about whether the
defect is real, never about whether a real defect deserves a fix. The one
exemption: a doc typo or comment fix that cannot change behaviour may be fixed
inline by the author and noted in the review. A comment that misdescribes
behaviour is not that: it carries the severity of the behaviour it misdescribes.

**A finding carries two things.**

- **A pin**: an independent measurement of the defect, or a failing test. `curl`
  a running server, query the SQLite file with a fresh connection, hash the
  stored blob. Software reporting its own success is the thing under review, not
  evidence for it. A pasted console transcript is not a pin.
- **A mutation**: the one- or two-line edit that reintroduces the defect and
  turns the pin red. It proves the pin is load-bearing. Run mutations on a
  scratch copy, never on the branch under review.

A real defect outside the paths the pull request touches is recorded as
`OUT_OF_SCOPE` with its severity and pin, never blocks the verdict, and gets its
own issue.

**Waiving review.** Only the operator can waive review or a round of it, and only
in the issue or pull request, in words. A waiver is recorded where the work is
recorded, with what was not reviewed. It is never assumed from urgency.

## Live verification

Work that needs credentials, an operator or the world is verified with a probe,
not a transcript.

- Probes live behind `//go:build live` in the package they exercise
  (`live_test.go`). They never run in CI and never gate a build: a live call is
  evidence, and a red build caused by someone else's outage teaches nothing.
- Commit the probe, not a transcript. A pasted console log cannot be re-run.
  Paste the transcript into the issue as well, because the interesting part is
  usually the response shape rather than pass or fail.
- Guessing a response format and building on it is the expensive option. Paid
  calls are fine when the paid call is the one that settles the shape. Where a
  probe only needs auth and routing, prefer a path that rejects a bad id before
  doing any billable work.

## Stack

Go, standard library first: `net/http`, `encoding/json`, `html/template`, `sync`,
`golang.org/x/sync/errgroup`. `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`.
Ship on `gcr.io/distroless/base-debian12:nonroot`, `EXPOSE 8080`, listen on
`0.0.0.0:${PORT}`.

The direction is Keel for the backend building blocks and Chaaya with Svelte 5
for the UI, with Vertex AI and FAL as providers behind per-role config (issues
#2, #3, #4). Until each move lands, the code you are editing is what is on
`main`: a Preact shell vendored into `static/vendor/` with no build step, a
server-rendered `html/template` page so a cold link works without JS, and the
GMI clients. Adding a build step, a dependency or a framework is allowed when
an issue calls for it and a reviewer agrees.

Defaults that still hold because they are cheap to keep: no Tailwind; htm
templates use backticks and `${}`, never `<>` or `{}`; hooks before signals.

## Go style, enforced

`gofmt` and `go vet` catch none of these, which is exactly why they are written
down. A reviewer rejects drift against them mechanically.

**Types and pointers**

- No `**T`. No pointer to a slice, map, channel, func or interface.
- `any`, not `interface{}`. `any` is not a data model: every provider response
  shape is a named struct, never `map[string]any` at a call site. If a union
  genuinely needs `any` on the wire, give the type an `UnmarshalJSON` that
  resolves it into typed fields, and never document a field as holding a named
  struct that `encoding/json` cannot actually produce.
- Small structs go by value (below about 64 bytes a pointer buys nothing).
- The zero value is usable, or a constructor makes it so. Not both halves
  optional.

**Functions and APIs**

- `ctx context.Context` is the first parameter of anything that does I/O. Never
  stored in a struct field. Never `context.TODO()` in committed code.
- Accept interfaces, return concrete types. The consumer declares the interface,
  one or two methods wide. A package does not export an interface for its own
  struct.
- Never return a non-nil value alongside a non-nil error.
- No naked returns. Named results only when a deferred function assigns to them.
- Every exported identifier has a doc comment starting with its own name.
- No `panic` and no `log.Fatal` outside `func main`. A library returns errors.

**Errors**

- One sentinel per distinct condition; wrap with `%w`; compare with `errors.Is`
  or `errors.As`. Never match a substring of a provider's error message.
- Error strings are lowercase with no trailing period and no error code the user
  sees. Child-facing strings are governed separately, below.
- `_ = f()` needs a comment on the same line saying why the error cannot matter.
- A retry classification is part of the error's contract. If a sentinel says
  "retryable", every status mapped to it must actually be safe to retry. Mapping
  an unknown 4xx to a retryable sentinel is a defect, not a default.

**Concurrency**

- Every goroutine has a defined exit path, named in a comment.
- Fan-out uses `errgroup` with `SetLimit`, never an unbounded `go` loop.
- `go test -race` is mandatory. A race is a C.

**Comments describe behaviour.** A comment that describes a retry, a fallback or
a default the code does not implement is a defect at the severity of the
behaviour it misdescribes, because the next agent will code against it. Two
docstrings in one package that contradict each other is an automatic finding.

## Testing: what "enough" means

Statement coverage is a floor, not the target. A package once sat at 85% while
shipping the wrong model constant, because the two statements that carried it
were `if model == "" { ... }` defaults and every test passed an explicit model.

1. Every exported function has at least one test.
2. **Every default is exercised through its default path.** If a function
   substitutes a value when an argument is empty, a test calls it with that
   argument empty and asserts what reached the wire. A zero-value config reaching
   production behaviour (a pacer, a limit, a timeout) gets the same treatment: a
   test that fails if the zero value silently becomes something else.
3. Every error branch has a test asserting the sentinel with `errors.Is`,
   including the branches you expect never to fire, which are where the wrong
   classification hides.
4. **Every documented upstream quirk gets a raw-wire pin test, named for the
   quirk.** It asserts against the marshalled bytes (or the raw reply), not a Go
   struct, so a rename cannot silently unfix it. Examples already in the tree:
   the `MiniMaxAI/` model prefix, the `thinking` body field, the
   `need_volumn_normalization` typo, and a judge reply with NUL bytes scattered
   through otherwise valid JSON.
5. Table-driven past two cases. `t.Setenv`, never `os.Setenv`. The unit suite
   uses `httptest`; no live call runs under `go test ./...`.
6. **Tests must not be flaky by construction.** Anything that depends on random
   draws, goroutine order or the wall clock injects a seed or a clock. A test
   that can redden CI on a loaded runner is a defect. Run new concurrency tests
   with `-race -shuffle=on -count=5` and under CPU load before you call them done.
7. A done condition is mechanically checkable from a clean tree. If it says "a
   test does X", that test exists and runs. If only an operator can check it, it
   is a live probe (above), not prose.
8. **Coverage floor: 75% of statements per package, 85% for
   `thutapi/internal/gmi/*`**, enforced in `verify.yml`. The floor exists to
   catch an untested package; rules 1 to 6 are what catch an untested decision.

## Providers today: GMI

The code currently talks to GMI. These facts stay until the provider move
(issue #3) lands; the provider layer will carry them as per-role config.

- **Text.** `https://api.gmi-serving.com/v1/chat/completions`, OpenAI-compatible.
  The model id is the full **`MiniMaxAI/MiniMax-M3`**; the bare `MiniMax-M3`
  404s. Reasoning is enabled only by the body field `thinking:{"type":"enabled"}`;
  `reasoning_effort` is ignored by MiniMax models.
- **Image and audio.**
  `https://console.gmicloud.ai/api/v1/ie/requestqueue/apikey/requests`, with a
  `{model, payload}` envelope.
- **Rate limits are per model per account.** The image model allows 2 requests
  per 60 seconds. Pace and retry (`internal/illustrate`); do not rely on
  concurrency caps to stay under a per-minute rate.
- **TTS typo.** `need_volumn_normalization: true` (spelled `volumn`) and
  `need_noise_reduction: true`. Match the typo or it is silently ignored.
- **`source_audio`** for voice clone must be a public URL GMI can fetch; images
  take inline base64 in the text client.

## CI and images

- **`verify.yml` runs on code pushes and pull requests:** `go vet ./...`,
  `go test ./... -race -cover`, the two-tier coverage floor, `gofmt -l .` across
  the whole tree (never narrowed to a subtree: that switches off the mechanism
  that removes drift between agents), and `bash -n` on the deploy script. It
  ignores doc-only changes. The workstation runs the same checks.
- **`image.yml` is `workflow_dispatch` only.** Publishing an image is a
  deliberate act, never a side effect of committing. A successful image run
  triggers `deploy.yml`, which has a health gate and rolls back on failure.
  `gh workflow run image.yml -f latest=true`.
- **Deploy a SHA tag, not `latest`,** for anything you need to reproduce.
- **Container logs reset on every deploy.** Capture what you need from them
  before shipping, or it is gone.
- **Secrets never enter the repo or an image layer.** `GMI_API_KEY` flows
  `/etc/thutapi/env` (root, 0600) to the deploy script's environment to docker's
  name-only `--env GMI_API_KEY` to the container. The name-only form is
  deliberate: the secret never enters the argument list and is not visible to
  `ps`. Never `export` it in an interactive shell; that writes it to shell
  history in plaintext. The origin IP of the box is in the gitignored `.env` as
  `ORIGIN_IP`.

## Safety and data rules

- No API key in the repo. Sweep history before making anything public.
- Content is synthetic or child-generated. No real PII, no real voices in the
  repo, no real voice clones stored alongside source. Issues and pull requests
  name no children and quote no stories.
- Voice samples are short-lived and unguessable URLs, not stable paths. It is a
  child's voice.
- Generated media accumulates on disk; cap retention.
- A public generate button is an open wallet. Gate it behind a passcode, a
  per-IP cap and, as it lands, a budget that refuses before the paid call.
- Do not leave background processes behind. Bound every load-test child with
  `timeout`, and do not write `until pgrep` loops that match themselves.

## Repository layout

```
/
├── AGENTS.md                ← this file
├── README.md                ← public-facing
├── Dockerfile               ← distroless static, multi-stage
├── .env.example             ← copy to .env (gitignored)
├── .github/workflows/       ← verify.yml, image.yml (manual), deploy.yml
├── deploy/                  ← docker-run.sh, redeploy.sh, operator notes
├── cmd/thutapi/             ← process entry
├── internal/                ← packages (stream, gmi, store, illustrate, ...)
├── static/                  ← the UI shell and vendored vendor/
├── data/                    ← gitignored: SQLite, generated media
└── dev-diary/               ← product spec, history, review records
```

Anything that points at `PLAN.md`, a track number, or an `Owns` line in code,
comments or tests is stale: rewrite it so it states the reason itself.
