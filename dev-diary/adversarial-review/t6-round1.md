# T6 round 1 — adversarial review of `internal/illustrate`

| | |
|---|---|
| **Target** | T6, uncommitted working tree on `main` at base `aaf33bc`. `git status`: ` M go.mod`, `?? internal/illustrate/` (10 new files — decode.go, illustrate.go, plan.go, prompt.go, types.go plus five test files), `?? dev-diary/adversarial-review/t6-round1-notes.md`. Nothing else changed. |
| **Evidence** | `AGENTS.md` in full; `PLAN.md` §T6 (locks, forbidden-model table, "eight pages render with a recognisably constant cast"), §T5b, §T7, §T3, §Architectural invariants 1–9, §Unowned seams, §Status; `project.md` §2, §2b, §3 (model table, per-**book** column), §Scope, §Pipeline; `adversarial-review/README.md`; `t2-round3.md` (H1/M2 — the model-default trap); `t2b-t5b-live-record.md` (the two live cast shapes and the live TTS terminal record); `t6-round1-notes.md`; all of `internal/illustrate/**`; `internal/gmi/media/client.go` + `polling.go` and `internal/story/validate.go` read at the surface T6 consumes. |
| **Method** | Probe, don't trust prose. Ten reviewer-written probes in throwaway `internal/illustrate/zz_probe_test.go` / `zz_probe2_test.go`, run, recorded below, then **deleted**. Eleven source mutations applied one at a time and reverted from a pre-mutation copy; md5sums of all ten package files verified identical before and after (`e86bf91…` decode.go, `9bf8a56…` illustrate.go, `b1048c4…` plan.go, `16a293e…` prompt.go, …). `go mod tidy` was run once to test ruling 1 and go.mod/go.sum restored from backup byte-identical. No fixes applied, no formatter run, no git mutation, **no live GMI call and no money spent**. |
| **Date** | 2026-09-05. Reviewer: fresh agent, not the implementer, no prior T6 round. |

## Verdict

**REMEDIATE — 0 × C, 3 × H, 3 × M, 2 × L.**

The three locks are real, the default-model trap that broke T2 is properly
closed, and the verbatim lock survives everything I could throw at it. The
findings are not in the locks themselves — they are in the two heuristics that
decide *which* character a page is locked to, and in the decode that decides
*which bytes* are the picture. All three H findings produce a book that renders
successfully and has the wrong cast, which is the one failure mode this track
exists to prevent and the one T7 cannot catch.

**H1 is the serious one.** The request queue echoes the submitted `payload` back
in its terminal record — that is not a hypothesis, it is what the live TTS call
in `t2b-t5b-live-record.md` returned. `EditImage`'s payload contains the
reference sheet inlined as base64. `decodeImage` scans every string in the body
for image magic *before* it looks at any URL, so the echoed input beats the real
result URL and **every page comes back as the reference sheet itself**. T7 cannot
catch it: asked whether the page matches the reference, M3 would answer `true`,
because it is the reference.

## Gates (run by this reviewer, on the restored tree)

| Gate | Result |
| --- | --- |
| `go vet ./...` | clean |
| `go test ./... -race` | ok, all packages |
| `go test ./internal/illustrate/ -race -count=5` | `ok  thutapi/internal/illustrate  2.221s` — **no race**, phase ordering stable |
| `gofmt -l .` | empty |
| `go test ./internal/illustrate/ -cover` | **100.0% of statements** — confirmed, and see M2 for what that does not mean |

---

## Severity key

Per `adversarial-review/README.md`. C = breaks the demo. H = real defect the
demo survives. M = real defect with a workaround. L = polish. No severity is
exempt.

---

## H1 — the echoed request payload beats the result URL: every page decodes to its own reference sheet

**Severity:** H. (**C** the moment a live probe confirms the image queue echoes
`payload` the way the TTS queue does — the book then renders eight copies of a
character sheet and every lock reports success.)

**Where:** `internal/illustrate/decode.go:89-101` (the base64 pass over *every*
string in the body) versus `decode.go:103-120` (the URL pass, which only runs if
the first found nothing). Producer: `internal/gmi/media/client.go` `EditImage`,
which puts the reference sheet in `payload.image` as a `data:` URI.

**What:** `decodeImage` is documented as structural rather than schematic, and
the ordering rule it encodes is "inline beats a URL" (`TestDecodeImage_InlineBeatsURL`).
That rule is correct for a body containing *one* image. It is wrong for this
body, because the request queue hands back the submitted payload alongside the
outcome. From the live TTS record in `t2b-t5b-live-record.md`:

```json
{"request_id":"2ba8cd0d-…","model":"minimax-tts-speech-2.8-hd","status":"success",
 "payload":{"need_noise_reduction":true,"need_volumn_normalization":true,
            "text":"Once upon a time, a small dragon lost her shoe.", …},
 "outcome":{"audio_url":"https://storage.googleapis.com/…/….mp3", …}}
```

