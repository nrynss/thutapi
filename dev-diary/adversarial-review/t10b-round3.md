# T10b round 3 — adversarial re-review of the player and C4 recovery

| | |
|---|---|
| **Target** | T10b's server-rendered `/book/{id}` player, C4 snapshot/stream recovery, artifact downloads, and the round-2 ownership/lifecycle remediation. Reviewed against PLAN.md §T10 / C4, T10c C4, and the T9 adapter contract. |
| **Reviewer** | Fresh independent Terra reviewer (`gpt-5.6-terra`), 2026-09-06. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 1 × M, 0 × L** |

## Evidence

Round-1 H2/H3 remain closed: `BookHandler.load` clears previous-run page and
artifact URLs for explicit `running`/`failed`, and `DownloadHandler` refuses
those states. The server-rendered page escapes title, byline, and page text via
`html/template`; the player is native `controls playsinline`; PDF and MP4
attachments use title-derived ASCII-only slugs; and the byte serving remains
with mediastore. Round-2's open barrier is real for an established SSE
connection: `ServeTopic` subscribes before it flushes headers, and
`readBookState` waits for the EventSource `open` before its one state read.
The browser pin also proves a boundary `page_approved` merges with a later
snapshot and a failed state read retains the live stream.

Read-only verification on this tree:

| Check | Result |
|---|---|
| `go vet ./...`; `git diff --check`; `gofmt -l .` | PASS (no diagnostic output) |
| `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1` | PASS |
| `go test ./... -race -count=1` | PASS: every package, including `internal/interview` and `internal/job`; no timing-flake exemption was needed. |
| `node --check static/app.js static/book/catchup.js static/browser-test.js` | PASS |
| Browser execution | Not run: CUA reported `apps: []`, `browsers: []`; there is no browser surface in this environment. The static browser harness is therefore syntax-checked and inspected, not treated as a browser PASS. |

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `internal/web/web.go:254-267,120-123`; `static/app.js:278-319` | C4's explicit `unknown` state is neither truthful nor safe on a retry. `bookgen.CatchUp` returns `unknown` when it remembers the latest run but cannot read that job result. `load` does not take the current-run branch for `unknown`; if a prior run left a PDF and film, it upgrades the response to `ready` and exposes those stale artifacts. `DownloadHandler` also permits `unknown`. The T9 adapter recognizes only `ready`, `failed`, and `running`, so an `unknown` snapshot closes the authoritative stream and re-POSTs generation. Thus a latest run whose status cannot be read is presented as a completed previous book and can trigger another billable run. This is the same retry-truth failure round 1 closed for the other non-ready statuses. | Add `TestBookStateUnknownDoesNotExposePriorRunArtifacts` using `fixedGeneration{Status: GenerationUnknown}` plus old image/PDF/MP4 rows: state and SSR must contain no old media and direct PDF/MP4 downloads must be 404. Add a browser C4 case returning `{status:"unknown"}` after `open`: it must keep the subscribed stream and show a non-terminal checking state, with no `POST /generate`; a later `book_ready` must still navigate. It fails now because `load` returns `ready` with the old URLs and `recoverGeneration` falls through to `generate()`. | Restore the present `unknown` fall-through or the old-artifact readiness promotion. The state/SSR/download pins re-expose the earlier artifact and the browser pin repeats the generation POST despite an unresolved latest run. |
| M1 | M | `internal/bookgen/bookgen.go`, `internal/bookgen/bookgen_test.go`, `internal/bookgen/pipeline.go`, `static/app.js`, `static/browser-test.js`; PLAN.md:115,120; `t10c-round1.md` C4 | The round-2 ownership finding is not closed. T10b owns `internal/web/**`, `static/book/**`, and route lines, while its current diff changes the closed T10c-owned `internal/bookgen/**` and the closed T9-owned `static/app.js` / `static/browser-test.js`. T10c C4 sanctioned only a recorded dependency: it says T10b owns the HTTP surface and that run state *may require a persisted outcome decision*; it does not transfer `bookgen` ownership. Moving reusable protocol code into `static/book/catchup.js` narrows the adapter, but it does not authorize the adapter edits or the new `bookgen.CatchUp` lifecycle API. No T10b contract row names these external files, line changes, owner consent, and reason. Definition of done item 7 remains false. | Before remediation, add a T10b contract row that names each outside path and its exact C4 responsibility, or amend PLAN ownership through the orchestrator and record the transfer; then make a diff-ownership pin reject all changed paths not covered by T10b's `Owns`, that contract row, or the sanctioned route-line exception. The pin fails on the five current non-owned paths. | Remove the approved transfer/contract-row check. A future T10b change can silently modify T9 or T10c code again while its record claims the work stayed in its seam. |

## Zero-residue assessment

Round-1 H2/H3 and round-2 H1/H2 are closed for their explicitly tested
`running` and `failed` paths: the open-acknowledged C4 read prevents the
constructor-to-subscription gap, and a read failure keeps the stream alive.
They leave H1 above in the separate, exported `unknown` status path, where
the same old-artifact and repeat-generation behavior remains. Round-2 M1 also
has residue because the implementation still edits T9 and T10c-owned paths
without a recorded transfer or sanctioned contract row. There is no
zero-residue claim.

**VERDICT: REMEDIATE — 0 × C, 1 × H, 1 × M, 0 × L.**
