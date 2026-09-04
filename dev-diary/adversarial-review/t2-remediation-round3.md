# T2 round 3 — remediation

| | |
|---|---|
| **Target** | All 11 findings (2 × H, 4 × M, 5 × L) in `dev-diary/adversarial-review/t2-round3.md`. Verdict was REMEDIATE (0C/2H/4M/5L). |
| **Date** | 2026-09-04 |
| **Commit** | Uncommitted at remediation time; lands per AGENTS.md §Process step 5 (the orchestrator commits after the round-4 APPROVE). |
| **Round file** | `dev-diary/adversarial-review/t2-round3.md` is unchanged. The T2 status row in PLAN.md is untouched — it flips on APPROVE. |
| **Verification methodology** | (a) every round-3 Pin re-run green as a permanent test; (b) every round-1 Pin re-run green as the residue check; (c) each Pin's Mutation applied by hand, observed red, reverted, observed green, `diff` against the pre-mutation file byte-identical. Gate outputs in the aggregate section below. |
| **Scope note** | Cross-track edits are the ones the round-3 review sanctioned: M4 touches `.github/workflows/verify.yml`, M3 amends PLAN.md §T2's Done-when plus a T2b Status row. One edit sits outside both: `AGENTS.md` lines 12 and 60, `(0/0/0)` → `(0/0/0/0)` (twice) — an orchestrator in-line fix (doc-only, cannot change behaviour), recorded by name and file here per the in-line exemption's condition 3. Everything else is inside `internal/gmi/**`. Live GMI probes remain impossible (the operator key is rejected by both providers — recorded in the round file); every test is httptest. |

## Rows

### H1 — `EditImage` defaults to `Qwen-Image-2512`, a model that cannot accept the reference image

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `internal/gmi/media/client.go` — was the `model == ""` fallback at client.go:133-135 (and the same constant cited in the envelope doc at :84). |
| **What was done** | `EditImage` no longer has any model default. An empty model returns an error wrapping `gmi.ErrBadRequest` — image-to-image is the mechanism T6's character lock rides on, so a silent fallback is exactly the failure mode that shipped the forbidden constant. The docstring states the requirement, points at PLAN.md §T6's provider switch (Flux2-Klein / Z-Image, gemini-2.5-flash-image on drift), and the `Qwen-Image-2512` citation in the envelope doc comment is gone. `GenerateImage`'s default moved to `defaultImageModel` (see M2). |
| **Test added** | `TestEditImage_EmptyModelRejected` — empty model ⇒ `errors.Is(err, gmi.ErrBadRequest)` and **zero** upstream hits. Mutation-checked: reverting to a fallback makes the Pin red. |
| **Pin re-run status** | Reviewer's `TestProbe_EditImageDefaultModel` re-run as the permanent test above — green. Mutation (restore a default on EditImage) — red, then green after revert. |

### H2 — The T2 retry contract is unimplemented, and `errors.go` tells callers the opposite