`payload` is the input, echoed verbatim; the result is a URL under `outcome`.
For an image edit the echoed `payload.image` is a real, decodable PNG. The
base64 pass finds it, `imageType` confirms `image/png`, and `decodeImage`
returns — never reaching the URL pass that holds the actual render.

Two things make this worse than a one-line ordering bug:

* **The correct case today is an accident of the alphabet.** If the queue
  answers images *inline* instead, the walk is sorted by key and `outcome` <
  `payload`, so the render wins by luck. Rename that field `result` or `url`
  upstream (`payload` < `result`) and the inline case fails the same way.
* **T7 is blind to it.** §T7's closing loop sends M3 the reference sheet and the
  page and asks whether they match. They match perfectly. The one mechanism the
  plan has for catching drift reports success on a book with no illustrations in
  it at all.

The shipped suite cannot see this because every fixture — `queueEnvelope` in
`illustrate_test.go:79` and `newWireServer` in `wire_test.go:68` — answers with
`outcome.image` inline and **never echoes the payload**, so the only response
shape the live endpoint is known to produce is the one shape untested.

**Pin** (reviewer-written; fails on the working tree). A queue fixture that
echoes the payload as the live one does, with the result at `outcome.image_url`:

```go
queue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var env struct{ Model string `json:"model"`; Payload map[string]any `json:"payload"` }
	json.Unmarshal(raw, &env)
	resp := map[string]any{"request_id": "r1", "status": "success",
		"model": env.Model, "payload": env.Payload,      // <- the echo, verbatim
		"outcome": map[string]any{"image_url": store.URL + "/out.png"}}
	if env.Payload["image"] == nil {                      // a reference sheet
		resp["outcome"] = map[string]any{"image": "data:image/png;base64," + b64(sheet)}
	}
	json.NewEncoder(w).Encode(resp)
}))
…
book, _ := Illustrate(context.Background(), cfg, oneCharacterOnePageStory)
if string(book.Pages[0].Image) == string(sheet) { t.Errorf("page came back as the REFERENCE SHEET") }
```

```console
$ go test ./internal/illustrate/ -run TestProbe_EchoedPayloadBeatsTheResultURL -v
=== RUN   TestProbe_EchoedPayloadBeatsTheResultURL
    zz_probe_test.go:80: page 1 image = "\x89PNG\r\n\x1a\nSHEET-MIRA.............................................."
    zz_probe_test.go:82: page 1 came back as the REFERENCE SHEET, not the render: the echoed payload.image beat outcome.image_url
    zz_probe_test.go:85: page 1 image = "…SHEET-MIRA…", want the rendered page "…THE-ACTUAL-PAGE…"
--- FAIL: TestProbe_EchoedPayloadBeatsTheResultURL (0.00s)
FAIL
```

**Mutation:** whatever the fix is — scoping the walk away from the request's own
echo, comparing a candidate against the bytes this call sent, or preferring the
outcome subtree — deleting that one guard and letting the base64 pass run over
the whole body again returns the Pin to red. Conversely the Pin is load-bearing
only because the fixture echoes the payload: the shipped fixtures do not, which
is exactly why 100% coverage saw nothing. Any fix must land the echoing fixture,
not only a reordering.

---

## H2 — the variant rule fuses two genuinely different characters, including the implementer's own counterexample

**Severity:** H.

**Where:** `internal/illustrate/plan.go:282-306`, specifically the second-signal
test at `plan.go:299` (`if mine[tok] || visualTokens[tok]`).

**What:** `variantGroup` requires two signals — a back-reference marker opening
the visual, **and** a shared identifying token. The notes (§3.4) argue this
"deliberately under-matches" and cite `Mira's Mum` / *"a tall woman with the same
red hair"* as the case the marker-must-open rule protects. It does not protect
it. Move the same clause to the front, which is the more natural phrasing, and
Mum is fused onto her daughter's sheet:

```console
$ go test ./internal/illustrate/ -run TestProbe_MumFusesWhenSheSaysItFirst -v
=== RUN   TestProbe_MumFusesWhenSheSaysItFirst
    zz_probe2_test.go:58: Sheets=[Mira] Skipped=[{Name:Mira's Mum Visual:the same red hair as Mira, on a tall
        woman in a yellow raincoat Reason:variant of another cast member SheetOf:Mira}]
    zz_probe2_test.go:60: Sheets = [Mira], want 2 — the documented counterexample fuses as soon as it is phrased marker-first
--- FAIL
```

Two further shapes, both ordinary children's-story material:

```console
$ go test ./internal/illustrate/ -run TestProbe_VariantFusesTwoDifferentCharacters -v
=== RUN   …/two_dragons_sharing_a_species_token
    Sheets=[Blue Dragon] Skipped=[{Name:Green Dragon … Reason:variant of another cast member SheetOf:Blue Dragon}]
    Sheets = [Blue Dragon], want 2 — two different characters were fused onto one reference sheet
=== RUN   …/a_dog_described_by_comparison_to_the_girl
    Sheets=[Mira] Skipped=[{Name:Bramble Visual:the same height as Mira's knee, a shaggy brown dog with one
        white ear Reason:variant of another cast member SheetOf:Mira}]
    Sheets = [Mira], want 2 — two different characters were fused onto one reference sheet
--- FAIL
```

