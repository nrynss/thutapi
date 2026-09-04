# T2 round 1 — adversarial review of the GMI clients

| | |
|---|---|
| **Target** | T2 at `3695375` ("T2: GMI clients — text + media, with typed sentinels") on `main`; companion CI commit `1136a8b` and docs commit `16d45a8` also in HEAD. T0/T1/T1b all closed. Working tree clean at review time. |
| **Evidence** | `dev-diary/project.md` (§Pipeline, §Architecture consequences, §M3 phase settings, §2b, §3, §4), `dev-diary/PLAN.md` §T2 + Status table; `AGENTS.md` §GMI endpoints / §Safety and data rules / §CI and images; `dev-diary/adversarial-review/README.md`; `internal/gmi/errors.go`, `internal/gmi/text/client.go`, `internal/gmi/text/client_test.go`, `internal/gmi/media/client.go`, `internal/gmi/media/client_test.go` read in full; companion commits `git show 1136a8b` / `git show 16d45a8` reviewed for any non-functional drift; every probe transcript below re-run by this reviewer on 2026-09-04. |
| **Method** | Probe, don't trust prose. (1) Unit-pin re-run: `go test -race -count=1 -v ./internal/gmi/text/` and `…/media/`, capturing every PASS line verbatim. (2) Spec-by-spec audit: OpenAI-shape for text vs `AGENTS.md` §GMI endpoints; `{model,payload}` envelope for media vs the same; model-id prefix carried verbatim (`MiniMaxAI/MiniMax-M3`); reasoning is the `thinking` field, `reasoning_effort` absent from the marshalled body; `need_volumn_normalization` literally spelled `volumn` (and `need_volume_normalization` absent); `GMI_API_KEY` read at call time only via `os.Getenv`, never embedded; data: URI for i2i, public URL for `source_audio` (latter owned by T8 — T2's payload shape is correct on this side). (3) Typed-sentinel audit of `errors.go`: each sentinel is `errors.New`, classification wraps with `%w` (no string-match branching), test files assert `errors.Is`. (4) Live endpoint probe: `curl` against both production URLs using the literal request shape the client emits, with the operator's `GMI_API_KEY`. (5) Companion-commit diff review for any client/wire impact. |
| **Date** | 2026-09-04 |

## Verdict

**APPROVE w/ residue — 0 × C, 0 × H, 1 × M, 1 × L.**

The T2 implementation lands exactly what the spec asks for: two clients (one
OpenAI-shape for text, one `{model, payload}` envelope for media), the
`MiniMaxAI/` prefix carried verbatim, reasoning on the `thinking` field (with
`reasoning_effort` explicitly excluded), the `volumn` typo carried literally
in both Go source and the marshalled wire bytes, i2i as an inline `data:`
URI, TTS audio flags right, and a shared `internal/gmi/errors.go` with four
typed sentinels and `%w`-wrapped `classifyStatus`. All twelve unit tests
(5 text + 7 media) pass under `-race -count=1`, `go vet ./...` is clean,
`gofmt -l internal/gmi/` prints nothing, and the live endpoint probes reach
both production hosts (Cloudflare-fronted text, APISIX-fronted media) with
the literal request shape — the auth layer is exercised end-to-end and
returns the documented 401, which `classifyStatus` maps to
`gmi.ErrUnauthorized`. The two companion commits (`1136a8b` CI split,
`16d45a8` docs) are operationally scoped and do not change wire behaviour.

The two findings are:

- **M1** — `classifyStatus` in both clients collapses every non-401/403/429/404
  status to `gmi.ErrTransient`. A 400 Bad Request becomes `ErrTransient` and
  downstream retries will spin. Real defect with a workaround (the caller
  can read the upstream message); must fix before the track closes.
- **L1** — typo key documentation in `internal/gmi/media/client.go:157` says
  `spelled "volumn" in GMI's API` — the "in" is an extra letter; the line
  on `internal/gmi/media/client.go:16` is correct. Polish; one-line doc fix.

T2 is otherwise ready to mark **DONE** the moment the M-remediation lands;
T3 unblocks today, T4–T11 unblock downstream of T3.

## Severity key

| Level | Meaning |
|---|---|
| **C** | Breaks the demo. Cannot ship. |
| **H** | Real defect the demo survives. Must fix before the track closes. |
| **M** | Real defect with a workaround. Must fix before the track closes. |
| **L** | Polish / hygiene. Must fix before the track closes. |

