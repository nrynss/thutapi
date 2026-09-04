# T2 round 4 — adversarial review of the GMI clients (post-round-3 remediation)

| | |
|---|---|
| **Target** | T2 at `6bdb003` ("T2 reopened at round 3, and the process gaps that let it close") with the **round-3 remediation present as uncommitted working-tree changes** — 8 modified files plus the untracked `t2-remediation-round3.md`. This is the state round 4 reviews: the remediation lands as a commit only after this round's verdict (t2-remediation-round3.md header, per AGENTS.md §Process step 5). All sha256 checksums and the `git status` snapshot below were taken before and after probing; the tree at review close is byte-identical to the tree at review start. |
| **Evidence** | `AGENTS.md` (in full); `dev-diary/project.md` (§Pipeline, §2, §2b, §3 model table, §4, §Architecture consequences); `dev-diary/PLAN.md` (§T2 amended Done-when + Retry contract, §T2b Status row, §T6, §Architectural invariants, §Unowned seams, Status table); `dev-diary/adversarial-review/README.md`; `t2-round1.md`, `t2-remediation-round1.md`, `t2-round2.md`, `t2-round3.md`, `t2-remediation-round3.md`; the code in full: `internal/gmi/errors.go`, `internal/gmi/text/client.go`, `internal/gmi/text/client_test.go`, `internal/gmi/media/client.go`, `internal/gmi/media/client_test.go`, `.github/workflows/verify.yml`; `git log`/`git show`/`git diff HEAD` for provenance of every doc string a finding rests on. |
| **Method** | Round 3's, repeated: probe, don't trust prose. (1) Re-ran every round-3 Pin (now permanent tests) and every round-1 residue Pin under `-race -count=1 -v`, output recorded verbatim. (2) Applied the load-bearing Mutations by hand — restored a model default on `EditImage`; set the retry budget to 1 in both clients; narrowed the media 4xx catch-all — observed each Pin go red for the named reason, reverted, confirmed sha256-identical. (3) Fresh-eyes of the whole seam against the amended Done-when and the spec facts, with two reviewer-written probes in `zz_probe_test.go` files (one per package), run, recorded, removed. (4) Judged the M3 plan amendment and the T2b split. (5) Judged M4's two-tier coverage floor against AGENTS.md §Testing item 7. (6) Ran the gates: `go vet ./...`, `go test ./... -race -count=1`, `gofmt -l .`, `go test ./... -cover`, plus the workflow's exact awk coverage gate (positive and negative direction). Live GMI probes remain impossible (the operator key is rejected by both providers — blocker unchanged since round 1); wire contracts judged via httptest and raw-JSON pins only. |
| **Date** | 2026-09-05 |

## Verdict

**REMEDIATE — 0 × C, 0 × H, 2 × M, 2 × L.**

The round-3 remediation itself is good work, and its central claims all survived
adversarial re-testing. Every round-3 Pin passes as a permanent test under
`-race`; every Mutation I applied by hand turned the named Pin red for exactly
the recorded reason; the coverage numbers in the remediation file (media 95.1%,
text 90.2%, cmd 79.7%) reproduce to the decimal; the awk coverage gate passes
all packages, and fails (exit 1) when the floor is raised past any of them, so
the gate is load-bearing in both directions; `go vet` and `gofmt -l .` are
clean; the full module passes under `-race -count=1`. The H1/H2/M1/M2 fixes are
real, default-path-tested, and doc-consistent. The M3 amendment and the T2b
split are lawful (judged below). Round-1/2/3 residue is zero, severity by
severity, claimed explicitly below.

What fails is the paper trail around the gate and the binding file, plus two
docstrings that misdescribe wire/classification behaviour — the exact defect
class round 3 exists for:

- **M1 (new)** — three statements define the coverage floor and no two agree:
  AGENTS.md §CI says a 75%-per-package floor; AGENTS.md §Testing item 7 says
  the floor "rises to 85% once T2's round-3 remediation lands" (unqualified —
  and the remediation has landed, so the letter says 85% everywhere, which
  cmd/thutapi at 79.7% fails); verify.yml implements a two-tier 75/85 gate and
  cites item 7 as its authority for a shape item 7 does not describe. The
  two-tier shape itself is right (judged below); the binding docs were never
  conformed to it.
