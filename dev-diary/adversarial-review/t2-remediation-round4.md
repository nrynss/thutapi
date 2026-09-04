# T2 round 4 — remediation

| | |
|---|---|
| **Target** | All 4 findings (2 × M, 2 × L) in `dev-diary/adversarial-review/t2-round4.md`. Verdict was REMEDIATE (0C/0H/2M/2L) with zero residue against rounds 1–3. |
| **Date** | 2026-09-05 |
| **Commit** | Uncommitted at remediation time; the working tree stays uncommitted — the orchestrator commits on APPROVE (AGENTS.md §Process step 5). |
| **Round file** | `dev-diary/adversarial-review/t2-round4.md` is unchanged. The T2 status row in PLAN.md is untouched — it flips on APPROVE. |
| **Verification methodology** | Each Pin re-run against the remediated tree; the L1 Pin additionally mutation-checked (sanitization disabled by hand, observed red for the recorded reason, restored byte-identical via `cmp`, green again). Gate outputs in the aggregate section below. Live GMI probes remain impossible (round-3 record); everything is httptest. |
| **Scope note** | Four tracks: `AGENTS.md` (M1 — the two statements the mutation prescribes, no more), `dev-diary/adversarial-review/t2-remediation-round3.md` (M2 — the amendment the mutation prescribes), `internal/gmi/text/**` (L1), `internal/gmi/errors.go` (L2). Nothing else moved. |

## Rows

### M1 — Three definitions of the coverage floor, no two agreeing

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `AGENTS.md` §Testing item 7 (was :235-236) and §CI's `verify.yml` bullet (was :256-258). No code change — `.github/workflows/verify.yml:53-61` stays as the implementation of record. |
| **What was done** | Exactly the mutation's two statements. Item 7 now reads: "**Coverage floor: 75% of statements per package, 85% for `thutapi/internal/gmi/*`**, enforced in `verify.yml`" — the unqualified "It rises to 85% once T2's round-3 remediation lands" sentence is deleted. The §CI bullet now names the same two-tier floor: "a **two-tier coverage floor** (75% per package, 85% for `thutapi/internal/gmi/*`)". No other sentence in either section was reworded. |
| **Test added** | None — doc-only; the Pin is the round-4 grep triple. |
| **Pin re-run status** | Green. `grep -n "75%-per-package coverage floor" AGENTS.md` — no match; `grep -n "It rises to 85%" AGENTS.md` — no match; `grep -n "GMI_MIN: '85.0'" .github/workflows/verify.yml` — still `:61`. The three greps now describe one contract, and the cover output passes it: cmd/thutapi 79.7% ≥ 75, media 95.1% ≥ 85, text 90.4% ≥ 85. Mutation: reverting either AGENTS.md statement re-introduces the disagreement and the Pin goes red again (by construction — the Pin greps for exactly those sentences). |

### M2 — AGENTS.md modified in the working tree, unrecorded in the loop documents

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `dev-diary/adversarial-review/t2-remediation-round3.md` — the Scope note (line 10) and the Files-changed list (new final entry). |
| **What was done** | The mutation's letter. The Scope note now carries the clause: `AGENTS.md` lines 12 and 60, `(0/0/0)` → `(0/0/0/0)` (twice) — an orchestrator in-line fix (doc-only, cannot change behaviour), recorded by name and file per the in-line exemption's condition 3. The Files-changed list gains the matching `AGENTS.md` entry naming the two hunks and the same attribution. The round-3 rows themselves are untouched. |
| **Test added** | None — doc-only; the Pin is the Files-changed grep. |
| **Pin re-run status** | Green. `grep -c "AGENTS" <(sed -n '/## Files changed/,$p' dev-diary/adversarial-review/t2-remediation-round3.md)` now prints `1` (was `0`); the Scope note no longer asserts that every non-sanctioned change is inside `internal/gmi/**`. |