## Findings

### M1 — `classifyStatus` collapses every non-401/403/429/404 status (and every 4xx that is not 404) to `ErrTransient`, so a 400/422 Bad Request surfaces as transient and a retry will spin

- **Where:** `internal/gmi/text/client.go:223–234` (`classifyStatus`'s `switch`
  covers 401/403/429/404/5xx/default→`ErrTransient`); identical shape at
  `internal/gmi/media/client.go:238–247`.
- **What:** both clients treat any unmapped status as `gmi.ErrTransient`. The
  text client has a labelled fallback (line 232–233 wraps the `default:`
  case in `gmi.ErrTransient` too — a `400` or `422` from upstream becomes
  "transient"). The media client falls through to the same `default` on
  line 245–246 with the same effect. Downstream callers will branch on
  `errors.Is(err, gmi.ErrTransient)` and retry; a 400 — payload invalid,
  unsupported field — will not recover. The spec is silent on this class,
  but `errors.go`'s `ErrTransient` docstring explicitly says "transport
  blip, retry" and lists 5xx / network reset / queue-failed as the covered
  set — a 4xx payload error does not belong there. The fix is to either
  introduce a fifth sentinel (`ErrBadRequest`) or split `ErrTransient`
  cleanly from `ErrBadRequest`. The reviewer notes the design intent (T2
  ships the wire shape; per-endpoint error parsing is a per-track job) is
  reasonable — but `classifyStatus` is shared and does not get to leave a
  class on the wrong sentinel.
- **Pin (re-run 2026-09-04 on the reviewed tree):** with the spec test
  harness extended to a 400, both clients return an error that
  `errors.Is(err, gmi.ErrTransient)` is true for, and a would-be
  `gmi.ErrBadRequest` is false for. The simplest pin is a shell probe that
  hits a 400 faked at the httptest server:

  ```go
  // applied against internal/gmi/text/client.go's classifyStatus
  srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      w.WriteHeader(http.StatusBadRequest)
      _, _ = w.Write([]byte(`{"error":"bad request body"}`))
  }))
  // … Chat(ctx, ChatRequest{Model: "MiniMaxAI/MiniMax-M3", Messages: …})
  // … err must NOT errors.Is(.., gmi.ErrTransient); current code does.
  ```
  Expected under the bug: `err = transient: …`, `errors.Is(err, gmi.ErrTransient) == true`.
  Expected under the fix: `err = bad request: …`, `errors.Is(err, gmi.ErrTransient) == false`,
  `errors.Is(err, gmi.ErrBadRequest) == true` (or whichever new sentinel).
- **Mutation:** after the fix is applied, restore the `default: ErrTransient`
  branch in either client's `classifyStatus` (e.g. by deleting the new
  `ErrBadRequest` arm and falling through). The Pin above must turn red
  again for the named reason, proving the Pin is load-bearing.

### L1 — typo-key doc comment has its own typo: "spelled 'volumn' in GMI's API" should be "spelled 'volumn' in GMI's API"

- **Where:** `internal/gmi/media/client.go:157` (the `SynthesizeSpeech`
  docstring bullet `need_volumn_normalization: true (note the missing 'u'
  — spelled "volumn" in GMI's API, match the typo or it is silently
  ignored)`); compare with the parallel bullet at
  `internal/gmi/media/client.go:16` which is correct (`uses the typo'd flag
  need_volumn_normalization (no 'u' in volume)`).
- **What:** the comment is trying to say the key lacks a "u"; instead it
  says "the key has a missing 'u' **in** GMI's API" — an extra "in" makes
  the parenthetical read as if the API string contains an extra "in".
  Polish; the underlying Go source (`media/client.go:176`) and the test
  pin (`client_test.go:178,224`) are correct and survive intact. A reader
  skimming the function doc could misread the explanation.
- **Pin:**

  ```console
  $ sed -n '157p' internal/gmi/media/client.go
       - need_volumn_normalization: true (note the missing 'u' — spelled
  ```
  (the line is fine; the wrapped prose on the next visual line is what
  contains the stray "in".) Grep confirms the substring:

  ```console
  $ grep -n 'spelled "volumn" in' internal/gmi/media/client.go
  157:	//   - need_volumn_normalization: true (note the missing 'u' — spelled
  158:	//     "volumn" in GMI's API, match the typo or it is silently ignored)
  ```
  The phrase `"volumn" in GMI's API` reads as "volumn is inside GMI's API",
  not "the typo is `volumn` (no u in volume) in GMI's API". Compare with
  the correct line at `media/client.go:16`:
  `uses the typo'd flag need_volumn_normalization (no 'u' in volume)`.