- **M2 (new)** — the working tree modifies `AGENTS.md` (two hunks: `(0/0/0)` →
  `(0/0/0/0)`), but the remediation file's Scope note says "Everything else is
  inside `internal/gmi/**`" and its Files-changed list omits AGENTS.md. A
  substantively correct edit to the binding protocol file arrives unrecorded —
  the unreviewed-arrival the role separation exists to prevent.
- **L1 (new)** — the `Reasoning` docstring claims "an empty `Reasoning{}` omits
  the field"; the probe shows `&Reasoning{}` marshals `"thinking":{"type":""}`.
  Only a nil `Thinking` omits. A T4 author coding phase A "off" per that doc
  sends a junk field believing it omitted.
- **L2 (new)** — the `ErrModelNotFound` docstring conditions the sentinel on
  "any request-queue response whose error message identifies a missing or
  unknown model"; classification is status-only (probe: a 404 silent about
  models gets the sentinel; a 400 naming an unknown model does not). In the
  package whose stated doctrine is never to parse the message.

No C and no H: nothing found blocks the demo or breaks a caller today. All
four findings are cheap to close before the round-3 remediation is committed —
which is the right order, so the commit that lands T2 contains an accurate
audit trail.

---

## Severity key

Per `dev-diary/adversarial-review/README.md`. C = breaks the demo. H = real
defect the demo survives. M = real defect with a workaround. L = polish. No
severity is exempt.

---

## M1 — Three definitions of the coverage floor, no two agreeing, and the tree currently violates one of them