| Field | Value |
|---|---|
| **Severity** | H |
| **Where** | `internal/gmi/errors.go:32-38` (the rewritten claim); `internal/gmi/text/client.go:209-305` (`Chat` + new `attemptChat`); `internal/gmi/media/client.go:217-304` (`post` + new `attempt`); the third statement was text/client.go:157, now rewritten in the `Chat` doc. |
| **What was done** | PLAN.md §T2's contract is implemented exactly as written: **exactly one internal retry** on a failure classified `ErrTransient` (5xx, transport errors, a request-queue 200 whose `status` is `"failed"`, an undecodable 2xx body) — and **never a 4xx**, which the classifier now guarantees since every 4xx lands on a non-transient sentinel (see M1). A retry is also withheld when the caller's context is already dead. The contract's third sentence is enforced: when `ctx` carries no deadline, both clients apply `context.WithTimeout` with a package default (60s text / 120s media) before the first attempt; a caller-supplied deadline wins. The request-queue `failed` status is peeked with a minimal **named** struct (`queueStatus{Status string}`) — no `map[string]any`; the raw bytes still go back to callers on success. All three prose statements now agree: `errors.go` `ErrTransient` says each client retries once internally and the caller MAY retry beyond that; `Chat`'s doc says the same; PLAN §T2 needed no change. |
| **Test added** | `TestRetry_5xxHitTwice` and `TestChat_Retry_5xxHitTwice` (5xx ⇒ upstream hit exactly **twice**, one `ErrTransient`); `TestRetry_FailedStatusThenSuccess` (failed status ⇒ retried once, completed response returned as raw bytes); `TestRetry_FailedStatusBudgetSpent` (budget spent ⇒ `ErrTransient`, nil body); `TestClassifyStatus_Table` / `TestChatClassifyStatus_Table` (every 4xx row asserts `wantHits == 1` — never retried); `TestNoRetryWhenContextDead` (dead context ⇒ 1 hit); `TestDefaultDeadlineApplied` + `TestCallerDeadlineRespected` in both packages (deadline observed on the outbound request via a recording `RoundTripper`: default applied for `context.Background()`, caller's deadline respected). |
| **Pin re-run status** | Reviewer's `TestProbe_RetryOn5xx` re-run as `TestRetry_5xxHitTwice` — green (hits == 2). The reviewer's required second Pin (4xx hit exactly once) is asserted per-row in both classifier tables and in `TestPayloadTooLarge_413` — green. Mutations (retry budget removed in either client) — red, then green after revert. |

### M1 — Every unclassified 4xx is labelled `ErrTransient`, inviting the retry the contract forbids

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/gmi/text/client.go:320-337` and `internal/gmi/media/client.go:314-337` (`classifyStatus` in both); `internal/gmi/errors.go:20-26` (`ErrBadRequest` doc). |
| **What was done** | A catch-all arm `case code >= 400 && code < 500: return ErrBadRequest` sits after the specific arms in both classifiers; `default` keeps 3xx-not-followed and anything else as `ErrTransient`. The old `400 || 422` arm folds into the catch-all with identical classification for every status. **`ErrPaymentRequired`** is a new sentinel for 402 — billing is operator-actionable, its own class per PLAN.md §T11.2 — placed before the catch-all and never retried. `ErrBadRequest`'s doc now states the catch-all honestly (400, 413, 422, 418, …), per the AGENTS.md rule that a retry classification is part of the error's contract. |
| **Test added** | `TestPayloadTooLarge_413` (the reviewer's Pin: `ErrBadRequest`, NOT `ErrTransient`, hit once, nil body) and `TestPaymentRequired_402` (`ErrPaymentRequired`, not Transient/BadRequest, hit once) on the media path; `TestChat_PaymentRequired_402` on the text path; both classifier tables walk 401/402/403/404/413/418/429/302/502 with sentinel and hit-count assertions. |
| **Pin re-run status** | Reviewer's `TestProbe_413NotTransient` re-run as `TestPayloadTooLarge_413` — green. Mutation (narrow the catch-all so 413 falls to the default) — red, then green after revert. Round-1 Pins `TestChat_BadRequest_400` and `TestBadRequest_400` — green (residue check). |

### M2 — `GenerateImage` also defaults to the struck-through model

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/gmi/media/client.go:76` (`defaultImageModel = "Flux2-Klein"`) and client.go:137-139 (the default branch). |
| **What was done** | The empty-model default is `defaultImageModel` = **`Flux2-Klein`** — project.md §3 "Start on Flux2-Klein or Z-Image". The docstring cites §3 correctly this time (see L5) and states that §3's heading — choose on consistency, not price — is the rule the old default got backwards. |
| **Test added** | `TestGenerateImage_DefaultModelPinnedInRawJSON` — calls with the model **empty** and asserts the raw wire bytes contain `"model":"Flux2-Klein"` and do not contain `Qwen-Image-2512`. `TestSynthesizeSpeech_DefaultModelPinnedInRawJSON` does the same for the TTS default, which had the same never-executed-default-path blind spot. |
| **Pin re-run status** | Reviewer's M2 Pin (as H1, against `GenerateImage(ctx, "p", "")`) re-run as the raw-JSON pin — green. Mutation (restore `Qwen-Image-2512`) — red, then green after revert. |

### M3 — T2's `Done when` is not met, and the Status table says DONE

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `dev-diary/PLAN.md` §T2 `Done when` (rewritten) and the Status table (one new T2b row). The T2 status row itself is untouched — the orchestrator flips it on APPROVE. |
| **What was done** | Option (a) per the review's remediation note. The `Done when` now matches what shipped: raw-wire httptest coverage (envelope, bearer auth, prefix pin, typo pin, retry contract) with **raw bytes** returned to callers, and the rationale written down where the criterion lives — per-model result schemas GMI has not published, two-day window, a typed struct would be a fabrication. The typed-shape requirement moves onto T6/T8 where the model is known. The live-integration half becomes **T2b**, an operator track on the T1/T1b precedent (AGENTS.md §Testing rule 6: operator-only checks go in a `b`-suffixed track, not in prose), so the criterion stays mechanically checkable from a clean tree. The media package doc now points at the amended criterion instead of asserting the old one away in a docstring. |
| **Test added** | None — documentary finding; the fix is the plan edit plus the already-committed raw-wire pins the new criterion names. |
| **Pin re-run status** | The documentary Pin's ingredients changed state on purpose: `go test ./... -run Live` still matches nothing (by design — live is T2b's job now), while the raw-wire pins the amended criterion cites (`TestSynthesizeSpeech_TypoPinnedInRawJSON`, `TestGenerateImage_DefaultModelPinnedInRawJSON`) are green. |

