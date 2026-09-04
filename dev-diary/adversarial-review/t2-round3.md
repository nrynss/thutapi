# T2 round 3 — adversarial review of the GMI clients (post-APPROVE re-open)

| | |
|---|---|
| **Target** | T2 at `aaf33bc` on `main`. Tree clean at review time and restored clean afterwards. |
| **Evidence** | `dev-diary/project.md` (§Pipeline, §2, §2b, §3 model table, §4, §Architecture consequences), `dev-diary/PLAN.md` (§T2 done-when + retry contract, §T4, §T6, §T9, §T10, Status table), `AGENTS.md` (§GMI endpoints, §CI and images, §Definition of done), `dev-diary/adversarial-review/README.md`, `dev-diary/adversarial-review/t2-round1.md`, `t2-remediation-round1.md`, `t2-round2.md`; `internal/gmi/errors.go`, `internal/gmi/text/client.go`, `internal/gmi/text/client_test.go`, `internal/gmi/media/client.go`, `internal/gmi/media/client_test.go`, `cmd/thutapi/main.go`, `.github/workflows/verify.yml` read in full. |
| **Method** | Probe, don't trust prose. Three reviewer-written Pins were placed in `internal/gmi/media/zz_probe_test.go`, run against `aaf33bc`, observed to fail, and the file removed — the tree is byte-identical to `aaf33bc` at the close of this review. Also: `go vet ./...`, `go test ./... -race`, `gofmt -l .` (all clean), and `go test ./... -cover` for the coverage baseline. |
| **Date** | 2026-09-04 |

## Verdict

**REMEDIATE — 0 × C, 2 × H, 4 × M, 5 × L.**

T2 was closed at `a209226` on a round-2 APPROVE. That APPROVE is **not
withdrawn as to what it examined** — round 2's scope was round-1 residue (M1,
L1) plus a diff audit of `3695375..4a176a2`, and within that scope it was
correct and its Mutation probes were sound. This round re-reviews T2 against
the **whole** of its `Done when` and the spec facts the clients encode, which
round 2 did not re-open.

Two findings are High. The first is latent today and breaks T6 the moment T6
calls the client: `EditImage`'s default model is the one model `project.md`
strikes through as incapable of image-to-image. Character consistency is on
the **do not cut** list, and this defect silently removes the mechanism that
delivers it.

### Why the earlier rounds missed this

Not reviewer error so much as a test-shape blind spot worth recording, because
it will recur on every later track:

**Every media test passes an explicit model id.** `TestGenerateImage` passes
`Z-Image-Turbo`, `TestEditImage` passes `Flux2-Klein`, `TestSynthesizeSpeech`
passes `minimax-tts-speech-2.8-hd`. So `client.go:108` and `client.go:134` —
the `if model == "" { model = ... }` fallbacks — are **never executed by the
suite**. 85.1% statement coverage on the package hides the two statements that
carry the wrong constant, because coverage counts lines, not decisions about
defaults. The remediation for H1 must add the default-path assertions, not
only change the constant.

---

## Severity key

Per `dev-diary/adversarial-review/README.md`. C = breaks the demo. H = real
defect the demo survives. M = real defect with a workaround. L = polish. No
severity is exempt.

---

## H1 — `EditImage` defaults to `Qwen-Image-2512`, a model that cannot accept the reference image

**Severity:** H. (C for T6 the day T6 calls it with an empty model; H today
because nothing calls it yet.)

**Where:** `internal/gmi/media/client.go:133-135`; the same constant at
`client.go:107-109` and cited in the package doc at `client.go:84`.

**What:** `EditImage` is the image-to-image entry point — its entire reason to
exist is carrying a character reference sheet into a page render
(`project.md` §2 "Image lock"). When the caller passes an empty model it
substitutes `Qwen-Image-2512`. `project.md:197` lists that id **struck
through**, with the reason in bold: *"no — t2i only, cannot take the
reference"*. `PLAN.md` §T6 repeats it as an imperative: *"Do not use
`Qwen-Image-2512`: it is text-to-image only and cannot take the reference."*

The failure is silent in the worst way. The call succeeds, an image comes
back, and the reference is simply ignored — so the cast drifts page to page
and the book falls apart exactly as `project.md` §2 predicts. There is no
error to catch and no log line to read. T7's consistency check would flag the
drift page by page and burn its 2-retry cap on every page, turning a silent
defect into a slow, expensive one.