### L1 — `Reasoning` docstring vs the wire: `&Reasoning{}` marshalled `{"type":""}`

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/text/client.go` — `Chat`'s request-build path (the sanitization sits immediately before `json.Marshal(req)`); the `Reasoning` docstring (:101-106) and the `ChatRequest` doc (:111-114); `internal/gmi/text/client_test.go` (the new pin). |
| **What was done** | The wire now matches the docstring. `Chat` nils `Thinking` when it points at a zero `Reasoning` before marshalling, so `omitempty` drops the field — "an empty `Reasoning{}` omits the field and reasoning stays off" is now true. `req` is a value copy, so the caller's `ChatRequest` is untouched. The `Reasoning` docstring is kept verbatim (it is the claim the fix makes true). One clause beyond the prescribed statements: the `ChatRequest` doc's "Reasoning, when non-nil, is serialised" became "Thinking serialises a non-empty Reasoning … (an empty Reasoning{} is dropped — see Reasoning)" — leaving it would have traded the round-4 false docstring for a fresh one one struct above. |
| **Test added** | `TestChat_EmptyReasoningOmittedOnWire` — permanent raw-wire pin, both arms, asserted against the marshalled bytes of the request the client actually sends (httptest capture), not a decoded struct: `&Reasoning{}` ⇒ the wire contains no `"thinking"` key at all; `&Reasoning{Type: "enabled"}` ⇒ the wire contains `"thinking":{"type":"enabled"}`. Going through `Chat` is what makes it load-bearing: the sanitization lives in the request path, so a bare `json.Marshal` pin could not see its removal. |
| **Pin re-run status** | Green under `-race -count=1` (both subtests). Mutation-checked: the nil-ing disabled by hand ⇒ the empty arm fails with exactly the round-4 probe's transcript (`wire = {"model":"MiniMaxAI/MiniMax-M3","messages":[{"role":"user","content":"hi"}],"thinking":{"type":""}}, want the thinking key absent`); restored, `cmp` byte-identical, green again. |

### L2 — `ErrModelNotFound` docstring implied message-conditional classification

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/errors.go:54-62` (the `ErrModelNotFound` doc). Classification code unchanged — `internal/gmi/media/client.go:327-328` and the text classifier's 404 arm stay as the round-1-approved mapping. |
| **What was done** | The docstring now states status-only classification: `ErrModelNotFound` is returned on HTTP 404 from either endpoint — text and request queue alike — regardless of what the error message says; the message is never inspected, and matching it is forbidden (AGENTS.md §Errors), the upstream text being surfaced verbatim for the operator. The "MiniMaxAI/" prefix sentence is kept. The message-conditional sentence ("whose error message identifies a missing or unknown model") is gone. |
| **Test added** | None — the behaviour was never wrong; `TestClassifyStatus_Table` already pins it, including the 4xx rows that prove the doc's old condition was neither necessary nor sufficient. |
| **Pin re-run status** | Green. `TestClassifyStatus_Table` re-run under `-race -count=1` — PASS; the round-4 probe's two rows remain true of the pinned behaviour (404 silent about models ⇒ sentinel; 400 naming an unknown model ⇒ `ErrBadRequest`), and the docstring now agrees with both. Mutation: restoring the message-conditional wording would re-introduce the defect by construction (doc-only; no test pins prose). |

## Residue against rounds 1–4

Round-4 residue: **zero.** All four findings have a row above with the mutation's letter followed; no new code path was introduced beyond L1's two-line sanitization, and the full suite (which exercises every round-1/2/3 Pin) is green.

Rounds 1–3 residue: **zero**, as round 4 already confirmed — no remediation here touched a pinned path except L1's, which strengthens its own pin. Round-1 Pins (`TestChat_BadRequest_400`, `TestBadRequest_400`) and the round-3 Pins (retry, 413/402, classifier tables, defaults, deadline) all pass inside the `-race -count=1` full-suite run below.

## Aggregate verification

```
$ go vet ./...                                                  clean
$ go test ./... -race -count=1
ok   thutapi/cmd/thutapi           9.9s
ok   thutapi/internal/gmi/media     1.8s
ok   thutapi/internal/gmi/text      1.8s
$ go test ./... -cover
cmd/thutapi 79.7%   (floor 75%)
media       95.1%   (floor 85%)
text        90.4%   (floor 85%)
$ gofmt -l .                                                    empty
$ coverage-floor awk gate on the same run                        ok, exit 0
```

Mutation check (applied by hand, observed red, reverted, `cmp` byte-identical):

| Mutation | Pin that goes red |
|---|---|
| L1 sanitization disabled (`if false && …`) | `TestChat_EmptyReasoningOmittedOnWire/empty_Reasoning_omits_the_field` |

## Files changed

* `AGENTS.md` — §Testing item 7 and the §CI `verify.yml` bullet now state the two-tier floor (M1).
* `dev-diary/adversarial-review/t2-remediation-round3.md` — Scope note + Files-changed entry recording the AGENTS.md hunks as an orchestrator in-line fix (M2).
* `internal/gmi/text/client.go` — `Chat` nils a zero `Reasoning` before marshalling; `ChatRequest` doc clause made truthful (L1).
* `internal/gmi/text/client_test.go` — `TestChat_EmptyReasoningOmittedOnWire` raw-wire pin (L1).
* `internal/gmi/errors.go` — `ErrModelNotFound` doc rewritten to status-only classification (L2).
* `dev-diary/adversarial-review/t2-remediation-round4.md` — this file.