* **"Blue Dragon" / "Green Dragon"**, the second's visual opening *"the same size
  as her brother, but bright green…"*. The shared token is the **species**, not
  the identity. `nameStopwords` filters "little" and "big" but not "dragon",
  "river", "mouse", "robot" — the words a child's cast is actually made of.
* **"Mira" / "Bramble"**, the dog's visual opening *"the same height as Mira's
  knee, a shaggy brown dog…"*. This is the `visualTokens[tok]` half firing: any
  mention of an earlier character's name anywhere in the visual counts as
  identity evidence, and a comparative back-reference is precisely a sentence
  that names another character. The dog is then drawn image-to-image from the
  girl's reference sheet, gets no sheet of its own, and is filed under
  `SkipVariant` — so the skip log says the fusion was intended.

The notes' own risk assessment is the right one and it points the other way:
*"a missed variant costs one extra reference sheet, a false one costs two
characters drawn as one."* The rule as written buys the cheap error at the price
of the expensive one, on inputs that are not exotic.

**Pin:** `TestProbe_MumFusesWhenSheSaysItFirst` and
`TestProbe_VariantFusesTwoDifferentCharacters` above (both currently red).
Note that the shipped
`TestPlanReferences_NoBackReferenceKeepsSeparateSheets` passes with `Mira's Mum`
in it — it pins only the marker-position half, and the finding is that the
second half does no work.

**Mutation:** the tightening (require the shared token to be a token of *both
names* and not a species/common noun, or require the back-reference to name the
anchor explicitly, or demand a stronger marker such as `"the same <anchor name>"`)
is re-broken by restoring `visualTokens[tok]` at `plan.go:299` — at which point
the Mira/Bramble probe reddens again. The Blue/Green Dragon probe is re-broken by
removing whatever distinguishes a shared species token from a shared identity.

---

## H3 — `NeedsReferenceSheet` classifies drawable characters as having no appearance, and the cost is the whole book, not one skip

**Severity:** H.

**Where:** `internal/illustrate/plan.go:100-124` (`noAppearancePhrases`) and
`plan.go:173-187`; the understated cost is documented at `plan.go:94-99` and in
notes §3.3.

**What:** the predicate is a case-insensitive **substring** match over 22
English fragments. Several are ordinary description, not assertions of absence:

```console
$ go test ./internal/illustrate/ -run TestProbe_NeedsReferenceSheetFalsePositives -v
    NeedsReferenceSheet("a shy little ghost with no face, just two floating eyes and a wobbly white sheet") = false
    NeedsReferenceSheet("a snowman in a top hat who is never seen without his red scarf") = false
    NeedsReferenceSheet("a boy in an invisible cloak, only his boots showing") = false
    NeedsReferenceSheet("a faceless rag doll with button eyes sewn on crooked") = false
--- FAIL
```

A ghost with no face, a faceless rag doll and a snowman never seen without his
scarf are not narrators — they are the cast of a six-year-old's story, and each
of them is drawable in one line.

The documented cost is wrong in both directions:

* **It is not "one skipped sheet".** If any page names *only* the misclassified
  character, `Illustrate` returns the zero `Book` and the whole run fails —
  no pages, no partial book, nothing to show:

```console
$ go test ./internal/illustrate/ -run TestProbe_FalsePositiveKillsTheWholeBook -v
    err=illustrate: page 2: illustrate: no reference sheet for page: none of the characters it names has
        a reference sheet (Boo) pages=0 calls=0
    a story whose only hazard is the word "no face" returned no book at all
--- FAIL
```

* **It is not always loud.** On every *other* page the character is silently
  dropped from the prompt (`prompt.go:131-139`) — no name, no visual, no lock.
  The model is never told to draw them. That is lock 1 failing quietly, which is
  the case the package doc says cannot happen.

`plan.go:94-99` describes the failure as *"the member lands in Plan.Skipped, and
a page naming only that member fails with ErrNoReference instead of quietly
rendering text-to-image"*, presented as an acceptable trade. Per AGENTS.md
§"Docs about behaviour must match the behaviour", that comment carries the
severity of what it misdescribes.

The false-negative direction is accepted and needs no fix: a narrator phrased
without any of the 22 fragments (*"a warm voice from somewhere above"*) buys one
$0.01 sheet nobody uses. That is the cheap error and it is the right one to keep.

**Pin:** `TestProbe_NeedsReferenceSheetFalsePositives` and
`TestProbe_FalsePositiveKillsTheWholeBook` above. Note the shipped
`TestNeedsReferenceSheet` table records `{"documented false positive", "an
invisible boy in a blue coat", false}` — it pins the defect *as the expected
value*, so the suite cannot report it.