**Pin** (reviewer-written; fails on `aaf33bc`):

```go
func TestProbe_EditImageDefaultModel(t *testing.T) {
	var got envelope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)
	t.Setenv("GMI_API_KEY", "k")

	png := []byte("\x89PNG\r\n\x1a\n" + string(make([]byte, 600)))
	if _, err := New().EditImage(context.Background(), png, "draw", ""); err != nil {
		t.Fatal(err)
	}
	if got.Model == "Qwen-Image-2512" {
		t.Errorf("EditImage default model = %q, which project.md:197 marks t2i-only (cannot take the reference)", got.Model)
	}
}
```

```console
$ go test ./internal/gmi/media/ -run TestProbe_EditImageDefaultModel
    zz_probe_test.go:31: EditImage default model = "Qwen-Image-2512", which project.md:197 marks t2i-only (cannot take the reference)
--- FAIL: TestProbe_EditImageDefaultModel (0.00s)
FAIL
```

**Mutation:** set `model = "Qwen-Image-2512"` back at `client.go:134`. The Pin
returns to red. Conversely the Pin is load-bearing only if it asserts the
default path — a test that passes an explicit model id cannot observe this
bug, which is precisely how it survived two rounds.

**Remediation note (for the remediation agent, not applied here):** the
default should be `Flux2-Klein` or `Z-Image` per `project.md` §3 "Start on".
Consider whether a default is wanted at all on `EditImage` — an explicit
`ErrBadRequest` on an empty model is arguably safer than any default, since
T6 owns the provider switch and a silent fallback is what caused this.

---

## H2 — The T2 retry contract is unimplemented, and `errors.go` tells callers the opposite

**Severity:** H.

**Where:** `internal/gmi/errors.go:45-50` (the claim);
`internal/gmi/text/client.go:160-209` and `internal/gmi/media/client.go:184-224`
(no retry anywhere in either); `internal/gmi/text/client.go:157` (a third,
contradictory statement).

**What:** `PLAN.md` §T2 states the contract in three sentences: *"**Retry:**
one retry on 5xx and on a request-queue `failed` status. Never retry a 4xx.
Every call carries a context deadline."* Neither client retries, and neither
inspects request-queue status.

That alone is a `Done when` gap. What raises it to H is that the shipped
package documentation asserts the missing behaviour as fact.
`internal/gmi/errors.go:48-49`:

> "The caller MAY retry, but each client only retries once internally —
> caller-level retry with a fresh context is the expected pattern."

while `internal/gmi/text/client.go:157` says:

> "The function does not retry."

Both cannot be true, and they are 200 lines apart in the same package. A T6 or
T8 author who reads the sentinel documentation — the natural place to look,
since it is where the retry vocabulary is defined — budgets for one internal
retry that does not exist. Under an image fan-out of ~10 calls bounded at 4
concurrent (`PLAN.md` §T6), a transient 5xx from the request queue then fails a
page outright instead of being absorbed.

**Pin** (reviewer-written; fails on `aaf33bc`):

```go
func TestProbe_RetryOn5xx(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(500)
	}))
	defer srv.Close()
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)
	t.Setenv("GMI_API_KEY", "k")

	New().GenerateImage(context.Background(), "p", "Z-Image")
	if hits != 2 {
		t.Errorf("upstream hit %d time(s) on 5xx, want 2 (one call + one retry per PLAN.md T2)", hits)
	}
}
```

```console
$ go test ./internal/gmi/media/ -run TestProbe_RetryOn5xx
    zz_probe_test.go:48: upstream hit 1 time(s) on 5xx, want 2 (one call + one retry per PLAN.md T2)
--- FAIL: TestProbe_RetryOn5xx (0.00s)
FAIL
```

**Mutation:** delete the retry loop once added; hit count returns to 1 and the
Pin goes red. A second Pin must assert the negative half of the contract —
that a 4xx is hit exactly **once** — or a naive `for i := 0; i < 2` retry
loop passes the first Pin while violating "never retry a 4xx".

**Remediation note:** whichever way this is closed, the three prose statements
must end up agreeing. If the decision is *no internal retry* — defensible,
since `text/client.go:157` argues it deliberately and the caller owns the
budget — then `PLAN.md` §T2 is the document that changes, and `errors.go:48-49`
must be rewritten. A finding is not closed by picking a behaviour; it is
closed when code and all three docstrings say the same thing.

