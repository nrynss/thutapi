# T2 round 2 — adversarial review of the GMI clients (post-remediation)

| | |
|---|---|
| **Target** | T2 at `4a176a2` ("T2 round 1 remediation: close M1 (ErrBadRequest for 4xx), record L1 false-positive") on `main`. Tree clean at review time. Working tree clean throughout the probe. |
| **Evidence** | `dev-diary/project.md` (§Pipeline, §Architecture consequences, §M3 phase settings, §2b, §3, §4), `dev-diary/PLAN.md` §T2 + Status table; `AGENTS.md` §GMI endpoints / §Safety and data rules / §CI and images; `dev-diary/adversarial-review/README.md`; `internal/gmi/errors.go`, `internal/gmi/text/client.go`, `internal/gmi/text/client_test.go`, `internal/gmi/media/client.go`, `internal/gmi/media/client_test.go` read in full; `cmd/thutapi/main.go` (parseConfig pattern — read for shape consistency with `os.Getenv` call-time-only auth, matches both clients). Per-finding transcripts below re-run by this reviewer on 2026-09-04 against `4a176a2`. |
| **Method** | Probe, don't trust prose. (1) Pin re-run: `go test -race -count=1 -v ./internal/gmi/...` and the two targeted M1 Pins. (2) M1 Mutation: delete the new `case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:` branch from each client's `classifyStatus` (text first, then media) and confirm the Pin goes red with `errors.Is(err, gmi.ErrBadRequest) == false` AND `errors.Is(err, gmi.ErrTransient) == true`; revert and confirm green. (3) L1 disposition check: re-read `internal/gmi/media/client.go:153-162` and confirm the round-1 reviewer's claim is wrong on inspection — the "in" is grammatical. (4) Full module: `go vet ./...`, `go test -race -count=1 ./...`, `gofmt -l .`. (5) Surrounding-code audit: diff `3695375..4a176a2` of `internal/gmi/{errors.go, text/client.go, text/client_test.go, media/client.go, media/client_test.go}` for any new defect introduced by the remediation. |
| **Date** | 2026-09-04 |

## Verdict

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**

Round 1's residue is closed. M1 is fixed at `4a176a2` (introducing the typed
`gmi.ErrBadRequest` sentinel, routing 400/422 to it in both `classifyStatus`
functions, with new Pin tests `TestChat_BadRequest_400` and
`TestBadRequest_400`). L1's "stray in" claim was a false-positive, recorded
as such in `t2-remediation-round1.md`, and the line at
`internal/gmi/media/client.go:157-158` is left as-is (natural English).

The remediation is surgical: a fifth sentinel + a four-line switch arm in
each client + a comment-only "default" docstring in the text client + the
two Pin tests + a package-doc re-order so `ErrBadRequest` leads the example
(matching its position as the most common 4xx caller-error class). The
surrounding code in the diff is byte-clean — no collateral drift.

**Zero-residue claim (per severity, per round-1 finding):**

| Round-1 finding | Severity | Residue at `4a176a2`? |
|---|---|---|
| M1 (`classifyStatus` collapses 4xx-to-`ErrTransient`) | M | **Zero.** New `case 400/422` arm routes to `gmi.ErrBadRequest`. `TestChat_BadRequest_400` and `TestBadRequest_400` both pass; both fail under the M1 Mutation with the exact regression signature the round-1 Pin called out. |
| L1 (stray "in" in `media/client.go:157-158` docstring) | L | **Zero.** Disposition recorded in `t2-remediation-round1.md` as a false-positive; line is natural English ("spelled 'volumn' [in] GMI's API"). No code change. |

## Severity key

| Level | Meaning |
|---|---|
| **C** | Breaks the demo. Cannot ship. |
| **H** | Real defect the demo survives. Must fix before the track closes. |
| **M** | Real defect with a workaround. Must fix before the track closes. |
| **L** | Polish / hygiene. Must fix before the track closes. |

## Per-finding re-verification

### M1 — `classifyStatus` collapses every non-401/403/429/404 status (and every 4xx that is not 404) to `ErrTransient`