**Mutation:** whichever narrowing lands (anchor the phrases to whole-visual or
clause-initial assertions, drop the ones that are ordinary description —
`invisible`, `faceless`, `no face`, `never seen`, `not seen` — or require the
match to be the *subject* of the visual rather than any substring), restoring
the bare `strings.Contains(v, phrase)` loop at `plan.go:181-185` returns the
probes to red. A fix that only edits the doc comment is not a fix: the run still
returns no book.

---

## M1 — the forbidden-model guard is exact-match, so any decorated spelling and every video model but the literal `H3` pass

**Severity:** M.

**Where:** `internal/illustrate/illustrate.go:167` (`forbiddenModels`) and
`illustrate.go:176-183` (`IsForbiddenModel` — `EqualFold(TrimSpace(model), f)`).

**What:** the guard is the single mechanism standing between this track and
`t2-round3.md` H1 recurring, and PLAN.md §T6 says *"grep for it before closing
any track that renders"*. It matches only the exact string after trimming, so
every decorated spelling of the same upstream model is admitted, and it reaches
the wire:

```console
$ go test ./internal/illustrate/ -run 'TestProbe_Forbidden' -v
    IsForbiddenModel("Qwen/Qwen-Image-2512")      = false
    IsForbiddenModel("MiniMaxAI/Qwen-Image-2512") = false
    IsForbiddenModel("Qwen-Image-2512/")          = false
    IsForbiddenModel("qwen-image-2512:latest")    = false
    IsForbiddenModel("H3-2")                      = false
    IsForbiddenModel("MiniMax-Hailuo-02")         = false
--- FAIL: TestProbe_ForbiddenModelDecoratedSpellings

    a generate call went out carrying "Qwen/Qwen-Image-2512" — the t2i-only model reached the wire
--- FAIL: TestProbe_ForbiddenPrefixedModelReachesTheWire
```

A prefixed id is not hypothetical in this repo: AGENTS.md §GMI endpoints records
that the *text* model **must** carry a `MiniMaxAI/` prefix and that the bare id
404s, so a future agent flipping the provider switch has been trained by this
codebase to reach for a prefixed form. And PLAN.md §T6's forbidden table reads
"`H3` / **any video model**" — the guard implements the literal `H3` and nothing
else, so the row is under-implemented against its own wording.

**Pin:** the two probes above.

**Mutation:** after the guard is widened (normalised comparison on the id's last
path segment plus a case-folded containment test, or a documented allow-list of
the ids §3 actually sanctions), reverting `illustrate.go:178` to
`strings.EqualFold(strings.TrimSpace(model), f)` returns both probes to red.

---

## M2 — "the Book is zero on any error" is unpinned on the render paths: a partial-book mutant is green

**Severity:** M. (The behaviour is correct today. The pin that protects it does
not exist, which is precisely how `t2-round3.md` H1 survived two rounds.)