---

## M1 — Every unclassified 4xx is labelled `ErrTransient`, inviting the retry the contract forbids

**Severity:** M.

**Where:** `internal/gmi/text/client.go:236-240`; `internal/gmi/media/client.go:249-250`.

**What:** Round 1's M1 added an arm for 400 and 422. Everything else falls to
`default`, which returns `ErrTransient` — documented in `errors.go:14` as
*"transport blip, retry"*. So every 4xx outside {400, 401, 403, 404, 422, 429}
is handed to callers flagged retryable, directly against *"Never retry a 4xx"*.

Two are not hypothetical on this project:

- **413 Payload Too Large.** `EditImage` inlines a full reference image as
  base64 (`project.md` §2b "images go in as inline `data:` base64"), which is
  the single largest request body the app sends and grows ~33% in encoding. A
  size rejection is the expected upstream response, and retrying it re-uploads
  the same oversized body.
- **402 Payment Required.** `project.md` §"Three things the box does not
  solve" records that the free window closes **2026-09-06** while judging runs
  to **2026-09-11**. A billing rejection during judging would present to T6's
  errgroup as a transient blip and drive a retry storm against a now-paid
  endpoint — the "open wallet" failure mode `PLAN.md` §T11 exists to prevent,
  arriving through the error classifier instead of the front door.

**Pin** (reviewer-written; fails on `aaf33bc`):

```go
func TestProbe_413NotTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		w.Write([]byte(`{"error":"payload too large"}`))
	}))
	defer srv.Close()
	t.Setenv("GMI_MEDIA_BASE_URL", srv.URL)
	t.Setenv("GMI_API_KEY", "k")

	_, err := New().GenerateImage(context.Background(), "p", "Z-Image")
	if errors.Is(err, gmi.ErrTransient) {
		t.Errorf("413 classified ErrTransient (retryable); PLAN.md T2 says never retry a 4xx. err=%v", err)
	}
	if !errors.Is(err, gmi.ErrBadRequest) {
		t.Errorf("413 not classified ErrBadRequest; err=%v", err)
	}
}
```

```console
$ go test ./internal/gmi/media/ -run TestProbe_413NotTransient
    zz_probe_test.go:64: 413 classified ErrTransient (retryable); PLAN.md T2 says never retry a 4xx. err=gmi: transient error: {"error":"payload too large"}
    zz_probe_test.go:67: 413 not classified ErrBadRequest; err=gmi: transient error: {"error":"payload too large"}
--- FAIL: TestProbe_413NotTransient (0.00s)
FAIL
```

**Mutation:** narrow the arm back to `code == 400 || code == 422`; the Pin goes
red on the 413 case.

**Remediation note:** `case code >= 400 && code < 500: return ErrBadRequest`
placed after the specific arms closes it, and `default` keeps 3xx and anything
else. 402 may deserve its own sentinel — an operator-actionable billing state
is not the same class of thing as a malformed payload, and `PLAN.md` §T11.2
already treats billing as its own risk.

---

## M2 — `GenerateImage` also defaults to the struck-through model

**Severity:** M (not H: `Qwen-Image-2512` is genuinely t2i-capable, so the call
works — it is the wrong model per spec, not a broken one).

**Where:** `internal/gmi/media/client.go:107-109`.

**What:** `project.md:197` strikes the row out entirely and `PLAN.md` §T6 says
"do not use" without qualifying it to i2i. Reference sheets generated on a
provider the page renders will not use also weakens the image lock: the
reference and the pages come from different model families, which is the
opposite of what `project.md` §2 asks for. Same default as H1, same fix.

**Pin:** as H1, against `GenerateImage(ctx, "p", "")`.
**Mutation:** restore the constant; Pin goes red.

---

## M3 — T2's `Done when` is not met, and the Status table says DONE

**Severity:** M.

**Where:** `dev-diary/PLAN.md` §T2 `Done when`; `internal/gmi/media/client.go:25-29`.

**What:** `Done when` reads: *"an integration test hits both endpoints live and
unmarshals into typed structs."* Neither half holds for the media client.