- **Severity:** M.
- **Where:** `AGENTS.md:257` (§CI and images: "a **75%-per-package coverage
  floor**"); `AGENTS.md:235-236` (§Testing item 7: "**Coverage floor: 75% of
  statements per package**, enforced in `verify.yml`. It rises to 85% once T2's
  round-3 remediation lands."); `.github/workflows/verify.yml:53-61` (the
  two-tier gate: `MIN: '75.0'`, `GMI_MIN: '85.0'`, commented "Two-tier per
  AGENTS.md item 7 and t2-round3.md M4").
- **What:** the gate and the docs implement three different contracts.
  §CI's letter: one floor, 75%, every package — contradicted by the gate's 85%
  tier for `internal/gmi/*`. Item 7's letter: the floor rises to 85% once the
  round-3 remediation lands — unqualified, and the remediation **has** landed
  in this tree, so item 7 as written makes the current tree a violator:
  `cmd/thutapi` measures 79.7% (`go test ./... -cover`, below), and no track
  owns raising it. The implemented reality is two-tier 75/85 — a shape neither
  AGENTS.md statement describes, and which verify.yml justifies by citing item
  7 for something item 7 does not say. The deviation itself is orchestrator-
  approved and honestly recorded in `t2-remediation-round3.md` §M4 — but a
  mid-round remediation-file note is not where the floor's definition lives;
  AGENTS.md is, and it still says three things. Concrete drift vectors: an
  agent reading §CI "fixes" the gmi tier back down to 75 and silently undoes
  M4's raise; an agent reading item 7 globally raises `cmd/thutapi`'s floor to
  85 and turns CI red on a package no open track owns — the exact failure the
  remediation row warns against. Both edits would cite AGENTS.md correctly.
  This is the H2 defect class (doc asserts what the mechanism does not) at
  protocol level, one tier down in severity because the true gate is
  discoverable from verify.yml.
- **Pin (re-run 2026-09-05 on the reviewed tree):**

  ```console
  $ grep -n "75%-per-package coverage floor" AGENTS.md
  257:  `go test ./... -race -cover`, a **75%-per-package coverage floor**,
  $ grep -n "It rises to 85%" AGENTS.md
  236:    `verify.yml`. It rises to 85% once T2's round-3 remediation lands. The floor
  $ grep -n "GMI_MIN: '85.0'" .github/workflows/verify.yml
  61:          GMI_MIN: '85.0'
  $ go test ./... -cover
  ok  	thutapi/cmd/thutapi	(cached)	coverage: 79.7% of statements
  ok  	thutapi/internal/gmi/media	0.816s	coverage: 95.1% of statements
  ok  	thutapi/internal/gmi/text	0.813s	coverage: 90.2% of statements
  ```

  The failure is mechanical: per item 7's letter the floor is now 85% and
  `cmd/thutapi` at 79.7% fails it; per §CI's letter the gmi packages exceed
  their floor with no authority for the 85% tier; per the gate all three
  packages pass. One criterion, three verdicts.
- **Mutation:** conform AGENTS.md to the implemented gate — item 7 becomes
  "Coverage floor: 75% of statements per package, 85% for
  `thutapi/internal/gmi/*`" and §CI's bullet names the two-tier floor — and the
  Pin's three greps describe one contract; the cover output passes all of them.
  Reverting either AGENTS.md statement re-introduces the disagreement and the
  Pin goes red again. (The inverse fix — flattening the gate to one tier — is
  also a lawful closure, but it must then survive the `cmd/thutapi` at 79.7%
  problem explicitly; see M4 judgement below.)

### Judgement the round was asked to make: is the two-tier deviation itself sound?

Yes. `cmd/thutapi` (79.7%) is outside T2's `Owns`; the remediation agent
raising its coverage would be fixing something nobody found — the scope creep
AGENTS.md §Agentic development forbids — and a flat 85% would leave every push
red until some future track happened to own that package. Holding the
remediated packages to 85% and leaving the rest at 75% is the shape item 7's
own logic ("the floor catches an untested *package*") wants. The defect is not
the deviation; it is that the deviation lives only in a remediation-file row
while the binding docs still describe three different gates.

---

## M2 — AGENTS.md is modified in the working tree, and no loop document records the edit

- **Severity:** M.
- **Where:** `AGENTS.md:12` and `AGENTS.md:60` (working tree vs `6bdb003`:
  `(0/0/0)` → `(0/0/0/0)`, twice); `dev-diary/adversarial-review/t2-remediation-round3.md`
  — the Scope note ("Cross-track edits are the ones the round-3 review
  sanctioned: M4 touches `.github/workflows/verify.yml`, M3 amends PLAN.md
  §T2's Done-when plus a T2b Status row. **Everything else is inside
  `internal/gmi/**`.**") and the Files-changed list (seven entries, AGENTS.md
  absent).
- **What:** the tree this round reviews contains a two-hunk edit to the
  binding protocol file that neither the remediation file's scope statement
  nor its Files-changed list accounts for, and no review file records it under
  the in-line exemption's condition 3 ("recorded by name and file in that
  round's review file"). The edit's content is trivially correct — the loop
  approves on 0/0/0/0 across four severities everywhere else, and the two
  changed sentences were miscounted — but process is the product here: an
  unrecorded change to AGENTS.md is precisely the "arrives unreviewed" arrival
  the four-role separation exists to prevent, it makes the remediation file's
  own Scope note false, and it is invisible to any reviewer who diffs only the
  claimed file list (this review found it only by running `git diff HEAD --stat`
  before starting). Whoever made it — remediation agent or orchestrator — the
  loop's own accounting rule is the same: record it or revert it, in the
  document that owns the record.
- **Pin (re-run 2026-09-05 on the reviewed tree):**

  ```console
  $ git diff HEAD --name-only
  .github/workflows/verify.yml
  AGENTS.md
  dev-diary/PLAN.md
  internal/gmi/errors.go
  internal/gmi/media/client.go
  internal/gmi/media/client_test.go
  internal/gmi/text/client.go
  internal/gmi/text/client_test.go
  $ git diff HEAD -- AGENTS.md | grep -c '^[+-][^+-]'
  4
  $ grep -c "AGENTS" <(sed -n '/## Files changed/,$p' \
      dev-diary/adversarial-review/t2-remediation-round3.md)
  0
  ```

  AGENTS.md is modified in the working tree; the remediation file's
  Files-changed section does not mention it; the Scope note asserts every
  non-sanctioned change is inside `internal/gmi/**`. The probe fails on all
  three legs.
- **Mutation:** add AGENTS.md to the remediation file's accounting (one clause
  in the Scope note, one entry in Files-changed, naming the two hunks — or an
  explicit note that the orchestrator fixed it in-line under the exemption).
  The probe's third leg goes green. Deleting that record re-introduces the
  defect and the Pin goes red again.

---

## L1 — The `Reasoning` docstring claims an empty `Reasoning{}` omits the field; the wire carries `{"type":""}`

