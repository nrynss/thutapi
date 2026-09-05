# T6 round 1 — implementation notes

**This is not a review.** It is the T6 implementation agent's record of
the one edit it made outside its `Owns` seam, the contract changes it
wanted and did not make, and the judgement calls a reviewer should
attack first. The review verdict lives in `t6-round1.md`, written by
someone else.

| | |
|---|---|
| **Track** | T6 — Illustration. `Owns: internal/illustrate/**`. |
| **Author** | Implementation agent (M3 role), 2026-09-05. |
| **State** | Implemented, unreviewed, uncommitted. Working tree only. |

---

## 1. The one edit outside the seam — `go.mod`

`go.mod` is T0's `Owns`. I changed it.

```diff
-require modernc.org/sqlite v1.58.0
+require (
+	golang.org/x/sync v0.22.0
+	modernc.org/sqlite v1.58.0
+)
```

**Why, and why I judged it not to be a contract change.** PLAN.md §T6
says "Fan out with `errgroup`, bounded to ~4 concurrent"; AGENTS.md
§Concurrency says "Fan-out uses `golang.org/x/sync/errgroup` with
`SetLimit`, never an unbounded `go` loop"; AGENTS.md §Stack and §T0
both name `golang.org/x/sync/errgroup` as one of the two sanctioned
third-party dependencies. The dependency was therefore already
approved by three documents — it had simply never been required by a
package before. Correction (t6-round1.md L1): the require line is
**new**, not a promotion — `golang.org/x/sync` appears nowhere in the
pre-T6 `go.mod` (`git show aaf33bc:go.mod | grep -c golang.org/x/sync`
is 0), neither in the direct block nor as an `// indirect` line; the
edit adds it as a direct require. `go.sum` already carried the hashes
— the module sits in `modernc.org/sqlite`'s graph — and is
**unchanged**; `git diff --stat` is `go.mod | 5 ++++-` and nothing
else.

The alternative — hand-rolling a semaphore to avoid touching `go.mod` —
would have violated an explicit AGENTS.md rule to preserve a seam
boundary, which is the wrong trade. Flagging it here rather than
mentioning it in a commit message is the point of this file.

**If the reviewer disagrees**, the fix is one line and the package
compiles either way; say so and it becomes a remediation row.

---

## 2. Contract changes wanted, NOT made

### 2.1 Nobody owns persisting the rendered images

`Illustrate` returns image bytes and content types. It does **not**
call `mediastore.Persist` or `store.SetMediaPlace`, and no track's
`Owns` line covers the wiring that should.

This is deliberate, not an oversight, and the reason is T7: PLAN.md
§T7 lives in the same package and regenerates a page whose character
drifted, capped at 2 retries. Persisting inside the render loop would
write blobs and metadata rows for pictures T7 is about to discard, and
T3's schema makes one illustration per page a **unique** slot
(`store.SetMediaPlace` returns `ErrConflict` on a second one), so the
regenerated page could not be stored without a delete-then-insert
dance that does not belong in a render loop.

What I did instead, so the seam is cheap for whoever gets it:

* `Reference.ContentType` and `Illustration.ContentType` are already
  constrained to `image/png` / `image/jpeg` / `image/webp` — exactly
  `internal/mediastore`'s image set — so a later `Persist` cannot fail
  on a type this package let through.
* `StageReference` and `StageIllustration` are literally
  `store.MediaReference` and `store.MediaIllustration`'s values, so
  progress events map to media kinds with no translation.
* `Illustration.Reference` names the cast member whose sheet the page
  used, and `Book.Skipped` explains every cast member that has no
  sheet, so a persistence layer can fill `MediaPlace.CastName` and
  `MediaPlace.PageN` without re-deriving anything.

**Ask:** assign the persistence + job/SSE wiring explicitly, either as
a row in PLAN.md §Unowned seams or inside T7/T10's `Owns`. It is
currently assumed by invariant 7 and by nothing else.

### 2.2 No route was added to `newServer`

T6 exposes a library, not an endpoint, so the one sanctioned
`main.go` exception (invariant 5) was not used. `cmd/thutapi/main.go`
is untouched. Whoever wires the pipeline will need that line.

### 2.3 `story.Validate` accepts two shapes T6 has to defend against

Reported for the record only — **T5 is closed and I changed nothing in
`internal/story`.** The orchestrator supplied both from live Phase-B
runs on 2026-09-05:

1. A cast member with no appearance (`Narrator` / `"no visual"`, and
   in a second run `Narrator` / `"an unseen storyteller with no
   appearance"`) validates fine. A naive per-member loop pays for a
   reference sheet that no page should ever be locked against.
