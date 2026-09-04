# T2 round 5 — adversarial review of the GMI clients (post-round-4 remediation)

| | |
|---|---|
| **Target** | T2 at `6bdb003` ("T2 reopened at round 3, and the process gaps that let it close") with the **round-3 and round-4 remediations present as uncommitted working-tree changes** — 8 modified files plus the untracked `t2-round4.md`, `t2-remediation-round3.md`, `t2-remediation-round4.md`. This is the state round 5 reviews; per AGENTS.md §Process step 5 the commit lands only after this verdict. All nine snapshot files (`AGENTS.md`, `internal/gmi/errors.go`, both clients and both test files, `PLAN.md`, `verify.yml`, `t2-remediation-round3.md`) were sha256-recorded before probing and verified identical after; the probe file was run, recorded and removed; the tree at review close is byte-identical to the tree at review start. |
| **Evidence** | `t2-round4.md` and `t2-remediation-round4.md` (the four fixes under review) in full; `t2-round3.md` + `t2-remediation-round3.md` (including the amended Scope note and Files-changed list) in full; `t2-round1.md`, `t2-remediation-round1.md`, `t2-round2.md`; `AGENTS.md` (post-edit §Testing item 7 + §CI) in full; `dev-diary/adversarial-review/README.md`; `PLAN.md` §T2/§T2b + Status table; the code in full: `internal/gmi/errors.go`, `internal/gmi/text/client.go`, `internal/gmi/text/client_test.go`, `internal/gmi/media/client.go`, `internal/gmi/media/client_test.go`, `.github/workflows/verify.yml`; `git status`/`git diff HEAD` for provenance of every hunk judged. |
| **Method** | Round 4's, repeated: probe, don't trust prose. (1) Re-ran every round-4 Pin — the M1 grep triple, the M2 Files-changed grep, both arms of `TestChat_EmptyReasoningOmittedOnWire` (L1, raw wire through `Chat`), `TestClassifyStatus_Table` (L2/M1 behaviour) — recorded verbatim below. (2) Re-ran the five round-3 core Pins and both round-1 residue Pins as regression checks. (3) Verified the round-4 edits are **exactly** the prescribed mutations and nothing more: the `AGENTS.md` diff is four hunks (two round-3 `0/0/0/0` fixes, both recorded in the loop documents; the two two-tier-floor statements of M1, both sanctioned); L1's sanitization sits in `Chat`'s request-build path immediately before `json.Marshal` on a value-copy parameter; the L2 docstring states status-only classification and the message-conditional sentence is gone (grep: no match). (4) One reviewer-written probe (`zz_probe_test.go`, removed after the round) pinning the one remediation claim no permanent test asserts — the caller's `ChatRequest` and its `Reasoning` pointee untouched through `Chat`. (5) L1's Mutation re-applied by hand (sanitization disabled), observed red for the recorded reason, restored `cmp` byte-identical, green again. (6) Gates: `go vet ./...`, `go test ./... -race -count=1`, `gofmt -l .`, `go test ./... -cover`, and the `verify.yml` awk gate run verbatim — plus its negative control (`gmiMin=96.0` → exit 1). |
| **Date** | 2026-09-05 |

## Verdict

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**

Every round-4 Pin is green on the remediated tree, and each fix is exactly
the mutation its finding prescribed — no more, no less:

- **M1** — the two stale sentences are gone from `AGENTS.md`; item 7 and the
  §CI bullet now state the two-tier floor the gate implements. Three
  statements, one contract, and the cover output satisfies it (cmd 79.7% ≥
  75; media 95.1% ≥ 85; text 90.4% ≥ 85 — the +0.2 on text over round 4 is
  the new pin's covered statements).
- **M2** — the AGENTS.md hunks are recorded by name and file in
  `t2-remediation-round3.md` (Scope-note clause + Files-changed entry, grep
  = 1, was 0), and the Scope note is now true of the tree it ships in.
- **L1** — the wire now matches the docstring: `Chat` nils a zero
  `Reasoning` before marshalling, `omitempty` drops the field, and the
  permanent pin asserts both arms against the raw bytes of the request the
  client actually sends. The sanitization is at request build (two
  statements, immediately before `json.Marshal`), `req` is a value copy, and
  the probe confirms the caller's struct and pointee are untouched. The
  Mutation re-check reproduced the remediation's recorded failure transcript
  byte for byte.
- **L2** — `ErrModelNotFound`'s doc now states status-only classification on
  both endpoints, the message-conditional sentence is deleted, and no
  message-matching crept in anywhere (grep: no match; the media 404 arm at
  `client.go:327-328` still never reads the body for classification).

No new findings. Round-1/2/3/4 residue is zero, severity by severity,
claimed explicitly below. The gates (`go vet`, `-race -count=1`, `gofmt -l .`,
`-cover`, the awk coverage gate) are green, and the gate is load-bearing in
both directions (negative control exits 1).

---

## Severity key

Per `dev-diary/adversarial-review/README.md`. C = breaks the demo. H = real
defect the demo survives. M = real defect with a workaround. L = polish. No
severity is exempt.

---

## Round-4 Pins re-run (2026-09-05, verbatim)

### M1 — the three greps now describe one contract

```console
$ grep -n "75%-per-package coverage floor" AGENTS.md
$ echo $?
1
$ grep -n "It rises to 85%" AGENTS.md
$ echo $?
1
$ grep -n "GMI_MIN" .github/workflows/verify.yml
61:          GMI_MIN: '85.0'
63:          awk -v min="$MIN" -v gmiMin="$GMI_MIN" '
$ go test ./... -cover
ok  	thutapi/cmd/thutapi	(cached)	coverage: 79.7% of statements
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	(cached)	coverage: 95.1% of statements
ok  	thutapi/internal/gmi/text	1.833s	coverage: 90.4% of statements
```

Both stale sentences: no match. The two-tier gate: in place at `:61`. The
cover output passes every package at its tier. Round-4 exit criterion 1 is
met: one decision, three agreeing statements.

### M2 — the accounting grep

```console
$ sed -n '/## Files changed/,$p' dev-diary/adversarial-review/t2-remediation-round3.md | grep -c "AGENTS"
1
```

The Files-changed list carries the `AGENTS.md` entry (two hunks, lines 12
and 60, `(0/0/0)` → `(0/0/0/0)`, orchestrator in-line fix recorded under the
exemption's condition 3), and the Scope note states the same edit. The Scope
note's "everything else is inside `internal/gmi/**`" is now true of the
tree. Round-4 exit criterion 2 is met.

### L1 — `TestChat_EmptyReasoningOmittedOnWire`, both arms, raw wire

```console
$ go test -race -count=1 -v -run 'TestChat_EmptyReasoningOmittedOnWire' ./internal/gmi/text/
=== RUN   TestChat_EmptyReasoningOmittedOnWire
=== RUN   TestChat_EmptyReasoningOmittedOnWire/empty_Reasoning_omits_the_field
=== RUN   TestChat_EmptyReasoningOmittedOnWire/enabled_emits_the_thinking_switch
--- PASS: TestChat_EmptyReasoningOmittedOnWire (0.00s)
    --- PASS: TestChat_EmptyReasoningOmittedOnWire/empty_Reasoning_omits_the_field (0.00s)
    --- PASS: TestChat_EmptyReasoningOmittedOnWire/enabled_emits_the_thinking_switch (0.00s)
PASS
ok  	thutapi/internal/gmi/text	1.013s
```

The empty arm fails with the round-4 probe's transcript when the
sanitization is disabled (Mutation re-check below), so the pin is
load-bearing: it pins the wire bytes, not the comment.

### L2 — `TestClassifyStatus_Table`, all nine rows

```console
$ go test -race -count=1 -v -run 'TestClassifyStatus_Table' ./internal/gmi/media/
=== RUN   TestClassifyStatus_Table
=== RUN   TestClassifyStatus_Table/401_unauthorized
=== RUN   TestClassifyStatus_Table/402_payment_required
=== RUN   TestClassifyStatus_Table/403_forbidden
=== RUN   TestClassifyStatus_Table/404_model_not_found
=== RUN   TestClassifyStatus_Table/413_payload_too_large
=== RUN   TestClassifyStatus_Table/418_teapot_is_the_4xx_catch-all
=== RUN   TestClassifyStatus_Table/429_rate_limited
=== RUN   TestClassifyStatus_Table/302_not_followed_is_the_default_arm
=== RUN   TestClassifyStatus_Table/502_bad_gateway
--- PASS: TestClassifyStatus_Table (0.01s)
    --- PASS: TestClassifyStatus_Table/401_unauthorized (0.00s)
    --- PASS: TestClassifyStatus_Table/402_payment_required (0.00s)
    --- PASS: TestClassifyStatus_Table/403_forbidden (0.00s)
    --- PASS: TestClassifyStatus_Table/404_model_not_found (0.00s)
    --- PASS: TestClassifyStatus_Table/413_payload_too_large (0.00s)
    --- PASS: TestClassifyStatus_Table/418_teapot_is_the_4xx_catch-all (0.00s)
    --- PASS: TestClassifyStatus_Table/429_rate_limited (0.00s)
    --- PASS: TestClassifyStatus_Table/302_not_followed_is_the_default_arm (0.00s)
    --- PASS: TestClassifyStatus_Table/502_bad_gateway (0.00s)
PASS
ok  	thutapi/internal/gmi/media	1.019s
```

The 404 row (sentinel present, message silent about models) and the 400- /
418-style rows (message named or not, sentinel is `ErrBadRequest`) are the
two directions the round-4 L2 probe used to convict the old docstring; the
behaviour they pin is unchanged, and the docstring now agrees with them.

---

## Round-3 core Pins re-run (regression check, 2026-09-05)

```console
$ go test -race -count=1 -v -run 'TestEditImage_EmptyModelRejected|TestRetry_5xxHitTwice$|TestPayloadTooLarge_413|TestGenerateImage_DefaultModelPinnedInRawJSON|TestSynthesizeSpeech_TypoPinnedInRawJSON' ./internal/gmi/media/
=== RUN   TestSynthesizeSpeech_TypoPinnedInRawJSON
--- PASS: TestSynthesizeSpeech_TypoPinnedInRawJSON (0.00s)
=== RUN   TestGenerateImage_DefaultModelPinnedInRawJSON
--- PASS: TestGenerateImage_DefaultModelPinnedInRawJSON (0.00s)
=== RUN   TestEditImage_EmptyModelRejected
--- PASS: TestEditImage_EmptyModelRejected (0.00s)
=== RUN   TestRetry_5xxHitTwice
--- PASS: TestRetry_5xxHitTwice (0.00s)
=== RUN   TestPayloadTooLarge_413
--- PASS: TestPayloadTooLarge_413 (0.00s)
PASS
ok  	thutapi/internal/gmi/media	1.018s
```

H1, H2, M1, M2 and the round-1 quirk pin: all green. Round-1's residue Pins
re-run green verbatim as well:

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
ok  	thutapi/internal/gmi/text	1.012s
```

---

## The round-4 edits are exactly the prescribed mutations, and nothing more

- **`AGENTS.md`** — `git diff HEAD` shows exactly four hunks: the two
  round-3 `(0/0/0)` → `(0/0/0/0)` fixes (lines 12 and 60), both recorded in
  `t2-remediation-round3.md` (M2) and both now sanctioned; item 7 rewritten
  to "**Coverage floor: 75% of statements per package, 85% for
  `thutapi/internal/gmi/*`**, enforced in `verify.yml`" with the
  "It rises to 85% once T2's round-3 remediation lands" sentence deleted;
  the §CI bullet naming "a **two-tier coverage floor** (75% per package, 85%
  for `thutapi/internal/gmi/*`)". Diffstat: 6 insertions, 5 deletions,
  nothing else. No third statement was touched.
- **L1** — the sanitization is two statements in `Chat`'s request-build path
  (`client.go:257-258`), immediately before `json.Marshal(req)` (:260), on a
  value-copy parameter — the caller's `ChatRequest` cannot be reached. The
  `Reasoning` docstring (:101-106) is kept verbatim — it is the claim the
  fix makes true — and the one clause beyond the prescribed statements (the
  `ChatRequest` doc's "serialises a non-empty Reasoning … an empty
  Reasoning{} is dropped") is exactly what `t2-remediation-round4.md`
  records, with its reason. The new permanent test is the only test the fix
  added, and it asserts the wire the client actually sends.
- **L2** — `errors.go:54-62` now states status-only classification on both
  endpoints, "the message is never inspected", and keeps the `MiniMaxAI/`
  prefix sentence. The message-conditional sentence is gone (`grep -n
  "identifies a missing or unknown model" internal/gmi/errors.go` → no
  match) and no message matching crept in: the media 404 arm
  (`media/client.go:327-328`) and the text 404 arm still never read the body
  for classification. Classification code unchanged, as the remediation row
  claims.

---

## Mutation re-check (applied by hand, observed red, restored byte-identical)

| Mutation | Pin that went red | Observed |
|---|---|---|
| L1 sanitization disabled (`if false && req.Thinking != nil && …`, `client.go:257`) | `TestChat_EmptyReasoningOmittedOnWire/empty_Reasoning_omits_the_field` | `wire = {"model":"MiniMaxAI/MiniMax-M3","messages":[{"role":"user","content":"hi"}],"thinking":{"type":""}}, want the thinking key absent` — the round-4 probe's transcript, reproduced exactly; the enabled arm stayed green |

Restored from a pre-mutation copy: `cmp` byte-identical, `sha256sum`
matches the pre-review snapshot (`04897892…`), pin green again. The
remediation's mutation-check claim is verified, not just recorded.

## Probe (run, recorded, removed — `internal/gmi/text/zz_probe_test.go`)

The one remediation claim no permanent test asserts is "req is a value copy,
so the caller's ChatRequest is untouched". Language-guaranteed for the
struct field, but not for the `Reasoning` **pointee** a sloppy fix could
have zeroed through the copy — so it was probed through the real `Chat`
path, asserting both the absence of `"thinking"` on the wire and that the
caller's pointer and pointee survive the call:

```console
$ go test -race -count=1 -v -run 'TestProbe_CallerStructUntouched' ./internal/gmi/text/
=== RUN   TestProbe_CallerStructUntouched
--- PASS: TestProbe_CallerStructUntouched (0.00s)
PASS
ok  	thutapi/internal/gmi/text	1.011s
```

The file was removed after the run; the tree is byte-identical at close
(sha256 all nine snapshot files, `git status --porcelain` unchanged except
this round file).

---

## Gates (run by this reviewer, 2026-09-05)

```console
$ go vet ./...                                                clean (exit 0)
$ go test ./... -race -count=1
ok  	thutapi/cmd/thutapi	10.032s
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	1.835s
ok  	thutapi/internal/gmi/text	1.834s
$ gofmt -l .                                                  empty
$ go test ./... -cover
cmd/thutapi 79.7% (floor 75)   media 95.1% (floor 85)   text 90.4% (floor 85)
$ verify.yml awk gate verbatim (MIN=75.0, GMI_MIN=85.0) on the same output:
  ok thutapi/cmd/thutapi: 79.7% / warn internal/gmi: no test files /
  ok internal/gmi/media: 95.1% / ok internal/gmi/text: 90.4%  — exit 0
  (negative control: gmiMin=96.0 → FAIL media, FAIL text, exit 1)
```

The awk gate is load-bearing in both directions on this tree: green at the
configured tiers, exit 1 the moment either gmi floor is raised past either
package.

---

## Residue claim

Against **round 1** (M1, L1): **zero.** Both round-1 Pins re-run green
verbatim above; the 400-classification they pin is preserved by the 4xx
catch-all the classifier table walks. L1 remains the recorded false
positive; `media/client.go:193-194` is unchanged and still reads as natural
English.

Against **round 2** (no findings; scope = round-1 residue plus the
`3695375..4a176a2` diff audit): **zero.** No round-2 finding exists to
regress; its audited surface (sentinels, classification shape, wire pins,
env-override seam) is green inside this round's `-race -count=1` full-suite
run.

Against **round 3** (2 × H, 4 × M, 5 × L): **zero.** H1, H2, M1 and M2
re-verified through their Pins, green verbatim above (spot re-verification
per this round's scope). M3 verified by reading: `PLAN.md` §T2 carries the
amended `Done when` (raw-wire criterion + T2b operator split) and the T2b
Status row, while the T2 row correctly still reads REMEDIATE — it flips on
this verdict's commit, not before. M4 verified operationally: the two-tier
gate read in `verify.yml` (:58-78) and executed in both directions. The
round-3 L rows' permanent tests (content-union raw-wire pins, `Text()`
table, Accept-header assertions, nil-body-on-error assertions, dead-context
and deadline tests) all pass inside the full-suite run; L2's dead branch is
gone from the source (no `mime == ""` remains to re-check) and L5's docs no
longer cite the forbidden model as viable.

Against **round 4** (2 × M, 2 × L): **zero.** M1: grep triple green, one
contract, cover satisfies it (exit criterion 1). M2: grep = 1, Scope note
true of the tree (exit criterion 2). L1: permanent raw-wire pin green both
arms, Mutation re-checked red for the recorded reason, caller-struct claim
probed (exit criterion 3, wire half). L2: docstring states status-only on
both endpoints, no message matching anywhere, behaviour pinned by the
classifier table (exit criterion 3, doc half). Exit criterion 4 (Pins +
gates green) is met by the tables above; exit criterion 6 is this section.

**New residue, created by this round:** none — 0 × C, 0 × H, 0 × M, 0 × L.

---

## What this round did not examine

Stated so the zero-residue claim has a boundary:

- **Live endpoint behaviour** — not probed; live GMI probes remain
  impossible from this workstation (the operator key is rejected by both
  providers, unchanged since round 1 and recorded in the round files). All
  pins are httptest/raw-JSON. T2b owns the live half and stays open.
- **The round-4 boundary judgements, carried forward un reopened** — the
  prefix/thinking pins' altitude (judged not a finding in round 4), bare
  non-sentinel validator errors, the request-side `Message.Content`
  interface{}, and the request-queue `queued`/`processing` 200 passthrough.
  This round re-read the code behind each and saw nothing that contradicts
  round 4's judgements.
- **The full round-3 remediation diff** — re-audited by round 4; this round
  spot-verified its H/M fixes (Pins above), read its M3/M4 outcomes where
  they live, and found no drift, but did not line-by-line re-diff all eight
  files.
- **`cmd/thutapi` internals, T1's deploy surface, the five Traefik labels,
  PLAN sections for T3–T14, project.md §2b's operator-verified multimodal
  claims, and git history before `3695375`** — outside this round's seam,
  as in rounds 3 and 4.

---

## Close-out

**T2 can close.** This APPROVE is the round the loop was waiting for: verdict
APPROVE with 0/0/0/0 and an explicit zero-residue claim against rounds 1
through 4. Per AGENTS.md §Process step 5, the orchestrator now lands the
remediation as the focused T2 commit — with the Files-changed lists of
`t2-remediation-round3.md` and `t2-remediation-round4.md` true of that
commit — and flips the `PLAN.md` T2 status row to DONE. T2b remains open as
the operator track and runs once a working key exists.