- **Severity:** L. (The behavioural outcome the sentence promises — reasoning
  stays off — is true: only `thinking:{"type":"enabled"}` engages reasoning
  per AGENTS.md §GMI endpoints, so `"type":""` is inert. What misdescribes is
  the marshalling mechanism, which is what the next author will code against.)
- **Where:** `internal/gmi/text/client.go:104-105` (`// Type is "enabled" or
  "disabled"; an empty Reasoning{} omits the field and reasoning stays off`),
  against `client.go:118` (`Thinking *Reasoning json:"thinking,omitempty"`).
- **What:** `Thinking` is a pointer; `omitempty` drops it only when it is nil.
  `&Reasoning{}` — the literal the docstring names — is non-nil and marshals as
  `"thinking":{"type":""}` on the wire. A T4 author keeping phase A's thinking
  off "explicitly" per this doc sends a field the doc says is omitted. The
  correct statement: a nil `Thinking` omits the field; `&Reasoning{}` emits
  `{"type":""}`. Pre-existing since `3695375` (git log -S), never yet
  load-bearing because every caller so far passes either nil or
  `&Reasoning{Type: "enabled"}`; in scope under the whole-seam review, same as
  round 3's L5.
- **Pin** (reviewer-written; fails on the reviewed tree; run, recorded,
  removed with the file):

  ```go
  // internal/gmi/text/zz_probe_test.go (removed after the round)
  nonNil := ChatRequest{Model: "MiniMaxAI/MiniMax-M3",
      Messages: []Message{{Role: "user", Content: "hi"}},
      Thinking: &Reasoning{}}
  raw, _ := json.Marshal(nonNil)
  if strings.Contains(string(raw), `"thinking"`) {
      t.Errorf("doc claims an empty Reasoning{} omits the field, but the wire carries thinking: %s", raw)
  }
  ```

  ```console
  $ go test -race -count=1 -v -run 'TestProbe_EmptyReasoningWire' ./internal/gmi/text/
  === RUN   TestProbe_EmptyReasoningWire
      zz_probe_test.go:27: &Reasoning{} wire: {"model":"MiniMaxAI/MiniMax-M3","messages":[{"role":"user","content":"hi"}],"thinking":{"type":""}}
      zz_probe_test.go:29: doc claims an empty Reasoning{} omits the field, but the wire carries thinking: {"model":"MiniMaxAI/MiniMax-M3","messages":[{"role":"user","content":"hi"}],"thinking":{"type":""}}
  --- FAIL: TestProbe_EmptyReasoningWire (0.00s)
  FAIL
  ```

  (The same probe's nil leg passes: nil `Thinking` omits the field, confirming
  the real mechanism.)
- **Mutation:** reword the docstring back to "an empty `Reasoning{}` omits the
  field" after the fix — the probe transcript contradicts the sentence again
  and the Pin is red. The Pin is load-bearing because it pins the wire bytes,
  not the comment.

---

## L2 — `ErrModelNotFound`'s docstring conditions the sentinel on the error message; classification is status-only