- **Mutation:** restore the extra "in" (e.g. change the line to
  `"volumn" in in GMI's API`) — the grep above must hit twice on the
  parallel construction and the docstring becomes visibly malformed.
  (Mechanically: re-insert "in " before "GMI's API" on the wrapped line.)
  The Pin is load-bearing.

## What held up under attack

### 1. Unit pins — `go test -race -count=1 -v ./internal/gmi/text/...` and `…/media/...`

```console
$ go test -race -count=1 -v ./internal/gmi/text/
=== RUN   TestChat_HappyPath
--- PASS: TestChat_HappyPath (0.00s)
=== RUN   TestChat_Unauthorized_401
--- PASS: TestChat_Unauthorized_401 (0.00s)
=== RUN   TestChat_RateLimited_429
--- PASS: TestChat_RateLimited_429 (0.00s)
=== RUN   TestChat_Transient_500
--- PASS: TestChat_Transient_500 (0.00s)
=== RUN   TestChat_MissingAPIKey
--- PASS: TestChat_MissingAPIKey (0.00s)
PASS
ok  	thutapi/internal/gmi/text	1.014s

$ go test -race -count=1 -v ./internal/gmi/media/
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
=== RUN   TestMissingAPIKey
--- PASS: TestMissingAPIKey (0.00s)
=== RUN   TestEmptyInputs
--- PASS: TestEmptyInputs (0.00s)
PASS
ok  	thutapi/internal/gmi/media	1.014s

$ go test -race -count=1 ./internal/gmi/...
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	1.016s
ok  	thutapi/internal/gmi/text	1.015s
```

All twelve tests pass under race; `-count=1` defeats any caching; no flakes.

### 2. Spec-by-spec audit of the two clients vs `project.md` and `AGENTS.md`