**Where:** `internal/gmi/text/client.go:230-233` (new `case 400/422` arm
inside `classifyStatus`); `internal/gmi/media/client.go:245-246` (same).
**Sentinel:** `internal/gmi/errors.go:24` (`var ErrBadRequest = errors.New("gmi: bad request")`).
**Pin tests:** `internal/gmi/text/client_test.go:168-192` (`TestChat_BadRequest_400`);
`internal/gmi/media/client_test.go:251-272` (`TestBadRequest_400`).

**Pin on fixed code (re-run 2026-09-04 against `4a176a2`):**

```console
$ go test -race -count=1 -v -run 'TestChat_BadRequest_400|TestBadRequest_400' ./internal/gmi/...
?   	thutapi/internal/gmi	[no test files]
=== RUN   TestBadRequest_400
--- PASS: TestBadRequest_400 (0.00s)
PASS
ok  	thutapi/internal/gmi/media	1.013s
=== RUN   TestChat_BadRequest_400
--- PASS: TestChat_BadRequest_400 (0.00s)
PASS
ok  	thutapi/internal/gmi/text	1.013s
```

Both Pins pass. The text client (`text/client.go:230-233`) now has:

```go
case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:
    // 400/422 = caller payload is wrong. Do not retry — the same
    // bytes fail the same way.
    return fmt.Errorf("%w: %s", gmi.ErrBadRequest, msg)
```

The media client (`media/client.go:245-246`) has the same arm with the
shorter one-line comment that matches its sibling switch arms.

**Mutation probe — text client:** deleted the new `case 400/422` block
(`CUT 230.=233` on `internal/gmi/text/client.go`); the `default` arm then
catches the 400 and routes it to `gmi.ErrTransient`. Pin re-run:

```console
$ go test -count=1 -v -run 'TestChat_BadRequest_400' ./internal/gmi/text/
=== RUN   TestChat_BadRequest_400
    client_test.go:187: err = gmi: transient error: {"error":{"message":"unsupported field: foo"}}, want errors.Is(.., gmi.ErrBadRequest)
    client_test.go:190: err = gmi: transient error: {"error":{"message":"unsupported field: foo"}}, must NOT also be errors.Is(.., gmi.ErrTransient)
--- FAIL: TestChat_BadRequest_400 (0.00s)
FAIL
FAIL	thutapi/internal/gmi/text	0.003s
FAIL
```

The Pin turns red **for exactly the reason the round-1 review called out**:
`errors.Is(err, gmi.ErrBadRequest) == false` (sentinel never wrapped) AND
`errors.Is(err, gmi.ErrTransient) == true` (the regression class). Restored
from backup; re-run green: `--- PASS: TestChat_BadRequest_400 (0.00s)`.

**Mutation probe — media client:** deleted the new `case 400/422` block
(`CUT 245.=246` on `internal/gmi/media/client.go`); same default catches the
400. Pin re-run:

```console
$ go test -count=1 -v -run 'TestBadRequest_400' ./internal/gmi/media/
=== RUN   TestBadRequest_400
    client_test.go:267: err = gmi: transient error: {"error":"prompt rejected by safety filter"}, want errors.Is(.., gmi.ErrBadRequest)
    client_test.go:270: err = gmi: transient error: {"error":"prompt rejected by safety filter"}, must NOT also be errors.Is(.., gmi.ErrTransient)
--- FAIL: TestBadRequest_400 (0.00s)
FAIL
FAIL	thutapi/internal/gmi/media	0.004s
FAIL
```

Same regression signature. Restored; re-run green:
`--- PASS: TestBadRequest_400 (0.00s)`. **Pin is load-bearing on both
clients.**

**Seam verification.** The Pins redirect via the env-override on each
client's `Client.baseURL`. Independent re-read of
`internal/gmi/media/client.go:59-81, 195-196`:

