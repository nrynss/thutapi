# Thutapi

A child is interviewed, and the answers become an illustrated, narrated
picture book.

Thutapi asks one short question at a time. *What colour is the dragon?* *Is
she brave, or is she sneaky?* It accepts whatever the child says without
correcting spelling, logic or plausibility. Their ideas are the story. When
it has enough, it structures the transcript into a cast and a page list. It
then illustrates the pages with a consistent cast, narrates them, and
assembles a book you can watch, read and share.

Built for MiniMax Week and GMI Cloud, Multimodality track.

**Try it live:** [thutapi.nryn.dev](https://thutapi.nryn.dev). Start with a
short interview, optionally add a grown-up-approved voice sample, and come
back to a shareable picture book with a PDF and captioned film.

**Demo video:** [Watch the Thutapi demo on YouTube](https://www.youtube.com/watch?v=SEMcJHynnzw).

> **Thutapi** (തുത്താപ്പി) is what Kunjupaathumma affectionately calls Aisha
> in Vaikom Muhammad Basheer's *Ntuppuppakkoranendarnnu*. It is a term of
> endearment for a small girl, on a product where a small girl tells the
> story.

## Why this project

Children have big story ideas before they can reliably type a whole story.
Thutapi keeps their role small and expressive: they answer one warm question
at a time, while the system turns those answers into a complete book. Spoken
questions, an optional music bed, and an optional consented voice sample make
the result more accessible without making the child do more work.

### Hackathon notes

- **Why MiniMax M3:** M3 conducts the interview, structures the transcript,
  and judges each illustrated page against its cast reference. It also helped
  write a substantial portion of this codebase; the product design, prompts,
  tests, review, and final decisions remained human-directed.
- **Why GMI Cloud:** one integration makes M3, Speech 2.8, Music 3.0, and the
  supporting image workflow practical in one production app. Thank you to
  **GMI Cloud** and **MiniMax** for the platform and models that made Thutapi
  possible.
- **What was hard:** TTS capacity failures and Music 3.0 rate limits were real
  integration problems. Thutapi degrades gracefully to a captioned-silent
  film when narration is unavailable, and background music is now opt-in so a
  rate-limited music request never blocks a book.
- **Image choice:** the MiniMax image models available to us did not meet the
  reference-image workflow needed for reliable character consistency. We use
  `seedream-5.0-lite` through GMI Cloud for reference sheets and illustrated
  pages, while M3 performs the consistency review.
- **What is next:** we planned to explore MiniMax H3 for generated video, but
  spent the hackathon making the interview, image consistency, TTS, music, and
  book assembly reliable first. H3 integration is post-hackathon work; today’s
  film is a deliberately dependable captioned MP4 assembled from the finished
  illustrations and narration.

## How it works

The pipeline runs in five stages. Each one is a job, and the browser watches
progress over Server-Sent Events.

1. **Interview.** One question per turn, driven by MiniMax M3. The loop ends
   itself when the checklist is filled or the child starts to tire.
2. **Structuring.** The transcript becomes a typed story. That means a title,
   a locked cast, and eight pages of text.
3. **Illustration.** A reference sheet is drawn per cast member. Every page is
   then drawn from those sheets, so the characters stay recognisable.
4. **Consistency check.** A judge model reviews each page against its cast. A
   page that drifts is regenerated rather than shipped.
5. **Assembly.** Pages are narrated, then muxed into an MP4 with captions. A
   printable PDF is produced from the same pages.

A book always ships. If narration fails, the film is still made with captions
and silence. Both the PDF and the video URL are delivered on every run.

## Models used

All inference runs on GMI Cloud. There is one key, `GMI_API_KEY`, and it is
read at call time so a rotation takes effect without a restart.

| Model | Used for |
|---|---|
| `MiniMaxAI/MiniMax-M3` | The interview, the structuring pass, and the consistency judge |
| `seedream-5.0-lite` | Character reference sheets and the eight page illustrations |
| `minimax-tts-speech-2.8-hd` | Page narration, in the finished book |
| `minimax-tts-speech-2.8-turbo` | Spoken interview questions, read aloud to the child |
| `minimax-music-3.0` | The instrumental music bed under the film |
| `minimax-audio-voice-clone-speech-2.8-turbo` | Optional voice clone, from a consented adult sample |

Four models are refused on sight by the illustration package. Those are
`Qwen-Image-2512`, `Flux2-Klein`, `Z-Image` and `MiniMax-Hailuo-02`. Two are
dead, and two are video models that a page renderer must never call.

## Technologies used

**Backend.** Go 1.27.1, standard library first. That means `net/http`,
`encoding/json`, `html/template` and `golang.org/x/sync/errgroup`. Storage is
SQLite through `modernc.org/sqlite`, which is pure Go, so the binary stays
static with `CGO_ENABLED=0`.

**Frontend.** Preact with `htm` and hooks, vendored into `static/vendor/`.
There is no build step, no bundler and no CDN. Styling is plain CSS with the
Fredoka variable font vendored alongside it.

**Media.** A static `ffmpeg` binary renders the film and mixes the music. PDFs
are produced with `github.com/go-pdf/fpdf`. Page images and audio are stored
on disk under content-addressed, unguessable ids.

**Streaming.** Server-Sent Events carry interview turns and generation
progress. A cold reload catches up from the store, so a refresh never loses a
running book.

**Delivery.** A distroless container on `gcr.io/distroless/base-debian12` runs
as `nonroot`. It sits behind Traefik v3 and Cloudflare.

## Run it

Locally, with Go installed:

```bash
go build ./... && go test ./...
go run ./cmd/thutapi
curl -s localhost:8080/healthz
```

Or as a container:

```bash
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t thutapi:local .
docker run --rm -p 18080:8080 -e PUBLIC_ORIGIN=https://example.test -e UPLOAD_TOKEN=dev thutapi:local
curl -s localhost:18080/healthz
```

`/healthz` reports the commit the binary was built from. A running instance is
always traceable back to its source.

Open `http://localhost:8080/` and click **Make your own book**. Answer a few
questions, skip or record the optional voice sample, choose whether to include
a background music bed, and wait. A full generation takes about six minutes
and costs roughly $0.35.

## Configuration

All configuration is environment. Copy [`.env.example`](.env.example) to
`.env`, which is gitignored, and fill it in.

| Variable | Required | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | Listen port. |
| `DATA_DIR` | no | `/data` | SQLite database and generated media. |
| `GMI_API_KEY` | **yes** | none | GMI Cloud inference key. Never committed. |
| `PUBLIC_ORIGIN` | **yes** | none | Public HTTPS origin GMI fetches a voice sample from. Private, local and bare IP origins are rejected. Not secret. |
| `UPLOAD_TOKEN` | **yes** | generated at deploy | Bearer token for `POST /voice-sample`. The process refuses to start without one. |
| `GATE_PASSCODE` | no | none | When set, every paid route also requires this shared passcode. |
| `PREWARM_DIR` | no | `$DATA_DIR/prewarm` | Finished books restored at boot, so the shelf is never empty. |

## Routes

| Route | Purpose |
|---|---|
| `GET /` | The shelf. Start here. |
| `GET /interview/{id}` | The interview, and the wait |
| `GET /book/{id}` | A finished book, shareable and public |
| `GET /book/{id}/download/{kind}` | The MP4 or the PDF |
| `GET /media/{id}` | One stored image, clip or film |
| `GET /healthz` | Liveness and the build commit |

Anything that spends model time is rate limited. That covers starting an
interview, answering a turn, generating a book and uploading a voice sample.
Reading a book is never gated, because a shared link must open for anyone.

## Images and deployment

Images are published to GitHub Container Registry manually, never on commit.
An image is a deployment artifact, not a byproduct of committing.

```bash
gh workflow run image.yml -f latest=true
```

The package is public, so the box pulls it with no registry credential.
Deployment runs behind an existing Traefik v3 edge. Operator details are in
[`deploy/README.md`](deploy/README.md).

`.github/workflows/verify.yml` runs `go vet`, `go test -race`, a coverage
floor, `gofmt` and a shell syntax check. It skips documentation-only commits.

## Layout

```
cmd/thutapi/     process entry, routes and wiring
internal/        packages (gmi, story, illustrate, audio, bookgen, gate, ...)
static/          vendored Preact shell, no build step
deploy/          run script and operator notes
dev-diary/       product spec, build plan, review records
```

## House rules

These bind every contributor, human or agent. The full text is in
[`AGENTS.md`](AGENTS.md), and the build plan is in
[`dev-diary/PLAN.md`](dev-diary/PLAN.md).

**The stack is frozen.** Go standard library first. Preact with `htm` and
hooks, written as tagged templates with backticks. No Tailwind, no bundler, no
CDN and no build step. Each of those would reintroduce tooling this project
deliberately does not have.

**Every track is reviewed adversarially.** The cycle is implement, review,
remediate, re-review, then approve. A track closes only on a clean round with
zero residue. Live verification against real endpoints splits into its own
`b` track.

**Never relax a validator to make a fixture pass.** A loud refusal is the
product working correctly. Weakening a check to get green is the one failure
this process exists to prevent.

**Do not edit a closed track's package without a declared contract row.**
Write down what you are touching and why, in the plan, before you touch it.

**Tests must pin decisions, not just lines.** A coverage number catches an
untested package. It does not catch an untested decision, such as a wrong
default that no test exercises.

**Secrets never enter the repository or a shell history.** The inference key
lives in a root-owned file on the host. It reaches the container by name, so
it never appears in a command line.

**Child-facing copy carries no error tokens.** Render warmth, never a class
name. Failures offer two doors, never one, and never a dead end.

**Style is enforced, not suggested.** `gofmt` runs over the whole tree in CI.
Formatting drift between contributors is removed by tooling rather than by
argument.

## License

None. **Copyright © 2026. All rights reserved.**

This source is published for review and judging. It is not open source. No
permission to use, copy, modify or distribute is granted.