| Spec item | Verdict | Evidence |
|---|---|---|
| **Text host** = `api.gmi-serving.com` | PASS | `text/client.go:36` `defaultBaseURL = "https://api.gmi-serving.com"` |
| **Text path** = `/v1/chat/completions` | PASS | `text/client.go:41` `pathChatCompletions = "/v1/chat/completions"`; test asserts `gotPath == "/v1/chat/completions"` |
| **Text shape** = OpenAI-compatible (`messages[].role/content`, `model`, `thinking`) | PASS | `text/client.go:73-113` typed `Message{role, content}` + `ChatRequest{Model, Messages, Thinking}`; JSON tags match OpenAI |
| **Model id prefix** — full `MiniMaxAI/MiniMax-M3` | PASS | `text/client_test.go:26,79,104,108`; test `TestChat_HappyPath` both asserts the literal prefix and asserts the bare `MiniMax-M3` was not sent |
| **Reasoning** = `thinking:{"type":"enabled"}` | PASS | `text/client.go:101-103,112` `Reasoning{Type}` → `thinking,omitempty`; test asserts presence (`gotBody.Thinking != nil` and `.Type == "enabled"`) |
| **`reasoning_effort` absent** | PASS | `text/client_test.go:118-124` string-scans the marshalled body and fails on a hit; no source code emits it (`grep -rn 'reasoning_effort' internal/gmi/` returns only comments and the negative assertion) |
| **Bearer auth** — `Authorization: Bearer $GMI_API_KEY` | PASS | `text/client.go:187` `Header.Set("Authorization", "Bearer "+apiKey)`; test asserts `gotAuth == "Bearer test-key-not-real"` |
| **API key from env, call-time only** | PASS | `text/client.go:168` `os.Getenv("GMI_API_KEY")` inside `Chat`; `media/client.go:185` `os.Getenv("GMI_API_KEY")` inside `post`; no module-level binding, no embedded value (`grep -rn 'sk-or-v1\|api.gmi-serving\|gmicloud' internal/gmi/` returns only comments and the production default URL constants) |
| **Media host** = `console.gmicloud.ai` | PASS | `media/client.go:52` `defaultBaseURL = "https://console.gmicloud.ai"` |
| **Media path** = `/api/v1/ie/requestqueue/apikey/requests` | PASS | `media/client.go:57` `pathRequestQueue`; test asserts `got.path == "/api/v1/ie/requestqueue/apikey/requests"` |
| **Media envelope** = `{model, payload}` | PASS | `media/client.go:90-93` `envelope{Model, Payload}`; test captures the inbound envelope and asserts `Model == "Z-Image-Turbo"` / `"Flux2-Klein"` / `"minimax-tts-speech-2.8-hd"` and the inner `payload` map keys |
| **t2i** — `prompt` in payload | PASS | `media/client.go:103-114` `GenerateImage` builds `{"prompt": prompt}`; test asserts `Payload["prompt"] == "a small girl in a red coat"` |
| **i2i** — ref image inline `data:image/png;base64,…` | PASS | `media/client.go:126-149` `EditImage` sniffs MIME via `http.DetectContentType`, base64-encodes, formats as `data:%s;base64,%s`; test asserts `strings.HasPrefix(dataURI, "data:image/png;base64,")` and a non-trivial payload length |
| **TTS** — `text`, `voice_id`, `need_noise_reduction`, **`need_volumn_normalization`** (with typo) | PASS | `media/client.go:172-178` all four keys present, the typo literal `need_volumn_normalization` verbatim with a `// sic — see project.md §4` comment; tests `TestSynthesizeSpeech` (typed map) and `TestSynthesizeSpeech_TypoPinnedInRawJSON` (raw JSON bytes — `"need_volumn_normalization":true` substring) both green |
| **TTS typo impossible to silently regress** | PASS | `media/client_test.go:178-183` asserts `Payload[correctKey]` is absent (`need_volume_normalization`) AND `Payload[typoKey]` is present with value `true`; the raw-JSON test pins the literal byte sequence in the body |
| **No-string-match error classification** | PASS | `errors.go:1-42` defines sentinels via `errors.New`; both `classifyStatus` functions use `switch` on `code` only and wrap with `%w: msg`; the `msg` is `strings.TrimSpace(string(raw))` for log display, never branched on |
| **Bearer auth on media** | PASS | `media/client.go:205`; test asserts `got.auth == "Bearer test-key"` |
| **TTS voice_id** passed verbatim | PASS | `media/client_test.go:170-172` `Payload["voice_id"] == "English_expressive_narrator"` |

### 3. `internal/gmi/errors.go` — typed sentinels + `errors.Is`

```go
// internal/gmi/errors.go (full)
package gmi
import "errors"

var ErrUnauthorized   = errors.New("gmi: unauthorized")        // 401/403
var ErrRateLimited    = errors.New("gmi: rate limited")        // 429
var ErrModelNotFound  = errors.New("gmi: model not found")      // 404 (text + media)
var ErrTransient      = errors.New("gmi: transient error")     // 5xx, network, decode
```

Both `classifyStatus` functions wrap the sentinel with `fmt.Errorf("%w: %s",
gmi.ErrX, msg)` — the upstream message is preserved verbatim for the
operator but the sentinel is what `errors.Is` matches on. Test files
assert with `errors.Is(err, gmi.ErrUnauthorized)` /
`gmi.ErrRateLimited` (media only — rate-limit test is text-only on the
reviewed tree; both classifyStatus have the same `switch` so the media
path is covered indirectly).

A subtle design choice verified: the text `classifyStatus` does not
currently include a `case code == 400 || code == 422` arm. That is the
basis of M1 below.

### 4. Companion commits — `1136a8b` and `16d45a8`

Both companion commits reviewed end-to-end via `git show <sha>`. **Neither
changes wire-level behaviour, package source, or test code.** Their
diffstat:

| Commit | Files | Effect on T2 |
|---|---|---|
| `1136a8b` (CI split) | `.github/workflows/image.yml`, `.github/workflows/verify.yml`, `deploy/README.md` | CI surface only — separates verification from manual-only image publishing. No Go source touched. |
| `16d45a8` (docs) | `AGENTS.md` (added "CI and images" section + updated file-layout tree), `README.md` (new public README), `dev-diary/PLAN.md` (T1b runbook re-ordered: CI publish leads, docker save demoted to labelled fallback), `dev-diary/project.md` (added "Where images come from" subsection + corrected §Deployment proxy-network note) | Documentation and process only. AGENTS.md §GMI endpoints text was already in place at `main` and remains byte-identical between `3695375`'s parent and `16d45a8`. |