**Where:** `internal/illustrate/illustrate.go:288-295` (the two render-phase
error returns) against the contract at `illustrate.go:250-251` (*"The returned
Book is zero on any error"*), AGENTS.md §Errors (*"Never return a non-nil value
alongside a non-nil error"*) and notes §3.6 (*"Every error table asserts the zero
Book"*).

**What:** `TestIllustrate_ErrorBranches` does assert the zero `Book` — but every
one of its eleven rows is a **pre-flight** failure (bad config, bad cast, no
pages, no reference). Not one row reaches a render. The three tests that *do*
fail a render — `TestIllustrate_GMISentinelsSurvive`,
`TestIllustrate_UndecodableResponses`, `TestIllustrate_FirstFailureCancelsTheRest` —
all discard the returned book with `_`. So the contract is unasserted exactly
where a caller is most likely to be handed a half-book.

**Pin** (mutation-first; the mutant survives the whole suite):

```console
$ # illustrate.go:292-295, return the sheets alongside the error
$ sed -i 's|return Book{}, err|return Book{References: refs, Skipped: plan.Skipped}, err|' …
$ go test ./internal/illustrate/
ok  	thutapi/internal/illustrate	0.047s        <-- GREEN. Nothing sees it.
```

The missing pin is one line in an existing test — assert `book` is the zero
`Book` in `TestIllustrate_GMISentinelsSurvive` and
`TestIllustrate_UndecodableResponses`, both of which already have the book in
hand.

**Mutation:** the one above. With the pin added it goes red; without it, green —
which is the finding.

---

## M3 — one unsupported candidate anywhere in the body fails a render that has a usable image in it

**Severity:** M.

**Where:** `internal/illustrate/decode.go:93-97` (the base64 pass returns the
error rather than continuing) and `decode.go:110-118` (same on the URL pass),
with the decision documented at `decode.go:158-164`.

**What:** `imageType` returns `ErrUnsupportedImage` — a hard stop — for any
candidate that sniffs as an image outside the closed set. The walk then abandons
the body, even when a supported image sits later in sorted-key order:

```console
$ go test ./internal/illustrate/ -run TestProbe_UnsupportedThumbnailAbortsAGoodRender -v
    err=illustrate: unsupported image type: image/gif (want image/png, image/jpeg or image/webp) ct=""
    decodeImage failed on a body that carries a usable PNG
--- FAIL
```

The body here carries a GIF preview under an alphabetically earlier key and the
real PNG under `outcome.image`. The comment's justification —
*"it is unmistakably the picture"* — holds only when there is exactly one
candidate, and the whole design of the walk is that the number of candidates is
unknown. Since a failed page fails the whole `Illustrate` run, the cost of
guessing wrong is the book, and the correct behaviour is to remember the
unsupported hit and report it only if nothing usable is found.

**Pin:** `TestProbe_UnsupportedThumbnailAbortsAGoodRender` above.

**Mutation:** once the walk carries the first unsupported type forward instead
of returning it, restoring `return nil, "", err` inside the two candidate loops
returns the probe to red. `TestDecodeImage_ErrorBranches`' `gif in a data URI`
row (the single-candidate case) must stay green either way — it is the row that
proves the deferred error still surfaces.

---

## L1 — the round's own record misstates the `go.mod` edit as an `// indirect` promotion

**Severity:** L. Prose only, in `dev-diary/`; no behaviour. Recorded rather than
waived because ruling 1 below turns on it and the audit trail should be exact.

**Where:** `dev-diary/adversarial-review/t6-round1-notes.md:29-39`.

**What:** the notes say the dependency *"sat in `go.mod` as an `// indirect`
line. The edit promotes it to a direct require."* It did not:

```console
$ git show HEAD:go.mod | grep -c 'golang.org/x/sync'
0
```

`golang.org/x/sync` appears nowhere in HEAD's `go.mod` — not in the direct block
and not in the `// indirect` block. The require line is **new**. (`go.sum`
already carried its hashes because the module is in `modernc.org/sqlite`'s graph,
which is the true half of the claim and the one the ruling rests on.)

**Mutation:** restoring the sentence re-reddens the `git show` check above.

---

## L2 — `fetchImage` follows a redirect to any host the container can reach

**Severity:** L. Hardening, not an exploited path: the URL comes from an
authenticated GMI response today.

**Where:** `internal/illustrate/decode.go:135-156`, and the claim at
`decode.go:129-134` that *"Only http and https are followed"*.

**What:** `imageURL` (`decode.go:224-238`) does check the scheme of the string
found in the response — but `r.http.Do` uses the default redirect policy, so a
`302` from that URL steers the GET anywhere, including loopback and link-local
addresses inside the box:

```console
$ go test ./internal/illustrate/ -run TestProbe_FetchFollowsRedirectAnywhere -v
    the redirect target http://127.0.0.1:38627 was fetched: a response-supplied URL can steer the GET at
    any host the box can reach
--- FAIL
```

The container runs on the `proxy` network next to Traefik and four other sites
(§T1b), so "any host the box can reach" is not an empty set. The method, size cap
(`MaxImageBytes`) and context deadline are all correctly bounded; only the hop
count and destination are not. A `CheckRedirect` that re-applies the scheme check
(and optionally caps the hops) closes it in three lines.

**Mutation:** removing the `CheckRedirect` returns the probe to red.

---

## What survived mutation — the claimed pins that are load-bearing

Every mutation below was applied to the working tree, run, and reverted; the
package files are md5-identical to their pre-review state.

| # | Mutation | Result |
|---|---|---|
| M-a | `DefaultModel = "Z-Image"` (wrong-but-legal default) | **RED** — `TestDefaultModelIsNotForbidden`, `TestDefaultModelOnRawWire` (all 4 requests) |
| M-b | delete `if model == "" { model = DefaultModel }` in `Config.resolve` | **RED** — `TestIllustrate_DefaultModelReachesBothCallKinds` + all five raw-wire tests (the real `media.EditImage` refuses the empty model). **The T2 H1 trap is genuinely closed: the default is asserted through the default path, on the marshalled bytes, on both call kinds.** |
| M-c | delete the `IsForbiddenModel` block from `resolve` | **RED** — `TestIllustrate_ForbiddenModelRefusedBeforeAnyCall` (all 3 ids), `TestIllustrate_ErrorBranches/forbidden_model`. Refusal is confirmed to happen with **zero** calls recorded, i.e. before any paid request |
| M-d | page rendered with `GenerateImage` instead of `EditImage` (the t2i fallback) | **RED** — 11 tests incl. `TestIllustrate_NeverCallsGenerateImageForAPage` and `TestImageLockOnRawWire`. There is no t2i path for a page anywhere, on the happy path or on any error/retry branch |
| M-e | `strings.TrimSpace(m.Visual)` in both prompt builders | **RED** — `TestReferencePrompt_VisualIsVerbatim`, `TestPagePrompt_VisualsAreVerbatim`, `TestVisualVerbatimInRawJSON_AwkwardCharacters` |
| M-f | one word changed inside `StyleSuffix` | **RED** — `TestStyleSuffixWording` (the raw-wire pin compares against the constant and self-heals; the literal pin is what catches it, and it exists) |
| M-g | `renderReferences` returns before its errgroup finishes | **RED** — `TestIllustrate_ReferencesFinishBeforeAnyPageStarts`, red on all 3 runs. Phase ordering is genuinely pinned |
| M-h | `NeedsReferenceSheet` always true | **RED** — 9 table rows + `TestIllustrate_LiveHazardStoryEndToEnd` + 2 error branches |
| M-i | `variantGroup` drops the back-reference requirement | **RED** — `TestPlanReferences_NoBackReferenceKeepsSeparateSheets` |
| M-j | `collectStrings` walks map keys unsorted | **RED** — `TestDecodeImage_IsDeterministic`, `TestCollectStrings` |
| M-k | return `Book{References: refs, Skipped: plan.Skipped}` beside a render error | **GREEN — finding M2** |

Ten of eleven mutations reddened a shipped test. The three locks named in
PLAN.md §T6 are each pinned on the marshalled request bytes and each mutation of
them fails. The verbatim lock additionally survived an adversarial corpus this
reviewer wrote — `<`/`&` (JSON-escaped on the wire and byte-identical after
decode), emoji, zero-width joiners, ligatures, precomposed vs decomposed
accents, embedded backslashes and escaped quotes, CRLF and tabs, an 8,500-char
visual, and leading/trailing whitespace — all reached both prompt builders
unaltered (`TestProbe_AdversarialVisualsStayVerbatim`, 8/8 PASS).

`-race -count=5` on the package is clean, and the reviewer read the two shared
writes (`refs[i]`/`out[i]` at distinct indices; `r.total` written before any
goroutine starts, `r.done` under `renderer.mu`) and found no unsynchronised
access.

---

## The three rulings this round was asked to make

### Ruling 1 — the `go.mod` edit: **sanctioned. Not a contract change.**

`go.mod` is T0's `Owns` and T6 edited it, so the question is real. The ruling is
that this is the mechanical consequence of a dependency three documents already
approved, not a new contract:

* `AGENTS.md` §Stack names `golang.org/x/sync/errgroup` as one of exactly two
  sanctioned third-party dependencies; §Concurrency **requires** it
  (*"Fan-out uses `golang.org/x/sync/errgroup` with `SetLimit`, never an
  unbounded `go` loop"*); PLAN.md §T0 conventions repeats it; PLAN.md §T6 says
  *"Fan out with `errgroup`, bounded to ~4 concurrent"*.
* The edit is exactly what the toolchain produces, and nothing more:

```console
$ cp go.mod go.sum <backup>; go mod tidy; diff <backup>/go.mod go.mod && echo IDENTICAL
IDENTICAL
$ diff <backup>/go.sum go.sum && echo IDENTICAL
IDENTICAL
```

`go mod tidy` reproduces the working tree's `go.mod` byte-for-byte and leaves
`go.sum` untouched. There is no version choice, no new module in the graph, and
no way to use the mandated package without this line. The alternative the notes
considered — hand-rolling a semaphore to preserve a seam boundary — would have
violated an explicit AGENTS.md rule to protect a formality, and would have been
the worse call.

One correction to the record, which is L1 above: the line was **added**, not
promoted from `// indirect`. That does not change the ruling — `go.sum` already
held the hashes because `x/sync` sits in `modernc.org/sqlite`'s module graph —
but the review file should say what the diff actually did.

**Recommendation to the orchestrator** (not a finding): land the `go.mod` hunk in
T6's commit with the reason in the commit message, and consider a one-line note
on T0's `Owns` in PLAN.md that a `go mod tidy`-equivalent require line is a
mechanical edit any track may make. Every remaining track that fans out will hit
this same question.

### Ruling 2 — persistence: **the reasoning is sound; the gap is real and unassigned**

The decision to return bytes rather than persist is correct, and for the reason
given. §T7 lives in this same package, regenerates a drifted page and is capped
at 2 retries; T3's schema makes one illustration per page a **unique** slot with
`store.SetMediaPlace` returning `ErrConflict` on a second. Persisting inside the
fan-out would therefore write blobs and metadata rows for pictures T7 is about to
discard, and the regenerated page would need a delete-then-insert inside a
render goroutine. That is a worse shape, and pushing the write past T7's verdict
is the right call. The seam was also left genuinely cheap: `ContentType` is
constrained to `internal/mediastore`'s exact image set, `Stage*` are
`store.MediaKind`'s literal values, and `Illustration.Reference` plus
`Book.Skipped` carry everything `MediaPlace` needs without re-derivation.

**The gap it leaves is that nobody owns the write.** PLAN.md invariant 7
(*"Persist GMI output on receipt"*) is satisfied only in its weak sense: the
expiring `storage.googleapis.com` URL *is* dereferenced immediately
(`decode.go:135`), which is the part that had a deadline on it. But the bytes
then live only in a `Book` in memory for the whole run, and **no track's `Owns`
line covers writing them anywhere.** T7 owns `verify.go`; T10 owns the templates;
T3 is closed. This is §Unowned seams' original failure mode — a thing the task
graph assumes and no `Owns` line covers — and it bites the first time anyone
tries to reload a book page.

**Recommendation:** add a row to PLAN.md §Unowned seams —
*"Illustration persistence + job/SSE wiring (`Book` → `mediastore.Persist` +
`store.SetMediaPlace`, progress → broker topic)"* — and assign it to T7 or T10
**before T7 starts**, since T7 is the track that decides when a page is final. Not
a T6 finding; T6 correctly raised it rather than reaching outside its seam.

A note for whoever takes it: a `Book` holds every image for the run, and
`MaxImageBytes` is 16 MiB, so the worst case is ~160 MiB resident per concurrent
book. Realistic PNGs make that ~20 MiB, but the gate in §T11 should know the
number.

### Ruling 3 — a `T6b` operator track: **recommended, for**

T6's `Done when` — *"eight pages render with a recognisably constant cast"* — is
not mechanically checkable from a clean tree, on either half. "Eight pages
render" needs ~10 paid calls (~$0.10 a book); "recognisably constant" needs a
human eye, or T7's M3 judge, which is a later track. AGENTS.md §Testing rule 6
requires exactly this split, and T1b/T2b/T5b are three precedents.

**Proposed section, for the orchestrator to place after §T6:**

> ## T6b — Live illustration verification
>
> **Owns:** `internal/illustrate/live_test.go` — the `//go:build live` file in the
> package T6 owns. Nothing else. (PLAN.md §Architectural invariants 9.)
>
> **Depends on:** T6 (APPROVE'd), plus a working `GMI_API_KEY`.
> **Unblocks:** T7 (it verifies the reference sheets T7's judge compares against),
> and decision 7 (image provider), which §T6 defers to evidence from the first
> reference sheet.

What it must prove, cheapest first:

1. **The terminal record's shape for one `EditImage` call** — whether the queue
   echoes `payload` back, and whether the result arrives inline or as a URL.
   **This settles H1 above and costs one image (~$0.01).** Do it first; do it
   before the eight-page run. Dump the top-level keys and the outcome subtree
   into the record file, since as with T2b the response shape is the interesting
   part.
2. Two reference sheets and eight pages render end to end against
   `Flux2-Klein`, with `Book.Skipped` and the wire prompts pasted into the record
   (~$0.10).
3. An operator eyeballs the eight pages for a constant cast, and records the
   verdict plus, if it drifts, the one-line switch to `gemini-2.5-flash-image`
   and its result — which is what decision 7 is waiting for.
4. `Qwen-Image-2512` is confirmed still callable upstream (free: model
   resolution succeeds — that is the *point*, per §Status T6), so the guard's
   necessity is documented against production rather than against a doc.

Cost ceiling: one book plus one probe, ~$0.11. Run it after remediation, not
before — an eight-page run against the current `decodeImage` would render eight
copies of a character sheet and teach nothing except H1.

---

## Exit criteria for round 2

1. All eight findings fixed, none exempt (H1, H2, H3, M1, M2, M3, L1, L2), each
   with a row in `t6-remediation-round1.md`.
2. **H1's fix lands the echoing fixture, not only a reordering.** A response
   fixture that echoes `payload` the way the live queue does must exist in the
   suite, and the page's decoded bytes must be asserted *different from* the
   reference sheet's — the assertion the suite has never made.
3. **H3's fix changes behaviour, not the comment.** A cast of a ghost, a faceless
   doll and a snowman must produce a book; a `Narrator` with either live phrasing
   must still cost nothing. Both directions in one table.
4. **M2's fix asserts the zero `Book` on a render failure**, and mutation M-k
   above must go red afterwards. State that it was re-run.
5. Round 2 re-runs every mutation in the table above by hand, records which
   reddened, and confirms the tree is byte-identical afterwards.
6. Gates green: `go vet ./...`, `go test ./... -race -count=1`,
   `go test ./internal/illustrate/ -race -count=5`, `gofmt -l .`,
   coverage ≥ 75% on `internal/illustrate` — **and coverage is not the argument.**
   This package was at 100.0% with three H findings in it.
7. An explicit zero-residue claim against this round, severity by severity.
8. Rulings 2 and 3 are the orchestrator's to action (a §Unowned seams row and a
   §T6b section). Neither blocks T6's APPROVE; both should exist before T7 starts.

---

## Candidates examined and dismissed

- **`fetchImage` calling out from outside `internal/gmi` (invariant 1)** — not a
  violation. Invariant 1 names `api.gmi-serving.com` and `console.gmicloud.ai`;
  both stay behind `internal/gmi`. This dereferences a `storage.googleapis.com`
  object URL, which invariant 7 requires be taken on receipt. Reading it the
  other way would forbid the thing invariant 7 mandates. (The redirect hop is
  L2, which is a different point.)
- **Two style instructions in one prompt (notes §3.5)** — the decision is right
  and the alternative was correctly rejected. Rewriting the model's generated
  prompt to strip its style sentence is the same move that breaks the verbatim
  lock next door. `styleDirective` names the override in words, `StyleSuffix` is
  byte-identical to the spec, and the ordering is pinned
  (`TestPagePrompt_StyleLockOverridesTheModelsOwnStyleWords`). Whether the model
  *obeys* it is a T6b question.
- **`Illustrate` not calling `story.Validate`** — deliberate and correct, with the
  reason in the code: `Validate` enforces exactly `PageCount` pages, which is
  Phase B's contract, and T7 re-renders single pages through this same package.
  The cast checks it does run are the ones the locks need.
- **Character names matched exactly rather than case-folded** — matches
  `story.Validate`'s own `cast[c]` exact-spelling lookup (`validate.go:81-86`);
  the doc claim at `prompt.go:91-92` is accurate. No skew between packages.
- **No second retry layer** — correct per `internal/gmi/errors.go`, and pinned
  (`TestIllustrate_NoSecondRetryLayer`). The media client's single internal retry
  is the only one.
- **`DefaultLimit` bounding each phase rather than the run** — the phases are
  strictly sequential (M-g proves it), so a per-phase bound *is* the run's bound.
- **A page naming both a variant and its anchor** — produces one prompt listing
  both with their own visuals against the anchor's sheet. Slightly odd, not
  wrong, and not observed in either live run. Not a finding.
- **Duplicate names in `p.Characters`** — duplicates a line in the prompt.
  Cosmetic; `story.Validate` permits it; no lock is affected.
- **`imageURL`'s 2048-char cap and the four base64 alphabets** — both defensible
  and pinned; the alphabets cost nothing and the cap is generous for a signed
  storage URL.
- **`excerpt` leaking a response body into an error string** — capped at 200
  chars, no credentials in a queue record, and error strings do not reach
  children (§T9 governs that). Fine.
- **`panic` in `queueEnvelope`** (`illustrate_test.go:88`) — test fixture, with
  the reason on the line. AGENTS.md §Functions targets library code.

---

## What this review did **not** examine

Stated so the next round's zero-residue claim has a boundary.

- **Anything live.** No GMI call was made and no money spent, per the review's
  constraint. **H1's severity therefore rests on inference**: the live TTS
  terminal record in `t2b-t5b-live-record.md` echoes `payload` and returns the
  result as a URL, and the image path uses the same envelope, the same endpoint
  and the same polling code — but the *image* response shape has never been
  observed. T6b item 1 settles it for one cent, and should run before anything
  else.
- **Whether the prompts work.** Nothing here says `Flux2-Klein` honours *"Keep
  Mira identical to that reference image"*, that a reference sheet is a good i2i
  base, or that `referenceDirective`'s framing helps. That is the whole of the
  `Done when` and it is T6b's.
- **`internal/gmi/media`** — closed at T2 round 5 and T2b. Read only at the
  surface T6 touches: `GenerateImage`/`EditImage` signatures and payload shape,
  the `{model, payload}` envelope, `EditImage`'s empty-model refusal, and
  `polling.go`'s contract that the terminal record's raw body is what reaches the
  caller. Its retry and poll logic were not re-reviewed.
- **`internal/story`** — closed at T5 round 2. Read `validate.go` only, to check
  the character-matching skew above.
- **`internal/store` / `internal/mediastore`** — closed at T3 round 2. Read only
  for the `MediaKind` and `MediaPlace` claims in ruling 2; the `ErrConflict`
  behaviour on a second illustration per page is taken from the notes and the T3
  review, not re-verified against the schema.
- **T7's `verify.go`** — does not exist. Its interaction with H1 (a judge that
  cannot fail a page that *is* the reference) is reasoning, not a tested claim.
- **The persistence, job and SSE wiring** — nothing to review; that is ruling 2's
  point.
- **`cmd/thutapi/main.go`** — correctly untouched; T6 exposes a library and used
  no route line. Verified by `git status`, not re-read.
- **Fuzzing.** `decodeImage` was probed with hand-written adversarial bodies
  (echoed payload, unsupported-then-supported, empty, non-JSON, JSON with no
  image, arrays, request ids, wrapped base64, four alphabets, non-http schemes,
  a redirect chain, an oversized body, a truncated body). It was **not** fuzzed,
  and no bound was checked on the size of the raw body handed in by the client.
- **Memory and wall-clock behaviour of a real ten-image run** beyond the
  arithmetic in ruling 2.
- **Dev-diary prose** outside the sections cited in the Evidence row.
