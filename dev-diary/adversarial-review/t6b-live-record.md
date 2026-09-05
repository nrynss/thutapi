# T6b — live illustration verification record

**Date:** 2026-09-05. **Operator:** orchestrator session.
**Precedent:** T1b / T2b / T5b — an operator track closes on transcripts.
**Cost so far:** three image requests submitted, none processed (see below).

---

## Item 1 — the terminal request-queue record for an image call

**Status: PARTIAL.** The queue accepted both image models and returns a
well-formed record, but **the image queue is not processing jobs right now**.
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
* **Operator action worth taking:** GMI's Discord is the cheap resolver the
  plan already names (§The deadline's timezone). "Image queue accepting jobs
  but not scheduling them, both Flux2-Klein and Z-Image, org
  `d3db60e8…`, since ~09:46 IST 2026-09-05" is the one-line report.