### M4 — CI does not `gofmt` the package this track added

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `.github/workflows/verify.yml:58-78` (coverage floor); the gofmt step at :80-88. |
| **What was done** | Two parts. (1) **gofmt repo-wide** — already in place: the orchestrator's round-3 process commit (6bdb003) widened the step from `gofmt -l cmd/` to `gofmt -l .` alongside the review; verified present, commented, and clean on this tree. Recorded here so the row is honest about who did what. (2) **Coverage floor 75% → 85%** — raised **for `thutapi/internal/gmi/*` only**, as a two-tier gate (`MIN: 75.0`, `GMI_MIN: 85.0`), because both remediated packages reach it honestly (media 95.1%, text 90.2%) while `cmd/thutapi` sits at 79.7% and is outside T2's seam — a flat 85% would turn CI red on a package this track cannot fix. The orchestrator approved the two-tier shape mid-round; the deviation and rationale live in this row so the round-4 reviewer sees it. No tests were padded to get there: the points came from the classifier tables, retry/deadline branches, and the L1 decode paths this round already required. |
| **Test added** | None (CI change). The gate was executed locally against a fresh `go test ./... -race -cover` run: every package passes its tier. |
| **Pin re-run status** | Reviewer's Pin (badly-indented `internal/` file passes the old `gofmt -l cmd/` step) is void against the current workflow — `gofmt -l .` inspects the whole tree, and `gofmt -l .` is empty on this tree. The floor Pin: the awk gate run locally prints `ok` for all four packages and exits 0. |