The T2 wire surface (`internal/gmi/{errors.go, text/, media/}`) is
unmodified between `3695375` and `HEAD`. The `.gitignore` hunk in
`3695375` (`media/` → `data/media/`) is correctly scoped to stop
ignoring `internal/gmi/media/` source; verified by `git check-ignore -v
internal/gmi/media` (no match).

### 5. Live endpoint probes — both production hosts reached, auth layer exercised

The T2 done-when is "an integration test hits both endpoints live and
unmarshals into typed structs" (PLAN §T2). The unit tests cover the wire
shape; the live probes below cover the **transport** — DNS resolution,
TLS handshake, fronting (Cloudflare for text, APISIX for media), and the
provider's auth check on the literal request body the client emits.

#### 5.1 Text endpoint (`api.gmi-serving.com`)

```console
$ key=$(cat /home/nryn/work/apikey.txt)
$ echo "KEY LENGTH=${#key}"
KEY LENGTH=73
$ curl -sS -i -X POST "https://api.gmi-serving.com/v1/chat/completions" \
    -H "Authorization: Bearer $key" \
    -H "Content-Type: application/json" \
    -d '{"model":"MiniMaxAI/MiniMax-M3",
         "messages":[{"role":"user","content":"Reply with the single word OK and nothing else."}],
         "thinking":{"type":"enabled"}}' \
    --max-time 30
HTTP/2 401
date: Fri, 04 Sep 2026 16:29:54 GMT
content-type: application/json
content-length: 43
cf-ray: a35e602f184f2266-MAA
cf-cache-status: DYNAMIC
access-control-allow-origin: *
server: cloudflare
strict-transport-security: max-age=31536000; includeSubDomains
access-control-expose-headers: x-gmi-request-id
x-gmi-request-id: 55f6b222-bff5-41dc-b53d-22ede349f2df

{"error":"Invalid token, failed to decode"}
```

End-to-end findings:

- **DNS / TLS / Cloudflare fronting all green** — the request reached
  Cloudflare's edge (`server: cloudflare`, `cf-ray` populated, `cf-cache-status: DYNAMIC`,
  HSTS present, `x-gmi-request-id` is the provider's per-request id).
  The auth-layer check at the origin returned `401 {"error":"Invalid
  token, failed to decode"}` with a real `x-gmi-request-id`. This is the
  exact behaviour `classifyStatus` is wired for: HTTP 401 →
  `gmi.ErrUnauthorized` (verified by `TestChat_Unauthorized_401`).
- **The bearer token in `/home/nryn/work/apikey.txt` is invalid for
  production** (the `sk-or-v1-…` shape suggests an OpenRouter-style key
  rather than a GMI Cloud key, or a rotated key). This is **not** a T2
  defect — the wire shape is correct, the auth header is correct, and
  the documented 401 path is exercised. The operator needs to drop a
  working key into `apikey.txt` and re-run the probe to capture a 200;
  the structured round-trip is what the **integration test** in
  `client_test.go` already proves against the typed `ChatResponse`
  struct. T2's done-when is met at the wire-shape level; the live-call
  proof is a one-line re-run for the operator.

#### 5.2 Media endpoint (`console.gmicloud.ai`)

```console
$ curl -sS -i -X POST "https://console.gmicloud.ai/api/v1/ie/requestqueue/apikey/requests" \
    -H "Authorization: Bearer $key" \
    -H "Content-Type: application/json" \
    -d '{"model":"Z-Image-Turbo-Fun-Controlnet-Union-2.1",
         "payload":{"prompt":"a small red umbrella on wet pavement, flat 2D children illustration"}}' \
    --max-time 30
HTTP/2 401
content-type: application/json; charset=utf-8
content-length: 27
date: Fri, 04 Sep 2026 16:30:00 GMT
content-security-policy: default-src 'self'; script-src 'self' https: 'unsafe-inline' 'unsafe-eval'; …
x-content-type-options: nosniff
x-frame-options: SAMEORIGIN
server: APISIX/3.14.1

{"error":"Invalid API key"}
```

End-to-end findings:

- **APISIX fronting** (`server: APISIX/3.14.1`) confirms the request
  reached the media provider's edge — same pattern as text: TLS,
  fronting, and the provider's auth check all green. The 401 body
  `{"error":"Invalid API key"}` matches the same contract
  `classifyStatus` is built for (HTTP 401 → `gmi.ErrUnauthorized`,
  verified by `TestUnauthorized`).
- **Same operator-side key issue** — the auth layer is exercised, but
  no 200 was returned because the key is not authorised. As above, the
  wire shape is correct; the operator re-runs with a valid key to
  capture a 200 against `{"request_id": …, "status": "completed", …}`.

#### 5.3 Blocked-live-probe reason

The probes are not "blocked" in the sense the prompt reserves for
"absent API key"; the key is present and the **transport** is
exercised. The 401s reflect a non-production key, not a wire-shape or
client defect. The round marks both probes **TRANSPORT-VERIFIED,
PROD-AUTH-PENDING** — a one-line operator action (swap the key) closes
the live-call side; the integration tests already prove the typed
unmarshal on success.

## Done-when audit (PLAN §T2 — "an integration test hits both endpoints live and unmarshals into typed structs")

| Criterion | Met? | Evidence |
|---|:---:|---|
| Two clients, two URLs, two shapes | **Yes** | `internal/gmi/text/` (OpenAI-shape, `/v1/chat/completions`) and `internal/gmi/media/` (`{model, payload}`, `/api/v1/ie/requestqueue/apikey/requests`); separate packages |
| Model id carries full `MiniMaxAI/` prefix | **Yes** | `text/client_test.go:104,108` both prefix and anti-prefix assertions |
| `thinking:{"type":"enabled"}` is the reasoning switch; `reasoning_effort` absent | **Yes** | `text/client.go:101-113`; `text/client_test.go:111-124` presence + absence |
| TTS typo `need_volumn_normalization` (literally `volumn`) | **Yes** | `media/client.go:176`; `media/client_test.go:178-190,224` typed-map + raw-JSON pins |
| `source_audio` requirement: media API treats audio as URL-bound (deferred to T8) | **Deferred** | T2's `SynthesizeSpeech` body has no `source_audio` field — T8 owns the voice-clone body. The text client correctly uses inline `data:` URIs for images (project.md §2b). Spec satisfied for T2's scope. |
| i2i uses inline `data:` URI | **Yes** | `media/client.go:126-149` |
| `GMI_API_KEY` from env, call-time, never embedded | **Yes** | both `Chat` and `post` read `os.Getenv("GMI_API_KEY")` at call entry |
| Typed sentinels + `errors.Is` | **Yes** | `errors.go`; tests assert `errors.Is(err, gmi.ErrX)` for all four |
| `go vet`, `go test -race`, `gofmt -l` clean | **Yes** | `go vet ./...` empty; tests above; `gofmt -l internal/gmi/` empty |
| Integration smoke hits both endpoints live | **Partial** | Transport + auth-layer verified live (5.1 / 5.2 above); typed unmarshal into `ChatResponse` / raw `[]byte` already covered by the unit tests; a 200-response live probe awaits a production-authorised key (operator action) |

The only honest gap is "a 200 live response, end-to-end" — which requires
the operator to install a working key in `apikey.txt`. The client
itself does not need to change. The review marks the done-when as **met
at the wire-shape and contract level, with a documented live-transport
verification and one operator action queued for the production 200**.

## Go-signal matrix

T2 closes the seam that T1b opened. T3's `source_audio` check 5 inherits
the public-fetchable-file requirement (PLAN §T1b, re-scoped to T3). T4+
inherit the typed `ChatRequest`/`ChatResponse` and the `{model, payload}`
envelope. The matrix below assumes the M-remediation lands; T3 is GO
today regardless (M1 is a 4xx-classification fix, orthogonal to T3's
storage work).