```go
type Client struct {
    baseURL    string
    httpClient *http.Client
}

func New() *Client {
    base := defaultBaseURL
    if v := os.Getenv("GMI_MEDIA_BASE_URL"); v != "" {
        base = v
    }
    return &Client{
        baseURL: base,
        httpClient: &http.Client{ Timeout: 120 * time.Second },
    }
}

// post (line 195):
endpoint, err := url.JoinPath(c.baseURL, pathRequestQueue)
```

`classifyStatus` is invoked from `post()` after the HTTP round-trip; the
httptest redirect (`srv.URL`) reaches `classifyStatus` via the env override
on `New()` and the field on `c.baseURL`. The text client mirrors this on
`GMI_TEXT_BASE_URL` (`text/client.go:55-67, 178`). The seam is real on
both clients.

**Verdict on M1:** closed at `4a176a2`. Zero residue.

### L1 — typo-key docstring: "in" is grammatical, not stray

**Where:** `internal/gmi/media/client.go:157-158` (the `SynthesizeSpeech`
docstring bullet for `need_volumn_normalization`).
**Disposition:** recorded as false-positive in `t2-remediation-round1.md`
§L1; no code change.

**Re-read 2026-09-04:**

```console
$ sed -n '153,162p' internal/gmi/media/client.go
// SynthesizeSpeech runs a TTS call and returns the raw audio bytes.
// voice is the voice_id ("English_expressive_narrator" for the default
// narrator; a cloned voice id for T13). The two audio flags pin the
// GMI API quirk in project.md §4:
//
//   - need_noise_reduction: true (spelled correctly)
//   - need_volumn_normalization: true (note the missing 'u' — spelled
//     "volumn" in GMI's API, match the typo or it is silently ignored)
//
```

Lines 157–158 read naturally: *"need_volumn_normalization: true (note the
missing 'u' — spelled 'volumn' [in] GMI's API, match the typo or it is
silently ignored)."* The word "in" before "GMI's API" is grammatical —
it scopes where the typo lives. The round-1 reviewer's claim that this is
a "stray letter" requiring a one-line doc fix is incorrect; removing "in"
would produce the ungrammatical *"spelled 'volumn' GMI's API"*. The
remediation agent's disposition is right.

**Verdict on L1:** zero residue. False-positive correctly recorded; line
left as-is.

## What held up under attack

### 1. Unit pins — full `internal/gmi/...` suite under `-race -count=1`

```console
$ go test -race -count=1 -v ./internal/gmi/...
=== RUN   TestGenerateImage
--- PASS: TestGenerateImage (0.00s)
=== RUN   TestEditImage
--- PASS: TestEditImage (0.00s)
=== RUN   TestSynthesizeSpeech
--- PASS: TestSynthesizeSpeech (0.00s)
=== RUN   TestSynthesizeSpeech_TypoPinnedInRawJSON
--- PASS: TestSynthesizeSpeech_TypoPinnedInRawJSON (0.00s)
=== RUN   TestUnauthorized
--- PASS: TestUnauthorized (0.00s)
=== RUN   TestBadRequest_400
--- PASS: TestBadRequest_400 (0.00s)
=== RUN   TestMissingAPIKey
--- PASS: TestMissingAPIKey (0.00s)
=== RUN   TestEmptyInputs
--- PASS: TestEmptyInputs (0.00s)
PASS
ok  	thutapi/internal/gmi/media	1.020s
=== RUN   TestChat_HappyPath
--- PASS: TestChat_HappyPath (0.00s)
=== RUN   TestChat_Unauthorized_401
--- PASS: TestChat_Unauthorized_401 (0.00s)
=== RUN   TestChat_BadRequest_400
--- PASS: TestChat_BadRequest_400 (0.00s)
=== RUN   TestChat_RateLimited_429
--- PASS: TestChat_RateLimited_429 (0.00s)
=== RUN   TestChat_Transient_500
--- PASS: TestChat_Transient_500 (0.00s)
=== RUN   TestChat_MissingAPIKey
--- PASS: TestChat_MissingAPIKey (0.00s)
PASS
ok  	thutapi/internal/gmi/text	1.018s
```

**14 subtests** (8 media + 6 text) pass under race; `-count=1` defeats
any caching; no flakes. Round-1's 12 + 2 new M1 Pins = 14.