### L1 — `ContentPart` is unreachable: `encoding/json` can never produce one

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/text/client.go:121-193` (`ContentPart`, `AssistantMessage` + `UnmarshalJSON` + `Text`). |
| **What was done** | The "`any` is not a data model" rule decides it, as the review expected: `AssistantMessage` gained an `UnmarshalJSON` that resolves the string|[]part union into typed fields — `TextBody` (string arm) or `Parts []ContentPart` (array arm), exactly one set, `interface{}` gone from the field list. A content value of any third shape fails loudly instead of decoding to something silently empty. `ContentPart` is now the type the unmarshaller actually produces (a non-text block survives with only its `Type`), documented as such. The `Text()` helper returns the reply as flat text — string arm as itself, array arm as its text parts concatenated in order — and errors when there is no text at all, so T4/T5/T7 never hand-roll the type switch. The type is documented decode-only. |
| **Test added** | `TestAssistantMessage_StringContentRawJSON` and `TestAssistantMessage_PartArrayRawJSON` — both union arms pinned at the raw-wire level, including a non-text part surviving Type-only; `TestAssistantMessage_UnusableContent` (object content ⇒ decode failure classified `ErrTransient`); `TestAssistantMessage_Text` (table: string arm, array concat, non-text-only ⇒ error, empty ⇒ error). `TestChat_HappyPath`'s assertions moved onto the typed fields. |
| **Pin re-run status** | Reviewer's grep (`ContentPart` referenced only by its own declaration) is void: the type is now produced by `UnmarshalJSON` and asserted by four tests. |

### L2 — Unreachable branch: `http.DetectContentType` never returns `""`

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/media/client.go:173-176`; the wrong docstring sentence was in the `EditImage` comment (old :122-125). |
| **What was done** | The `if mime == ""` branch and its wrong-comment rationale are deleted — `http.DetectContentType` always returns a valid MIME type (`application/octet-stream` is its own fallback). The adjacent hazard the review flagged is closed the belt-and-braces way: the sniffed value is cut at the first `;`, so a parameterized result (`text/plain; charset=utf-8`) can never be interpolated into the `data:` URI, and the docstring states the bare-MIME assumption instead of assuming it silently. |
| **Test added** | None beyond the existing data-URI pin — `TestEditImage` already asserts the PNG path produces `data:image/png;base64,…`, which is the only path T6 exercises; the parameter-cut has no observable wire effect on that path to assert without manufacturing a text-shaped image input. |
| **Pin re-run status** | n/a (dead code + comment). `TestEditImage`'s data-URI assertions green. |

### L3 — `post` returns a non-nil body together with a non-nil error

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/media/client.go:283-285` (non-200 path), :292-294 (failed-status path), inside `attempt`; enforced by the retry loop in `post` (:248-257). |
| **What was done** | Every error path in the media client now returns a nil body: non-200 ⇒ `return nil, classifyStatus(...)`, failed-status ⇒ nil + `ErrTransient`, and the retry loop returns `nil, err` on every exit. `attempt`'s doc states the invariant — raw is nil whenever err is non-nil — and `GenerateImage`/`EditImage`/`SynthesizeSpeech` inherit it. The upstream message is still surfaced to operators: it is embedded in the wrapped error. |
| **Test added** | `TestPayloadTooLarge_413` and `TestRetry_FailedStatusBudgetSpent` both assert `raw != nil` fails — a caller checking `err` never sees an error body as media. |
| **Pin re-run status** | n/a (convention). Both nil-body assertions green. |

### L4 — `Accept: application/json` is sent on the TTS call, which returns binary

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/media/client.go:80-85` (`acceptJSON` / `acceptAudio` consts) and per-call-site: :143, :184 (`acceptJSON`), :214 (`acceptAudio`). |
| **What was done** | `post` takes the Accept header per endpoint kind: image calls advertise `application/json`, `SynthesizeSpeech` advertises `audio/*`. A gateway that honours the header can no longer 406 the TTS call. |
| **Test added** | `captured` records the Accept header and all three wire-shape tests assert it — `application/json` on `TestGenerateImage`/`TestEditImage`, `audio/*` on `TestSynthesizeSpeech`. |
| **Pin re-run status** | n/a (header). Assertions green. |