- **No typed structs.** The package doc is explicit that this was deliberate:
  *"Response shapes are deliberately untyped … Both are exposed as raw bytes;
  callers decode what they need."* That may well be the right engineering call
  — but it is the negation of the acceptance criterion, made in a docstring
  rather than in the plan, and the Status row was then set to DONE against the
  unamended criterion. This also sits against `PLAN.md` §T0 conventions:
  *"every GMI response shape is a named struct in `internal/gmi`, never
  `map[string]any` at a call site"* — the raw-bytes return moves that decoding
  to exactly the call sites the convention protects.
- **No live integration test.** The suite is entirely `httptest`. The T2 row
  cites live endpoint probes, and those probes are real work — but they live
  in review prose, not in a runnable artifact, and `AGENTS.md` §Definition of
  done item 4 requires the cited test to *"pass from a clean tree on a fresh
  checkout"*. Prose cannot. The row also records that the operator key is
  rejected by both providers, so no live test could pass today regardless.

**Pin:** documentary. `go test ./... -run Live` matches nothing; `grep -rn
"gmi-serving.com\|gmicloud.ai" --include='*_test.go' .` returns no hits.

**Mutation:** n/a — the fix is a decision, then a document edit.

**Remediation note:** pick one. Either (a) amend §T2's `Done when` to match
what shipped, recording *why* raw bytes beat premature structs and moving the
typed-shape requirement onto T6/T8 where the model is known; or (b) add the
typed structs. (a) looks correct on the merits given two days and an
undocumented per-model schema, but it must be written down, because right now
the plan and the code disagree and the plan is the authority.

---

## M4 — CI does not `gofmt` the package this track added

**Severity:** M. **Cross-track:** the file is `.github/workflows/verify.yml`,
which T2 does not own. Recorded here because T2 is the track that made it
matter and the next track inherits the gap.

**Where:** `.github/workflows/verify.yml:44-49`.

**What:** the gofmt step is `gofmt -l cmd/`. Every line of Go that T2 added
lives under `internal/`, which the step never inspects. `AGENTS.md` §Stack
names gofmt as the mechanism that *"removes style drift between three
different agents"* — the primary reason the project chose Go — and it is
currently switched off for the tree where all the agents' output will land.

`gofmt -l .` is clean on `aaf33bc`, so this is a latent gap and not a live
defect. It stops being latent the first time an agent writes unformatted Go
under `internal/`, which is where every remaining track (T3–T13) writes.

**Pin:** insert a badly-indented line into `internal/gmi/errors.go`, run the
workflow's exact step body — `unformatted=$(gofmt -l cmd/)` — and observe it
exit 0 with the file unformatted.

**Mutation:** revert the step to `gofmt -l cmd/` after widening it; the Pin
passes green on a misformatted `internal/` file again.

---

## L1 — `ContentPart` is unreachable: `encoding/json` can never produce one

**Where:** `internal/gmi/text/client.go:115-121` (declaration),
`client.go:126` (the comment claiming it is produced).

**What:** `AssistantMessage.Content` is typed `interface{}`, annotated
`// string OR []ContentPart`. `encoding/json` decoding into an `interface{}`
produces only `string`, `float64`, `bool`, `nil`, `[]interface{}` and
`map[string]interface{}` — never a named struct. So a multimodal response
decodes to `[]interface{}` of `map[string]interface{}`, and `ContentPart` is
dead code whose comment actively misdescribes runtime behaviour. `grep -rn
ContentPart --include='*.go' .` returns three hits, all inside the declaration
and its comments; no constructor, no assertion, no test.

Beyond the dead type: every consumer (T4 interview turns, T5 structuring JSON,
T7 the match verdict) must hand-roll the same type switch over
`Content interface{}` with no helper, which is where a silent nil-deref or a
missed array case will land.

**Mutation:** n/a (dead code). **Remediation note:** either implement
`UnmarshalJSON` on `AssistantMessage` so the union resolves into typed fields,
or delete `ContentPart` and correct the comment. A `func (m AssistantMessage)
Text() (string, error)` helper would pay for itself three times over.

---

## L2 — Unreachable branch: `http.DetectContentType` never returns `""`

**Where:** `internal/gmi/media/client.go:137-140`.

**What:** `http.DetectContentType` is documented to *always* return a valid
MIME type, falling back to `application/octet-stream` when detection fails, so
`if mime == ""` cannot be true. The comment above it explains a fallback that
the stdlib already performs. Harmless; it is coverage-inflating dead code, and
the docstring reasoning is wrong about the API it calls.