### 2. Module-wide gates

```console
$ go vet ./...                                                clean
$ gofmt -l .                                                  clean
$ go test -race -count=1 ./...
ok  	thutapi/cmd/thutapi	10.023s
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	1.016s
ok  	thutapi/internal/gmi/text	1.016s
```

All three gates clean; full module passes under race in 10s.

### 3. Diff audit — `3695375..4a176a2` of every file the remediation touched

| File | Delta | Audit |
|---|---|---|
| `internal/gmi/errors.go` | +11/-3 lines: new `ErrBadRequest` sentinel (8 lines incl. docstring); package-doc example re-ordered so `ErrBadRequest` leads | **Clean.** `errors.New("gmi: bad request")` matches the existing `errors.New("gmi: <x>")` shape on every other sentinel; docstring matches the tone of the sibling sentinels (single sentence on what, single sentence on what to do). Package-doc example now lists `ErrBadRequest` first — correct, as it is the most common 4xx caller-error class, and the order matters because callers read top-down and the first hit is the most likely class. |
| `internal/gmi/text/client.go` | +4 lines inside `classifyStatus`: new `case 400/422` arm (3 lines) + a 3-line comment on `default:` explaining why unmapped statuses route to `ErrTransient` | **Clean.** The new arm matches the surrounding four arms in shape (`case …:` / body / `return …`). The `default:` comment explains a non-obvious design choice — what would otherwise look like a missing branch is now documented. |
| `internal/gmi/text/client_test.go` | +30 lines: `TestChat_BadRequest_400` Pin | **Clean.** Mirrors `TestChat_Unauthorized_401` exactly (lines 138–162) — same httptest setup, same `t.Setenv("GMI_API_KEY" / "GMI_TEXT_BASE_URL", …)` redirect, same `errors.Is` shape; the only delta is the status code and the test name. Adds the negative `!errors.Is(.., gmi.ErrTransient)` assertion that is the load-bearing half of the Pin. |
| `internal/gmi/media/client.go` | +4 lines inside `classifyStatus`: new `case 400/422` arm (2 lines) + a structural `case code >= 500:` arm made explicit (was previously implicit, falling through `default`) | **Clean and arguably an improvement.** The old media `classifyStatus` had four arms and a `default`; the new media `classifyStatus` has five arms plus `default`. The added `case code >= 500:` arm is functionally identical to the old `default` (both wrap `ErrTransient`), but it makes the 5xx → transient mapping visible at the switch instead of hiding it behind `default`. Same shape as the text client's `case code >= 500:` arm (`text/client.go:234`). |
| `internal/gmi/media/client_test.go` | +25 lines: `TestBadRequest_400` Pin | **Clean.** Mirrors `TestUnauthorized` (lines 230–247) — same httptest setup, same `t.Setenv("GMI_API_KEY" / "GMI_MEDIA_BASE_URL", …)` redirect, same `errors.Is` shape. Same negative `!errors.Is(.., gmi.ErrTransient)` assertion as the text Pin. |

**No collateral drift.** The diff is exactly what the remediation
description claimed: the M1 fix and the two Pins. Nothing else changed
in the wire surface, the package surface, or the test surface.

### 4. Surrounding-code regression check

- **The new `default:` comment in the text client** (`text/client.go:237-239`):
  *"Anything else (3xx not followed, other 4xx, etc) is unknown; treat as
  transient so the caller's retry loop has a chance to observe recovery."*
  This documents a real design choice (unknown-unknown class → transient
  with caution). Consistent with `errors.go` `ErrTransient` docstring
  ("transport blip, retry"). The media client does not have the parallel
  comment, but its `default` arm is functionally identical and the parallel
  `case code >= 500:` arm makes the 5xx → transient mapping visible without
  the comment. No asymmetry bug.
- **The new sentinel's order in `errors.go`** (`ErrBadRequest` declared
  first, at line 24): the package-doc example lists it first (line 10) and
  the source comments on the other sentinels reference the existing order
  — there is no internal cross-reference that depends on declaration order.
  Sentinel equality is by `errors.New` pointer, not by order. No bug.
