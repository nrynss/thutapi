# T6b — live illustration verification record

**Date:** 2026-09-05. **Operator:** orchestrator session.
**Precedent:** T1b / T2b / T5b — an operator track closes on transcripts.
**Cost:** ~$0.25 total — 3 requests that never ran (unbilled, never processed)
plus 5 that produced an image at $0.035 each.
**Outcome:** item 1 resolved; the chosen image model changed.

---

## Item 1 — the terminal request-queue record for an image call

**Status: RESOLVED — see §Item 1, resolved below.** This section is kept as
written because its evidence is sound and still load-bearing; only its
*conclusion* was wrong.

> **Correction (same day, later run).** The image queue is **not** broken. It
> is those two model ids. `seedream-5.0-lite` submitted at ~10:42 returned
> `status:"success"` **synchronously on the POST**, from the same queue, the
> same org and the same key, while the `Flux2-Klein` jobs submitted at 09:46
> were still `queued`. Do **not** send the GMI Discord report drafted at the
> end of this section — it would describe an outage that is not happening.

**Original status: PARTIAL.** The queue accepted both image models and returns
a well-formed record, but **the image queue is not processing jobs right now**
— *superseded; the models, not the queue*.
The terminal shape (where the result lands inside `outcome`, and whether an
`EditImage` record echoes the base64 reference back inside `payload`) is
pending; a background watcher polls the three request ids every 5 min and
captures the first terminal records.

### What the image queue is doing

| Request | Model | Submitted | Status as of 10:56 IST |
| --- | --- | --- | --- |
| `88d7ffdc-707f-4694-8456-8b93037ae5ea` | Flux2-Klein | 09:46 | `queued`, `outcome:null`, `updated_at == created_at` (never touched) |
| `5b7f3d5d-2133-4085-ba01-f36222677dda` | Flux2-Klein | 09:49 | `queued`, same |
| `0b71ea67-f1c2-4949-9d65-765e5d4f9ec8` | Z-Image | 10:51 | `queued`, same |

* Both §T6 "Start on" models were probed; **model id makes no difference** —
  the queue accepts the POST (200 + `request_id`) and never starts the job.
* The **audio** queue processed a real TTS call in ~24 s this morning through
  the same endpoint (`t2b-t5b-live-record.md`), so submission, auth and
  polling all work; this is specific to the image worker pool.
* Poll budget note: the first probe run died at the client's default 120 s
  poll timeout (`internal/gmi/media` `DefaultPollTimeout`); the live test now
  uses a 10-minute budget. The image queue's real latency is unknown — every
  observation so far is "longer than 30 minutes or never".

### Shape facts already captured (from the polled GET record, queued state)

Top-level keys, in record order:

```json
{"request_id":"…","org_id":"…","user_id":"…","model":"Flux2-Klein",
 "status":"queued","is_public":false,
 "payload":{"prompt":"a single flat red circle …"},
 "outcome":null,
 "created_at":1788584785,"updated_at":1788584785,"queued_at":1788584785}
```

1. **`payload` is echoed back verbatim** in the polled record — confirmed
   against production, not inferred. For `GenerateImage` the echo carries only
   the prompt; for `EditImage` it would carry `payload.image`, the base64
   reference. **This is the mechanism t6-round1 H1 predicts**, one step short
   of proof: the decisive observation is an `EditImage` *terminal* record.
2. **`outcome` is `null` while queued** — it appears only at terminal. The
   decoder's "prefer the outcome subtree" rule cannot fire early by accident.
3. `GET /api/v1/ie/requestqueue/apikey/requests/{request_id}` is the poll
   endpoint `internal/gmi/media/polling.go` already assumes — confirmed.
4. Extra keys the clients do not model: `org_id`, `user_id`, `is_public`,
   `queued_at` (alongside `created_at`/`updated_at`).

### Consequences

* **H1 stays live and leans Critical**: the echo is real; only its
  image-carrying case awaits a terminal record. Remediation proceeds on the
  keyed-on-`outcome` design (the same rule the §T8 section already records
  from the TTS shape) with a fixture that echoes `payload`, per the round's
  exit criterion 2.