- **Severity:** L.
- **Where:** `internal/gmi/errors.go:55-56` ("and on any request-queue response
  whose error message identifies a missing or unknown model"), against
  `internal/gmi/media/client.go:327-328` (the `case code == http.StatusNotFound`
  arm, which never reads the body for classification).
- **What:** the docstring's letter is false in both directions. A request-queue
  404 whose message says nothing about models still returns
  `ErrModelNotFound` (the condition is not required); a 400 whose message
  names an unknown model returns `ErrBadRequest` (the condition is not
  sufficient). The sentinel's whole doctrine — stated two arms away in both
  classifiers ("We do not try to parse the upstream error string") — is that
  classification is status-only and the message is carried verbatim for the
  operator. The one docstring in the package that implies message-
  conditionality invites the substring-matching the repo forbids
  (AGENTS.md §Errors: "Never match a substring of a provider's error
  message"). Pre-existing since `3695375` (git log -S); in scope under the
  whole-seam review. The mapping itself (404 → `ErrModelNotFound`, both
  endpoints) is round-1-approved and correct; only the sentence is wrong.
- **Pin** (reviewer-written; passes on the reviewed tree — which is what
  convicts the docstring: behaviour is status-only, the doc says otherwise;
  run, recorded, removed with the file):

  ```console
  $ go test -race -count=1 -v -run 'TestProbe_ModelNotFoundIsStatusNotMessage' ./internal/gmi/media/
  === RUN   TestProbe_ModelNotFoundIsStatusNotMessage
  === RUN   TestProbe_ModelNotFoundIsStatusNotMessage/404,_message_silent_about_models
  === RUN   TestProbe_ModelNotFoundIsStatusNotMessage/400,_message_names_an_unknown_model
  --- PASS: TestProbe_ModelNotFoundIsStatusNotMessage (0.00s)
      --- PASS: TestProbe_ModelNotFoundIsStatusNotMessage/404,_message_silent_about_models (0.00s)
      --- PASS: TestProbe_ModelNotFoundIsStatusNotMessage/400,_message_names_an_unknown_model (0.00s)
  PASS
  ok  	thutapi/internal/gmi/media	1.012s
  ```

  Row (a): 404 + `{"error":"route not found"}` → `errors.Is(.., ErrModelNotFound)`
  is true — the doc's condition absent, sentinel present. Row (b): 400 +
  `{"error":"unknown model minimax-tts-speech-2.8-hd"}` → false — the doc's
  condition present, sentinel absent.
- **Mutation:** restore the message-conditional wording after the fix; the
  probe's two rows contradict the sentence again and the Pin is red.

---

## Judgements the round was asked to make

### The M3 plan amendment (option a)

**Lawful, and it does what round 3 required.** The rewritten `Done when`
(PLAN.md §T2) is mechanically checkable from a clean tree: each named pin maps
to a named, runnable test — `{model, payload}` envelope (`TestGenerateImage` /
`TestEditImage` / `TestSynthesizeSpeech` capture and assert it), bearer auth
(same three, plus `TestChat_HappyPath`), the `MiniMaxAI/` prefix pin
(`TestChat_HappyPath` Pins #1/#2), the `need_volumn_normalization` typo pin
(`TestSynthesizeSpeech_TypoPinnedInRawJSON`, raw bytes), the retry contract
(`TestRetry_5xxHitTwice`, `TestChat_Retry_5xxHitTwice`,
`TestRetry_FailedStatusThenSuccess`, `TestRetry_FailedStatusBudgetSpent`) —
and "responses are handed to callers as **raw bytes**" is checkable from the
signatures and the raw-body assertions. All were run green for this round
(table below). The amendment is dated, cites its round, states its rationale
where the criterion lives, and was the option the round-3 review's remediation
note sanctioned. **T2b is a lawful T1/T1b-precedent split**: AGENTS.md §Testing
rule 6 requires operator-only checks to move into a `b`-suffixed track rather
than live in prose; T1b set the shape (owns no repo paths, operator executes,
writes back a Status row); T2b copies it exactly (no `Owns` paths, runs once a
working key exists, keeps the T2 criterion agent-checkable). The T2 Status row
correctly still reads REMEDIATE and was not flipped by the remediation.

### The M4 two-tier coverage floor against AGENTS.md §Testing item 7

The **shape** is sound (see the M1 judgement above) and both tiers are met
honestly by the packages they bind; the gate is enforced in verify.yml, was
executed locally for this round (ok per package, exit 0; exit 1 with the floor
raised past text's 90.2%, so it is not a no-op), and the gofmt half of the M4
row is accurate — `6bdb003` did widen the step to `gofmt -l .` and AGENTS.md
§CI records the scoping history. What is **not** sound is leaving item 7 and
§CI describing three different gates — that is finding M1 above.

### Round-3 H2's "three prose statements" agreement

Met. `errors.go` `ErrTransient`, the `Chat` doc, and the media `post` doc now
all state the same contract — exactly one internal retry on a failure
classified `ErrTransient`, never a 4xx, a deadline on every call — and the
code does that. One letter-level nuance examined and judged consistent: the
plan's sentence names 5xx and the request-queue `failed` status; the code also
retries transport errors and (text-only) undecodable-2xx/no-choices bodies,
all classified `ErrTransient` and documented as such. The plan names the
minimum transient set and forbids only 4xx retries; it does not say "and
nothing else". The `ErrRateLimited` docstring's "back off and retry" is
caller-level vocabulary, consistent with the package doc's "caller MAY retry
again" — the plan's never-4xx sentence governs the internal budget, which
indeed never touches 429. Examined; no finding.

---

## Pins re-run (all 2026-09-05, `-race -count=1 -v`)

| Pin | Round | Result |
|---|---|---|
| `TestEditImage_EmptyModelRejected` | 3 (H1) | **PASS** — `err` wraps `ErrBadRequest`, upstream hit 0 times |
| `TestRetry_5xxHitTwice` | 3 (H2) | **PASS** — hits == 2, one `ErrTransient` |
| `TestChat_Retry_5xxHitTwice` | 3 (H2) | **PASS** — hits == 2 |
| `TestPayloadTooLarge_413` | 3 (M1) | **PASS** — `ErrBadRequest`, not transient, hits == 1, nil body |
| `TestGenerateImage_DefaultModelPinnedInRawJSON` | 3 (M2) | **PASS** — raw bytes contain `"model":"Flux2-Klein"`, no `Qwen-Image-2512` |
| `TestChat_BadRequest_400` | 1 (M1 residue) | **PASS** |
| `TestBadRequest_400` | 1 (M1 residue) | **PASS** |
| `TestSynthesizeSpeech_TypoPinnedInRawJSON` | 1 (quirk pin) | **PASS** — literal `"need_volumn_normalization":true` in raw bytes |
| `TestRetry_FailedStatusThenSuccess` / `TestRetry_FailedStatusBudgetSpent` | 3 (H2) | **PASS** — failed status retried once; budget spent ⇒ `ErrTransient`, nil body |
| `TestSynthesizeSpeech_DefaultModelPinnedInRawJSON` | 3 (M2 adjunct) | **PASS** |
| `TestClassifyStatus_Table` / `TestChatClassifyStatus_Table` | 3 (M1) | **PASS** — 4xx rows hits == 1, 302/502 rows hits == 2 |

## Mutations applied by hand (observed red, reverted, sha256-identical)

| Mutation | Pin that went red | Observed |
|---|---|---|
| `EditImage` empty-model sentinel replaced by `model = defaultImageModel` | `TestEditImage_EmptyModelRejected` | `err = <nil>`, upstream hit 1 time(s), want 0 |
| media `maxAttempts = 2` → `1` | `TestRetry_5xxHitTwice`, `TestRetry_FailedStatusThenSuccess`, `TestRetry_FailedStatusBudgetSpent` | hits == 1; failed-status surfaced as error instead of retried |
| text `maxAttempts = 2` → `1` | `TestChat_Retry_5xxHitTwice` | hits == 1 |
| media catch-all `code >= 400 && code < 500` → `code >= 420 && …` | `TestPayloadTooLarge_413`, `TestClassifyStatus_Table/413…`, `…/418…` | `err = gmi: transient error: {"error":"payload too large"}`, upstream hit 2 — the exact 4xx-retry regression class round-3 M1 named |

Revert proof: `sha256sum` before == after for both client files
(`8e1d3b10…` media, `bac3262f…` text); probe files removed; final
`git status --porcelain` differs from the pre-review snapshot by nothing.

## Gates (run by this reviewer, 2026-09-05)

```console
$ go vet ./...                                                clean
$ go test ./... -race -count=1
ok  	thutapi/cmd/thutapi           9.863s
?   	thutapi/internal/gmi          [no test files]
ok  	thutapi/internal/gmi/media    1.833s
ok  	thutapi/internal/gmi/text     1.832s
$ gofmt -l .                                                  empty
$ go test ./... -cover
cmd/thutapi 79.7%   media 95.1%   text 90.2%
$ verify.yml awk coverage gate on `go test ./... -race -cover` output:
  ok thutapi/cmd/thutapi: 79.7% / warn internal/gmi: no test files /
  ok internal/gmi/media: 95.1% / ok internal/gmi/text: 90.2%  — exit 0
  (negative control: gmiMin=96.0 → FAIL media, FAIL text, exit 1)
```

## Residue claim

Against **round 1** (M1, L1): **zero.** Both M1 Pins green; the 400/422
classification they pin is preserved exactly by the new catch-all — mutation 3
shows the Pins fire the moment the catch-all narrows. L1 remains the recorded
false positive; the line (`media/client.go:193-194`) is unchanged and still
reads as natural English.

Against **round 2** (no findings; scope = round-1 residue plus the
`3695375..4a176a2` diff audit): **zero.** No round-2 finding exists to regress;
its audited surface (sentinels, classification shape, wire pins, env-override
seam) re-verified green above.

Against **round 3** (2 × H, 4 × M, 5 × L): **zero.** All eleven findings have
remediation rows; all six exit criteria verified met this round — (1) eleven
rows present; (2) H1/M2 closed with default-path tests asserting wire bytes;
(3) code and all three prose statements agree on the retry contract (nuance
judged above); (4) PLAN §T2 and the shipped client both say raw bytes; (5)
`gofmt -l .` runs repo-wide in CI (verified in `6bdb003`); (6) this round
re-ran every Pin and states its residue per round.

**New residue, created by this round:** 2 × M, 2 × L as filed above. That is
what round 5 must close; nothing else is open.

## What this round did not examine

Stated so round 5's zero-residue claim has a boundary:

- **Live endpoint behaviour** — not probed. The operator key remains rejected
  by both providers (unchanged since round 1; PLAN §T2 row records it); wire
  contracts were judged via httptest and raw-JSON pins only. T2b owns the live
  half.
- **The prefix/thinking pins' altitude** — `TestChat_HappyPath` asserts the
  prefix and the `thinking` field against the decoded `ChatRequest`, not raw
  marshalled bytes, and the tests are not quirk-named (AGENTS.md §Testing rule
  4's letter). Examined and judged **not a finding**: any wire rename breaks
  the decode and fails the assertion on a zero value, so the silent-unfix
  hazard rule 4 exists for is covered; the tests are round-1 code, and DoD
  item 6 scopes §Testing to the track's *new* code. Round 5 need not reopen.
- **Bare (non-sentinel) validator errors** — empty prompt/model/messages
  return plain `errors.New` at the package boundary (except `EditImage`'s
  empty model, which wraps `ErrBadRequest`). Pre-existing fail-fast design,
  examined in rounds 1–3; no consumer branches on these by class. No finding.
- **Outbound `Message.Content interface{}`** — the union on the *request*
  side. AGENTS.md's "any is not a data model" rule targets response shapes;
  the decode side got `UnmarshalJSON` per round-3 L1. No finding.
- **Request-queue `queued`/`processing` 200 passthrough** — returned as raw
  bytes with nil error; per PLAN invariant 4 the polling layer is an unowned
  seam and T2 ships one POST. Per-plan; not a finding.
- **`cmd/thutapi` internals, T1's deploy surface, the five Traefik labels,
  PLAN sections for T3–T14, project.md §2b's operator-verified multimodal
  claims, and git history before `3695375`** — outside this round's seam;
  their tracks are closed or unstarted.

## Exit criteria for round 5

1. **M1** closes with AGENTS.md stating the two-tier floor in both places —
   §CI's bullet and §Testing item 7 — matching `verify.yml` (or the gate
   deliberately flattened to one tier, with both doc statements conformed to
   that; one decision, three agreeing statements). The M1 Pin's greps must
   then describe one contract and `go test ./... -cover` must satisfy it.
2. **M2** closes with the AGENTS.md working-tree hunk recorded by name and
   file in `t2-remediation-round3.md` (Scope note + Files-changed), or
   reverted; the Scope note must be true of the tree it ships in.
3. **L1** closes with the `Reasoning` docstring stating the nil-pointer
   mechanism and what `&Reasoning{}` actually emits; L2 with the
   `ErrModelNotFound` docstring stating status-only classification on both
   endpoints. The two probes' assertions are worth keeping as permanent tests
   (wire bytes and classification rows respectively) — they are the kind of
   decision-pins AGENTS.md §Testing rule 3 wants.
4. Every Pin in this file's table re-runs green; the gates (`go vet ./...`,
   `go test ./... -race -count=1`, `gofmt -l .`, the coverage-floor awk gate)
   run green from a clean tree.
5. The remediation lands as the focused T2 commit only after this round's
   verdict is closed, with the Files-changed list true of the commit.
6. Round 5 claims zero residue against rounds 1–4, severity by severity, and
   states its own examination boundary.
