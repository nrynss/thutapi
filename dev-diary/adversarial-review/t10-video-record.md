# T10 — book-video verification record

**Date:** 2026-09-05. **Operator:** orchestrator session.
**Precedent:** T1b / T2b / T5b / T6b — a capability closes on transcripts, not
on argument.
**Cost:** $0.00. Every input was already on disk: the eight real page renders
from T6b item 2 (`data/live/t6b-book/`). No GMI call was made.
**Outcome:** the render → mux → file half of the generation path is
**VERIFIED**, including inside the shipping container. The gaps that remain
are named in §What is NOT verified — read that section before quoting this one.

---

## Why this record exists

The book-delivery decision (PLAN.md §Decisions row 13, 2026-09-05) replaced
the flipbook with an MP4. That decision is only as good as the claim that
ffmpeg can actually turn T6b's renders plus T8's narration into a playable,
downloadable file inside a distroless image with no shell. T6's round-1 H1 is
the standing lesson on what an unchecked assumption costs, so the claim was
executed rather than asserted.

## Inputs

`data/live/t6b-book/page-01..08.jpg` — the eight pages of *Mira and Bramble's
Long Day*, as rendered live by T6b item 2. All eight are 1792×2240 baseline
JPEG (4:5).

Narration is **stand-in**: T8 has not run, so each page got a generated AAC
clip of a different length (3.7 s … 8.6 s) to prove the timing model does not
depend on any duration being known in advance.

## Run 1 — segments and concat, workstation ffmpeg n9.0.1

Per page, one segment:

```console
$ ffmpeg -loop 1 -i page-0N.jpg -i narration-N.m4a \
    -vf "scale=1080:1350:force_original_aspect_ratio=decrease,pad=1080:1350:(ow-iw)/2:(oh-ih)/2:color=0x1b1614,setsar=1,format=yuv420p" \
    -r 25 -c:v libx264 -preset veryfast -crf 20 \
    -c:a aac -b:a 128k -ar 44100 -ac 2 -shortest -movflags +faststart seg-N.mp4
$ ffmpeg -f concat -safe 0 -i list.txt -c copy -movflags +faststart book.mp4
```

```console
encode wall: 13.3s
-rw-r--r-- 1 nryn nryn 2622406 book.mp4
codec_name=h264   width=1080  height=1350  nb_frames=1228
codec_name=aac    nb_frames=2131
format_name=mov,mp4,m4a,3gp,3g2,mj2
duration=49.223220
```

**49.223220 s is the sum of the eight stand-in clips, exactly.** That is the
load-bearing result: `-shortest` makes each page hold precisely as long as its
own narration, so nothing calls `ffprobe`, nothing sums durations, and a
re-narrated page cannot drift. Eight pages encode and join in 13.3 s.

## Run 2 — the file as a standalone artifact

Because the MP4 is downloadable, it leaves the site — uploaded to YouTube,
sent on, posted. A bare page sequence on someone else's timeline carries no
title and no attribution, so the cards are part of the deliverable:

* **Title card** — page 1 through `boxblur=18:2,eq=brightness=-0.22:saturation=0.8`
  with the story title and the child's byline drawn over it. No design asset.
* **End card** — flat `0x1b1614`, "made with Thutapi" and the domain.
* Both are ordinary segments built to the same geometry, so they join with
  `-c copy` like any page — no second encode pass.

```console
-rw-r--r-- 1 nryn nryn 2691992 Mira-and-Brambles-Long-Day.mp4
duration=55.723220
TAG:title=Mira and Bramble's Long Day
TAG:artist=Thutapi
TAG:comment=Made with Thutapi - thutapi.nryn.dev
```