* The three submitted jobs are paid for whether or not they ever run; the
  watcher (`t6b-watch`, `/tmp/t6b_watch.log`, terminal records land in
  `/tmp/t6b_terminal_<id>.json`) captures the first terminal record the
  moment GMI's workers pick anything up.
* ~~**Operator action worth taking:** GMI's Discord…~~ **Withdrawn.** The
  report would have been wrong: the queue schedules fine for a model that
  works. The correct reading is that `Flux2-Klein` and `Z-Image` are dead ids
  that accept and never run — which is worth knowing precisely because no
  error is ever returned.
* The watcher (`t6b-watch`) can be stopped; those three jobs will not
  complete. Two probes from the later run were left queued as well.

---

**Date:** 2026-09-05. **Operator:** orchestrator session. **Spend:** ~$0.25
(7 calls; 5 produced an image at $0.035 each). Live, paid, deliberate — per the
2026-09-05 operator policy in `AGENTS.md` §Testing.

This closes T6b item 1 and settles T6 round-1 **H1**. It also **changes the
chosen image model**, which is a plan change, not an implementation detail.

---

# Item 1, resolved — the second run

The first run concluded the image queue was down. It is not: the two model ids
§T6 told us to start on are dead. Everything below is from a later run the same
day, against the same key and org.

## 1. The chosen models never worked

`Flux2-Klein` and `Z-Image` — the models `project.md` §3 says to start on, and
T6's `DefaultModel` — **accept a request and then never generate.**

```
POST … {"model":"Flux2-Klein","payload":{"prompt":"a red cube on white background"}}
-> 200 {"request_id":"e116f9bb-…","status":"queued"}
   poll 1..6 over ~72s: status=queued, queued, queued, queued, queued, queued
```

`Z-Image` behaves identically. The operator reports the same result in the GMI
console, so this is the service, not our client.

**This is a worse failure than an error.** A 404 would have classified as
`ErrModelNotFound` and failed loudly on the first call. Instead the submit
succeeds, the queue accepts it, and the job sits in `queued` forever — so a
book generation would hang until its context deadline on every page, look like
a timeout or a network problem, and burn the whole render window before anyone
suspected the model id.

They move to the forbidden table alongside `Qwen-Image-2512`, for a different
reason: Qwen silently ignores the reference, these silently never finish.

## 2. `seedream-5.0-lite` works, and is synchronous

```
POST … {"model":"seedream-5.0-lite","payload":{"prompt":"a red cube on white background"}}
-> 200 {"request_id":"412767a6-…","status":"success", "outcome":{…}}
```

**`status:"success"` arrives on the POST response itself** — no polling, ~14s
wall clock (`created_at` 1788588145 → `updated_at` 1788588159). T3b's polling
loop is not needed for images; it stays harmless and still covers audio.

## 3. The terminal record shape — this is what H1 needed

```
top-level keys: created_at, is_public, model, org_id, outcome, payload,
                queued_at, request_id, status, updated_at, user_id
outcome keys  : media_urls, thumbnail_image_url
media_urls    : LIST of OBJECTS -> [{"id":"0","url":"https://storage.googleapis.com/…"}]
```

Three things a decoder must know, none of which were guessable:

1. **`media_urls` is an array of objects, not of strings.** The vendor doc says
   only "Find URLs in `outcome.media_urls`".
2. **`outcome.thumbnail_image_url` sits beside it.** A walker taking "the first
   URL under outcome" can pick the thumbnail.
3. **The payload echo is real but inconsistent.** The t2i call echoed
   `payload:{prompt}`; the i2i call echoed `payload:{}`. So the echo cannot be
   relied on either to be present or to be absent.

**H1 verdict: confirmed, and the fix is now unambiguous.** Do not walk the body
looking for something that resembles media. Read `outcome.media_urls[].url`
by name. The round-1 review rated H1 "C if T6b confirms the image queue echoes
payload" — it does echo, so **H1 is Critical**, though the practical risk
narrowed for a reason worth writing down: with seedream the reference goes out
as a *URL*, not as inline base64, so the echoed payload no longer contains an
image the base64 pass could mistake for the result. The bug is still live for
any body-walking decoder — `thumbnail_image_url` alone is enough to break it.

## 4. Image-to-image takes URLs, and the reference can be GMI's own output