Adjacent and worth a glance during remediation: for inputs sniffed as text,
`DetectContentType` returns `text/plain; charset=utf-8` — a value with a
parameter, which would be interpolated straight into the `data:` URI at
`client.go:142`. Not reachable with the PNG inputs T6 produces, but the
`fmt.Sprintf` assumes a bare MIME type without saying so.

**Mutation:** n/a.

---

## L3 — `post` returns a non-nil body together with a non-nil error

**Where:** `internal/gmi/media/client.go:220-222`.

**What:** `return raw, classifyStatus(resp.StatusCode, raw)` hands back both.
The Go convention is that a non-nil error means the other returns are not to
be trusted, and every call site in the package forwards the pair unchanged, so
`GenerateImage`/`EditImage`/`SynthesizeSpeech` all inherit it. A caller writing
the ordinary `b, err := c.GenerateImage(...)` and checking `err` is fine; one
that checks `len(b) > 0` first — plausible when the payload is image bytes —
silently processes an error body as media.

If the intent is to give operators the upstream message, that is already
served: `classifyStatus` embeds the body in the wrapped error.

**Mutation:** n/a. **Remediation note:** `return nil, classifyStatus(...)`.

---

## L4 — `Accept: application/json` is sent on the TTS call, which returns binary

**Where:** `internal/gmi/media/client.go:207`, against the package doc's own
description at `client.go:26-28` (*"a binary blob for TTS"*).

**What:** all three methods share one header block, so `SynthesizeSpeech`
advertises that it accepts only JSON while expecting audio bytes. Harmless
against the current upstream, which ignores the header. A gateway that honours
`Accept` answers 406, and this endpoint sits behind APISIX per the T2 row.

**Mutation:** n/a.

---

## L5 — The `Qwen-Image-2512` docstring misreads the table it cites

**Where:** `internal/gmi/media/client.go:99-102`.

**What:** *"the cheapest in the catalog at $0.10 a book and t2i capable
(project.md §3)"*. The price is right — `project.md:195-197` is a per-book
column and Qwen is $0.10 — but it is **tied** with Z-Image, Flux2-Klein,
GLM-Image, Flux2-Dev and the Controlnet variant, not cheapest, so cost cannot
select it. The docstring cites §3 as authority for a choice that the same
table forbids two rows down, and §3's heading is *"choose on consistency, not
price"*. This is the reasoning that produced H1 and M2, written out; fixing the
constant without fixing the comment leaves the next agent the same argument.

**Mutation:** n/a.

---

## What this round did not examine

Stated so the next round's zero-residue claim has a boundary:

- **The text client's wire shape** — model prefix, `thinking` vs
  `reasoning_effort`, bearer auth — re-read and found correct against
  `AGENTS.md` §GMI endpoints and `project.md` §1. No finding.
- **The `need_volumn_normalization` typo pin** — correct, and
  `TestSynthesizeSpeech_TypoPinnedInRawJSON` asserts it at the raw-JSON level,
  which is the right altitude. No finding.
- **Round 1 M1 / L1 residue** — re-checked, still zero. Round 2's conclusion
  stands.
- **Live endpoint behaviour** — not re-probed. The T2 row records that the key
  in `apikey.txt` is rejected by both providers (`sk-or-v1-…`, an OpenRouter
  shape), so no live probe is possible from this workstation today. That
  blocker is unchanged and still gates T6/T8.
- **Architectural findings** that are larger than T2's seam — the missing
  request-queue polling layer, the unowned SSE broker, env-read-at-construction
  config, and the absence of consumer-side interfaces — are **not** filed as T2
  findings, because closing them inside T2 would be scope creep. They are
  written up in `PLAN.md` §Architectural invariants and §Unowned seams, and
  belong to T3 and the tracks that follow.

## Exit criteria for round 4

1. H1, H2, M1, M2, M3, M4 and L1–L5 each land a row in
   `t2-remediation-round3.md`. No severity is exempt (`README.md`).
2. H1 and M2 close with tests that exercise the **default** model path, not
   only the explicit-model path.
3. H2 closes with code and all three prose statements agreeing, whichever way
   the retry decision goes.
4. `PLAN.md` §T2 `Done when` and the shipped media client agree about typed
   structs — one of them changes.
5. `gofmt -l .` (repo-wide) runs in CI.
6. Round 4 re-runs the three Pins above plus the round-1 Pins, and claims zero
   residue against rounds 1, 2 **and** 3.