**Text is passed with `textfile=`, never `text=`.** The title is model output
and a child's own words; `Mira and Bramble's Long Day` already carries an
apostrophe, and colons, commas and backslashes are all filtergraph syntax.
Write the string to a file under `/data` and point `drawtext` at it.

> **Shell trap found while proving this** (does not affect the Go
> implementation, and is the reason it must not): in zsh, `$F:textfile=…`
> silently applies the `:t` history modifier, rewriting the font path to its
> basename and producing the misleading error `Either text, a valid file, a
> timecode or text source must be provided`. The implementation passes an
> argument slice to `exec.Command` and builds no shell string, so it cannot
> meet this. Anyone reproducing by hand should brace: `${F}:textfile=…`.

## Run 3 — the same chain inside the shipping image

The runtime is `gcr.io/distroless/base-debian12:nonroot`: no shell, no apt,
uid 65532. A **static** ffmpeg goes in as one `COPY`:

```dockerfile
FROM mwader/static-ffmpeg:7.1 AS ff
COPY --from=ff /ffmpeg /usr/local/bin/ffmpeg
```

`ffmpeg version 7.1`, with libx264 / aac / freetype / fontconfig present.
Title card (`drawtext` + `textfile`), a page segment, and `concat -c copy`
with `-metadata` were then all executed **inside that image, as uid 65532**:

```console
$ docker run --rm -u 65532:65532 -v …:/pages:ro -v …:/out thutapi-ffmpeg …
codec_name=h264   width=1080  height=1350
codec_name=aac
duration=7.723220                     # 3.5 s title card + 4.22 s page
TAG:title=Mira and Bramble's Long Day
```

This closes the version-skew question: the encode was developed against
workstation ffmpeg n9.0.1, and the identical filter chain runs under the
containerised 7.1.

* **Cost: image 33.8 MB → 222 MB.** Deploy pull time on the box; nothing else.
  23 G free (§T11 item 4).
* `CGO_ENABLED=0` untouched — ffmpeg is a subprocess, not a link-time
  dependency, so the pure-Go/distroless posture from §T0 survives.
* The "no Node in the build" invariant is untouched: one `COPY` of a prebuilt
  binary, nothing compiled or generated at image-build time.
* A judge's `docker run` gains no network dependency — the binary is baked in.

## What IS verified

| # | Claim | Evidence |
| --- | --- | --- |
| 1 | Eight real T6b page renders mux into one playable MP4 | run 1 — 49.2 s, 2.6 MB, h264/aac, 1080×1350 |
| 2 | Page duration is driven by its own narration, with no duration arithmetic anywhere | run 1 — output duration is the exact sum of the eight clips |
| 3 | The join is free | `concat -c copy`; 8 pages encode + join in 13.3 s |
| 4 | The file stands alone off-site | run 2 — title card, end card, MP4 tags, slugified filename |
| 5 | Model-authored text reaches `drawtext` safely | run 2 — `textfile=`, apostrophe in the title survives |
| 6 | ffmpeg runs in the shipping image, unprivileged | run 3 — full chain under ffmpeg 7.1 as uid 65532 in distroless |

## What is NOT verified — do not quote this record past this line

| # | Gap | Closes in |
| --- | --- | --- |
| A | **Real narration.** Every clip here was a generated tone, not Speech 2.8 output. MP3-in / AAC-out and per-page emotion are unexercised. | T8, then T6b item 3 |
| B | **Serving it.** No route, no `Content-Disposition`, no Range behaviour for a `<video>` scrubbing a part-downloaded file. | T10 |
| C | **On-device playback.** Nothing was opened on an iPhone, iPad or Android. `playsinline` and the download affordance are untested on the primary device. | T10 |
| D | **The real font.** DejaVuSans-Bold stood in; T9 vendors Fredoka, and a different face changes every `fontsize`/`x`/`y` in the cards. | T9 → T10 |
| E | ~~**The pinned base image.**~~ **CLOSED by T10a**, same day: the `Dockerfile` carries `mwader/static-ffmpeg:7.1@sha256:a8090df5…` as its own stage, copied into the runtime image. | done |
| F | **Wall time on the box.** 13.3 s is a workstation figure; foleyflow is slower and shares CPU with five other services. | T10 |

## Reproducing

Inputs are committed under `data/live/t6b-book/`. The commands above are
complete; nothing depends on `GMI_API_KEY` and nothing bills. Tests that shell
out to ffmpeg must `t.Skip` when the binary is absent, in the shape
`internal/illustrate/live_test.go` already uses for its live probes.