```json
{"model":"seedream-5.0-lite","payload":{
  "prompt":"The same red cube, now on a wooden table beside a green apple. Keep the cube identical.",
  "image":["https://storage.googleapis.com/…<the t2i result>…"],
  "output_format":"png","max_images":1,"watermark":false}}
-> status success, PNG 2048x2048, 4,550,955 bytes
```

`payload.image` is an **array of reference image URLs** (up to 14) — *not* the
inline base64 data URI `media.EditImage` currently sends.

**But `project.md` §2b's "no hosting needed" conclusion survives**, by a
different mechanism than it claims: GMI's own output URLs are public and
unauthenticated (established in T2b, re-confirmed here). So a reference sheet
renders, GMI hands back a public URL, and that URL is fed straight into the
next call's `image` array. Nothing needs hosting on our side **during a
generation**. Those URLs are still assumed to expire, so T7 still downloads and
persists for the finished book.

## 5. Resolution — the constraint decides most of it

| Attempt | Result |
|---|---|
| `size:"1K"` | **rejected** — `500 / Backend error (400)`. Presets are 2K and 3K only. |
| `size:"2K"` + "4:5 vertical shape" in the prompt | 1792x2240 jpeg, 539 KB |
| `size:"1792x2240"` explicit | 1792x2240 jpeg, **342 KB** |
| `size` omitted (default) | 2048x2048 |

**There is a hard pixel floor of 3,686,400 (= 1920×1920).** Custom dimensions
must fall between that and 10,404,496. So a 1024×1024 page — what a phone
actually needs — **cannot be requested.** Every image is at least ~3.7 MP and
gets downscaled by the browser.

### Decisions

**Size: explicit `1792x2240`, not the `2K` preset.** Both produce the same
pixels, but the preset infers its shape from *prose in the prompt* — which
means a prompt edit that drops the phrase "4:5 vertical shape" silently
changes a page's aspect ratio. **A book cannot tolerate that**: eight pages
must be identical dimensions or the two-page spread has mismatched heights.
Pin the number; do not let layout depend on an adjective.

4:5 portrait is chosen for T10's layout fork — one page on a portrait phone,
two side by side on tablet/desktop giving an 8:5 (1.6) spread that sits well on
a 16:9 screen. Square pages would give a 2:1 spread and letterbox badly.

**Format: `jpeg`.** Measured 342 KB against 4.4 MB for the same-size PNG — 13×.
Three reasons, and the third is decisive:

* Disk: 5.8 MB a book against 47 MB. 23 GB free on the box.
* The book fills page-by-page over SSE on a child's tablet.
* **T7 inlines the reference sheet as base64 to M3** for the consistency
  verdict. A 4.4 MB PNG becomes ~5.9 MB of base64 inside a chat request; the
  jpeg keeps it near 450 KB.

PNG buys transparency these full-bleed illustrations do not use.

**`watermark: false` explicitly.** It is the documented default, but a
watermarked demo is not worth leaving to a default.

**Cost: $0.035 an image → ~$0.39 a book** (8 pages + ~3 reference sheets).
`project.md` §3 already prices seedream-5.0-lite at $0.35 a book, so the table
was right; it was the *availability* of the cheaper tier that was wrong.

## 6. Consequence for `internal/gmi/media` — a contract change

`media.EditImage(ctx, refImage []byte, prompt, model string)` inlines
`refImage` as a base64 data URI into `payload.image` as a **string**. Seedream
needs `payload.image` to be an **array of URL strings**. The current signature
cannot express that.

T2 is closed, so this is a contract change and not a fix to make in passing.
It needs an owner and a round. Recorded here and in §T6 rather than acted on.

## 7. Not established

* **`sequential_image_generation: "auto"`** — untested. The vendor doc offers
  it "for consistency", with `max_images` up to 15. If it holds a cast across a
  generated sequence, it is a materially different and possibly better answer
  to T6's whole problem than per-page i2i. Worth one experiment before T6
  remediation hardens the per-page approach.
* **Multi-reference** (up to 14 images) — untested. Relevant to pages with two
  or three cast members, where today only one reference is passed.
* **Whether a `data:` URI is accepted** in the `image` array — untested, and
  now moot, since chaining GMI's own URLs works and costs nothing.