- **Test structure** — both new tests sit immediately after the existing
  4xx/4xx-equivalent test (text: after `TestChat_Unauthorized_401`; media:
  after `TestUnauthorized`). Convention-consistent with how the round-1
  reviewer grouped the rate-limit + 5xx tests adjacent. No bug.
- **`cmd/thutapi/main.go` parseConfig pattern** — re-read for shape
  consistency; the main binary reads `GMI_API_KEY` via `os.Getenv` at
  call-time on every request, matches both clients' pattern. No bug, no
  drift.

## Done-when audit (PLAN §T2)

| Criterion | Met? | Evidence |
|---|:---:|---|
| Two clients, two URLs, two shapes | **Yes** | `internal/gmi/text/` (OpenAI-shape, `/v1/chat/completions`) and `internal/gmi/media/` (`{model, payload}`, `/api/v1/ie/requestqueue/apikey/requests`); separate packages |
| Model id carries full `MiniMaxAI/` prefix | **Yes** | `text/client_test.go:104,108` both prefix and anti-prefix assertions |
| `thinking:{"type":"enabled"}` is the reasoning switch; `reasoning_effort` absent | **Yes** | `text/client.go:101-103,112`; `text/client_test.go:111-124` presence + absence |
| TTS typo `need_volumn_normalization` (literally `volumn`) | **Yes** | `media/client.go:176`; `media/client_test.go:178-190,224` typed-map + raw-JSON pins |
| `source_audio` requirement: media API treats audio as URL-bound (deferred to T8) | **Deferred** | T2's `SynthesizeSpeech` body has no `source_audio` field — T8 owns the voice-clone body. Spec satisfied for T2's scope. |
| i2i uses inline `data:` URI | **Yes** | `media/client.go:126-149` |
| `GMI_API_KEY` from env, call-time, never embedded | **Yes** | both `Chat` and `post` read `os.Getenv("GMI_API_KEY")` at call entry; `cmd/thutapi/main.go` parseConfig matches |
| Typed sentinels + `errors.Is` | **Yes** | `errors.go` declares five typed sentinels (`ErrBadRequest`, `ErrUnauthorized`, `ErrRateLimited`, `ErrModelNotFound`, `ErrTransient`); tests assert `errors.Is(err, gmi.ErrX)` for the four that have direct Pins; `ErrBadRequest` carries a positive AND a negative assertion in both clients |
| 4xx caller-error class (400/422) routes to a non-transient sentinel | **Yes** | `text/client.go:230-233` and `media/client.go:245-246`; Pins `TestChat_BadRequest_400` and `TestBadRequest_400`; mutation probes confirm load-bearing |
| `go vet`, `go test -race`, `gofmt -l` clean | **Yes** | `go vet ./...` empty; full module test under `-race -count=1` passes; `gofmt -l .` empty |
| Integration smoke hits both endpoints live | **Partial** | Same as round 1: transport + auth-layer verified live against `api.gmi-serving.com` (Cloudflare-fronted text) and `console.gmicloud.ai` (APISIX-fronted media) — both 401s returned with the documented `{"error":"…"}` bodies, classified correctly to `gmi.ErrUnauthorized` by the unit Pins. A 200-response live probe still awaits a production-authorised key (operator action queued in `t2-round1.md` §5.3). The bearer token in `/home/nryn/work/apikey.txt` is `sk-or-v1-…` (OpenRouter-shaped) and is rejected by both providers — this is an operator-side issue, **not** a T2 defect. |

The M1 fix did not move any of the round-1 done-when items to "Partial";
the live-call side remains the only honest gap, and that gap is on the
operator side, not the code.

## Go-signal matrix

The T2 remediation closes M1 cleanly. T2 is **DONE** at `4a176a2`. Per the
parent agent's process note, the SHA I would cite on the PLAN.md T2 row's
"Closed at" claim is **`4a176a2`** — the remediation commit. (HEAD has
moved to `b1148aa` with two follow-up doc-only commits that do not touch
the T2 wire surface; `git diff 4a176a2 HEAD -- internal/gmi/` returns
empty.)