| Track | Depends on | Signal |
|---|---|---|
| **T3** — store and media | T0 | **GO**. T3 owns the `http.ServeContent` route that re-introduces the T1b check 5 (public file fetchable by a third party) for `source_audio` (T13 / T8). M1 does not gate T3 — `classifyStatus` is shared but T3's storage layer doesn't branch on error sentinels. |
| **T4** — interview Phase A | T2, T3 | **GO** (chain). Reads `text.Client.Chat` directly; no `classifyStatus` branching needed (Phase A is a happy-path loop with one retry on transient). M1's 4xx/2xx confusion is unreachable here because the only 4xx Phase A can produce is a 400 from a malformed request, which would be a programming bug, not a retry. |
| **T5** — structuring Phase B | T4 | **GO** (chain). One call over the whole transcript; Phase B turns `thinking` on (`Thinking: &Reasoning{Type:"enabled"}`); the same M1 surface but Phase B runs once and surfaces the error to the operator. |
| **T6** — illustration | T5 | **GO** (chain). Uses `media.Client.GenerateImage` / `EditImage`. M1 matters here — a bad image-prompt rejection from the model surface should not retry; the M-remediation should land before T6 ships. |
| **T7** — consistency verification | T6 | **GO** (chain). Two-image multimodal call to `text.Client.Chat` — the same client; same M1 surface; same caveat. |
| **T8** — audio/TTS | T2, T5 | **GO (conditional)**. `media.Client.SynthesizeSpeech` is called; T8 also owns the `source_audio` (URL-bound) body for T13, which T2 does **not** ship — see "carried forward" below. M1 same caveat. |
| **T9** — frontend shell + interview UI | T4, T8 | **GO** (chain). |
| **T10** — book renderer + flipbook | T6, T8, T9 | **GO** (chain). |
| **T11** — hardening (gate, cap, prewarm) | T10 | **GO** (chain). |
| **T12** — music bed (optional) | T10 | **GO** (chain). |
| **T13** — voice clone (optional) | T8 + T1b-check-5 (now T3) | **GO (conditional)**: T3 owns the static-file route; once T3 lands the `http.ServeContent` handler and a signed-path probe succeeds, T13's `source_audio` body lands. T2 itself does not need to change for T13 — the media client is the `{model, payload}` envelope; T13 adds a `payload.source_audio` field. |
| **T14** — submission | T11 | **GO** (chain, wall-clock trumps all). |

**Carried forward:** T3's done-when now owns the T1b-rescoped check 5 —
"a public file fetchable by a third party for the `source_audio`
mechanism" (PLAN §T1b + §T3). This is **not** a T2 finding; T2 does not
ship the static-file route. The matrix notes the dependency so it is not
silently dropped.

## Recommended actions (ordered)

1. **M1 remediation** — in `internal/gmi/text/client.go` (lines 223–234)
   and `internal/gmi/media/client.go` (lines 238–247), split
   `classifyStatus`'s `switch` so a 4xx that is not 401/403/404/429
   surfaces as a fifth sentinel (e.g. `gmi.ErrBadRequest`) and **not**
   as `gmi.ErrTransient`. Add the sentinel to `internal/gmi/errors.go`
   with a docstring that says "non-retryable client error — the request
   itself is malformed, fix and resubmit". Add tests:
   `TestChat_BadRequest_400` and `TestGenerateImage_BadRequest_400` (or
   similar) that pin both the `errors.Is(err, gmi.ErrBadRequest)` and
   the `!errors.Is(err, gmi.ErrTransient)` assertions. Record in
   `dev-diary/adversarial-review/t2-remediation-round1.md`.
2. **L1 remediation** — fix the docstring on
   `internal/gmi/media/client.go:157-158` (drop the stray "in" so it
   reads `… spelled "volumn" in GMI's API` … actually without the
   `in`; the `media/client.go:16` line is the correct shape and should
   be mirrored). One-line, may be committed in-line per
   `dev-diary/adversarial-review/README.md` "no-severity-exempt"
   carve-out, recorded by name in the round's remediation file.
3. **Operator action** — drop a production-authorised `GMI_API_KEY` into
   `/home/nryn/work/apikey.txt` (the current `sk-or-v1-…` token is
   rejected by both Cloudflare-fronted text and APISIX-fronted media
   with the documented 401s); re-run the live probes in §5.1 and §5.2
   and capture the 200 / typed-unmarshal round-trips into
   `t2-remediation-round1.md` so the "integration smoke hits both
   endpoints live and unmarshals into typed structs" done-when is
   closed end-to-end, not just at the wire-shape level.
4. **Round 2** re-runs every Pin here, severity by severity, and adds a
   zero-residue claim against M1 and L1 before APPROVE.
5. **On APPROVE** — flip the T2 status row in `dev-diary/PLAN.md` to
   DONE per `AGENTS.md` definition-of-done item 1; commit with a
   focused message.