### L5 — The `Qwen-Image-2512` docstring misreads the table it cites

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/media/client.go:66-75` (`defaultImageModel` doc), :125-132 (`GenerateImage` doc), :111-117 (envelope doc). |
| **What was done** | The cost argument is gone and the reasoning that produced H1/M2 is replaced: the docs now state that image models are chosen on **consistency, not price** (project.md §3's heading; four models tie at $0.10 a book, so cost selects nothing), the default cites §3's "Start on" sentence for what it is, and the Qwen example in the envelope doc is replaced with `Flux2-Klein`. No docstring in the package cites the forbidden model as viable any more. |
| **Test added** | None — doc-only; the behaviour behind it is pinned by the M2 raw-JSON test. |
| **Pin re-run status** | n/a. `grep -rn "Qwen-Image-2512" internal/` now matches only the docs that forbid it. |

## Residue against rounds 1 and 2

Round-1 residue: **zero.** M1's Pins (`TestChat_BadRequest_400`, `TestBadRequest_400`) re-run green under `-race`; the 400/422 classification they pin is preserved exactly by the new catch-all arm. L1 was a recorded false positive in round 1 and is untouched.

Round-2 residue: **zero.** Round 2 APPROVED on round-1 residue plus the `3695375..4a176a2` diff audit; this round re-ran its scope — the full suite, both round-1 Pins, and the wire-shape pins it audited (model prefix, `thinking` vs `reasoning_effort` negative pin, bearer auth, the `need_volumn_normalization` raw-JSON pin) — all green. No round-2 finding existed to regress.

Round-3 residue against this remediation: the review file's exit criteria are met item by item — every finding has a row above; H1/M2 close with default-path tests asserting wire bytes; H2 closes with code and all three prose statements agreeing; PLAN §T2 and the shipped client agree about typed structs; `gofmt -l .` runs repo-wide in CI; the three round-3 Pins and the round-1 Pins are green. The one deliberate deviation (two-tier coverage floor, M4 row) is recorded there with its reason and the orchestrator's approval.

## Aggregate verification

```
$ go vet ./...                                                  clean
$ go test ./... -race -count=1
ok   thutapi/cmd/thutapi           10.0s
ok   thutapi/internal/gmi/media     1.8s
ok   thutapi/internal/gmi/text      1.8s
$ go test ./... -cover
cmd/thutapi 79.7%   (floor 75%)
media       95.1%   (floor 85%)
text        90.2%   (floor 85%)
$ gofmt -l .                                                    empty
$ coverage-floor awk gate on the same run                        ok, exit 0
$ bash -n deploy/docker-run.sh                                   clean
```

Mutation checks (each applied by hand, observed red, reverted, `diff` byte-identical):

| Mutation | Pin that goes red |
|---|---|
| `model = "Qwen-Image-2512"` restored in `GenerateImage` | `TestGenerateImage_DefaultModelPinnedInRawJSON` |
| `EditImage` empty-model sentinel removed | `TestEditImage_EmptyModelRejected` |
| `maxAttempts = 1` in media `post` | `TestRetry_5xxHitTwice`, `TestRetry_FailedStatusThenSuccess` |
| `maxAttempts = 1` in text `Chat` | `TestChat_Retry_5xxHitTwice` |
| catch-all narrowed to `>= 420` in media `classifyStatus` | `TestPayloadTooLarge_413` |

## Files changed

* `internal/gmi/errors.go` — `ErrPaymentRequired` sentinel; `ErrBadRequest` doc states the 4xx catch-all; `ErrTransient` doc states the one-internal-retry contract; package doc example updated.
* `internal/gmi/text/client.go` — `defaultCallTimeout`; retry loop + `attemptChat`; `Chat` doc rewritten (the third prose statement); 402 + catch-all arms in `classifyStatus`; `AssistantMessage.UnmarshalJSON` + `Text()`; `ContentPart` now the produced type.
* `internal/gmi/text/client_test.go` — HappyPath assertions moved to typed fields; union-arm raw-wire tests; `Text()` table; retry, 402, classifier-table, dead-context, and deadline tests.
* `internal/gmi/media/client.go` — `defaultCallTimeout`, `defaultImageModel` (Flux2-Klein), per-endpoint Accept consts; `EditImage` empty-model sentinel; dead mime branch removed, parameter cut; `post`/`attempt` retry + deadline + nil-body-on-error; `queueStatus` named peek; 402 + catch-all arms; H1/M2/L5 docstrings; package doc rationale pointer.
* `internal/gmi/media/client_test.go` — default-model raw-JSON pins (t2i + TTS), empty-model rejection, retry pins (5xx twice, failed-status once-then-success, budget spent), 413/402 sentinels, classifier table, dead-context, deadline tests, Accept assertions.
* `.github/workflows/verify.yml` — two-tier coverage floor (85% for `internal/gmi/*`, 75% elsewhere); gofmt already repo-wide (6bdb003), verified and left as-is.
* `dev-diary/PLAN.md` — §T2 `Done when` amended (option a) and the T2b Status row added; the T2 status row itself untouched.
* `AGENTS.md` — two hunks (lines 12 and 60): severity counts `(0/0/0)` → `(0/0/0/0)`. Orchestrator in-line fix (doc-only, cannot change behaviour), recorded under the exemption's condition 3 — see the Scope note.