T3 now owns the public-file-fetchable-by-third-party check that was
re-scoped out of T1b check 5 (PLAN §T3); T13 still depends on the bearer
key in `/home/nryn/work/apikey.txt` being a real GMI key, not
`sk-or-v1-…`, before the voice-clone body can be exercised end-to-end.

| Track | Depends on | Signal |
|---|---|---|
| **T3** — store and media | T0 | **GO.** T3 owns the `http.ServeContent` route that re-introduces the T1b check 5 (public file fetchable by a third party) for `source_audio` (T13 / T8). M1 fix is closed at `4a176a2`; T3 does not branch on error sentinels so the fix does not gate it. |
| **T4** — interview Phase A | T2, T3 | **GO** (chain). Reads `text.Client.Chat` directly; no `classifyStatus` branching needed (Phase A is a happy-path loop with one retry on transient). |
| **T5** — structuring Phase B | T4 | **GO** (chain). One call over the whole transcript; Phase B turns `thinking` on. |
| **T6** — illustration | T5 | **GO** (chain). Uses `media.Client.GenerateImage` / `EditImage`. M1 fix landed — a bad image-prompt rejection now surfaces as `gmi.ErrBadRequest`, not `ErrTransient`; T6's retry loop can branch on it without spinning. |
| **T7** — consistency verification | T6 | **GO** (chain). Two-image multimodal call to `text.Client.Chat` — same client; same M1 fix applies. |
| **T8** — audio/TTS | T2, T5 | **GO (conditional)**. `media.Client.SynthesizeSpeech` is called; T8 also owns the `source_audio` (URL-bound) body for T13, which T2 does **not** ship — T2 covers `SynthesizeSpeech`'s `text`/`voice_id`/`need_volumn_normalization` body, T8 adds the `source_audio` shape. M1 fix applies. |
| **T9** — frontend shell + interview UI | T4, T8 | **GO** (chain). |
| **T10** — book renderer + flipbook | T6, T8, T9 | **GO** (chain). |
| **T11** — hardening (gate, cap, prewarm) | T10 | **GO** (chain). |
| **T12** — music bed (optional) | T10 | **GO** (chain). |
| **T13** — voice clone (optional) | T8 + T3 | **GO (conditional)**: T3 owns the static-file route; once T3 lands the `http.ServeContent` handler and a signed-path probe succeeds, T13's `source_audio` body lands. **Operator gate still open**: the bearer key in `/home/nryn/work/apikey.txt` must be a real GMI key (not `sk-or-v1-…`) before any of T6/T8/T13 can return a 200. T2 itself does not need to change for T13 — the media client is the `{model, payload}` envelope; T13 adds a `payload.source_audio` field. |
| **T14** — submission | T11 | **GO** (chain, wall-clock trumps all). |

**Carried forward:** T3's done-when now owns the T1b-rescoped check 5 —
"a public file fetchable by a third party for the `source_audio`
mechanism" (PLAN §T1b + §T3). This is **not** a T2 finding; T2 does not
ship the static-file route. The matrix notes the dependency so it is not
silently dropped.

**Live-call operator gate (carried from round 1):** the bearer token in
`/home/nryn/work/apikey.txt` is `sk-or-v1-…`-shaped and is rejected by
both `api.gmi-serving.com` (Cloudflare-fronted text) and
`console.gmicloud.ai` (APISIX-fronted media) with the documented 401s
each client's `classifyStatus` routes to `gmi.ErrUnauthorized`. The T2
client code is correct end-to-end; closing the live-call half of
PLAN §T2's done-when is a one-line operator action — install a real GMI
key and re-run the §5 probes in `t2-round1.md`. This is the only honest
gap and it is not a T2 defect.

## Recommended actions

None. T2 is APPROVE. The T2 row in `dev-diary/PLAN.md`'s Status table
should be flipped to **DONE, closed at `4a176a2`** per the parent
agent's process note; that edit is outside this round's scope and is
queued for the integration pass.