2. Two cast members can be one entity (`Grumpy River` and `Happy
   River`, the second's visual literally opening `"the same wide blue
   river"`). Names are unique, so `Validate` accepts them; two sheets
   make two unrelated rivers.

Both are handled inside `internal/illustrate` by `NeedsReferenceSheet`
and `PlanReferences`, and every skip is recorded in `Book.Skipped`
with a reason — nothing is dropped silently. If a later track would
rather Phase B not emit these at all, that is a T5 change and a
separate conversation.

---

## 3. Judgement calls a reviewer should attack first

These are the places where I chose, and where I would look for a
finding if I were reviewing this.

**3.1 Decoding the request-queue result.** T2 hands back raw bytes on
purpose (`t2-round3.md` M3: GMI has published no per-model result
schema, so a typed struct would be a fabrication). T6 is the track
that knows the model, so the decode landed here — but I will not
hard-code an unpublished schema either. `decodeImage` is therefore
*structural*: pass raw image bytes through; otherwise walk the JSON in
sorted-key order and accept the first string that decodes to real
image magic; failing that, fetch the first http/https URL. Magic
bytes are the test, never a field name. Attack surface: the heuristic
could pick the wrong string in a body I have not imagined. The
mitigations are the sorted-key determinism pin
(`TestDecodeImage_IsDeterministic`), the 64-character minimum before a
bare string is even tried, and the fact that a wrong pick still has to
be a real PNG/JPEG/WEBP.

**3.2 `fetchImage` makes an HTTP call from outside `internal/gmi`.**
Invariant 1 names `api.gmi-serving.com` and `console.gmicloud.ai`;
this fetches `storage.googleapis.com` object URLs, which invariant 7
says expire and must be taken on receipt. I read that as inside the
invariant, not around it. Only http/https are followed, the read is
capped at `MaxImageBytes` (16 MiB), and the client is
`Config.HTTPClient` (nil means a 30s-timeout client) — no environment
is read. If a reviewer reads invariant 1 more strictly, the
alternative is a `Fetcher` supplied by `main`, which is a Config field
change and nothing else.

**3.3 `NeedsReferenceSheet` has a documented false positive.** It
matches phrases asserting the *absence* of an appearance, not the name
"Narrator" (a child may legitimately name a character that). The cost:
a character whose appearance genuinely is invisibility ("an invisible
boy") is skipped. That is pinned as a test case with the expected
value, and it stays loud — the member lands in `Book.Skipped`, and a
page naming only such members fails with `ErrNoReference` rather than
silently rendering text-to-image.

**3.4 Variant detection requires two signals, deliberately.** A visual
must *open* with a back-reference ("the same ...") **and** share an
identifying token with an earlier member. Either alone over-matches:
`"Mira's Mum"`, whose visual reads `"a tall woman with the same red
hair"`, shares the token `mira` and would otherwise be locked to her
daughter's sheet. The rule under-matches on purpose — a missed variant
costs one extra reference sheet, a false one draws two characters as
one. `TestPlanReferences_NoBackReferenceKeepsSeparateSheets` is the
pin.

**3.5 Two style instructions in one prompt.** M3's page prompts
already carry style language (live 2026-09-05: a page prompt ending
"Soft storybook illustration style."), and PLAN.md §T6 mandates
appending the constant suffix on top. I did **not** edit the model's
prompt to strip its style sentence — rewriting generated text to make
a lock hold is how the verbatim lock next door gets broken. Instead a
constant `styleDirective` introduces the suffix and says in words that
it overrides any style wording above it. `StyleSuffix` itself is
byte-identical to the spec, so the raw-wire pin still matches the
constant.

**3.6 `Book` is zero on every error.** A partially illustrated book is
not a book, and AGENTS.md §Errors forbids a non-nil value beside a
non-nil error. Every error table asserts the zero `Book`.

---

## 4. What was verified, and what was not

Verified from a clean tree:

```console
$ go vet ./... && go test ./... -race && gofmt -l .
(all clean; internal/illustrate coverage 100.0% of statements)
```

**Not verified: anything live.** No GMI call was made from this
track — image calls cost about a cent each and the live probe is an
operator step on the T2b/T5b precedent. Every test in this package
uses `httptest`; the lock pins run the whole package against the
**real** `*media.Client` over `httptest` and assert on the marshalled
request bytes, so the client's own envelope, auth and polling are in
the path without a network call to GMI.

The live half of T6 — that eight pages actually come back with a
recognisably constant cast — is not mechanically checkable from this
tree and needs a `T6b`-style operator row if the orchestrator wants it
owned.
